// SPDX-License-Identifier: GPL-3.0-or-later

package contact_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alrayyes/form-handler/internal/clientip"
	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/alrayyes/form-handler/internal/contact/mailertest"
	"github.com/alrayyes/form-handler/internal/ratelimit"
)

const origin = "https://www.example.com"

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

func newHandler(t *testing.T, m contact.Mailer, perHour int) *contact.Handler {
	t.Helper()
	h, err := contact.NewHandler(contact.Form{
		ID:      "default",
		Origins: []string{origin},
	}, m, ratelimit.New(perHour, time.Hour), slog.New(slog.DiscardHandler), clientip.Resolver{})
	require.NoError(t, err)
	return h
}

func TestPostSendsTheMessage(t *testing.T) {
	mailer := mailertest.NewFake()

	res := post(t, newHandler(t, mailer, 100), goodBody, origin)

	require.Equal(t, http.StatusAccepted, res.Code, res.Body.String())
	assert.Equal(t, 1, mailer.Count())
}

func TestOriginsOtherThanOursAreRefused(t *testing.T) {
	mailer := mailertest.NewFake()

	res := post(t, newHandler(t, mailer, 100), goodBody, "https://someone-else.example")

	require.Equal(t, http.StatusForbidden, res.Code)
	// The point: refusing the CORS header is not enough, because the request
	// still arrived and would still have sent mail.
	assert.Zero(t, mailer.Count(), "a disallowed origin still sent mail")
}

func TestPreflightIsAnswered(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/contact", nil)
	req.Header.Set("Origin", origin)
	res := httptest.NewRecorder()

	newHandler(t, mailertest.NewFake(), 100).ServeHTTP(res, req)

	require.Equal(t, http.StatusNoContent, res.Code)
	assert.Equal(t, origin, res.Header().Get("Access-Control-Allow-Origin"))
}

func TestHoneypotLooksExactlyLikeSuccess(t *testing.T) {
	mailer := mailertest.NewFake()
	body := `{"name":"Bot","email":"bot@example.com","message":"Cheap watches for sale.","website":"http://spam.example"}`

	spam := post(t, newHandler(t, mailer, 100), body, origin)
	good := post(t, newHandler(t, mailertest.NewFake(), 100), goodBody, origin)

	assert.Equal(t, good.Code, spam.Code, "the difference tells a bot what to change")
	assert.Equal(t, good.Body.String(), spam.Body.String())
	assert.Zero(t, mailer.Count(), "honeypot submission was delivered")
}

func TestValidationErrorNamesTheField(t *testing.T) {
	res := post(t, newHandler(t, mailertest.NewFake(), 100), `{"name":"","email":"ada@example.com","message":"long enough to pass"}`, origin)

	require.Equal(t, http.StatusUnprocessableEntity, res.Code)
	assert.Contains(t, res.Body.String(), `"field":"name"`)
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	res := post(t, newHandler(t, mailertest.NewFake(), 100), `{"name":"Ada","email":"ada@example.com","message":"long enough here","admin":true}`, origin)

	require.Equal(t, http.StatusBadRequest, res.Code)
}

func TestRateLimitStopsRepeatedSubmissions(t *testing.T) {
	mailer := mailertest.NewFake()
	h := newHandler(t, mailer, 2)

	for i := range 2 {
		res := post(t, h, goodBody, origin)
		require.Equalf(t, http.StatusAccepted, res.Code, "submission %d", i+1)
	}

	res := post(t, h, goodBody, origin)

	require.Equal(t, http.StatusTooManyRequests, res.Code, "third submission")
	assert.Equal(t, 2, mailer.Count())
}

// The origin check comes first, and it has to. If the limiter counted refused
// requests, any page on the internet could spend a visitor's allowance for
// them: post twice from an origin this form does not serve, and the person it
// does serve gets a 429 from a form they have never used.
//
// The ordering is correct today. It is also invisible — it lives between the
// CORS headers and the JSON decoder in ServeHTTP, where nobody rearranging that
// function would think to look for it.
func TestARefusedOriginDoesNotConsumeTheRateLimit(t *testing.T) {
	h := newHandler(t, mailertest.NewFake(), 1)
	refused := post(t, h, goodBody, "https://someone-else.example")
	require.Equal(t, http.StatusForbidden, refused.Code, "the setup did not refuse the origin")

	res := post(t, h, goodBody, origin)

	assert.Equal(t, http.StatusAccepted, res.Code, "the refused attempt was counted against the limit")
}

func TestSendFailureIsReportedNotSwallowed(t *testing.T) {
	mailer := mailertest.NewFake().Breaks(errors.New("mail server is down"))

	res := post(t, newHandler(t, mailer, 100), goodBody, origin)

	require.Equal(t, http.StatusBadGateway, res.Code)
	// A form that says "thanks" and drops the message is worse than one that
	// says it failed.
	assert.NotContains(t, res.Body.String(), "accepted", "a failed send reported success")
}

// The subject is the form's, not the submission's: two forms on one service
// word it differently, which is half of why they are separate forms.
func TestTheFormsSubjectTemplateIsRendered(t *testing.T) {
	mailer := mailertest.NewFake()
	h, err := contact.NewHandler(contact.Form{
		ID:      "careers",
		Origins: []string{origin},
		Subject: "{{ .Form }}: {{ .Name }} <{{ .Email }}>",
	}, mailer, ratelimit.New(100, time.Hour), slog.New(slog.DiscardHandler), clientip.Resolver{})
	require.NoError(t, err)

	res := post(t, h, goodBody, origin)

	require.Equal(t, http.StatusAccepted, res.Code, res.Body.String())
	require.Equal(t, 1, mailer.Count())
	assert.Equal(t, "careers: Ada <ada@example.com>", mailer.Sent()[0].Subject)
}

func TestAFormWithNoSubjectGetsTheDefault(t *testing.T) {
	mailer := mailertest.NewFake()

	res := post(t, newHandler(t, mailer, 100), goodBody, origin)

	require.Equal(t, http.StatusAccepted, res.Code)
	require.Equal(t, 1, mailer.Count())
	assert.Equal(t, "Contact form: Ada", mailer.Sent()[0].Subject)
}

// The name reaches the subject line, and the name is whatever the visitor
// typed. A newline in a header is how injection works.
func TestSubjectCannotCarryAHeaderInjection(t *testing.T) {
	mailer := mailertest.NewFake()
	body := `{"name":"Ada\r\nBcc: everyone@example.com","email":"ada@example.com","message":"A message long enough to pass."}`

	res := post(t, newHandler(t, mailer, 100), body, origin)

	require.Equal(t, http.StatusAccepted, res.Code)
	require.Equal(t, 1, mailer.Count())
	assert.NotContains(t, mailer.Sent()[0].Subject, "\r")
	assert.NotContains(t, mailer.Sent()[0].Subject, "\n")
}

func TestABadSubjectTemplateIsRefusedAtStartup(t *testing.T) {
	_, err := contact.NewHandler(contact.Form{
		ID:      "broken",
		Origins: []string{origin},
		Subject: "Contact form: {{ .Name",
	}, mailertest.NewFake(), ratelimit.New(100, time.Hour), slog.New(slog.DiscardHandler), clientip.Resolver{})

	require.Error(t, err, "a template that cannot parse was accepted")
}

// Fail closed. "Nobody is allowed" must not be read as "everybody is" — that
// reading is how a contact form becomes an open relay for spam.
func TestAFormWithNoOriginsAcceptsNothing(t *testing.T) {
	mailer := mailertest.NewFake()
	h, err := contact.NewHandler(contact.Form{ID: "misconfigured"},
		mailer, ratelimit.New(100, time.Hour), slog.New(slog.DiscardHandler), clientip.Resolver{})
	require.NoError(t, err)

	res := post(t, h, goodBody, origin)

	require.Equal(t, http.StatusForbidden, res.Code)
	assert.Zero(t, mailer.Count())
}

// The bug in issue #6. X-Forwarded-For is set by whoever sent the request, so
// a service that believes it gives every caller a fresh rate-limit bucket per
// request just by varying the header.
func TestTheRateLimitCannotBeBypassedWithAForgedHeader(t *testing.T) {
	mailer := mailertest.NewFake()
	h := newHandler(t, mailer, 1)

	first := postAs(t, h, "198.51.100.1")
	second := postAs(t, h, "198.51.100.2")

	require.Equal(t, http.StatusAccepted, first.Code)
	require.Equal(t, http.StatusTooManyRequests, second.Code,
		"a second submission got its own bucket by claiming a different address")
	assert.Equal(t, 1, mailer.Count())
}

// And the reason it is read at all still works: a trusted proxy's header is
// believed, so two real visitors behind one proxy are not one client.
func TestBehindATrustedProxyVisitorsAreCountedSeparately(t *testing.T) {
	mailer := mailertest.NewFake()
	resolver, err := clientip.NewResolver([]string{"192.0.2.0/24"})
	require.NoError(t, err)
	h, err := contact.NewHandler(contact.Form{
		ID: "default", Origins: []string{origin},
	}, mailer, ratelimit.New(1, time.Hour), slog.New(slog.DiscardHandler), resolver)
	require.NoError(t, err)

	first := postAs(t, h, "198.51.100.1")
	second := postAs(t, h, "198.51.100.2")

	require.Equal(t, http.StatusAccepted, first.Code)
	assert.Equal(t, http.StatusAccepted, second.Code,
		"two visitors behind one proxy were counted as the same client")
}

// postAs sends from a fixed connection address while claiming, via
// X-Forwarded-For, to be somebody else.
func postAs(t *testing.T, h http.Handler, claimed string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(goodBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("X-Forwarded-For", claimed)
	req.RemoteAddr = "192.0.2.10:44321"
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func TestGetIsNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/contact", nil)
	req.Header.Set("Origin", origin)
	res := httptest.NewRecorder()

	newHandler(t, mailertest.NewFake(), 100).ServeHTTP(res, req)

	require.Equal(t, http.StatusMethodNotAllowed, res.Code)
}
