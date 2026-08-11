// SPDX-License-Identifier: GPL-3.0-or-later

package http_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adapterhttp "github.com/alrayyes/form-handler/internal/adapter/http"
)

// logged is one line of the service's JSON log, decoded.
type logged map[string]any

// capture builds a handler whose log goes somewhere the test can read, which is
// the only way to assert on the thing an operator actually sees.
func capture(t *testing.T, perHour int) (*adapterhttp.Handler, func() []logged) {
	t.Helper()

	var buf bytes.Buffer
	h := build(t, &recorder{}, perHour, slog.New(slog.NewJSONHandler(&buf, nil)))

	return h, func() []logged {
		var out []logged
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var entry logged
			require.NoError(t, json.Unmarshal([]byte(line), &entry), "log line is not JSON: %s", line)
			out = append(out, entry)
		}
		return out
	}
}

// The complaint this answers: a refused submission left no trace at all, so
// "nothing in the logs" meant either that the service turned it away or that it
// never arrived — and there was no way to tell which.
func TestEveryRefusalIsLogged(t *testing.T) {
	cases := map[string]struct {
		method string
		origin string
		body   string
		status float64
	}{
		"origin not allowed": {http.MethodPost, "https://someone-else.example", goodBody, 403},
		"unreadable body":    {http.MethodPost, origin, `{"name":`, 400},
		"unknown field":      {http.MethodPost, origin, `{"name":"Ada","email":"a@example.com","message":"long enough here","admin":true}`, 400},
		"invalid submission": {http.MethodPost, origin, `{"name":"","email":"a@example.com","message":"long enough to pass"}`, 422},
		"wrong method":       {http.MethodGet, origin, "", 405},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, lines := capture(t, 100)

			req := httptest.NewRequest(tc.method, "/contact/marketing", strings.NewReader(tc.body))
			req.Header.Set("Origin", tc.origin)
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)

			require.Equal(t, int(tc.status), res.Code)

			entries := lines()
			require.Len(t, entries, 1, "a refused submission logged nothing")
			assert.Equal(t, tc.status, entries[0]["status"])
			assert.Equal(t, "marketing", entries[0]["form"], "which form was refused")
			assert.NotEmpty(t, entries[0]["reason"], "a status code alone does not say why")
		})
	}
}

// The origin is the field that answers "was this us or something in front of
// us", so it has to be in the line rather than inferred from the status.
func TestARefusedOriginIsNamedInTheLog(t *testing.T) {
	h, lines := capture(t, 100)

	post(t, h, goodBody, "https://someone-else.example")

	entries := lines()
	require.Len(t, entries, 1)
	assert.Equal(t, "https://someone-else.example", entries[0]["origin"])
	// Warn, not info: somebody else's page posting at this form is worth
	// noticing, where a visitor mistyping their address is not.
	assert.Equal(t, "WARN", entries[0]["level"])
}

func TestRateLimitingIsLogged(t *testing.T) {
	h, lines := capture(t, 1)

	post(t, h, goodBody, origin)
	post(t, h, goodBody, origin)

	entries := lines()
	require.Len(t, entries, 2, "the refusal after the limit was not logged")
	assert.Equal(t, float64(429), entries[1]["status"])
	assert.Equal(t, "WARN", entries[1]["level"])
}

// Cloudflare stamps every request it forwards. Carrying it through is what lets
// a line here be matched against Cloudflare's own log for the same request.
func TestTheCloudflareRayIdIsCarriedThrough(t *testing.T) {
	h, lines := capture(t, 100)

	req := httptest.NewRequest(http.MethodPost, "/contact/marketing", strings.NewReader(goodBody))
	req.Header.Set("Origin", "https://someone-else.example")
	req.Header.Set("CF-Ray", "8f2b1c4d5e6f7a8b-AMS")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	entries := lines()
	require.Len(t, entries, 1)
	assert.Equal(t, "8f2b1c4d5e6f7a8b-AMS", entries[0]["cf_ray"])
}

// A header is whatever the sender put in it, and this one reaches a log line.
func TestHeadersReachingTheLogAreBounded(t *testing.T) {
	h, lines := capture(t, 100)

	req := httptest.NewRequest(http.MethodPost, "/contact/marketing", strings.NewReader(goodBody))
	req.Header.Set("Origin", "https://"+strings.Repeat("a", 5000)+".example")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	entries := lines()
	require.Len(t, entries, 1)
	logged, ok := entries[0]["origin"].(string)
	require.True(t, ok)
	assert.Less(t, len(logged), 300, "an unbounded header went straight into the log")
}

// Accepting still says so, and the honeypot still looks like an acceptance from
// outside while being distinguishable from inside.
// The attack the log-injection warnings describe: a newline in a logged value
// lets whoever sent it append what looks like another entry, so a refused
// request can claim in the log that it succeeded.
func TestARefusalCannotForgeASecondLogEntry(t *testing.T) {
	h, lines := capture(t, 100)

	req := httptest.NewRequest(http.MethodPost, "/contact/marketing", strings.NewReader(goodBody))
	req.Header.Set("Origin", "https://evil.example\n{\"level\":\"INFO\",\"msg\":\"sent message\",\"status\":202}")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	entries := lines()
	require.Len(t, entries, 1, "a header wrote its own log entry")
	assert.Equal(t, "refused submission", entries[0]["msg"])
	origin, ok := entries[0]["origin"].(string)
	require.True(t, ok)
	assert.NotContains(t, origin, "\n")
}

// X-Forwarded-For is set by whoever sent the request, so without checking it
// the limiter is keyed on arbitrary strings and the log carries them.
func TestAClientAddressIsAlwaysAnAddress(t *testing.T) {
	h, lines := capture(t, 100)

	req := httptest.NewRequest(http.MethodPost, "/contact/marketing", strings.NewReader(goodBody))
	req.Header.Set("Origin", "https://someone-else.example")
	req.Header.Set("X-Forwarded-For", "not-an-address\nforged")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	entries := lines()
	require.Len(t, entries, 1)
	ip, ok := entries[0]["ip"].(string)
	require.True(t, ok)
	assert.NotContains(t, ip, "forged")
	assert.NotContains(t, ip, "not-an-address")
}

func TestAcceptedSubmissionsAreStillLogged(t *testing.T) {
	h, lines := capture(t, 100)

	post(t, h, goodBody, origin)

	entries := lines()
	require.Len(t, entries, 1)
	assert.Equal(t, float64(202), entries[0]["status"])
}
