// SPDX-License-Identifier: GPL-3.0-or-later

package server_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alrayyes/form-handler/internal/config"
	"github.com/alrayyes/form-handler/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type entry map[string]any

// serve builds the whole service with its log going somewhere the test can
// read, which is the only way to assert on what an operator would actually see.
func serve(t *testing.T, level slog.Level) (http.Handler, func() []entry) {
	t.Helper()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level}))

	h, err := server.New(config.Config{
		Forms: []config.Form{{
			ID:               config.DefaultFormID,
			Origins:          []string{"https://www.example.com"},
			From:             "site@example.com",
			To:               "info@example.com",
			RateLimitPerHour: 100,
			SMTP:             &config.SMTP{Addr: "127.0.0.1:1", Timeout: time.Second},
		}},
	}, log)
	require.NoError(t, err)

	return h, func() []entry {
		var out []entry
		for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
			if line == "" {
				continue
			}
			var e entry
			require.NoError(t, json.Unmarshal([]byte(line), &e), "not JSON: %s", line)
			out = append(out, e)
		}

		return out
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	return res
}

// The gap this closes: only the contact handler logged, so everything else the
// service answered — health checks, preflights, requests for paths that do not
// exist — happened in silence.
func TestEveryRequestIsLogged(t *testing.T) {
	h, lines := serve(t, slog.LevelDebug)

	require.Equal(t, http.StatusOK, get(t, h, "/healthz").Code)

	entries := lines()
	require.Len(t, entries, 1, "a request went unlogged")
	assert.Equal(t, "GET", entries[0]["method"])
	assert.Equal(t, "/healthz", entries[0]["path"])
	assert.Equal(t, float64(200), entries[0]["status"])
	assert.Contains(t, entries[0], "duration_ms", "how long it took is half the point of an access log")
}

// An orchestrator probes this every few seconds. At info it would be the only
// thing anybody ever saw, so it is debug — present when asked for, quiet
// otherwise.
func TestHealthChecksAreQuietUnlessAskedFor(t *testing.T) {
	h, lines := serve(t, slog.LevelInfo)

	get(t, h, "/healthz")

	assert.Empty(t, lines(), "health checks would drown out everything else")
}

func TestAPathNobodyServesIsStillLogged(t *testing.T) {
	h, lines := serve(t, slog.LevelInfo)

	require.Equal(t, http.StatusNotFound, get(t, h, "/wp-login.php").Code)

	entries := lines()
	require.Len(t, entries, 1, "a request for an unknown path left no trace")
	assert.Equal(t, "/wp-login.php", entries[0]["path"])
	assert.Equal(t, float64(404), entries[0]["status"])
}

// A browser asks before it posts. If the preflight is being refused, nothing
// downstream ever happens and the form looks broken for no visible reason.
func TestThePreflightIsLogged(t *testing.T) {
	h, lines := serve(t, slog.LevelInfo)

	req := httptest.NewRequest(http.MethodOptions, "/contact", nil)
	req.Header.Set("Origin", "https://www.example.com")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	require.Equal(t, http.StatusNoContent, res.Code)
	entries := lines()
	require.NotEmpty(t, entries)
	assert.Equal(t, "OPTIONS", entries[0]["method"])
	assert.Equal(t, float64(204), entries[0]["status"])
}

// Level by outcome, so "show me what is going wrong" is a filter rather than a
// read-through.
func TestTheLevelFollowsTheOutcome(t *testing.T) {
	h, lines := serve(t, slog.LevelDebug)

	get(t, h, "/healthz") // debug
	get(t, h, "/nowhere") // 404, warn
	get(t, h, "/contact") // 405, warn — and a reason line from the handler too

	// Access lines only. The contact handler adds its own for anything it
	// refuses, which is the point of TestARefusalGetsBothAnAccessLineAndAReason
	// and only noise here.
	var access []entry
	for _, e := range lines() {
		if e["msg"] == "request" {
			access = append(access, e)
		}
	}

	require.Len(t, access, 3)
	assert.Equal(t, "DEBUG", access[0]["level"], "/healthz")
	assert.Equal(t, "WARN", access[1]["level"], "404")
	assert.Equal(t, "WARN", access[2]["level"], "405")
}

// The access line says what happened; the handler's own line says why. Both are
// wanted — one is for counting, the other for acting on.
func TestARefusalGetsBothAnAccessLineAndAReason(t *testing.T) {
	h, lines := serve(t, slog.LevelInfo)

	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://someone-else.example")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	require.Equal(t, http.StatusForbidden, res.Code)

	var access, reason bool
	for _, e := range lines() {
		switch e["msg"] {
		case "request":
			access = true
			assert.Equal(t, float64(403), e["status"])
		case "refused submission":
			reason = true
			assert.Equal(t, "origin not allowed", e["reason"])
		}
	}
	assert.True(t, access, "no access line")
	assert.True(t, reason, "no reason line")
}
