// SPDX-License-Identifier: GPL-3.0-or-later

package contact_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alrayyes/form-handler/internal/contact"
)

const origin = "https://www.andthensome.nl"

// recorder is the fake for the port the handler consumes. Only the unit tests
// use it; the integration test drives a real mail server instead.
type recorder struct {
	mu   sync.Mutex
	sent []contact.Message
	err  error
}

func (r *recorder) Send(_ context.Context, m contact.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, m)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

func post(t *testing.T, h http.Handler, body, org string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if org != "" {
		req.Header.Set("Origin", org)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

const goodBody = `{"name":"Ada","email":"ada@example.com","message":"A message long enough to pass."}`

func newHandler(m contact.Mailer, perHour int) *contact.Handler {
	return contact.NewHandler(m, slog.New(slog.DiscardHandler), []string{origin}, perHour)
}

func TestPostSendsTheMessage(t *testing.T) {
	mailer := &recorder{}

	res := post(t, newHandler(mailer, 100), goodBody, origin)

	require.Equal(t, http.StatusAccepted, res.Code, res.Body.String())
	assert.Equal(t, 1, mailer.count())
}

func TestOriginsOtherThanOursAreRefused(t *testing.T) {
	mailer := &recorder{}

	res := post(t, newHandler(mailer, 100), goodBody, "https://someone-else.example")

	require.Equal(t, http.StatusForbidden, res.Code)
	// The point: refusing the CORS header is not enough, because the request
	// still arrived and would still have sent mail.
	assert.Zero(t, mailer.count(), "a disallowed origin still sent mail")
}

func TestPreflightIsAnswered(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/contact", nil)
	req.Header.Set("Origin", origin)
	res := httptest.NewRecorder()

	newHandler(&recorder{}, 100).ServeHTTP(res, req)

	require.Equal(t, http.StatusNoContent, res.Code)
	assert.Equal(t, origin, res.Header().Get("Access-Control-Allow-Origin"))
}

func TestHoneypotLooksExactlyLikeSuccess(t *testing.T) {
	mailer := &recorder{}
	body := `{"name":"Bot","email":"bot@example.com","message":"Cheap watches for sale.","website":"http://spam.example"}`

	spam := post(t, newHandler(mailer, 100), body, origin)
	good := post(t, newHandler(&recorder{}, 100), goodBody, origin)

	assert.Equal(t, good.Code, spam.Code, "the difference tells a bot what to change")
	assert.Equal(t, good.Body.String(), spam.Body.String())
	assert.Zero(t, mailer.count(), "honeypot submission was delivered")
}

func TestValidationErrorNamesTheField(t *testing.T) {
	res := post(t, newHandler(&recorder{}, 100), `{"name":"","email":"ada@example.com","message":"long enough to pass"}`, origin)

	require.Equal(t, http.StatusUnprocessableEntity, res.Code)
	assert.Contains(t, res.Body.String(), `"field":"name"`)
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	res := post(t, newHandler(&recorder{}, 100), `{"name":"Ada","email":"ada@example.com","message":"long enough here","admin":true}`, origin)

	require.Equal(t, http.StatusBadRequest, res.Code)
}

func TestRateLimitStopsRepeatedSubmissions(t *testing.T) {
	mailer := &recorder{}
	h := newHandler(mailer, 2)

	for i := range 2 {
		res := post(t, h, goodBody, origin)
		require.Equalf(t, http.StatusAccepted, res.Code, "submission %d", i+1)
	}

	res := post(t, h, goodBody, origin)

	require.Equal(t, http.StatusTooManyRequests, res.Code, "third submission")
	assert.Equal(t, 2, mailer.count())
}

func TestSendFailureIsReportedNotSwallowed(t *testing.T) {
	mailer := &recorder{err: errors.New("mail server is down")}

	res := post(t, newHandler(mailer, 100), goodBody, origin)

	require.Equal(t, http.StatusBadGateway, res.Code)
	// A form that says "thanks" and drops the message is worse than one that
	// says it failed.
	assert.NotContains(t, res.Body.String(), "accepted", "a failed send reported success")
}

func TestGetIsNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/contact", nil)
	req.Header.Set("Origin", origin)
	res := httptest.NewRecorder()

	newHandler(&recorder{}, 100).ServeHTTP(res, req)

	require.Equal(t, http.StatusMethodNotAllowed, res.Code)
}
