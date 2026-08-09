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

	if res.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", res.Code, res.Body)
	}
	if mailer.count() != 1 {
		t.Fatalf("sent %d messages, want 1", mailer.count())
	}
}

func TestOriginsOtherThanOursAreRefused(t *testing.T) {
	mailer := &recorder{}

	res := post(t, newHandler(mailer, 100), goodBody, "https://someone-else.example")

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.Code)
	}
	// The point: refusing the CORS header is not enough, because the request
	// still arrived and would still have sent mail.
	if mailer.count() != 0 {
		t.Fatal("a disallowed origin still sent mail")
	}
}

func TestPreflightIsAnswered(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/contact", nil)
	req.Header.Set("Origin", origin)
	res := httptest.NewRecorder()

	newHandler(&recorder{}, 100).ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", res.Code)
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("allow-origin = %q, want %q", got, origin)
	}
}

func TestHoneypotLooksExactlyLikeSuccess(t *testing.T) {
	mailer := &recorder{}
	body := `{"name":"Bot","email":"bot@example.com","message":"Cheap watches for sale.","website":"http://spam.example"}`

	spam := post(t, newHandler(mailer, 100), body, origin)
	good := post(t, newHandler(&recorder{}, 100), goodBody, origin)

	if spam.Code != good.Code {
		t.Errorf("spam status %d, real status %d — the difference tells a bot what to change", spam.Code, good.Code)
	}
	if spam.Body.String() != good.Body.String() {
		t.Errorf("spam body %q, real body %q", spam.Body.String(), good.Body.String())
	}
	if mailer.count() != 0 {
		t.Fatal("honeypot submission was delivered")
	}
}

func TestValidationErrorNamesTheField(t *testing.T) {
	res := post(t, newHandler(&recorder{}, 100), `{"name":"","email":"ada@example.com","message":"long enough to pass"}`, origin)

	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"field":"name"`) {
		t.Errorf("body does not name the field: %s", res.Body)
	}
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	res := post(t, newHandler(&recorder{}, 100), `{"name":"Ada","email":"ada@example.com","message":"long enough here","admin":true}`, origin)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
}

func TestRateLimitStopsRepeatedSubmissions(t *testing.T) {
	mailer := &recorder{}
	h := newHandler(mailer, 2)

	for i := range 2 {
		if res := post(t, h, goodBody, origin); res.Code != http.StatusAccepted {
			t.Fatalf("submission %d: status = %d, want 202", i+1, res.Code)
		}
	}

	res := post(t, h, goodBody, origin)

	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("third submission: status = %d, want 429", res.Code)
	}
	if mailer.count() != 2 {
		t.Fatalf("sent %d messages, want 2", mailer.count())
	}
}

func TestSendFailureIsReportedNotSwallowed(t *testing.T) {
	mailer := &recorder{err: errors.New("mail server is down")}

	res := post(t, newHandler(mailer, 100), goodBody, origin)

	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", res.Code)
	}
	// A form that says "thanks" and drops the message is worse than one that
	// says it failed.
	if strings.Contains(res.Body.String(), "accepted") {
		t.Errorf("a failed send reported success: %s", res.Body)
	}
}

func TestGetIsNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/contact", nil)
	req.Header.Set("Origin", origin)
	res := httptest.NewRecorder()

	newHandler(&recorder{}, 100).ServeHTTP(res, req)

	if res.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", res.Code)
	}
}
