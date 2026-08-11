// SPDX-License-Identifier: GPL-3.0-or-later

// What is left to test at this layer once the rules moved out: the translation.
// Which status code each outcome becomes, what CORS answers, and that the body
// is JSON. The rules themselves — who may post, what counts as valid, when the
// limit bites — are tested in internal/usecase, by calling functions.
package http_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adapterhttp "github.com/alrayyes/form-handler/internal/adapter/http"
	"github.com/alrayyes/form-handler/internal/adapter/ratelimit"
	"github.com/alrayyes/form-handler/internal/clientip"
	"github.com/alrayyes/form-handler/internal/domain"
	"github.com/alrayyes/form-handler/internal/usecase"
)

const origin = "https://www.example.com"

const goodBody = `{"name":"Ada","email":"ada@example.com","message":"A message long enough to pass."}`

// recorder is the fake for the Mailer port.
type recorder struct {
	mu   sync.Mutex
	sent []domain.Message
	err  error
}

func (r *recorder) Send(_ context.Context, m domain.Message) error {
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

func build(t *testing.T, m usecase.Mailer, perHour int, log *slog.Logger) *adapterhttp.Handler {
	t.Helper()
	submit, err := usecase.NewSubmit(
		domain.Form{ID: "marketing", Origins: []string{origin}, RatePerHour: perHour},
		m, ratelimit.New(perHour, time.Hour))
	require.NoError(t, err)
	return adapterhttp.NewHandler(submit, log, clientip.Resolver{})
}

func newHandler(t *testing.T, m usecase.Mailer, perHour int) *adapterhttp.Handler {
	t.Helper()
	return build(t, m, perHour, slog.New(slog.DiscardHandler))
}

func post(t *testing.T, h http.Handler, body, org string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/contact/marketing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if org != "" {
		req.Header.Set("Origin", org)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func TestAnAcceptedSubmissionIs202(t *testing.T) {
	mailer := &recorder{}

	res := post(t, newHandler(t, mailer, 100), goodBody, origin)

	require.Equal(t, http.StatusAccepted, res.Code, res.Body.String())
	assert.Equal(t, 1, mailer.count())
}

// The mapping table, which is this layer's whole opinion.
func TestEachOutcomeGetsItsStatus(t *testing.T) {
	cases := map[string]struct {
		body   string
		origin string
		want   int
	}{
		"refused origin":     {goodBody, "https://someone-else.example", http.StatusForbidden},
		"unreadable body":    {`{"name":`, origin, http.StatusBadRequest},
		"unknown field":      {`{"name":"Ada","email":"a@example.com","message":"long enough here","admin":true}`, origin, http.StatusBadRequest},
		"invalid submission": {`{"name":"","email":"a@example.com","message":"long enough to pass"}`, origin, http.StatusUnprocessableEntity},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := post(t, newHandler(t, &recorder{}, 100), tc.body, tc.origin)

			assert.Equal(t, tc.want, res.Code, res.Body.String())
		})
	}
}

func TestTheRateLimitBecomes429(t *testing.T) {
	h := newHandler(t, &recorder{}, 1)

	require.Equal(t, http.StatusAccepted, post(t, h, goodBody, origin).Code)
	res := post(t, h, goodBody, origin)

	assert.Equal(t, http.StatusTooManyRequests, res.Code)
}

// Same status, same body, byte for byte. Telling a bot the honeypot fired only
// teaches whoever wrote it which field to leave alone next time.
func TestTheHoneypotLooksExactlyLikeSuccess(t *testing.T) {
	mailer := &recorder{}
	body := `{"name":"Bot","email":"bot@example.com","message":"Cheap watches for sale.","website":"http://spam.example"}`

	spam := post(t, newHandler(t, mailer, 100), body, origin)
	good := post(t, newHandler(t, &recorder{}, 100), goodBody, origin)

	assert.Equal(t, good.Code, spam.Code, "the difference tells a bot what to change")
	assert.Equal(t, good.Body.String(), spam.Body.String())
	assert.Zero(t, mailer.count(), "honeypot submission was delivered")
}

func TestAValidationErrorNamesTheField(t *testing.T) {
	res := post(t, newHandler(t, &recorder{}, 100),
		`{"name":"","email":"ada@example.com","message":"long enough to pass"}`, origin)

	require.Equal(t, http.StatusUnprocessableEntity, res.Code)
	assert.Contains(t, res.Body.String(), `"field":"name"`)
}

func TestADeliveryFailureBecomes502(t *testing.T) {
	mailer := &recorder{err: domain.Undeliverable("smtp", "dial", errors.New("mail server is down"))}

	res := post(t, newHandler(t, mailer, 100), goodBody, origin)

	require.Equal(t, http.StatusBadGateway, res.Code)
	// A form that says "thanks" and drops the message is worse than one that
	// says it failed.
	assert.NotContains(t, res.Body.String(), "accepted", "a failed send reported success")
}

func TestPreflightIsAnswered(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/contact/marketing", nil)
	req.Header.Set("Origin", origin)
	res := httptest.NewRecorder()

	newHandler(t, &recorder{}, 100).ServeHTTP(res, req)

	require.Equal(t, http.StatusNoContent, res.Code)
	assert.Equal(t, origin, res.Header().Get("Access-Control-Allow-Origin"))
}

func TestGetIsNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/contact/marketing", nil)
	req.Header.Set("Origin", origin)
	res := httptest.NewRecorder()

	newHandler(t, &recorder{}, 100).ServeHTTP(res, req)

	require.Equal(t, http.StatusMethodNotAllowed, res.Code)
}
