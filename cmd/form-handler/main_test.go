// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// run drives the command exactly as main() does, and hands back what a user
// would have seen.
func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = cli(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionIsPrintedBare(t *testing.T) {
	code, stdout, _ := runCLI(t, "--version")

	require.Zero(t, code)
	// Bare, not cobra's "form-handler version X" sentence: this is documented
	// as the way to ask a running container which release its digest is, so
	// something is probably parsing it.
	assert.Equal(t, version+"\n", stdout)
}

// The flags were parsed by the standard library before cobra, and `flag` treats
// one dash and two alike. Anything already deployed asks for them this way.
func TestLegacySingleDashFlagsStillWork(t *testing.T) {
	code, stdout, _ := runCLI(t, "-version")

	require.Zero(t, code)
	assert.Equal(t, version+"\n", stdout)
}

// Both spellings have to actually probe. This is the test that pins the shim:
// without it pflag reads "-healthcheck" as a cluster of shorthands and dies on
// "unknown shorthand flag: 'e' in -ealthcheck", so a container that has been
// probing itself this way since before cobra goes unhealthy on upgrade while
// the service behind it is answering perfectly well.
func TestBothSpellingsOfHealthcheckProbeAServingProcess(t *testing.T) {
	// probe() talks to the loopback on the port from ADDR, so the test has to
	// put a real server on a real port rather than fake the call.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for _, flag := range []string{"--healthcheck", "-healthcheck"} {
		t.Run(flag, func(t *testing.T) {
			t.Setenv("ADDR", ":"+portOf(t, srv.URL))

			code, _, stderr := runCLI(t, flag)

			assert.Zero(t, code, "stderr: %s", stderr)
		})
	}
}

func TestHealthcheckFailsWhenNothingIsServing(t *testing.T) {
	t.Setenv("ADDR", unreachableAddr(t))

	code, _, stderr := runCLI(t, "--healthcheck")

	require.Equal(t, 1, code)
	assert.Contains(t, stderr, "error:")
}

func TestUnknownFlagIsRefused(t *testing.T) {
	code, _, stderr := runCLI(t, "--nonsense")

	require.Equal(t, 1, code)
	assert.Contains(t, stderr, "error:")
}

// Only the two documented flags are rewritten. Anything else keeps pflag's
// meaning, rather than being promoted into a long flag nobody defined.
func TestAnUnknownSingleDashFlagIsStillAnError(t *testing.T) {
	code, _, stderr := runCLI(t, "-nonsense")

	require.Equal(t, 1, code)
	assert.Contains(t, stderr, "error:")
}

// It takes no arguments at all — everything else comes from the environment.
func TestStrayArgumentsAreRefused(t *testing.T) {
	code, _, stderr := runCLI(t, "serve")

	require.Equal(t, 1, code)
	assert.Contains(t, stderr, "error:")
}

func TestHelpIsNotAnError(t *testing.T) {
	code, stdout, _ := runCLI(t, "--help")

	require.Zero(t, code)
	assert.Contains(t, stdout, "form-handler")
	assert.Contains(t, stdout, "--healthcheck")
}

// unreachableAddr returns an address nothing is listening on, by taking a port
// and immediately giving it back. Deterministic where a hard-coded port would
// depend on what else this machine happens to be running.
func unreachableAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr, ok := l.Addr().(*net.TCPAddr)
	require.True(t, ok)
	require.NoError(t, l.Close())
	return ":" + strconv.Itoa(addr.Port)
}

func portOf(t *testing.T, rawURL string) string {
	t.Helper()
	_, port, found := strings.Cut(strings.TrimPrefix(rawURL, "http://"), ":")
	require.True(t, found, "no port in %s", rawURL)
	return port
}
