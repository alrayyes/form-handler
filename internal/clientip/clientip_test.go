// SPDX-License-Identifier: GPL-3.0-or-later

package clientip_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alrayyes/form-handler/internal/clientip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func request(t *testing.T, remoteAddr, xff string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/contact", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}

	return r
}

func resolver(t *testing.T, trusted ...string) clientip.Resolver {
	t.Helper()
	r, err := clientip.NewResolver(trusted)
	require.NoError(t, err)

	return r
}

// The bug this exists for. X-Forwarded-For is set by whoever sent the request,
// so a service that believes it unconditionally hands every caller a fresh
// rate-limit bucket per request just by varying the header.
func TestAnUntrustedSenderCannotChooseItsOwnAddress(t *testing.T) {
	// Nothing trusted: the zero value of the whole feature, and the default.
	r := resolver(t)

	got := r.From(request(t, "203.0.113.7:44321", "198.51.100.1"))

	assert.Equal(t, "203.0.113.7", got, "a forged header was believed")
}

func TestWithNoTrustedProxiesTheHeaderIsIgnoredEntirely(t *testing.T) {
	r := resolver(t)

	for _, xff := range []string{"198.51.100.1", "10.0.0.1, 198.51.100.1", "nonsense"} {
		got := r.From(request(t, "203.0.113.7:44321", xff))
		assert.Equal(t, "203.0.113.7", got)
	}
}

// The reason the header is read at all: behind a proxy, RemoteAddr is the proxy
// for every visitor, which would rate-limit the whole internet as one client.
func TestATrustedProxysHeaderIsBelieved(t *testing.T) {
	r := resolver(t, "10.0.0.0/8")

	got := r.From(request(t, "10.0.0.5:44321", "198.51.100.1"))

	assert.Equal(t, "198.51.100.1", got)
}

// The chain is appended to left to right, and anyone can prepend entries. Only
// the part contributed by proxies we trust can be believed, so the answer is
// the rightmost address that is not itself one of ours.
func TestTheRightmostUntrustedAddressWins(t *testing.T) {
	r := resolver(t, "10.0.0.0/8")

	// A visitor claiming to be 1.2.3.4, then the real address our proxy saw,
	// then a hop between our own proxies.
	got := r.From(request(t, "10.0.0.5:44321", "1.2.3.4, 198.51.100.1, 10.0.0.9"))

	assert.Equal(t, "198.51.100.1", got, "a prepended entry was believed over the proxy's own")
}

func TestAChainOfOnlyTrustedProxiesFallsBackToTheConnection(t *testing.T) {
	r := resolver(t, "10.0.0.0/8")

	got := r.From(request(t, "10.0.0.5:44321", "10.0.0.9, 10.0.0.8"))

	assert.Equal(t, "10.0.0.5", got)
}

func TestGarbageInTheChainIsNotReturned(t *testing.T) {
	r := resolver(t, "10.0.0.0/8")

	got := r.From(request(t, "10.0.0.5:44321", "not-an-address"))

	assert.Equal(t, "10.0.0.5", got, "an unparseable entry was returned")
}

func TestASingleTrustedAddressWorksWithoutAPrefix(t *testing.T) {
	r := resolver(t, "10.0.0.5")

	got := r.From(request(t, "10.0.0.5:44321", "198.51.100.1"))

	assert.Equal(t, "198.51.100.1", got)
}

func TestIPv6IsHandled(t *testing.T) {
	r := resolver(t, "2001:db8::/32")

	// The visitor's address has to be outside the trusted prefix, or it is one
	// of our own hops by definition — 2001:db8:ffff::1 would be inside /32.
	got := r.From(request(t, "[2001:db8::1]:44321", "2606:4700::1111"))

	assert.Equal(t, "2606:4700::1111", got)
}

// Ports and brackets are transport detail; the limiter and the log want the
// address.
func TestThePortIsNotPartOfTheAddress(t *testing.T) {
	r := resolver(t)

	assert.Equal(t, "203.0.113.7", r.From(request(t, "203.0.113.7:44321", "")))
	assert.Equal(t, "2001:db8::1", r.From(request(t, "[2001:db8::1]:44321", "")))
}

func TestAnUnreadableRemoteAddrIsNotFatal(t *testing.T) {
	r := resolver(t)

	got := r.From(request(t, "garbage", ""))

	assert.Equal(t, "unknown", got)
}

func TestBadTrustedProxyConfigIsRefused(t *testing.T) {
	for _, bad := range []string{"not-a-cidr", "10.0.0.0/99", "10.0.0.0/8, oops"} {
		_, err := clientip.NewResolver([]string{bad})

		require.Errorf(t, err, "accepted %q", bad)
	}
}
