// SPDX-License-Identifier: GPL-3.0-or-later

// The Mailgun adapter's own tests.
//
// The SMTP adapter's tests run against Mailpit, a real server in a container,
// because a real one exists to run. Mailgun is a hosted HTTP API with no
// runnable equivalent: the options are the live API — real mail, real money,
// real credentials in CI — or something standing in for it, and anything
// standing in for it is code written here whether it runs in a container or
// in this process. A container would add a hop without adding a single byte
// of fidelity, which is why this carries no build tag at all: it needs
// nothing more than an httptest.Server already gives it.
//
// So this drives the adapter over real HTTP, on a real socket, against a
// listener that asserts the exact request Mailgun would have received: the
// endpoint, the credentials, and every field of the multipart form. What it
// cannot prove is that Mailgun then does the right thing with it — that is what
// the first live send tells you, and no amount of local testing replaces it.
package mailgun_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/alrayyes/form-handler/internal/contact/mailertest"
	"github.com/alrayyes/form-handler/internal/mail/mailgun"
	mg "github.com/mailgun/mailgun-go/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capture is what the fake API saw, so the assertions read as "Mailgun was
// asked to send this" rather than as assertions about our own structs.
type capture struct {
	path     string
	user     string
	password string
	fields   map[string]string
}

// stubMailgun stands up an HTTP listener that answers the send endpoint the way
// the real API does, and records what it was sent.
func stubMailgun(t *testing.T, status int, body string) (baseURL string, got *capture) {
	t.Helper()

	got = &capture{fields: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.user, got.password, _ = r.BasicAuth()

		// require inside a handler goroutine would only kill that goroutine on
		// failure, not the test — assert plus an explicit return is what
		// actually stops it here.
		if !assert.NoError(t, r.ParseMultipartForm(1<<20), "Mailgun sends a multipart form") {
			return
		}
		for k, v := range r.MultipartForm.Value {
			if len(v) > 0 {
				got.fields[k] = v[0]
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv.URL, got
}

const accepted = `{"id":"<20260811.1@mg.example.com>","message":"Queued. Thank you."}`

const refused = `{"message":"Invalid private key"}`

// The contract every Mailer keeps, run against this one.
//
// This is the assertion that the two adapters are interchangeable where it
// matters. One speaks SMTP over a socket and the other HTTP to a hosted API,
// they fail at completely different things, and the handler must not be able to
// tell which it is holding — it reads Provider and Op off a DeliveryError and
// answers 502 either way.
func TestTheSenderKeepsTheMailerContract(t *testing.T) {
	mailertest.Contract(t, mailertest.Subject{
		Provider: "mailgun",
		Working: func(t *testing.T) contact.Mailer {
			t.Helper()

			baseURL, _ := stubMailgun(t, http.StatusOK, accepted)

			return mustSender(t, baseURL)
		},
		Failing: func(t *testing.T) contact.Mailer {
			t.Helper()

			baseURL, _ := stubMailgun(t, http.StatusUnauthorized, refused)

			return mustSender(t, baseURL)
		},
	})
}

func TestSendReachesMailgunAsThatDomain(t *testing.T) {
	baseURL, got := stubMailgun(t, http.StatusOK, accepted)

	sender, err := mailgun.New(mailgun.Config{
		Domain:  "mg.example.com",
		APIKey:  "key-secret",
		BaseURL: baseURL,
		From:    "site@mg.example.com",
		To:      "info@example.com",
		Timeout: 10 * time.Second,
	})
	require.NoError(t, err)

	err = sender.Send(context.Background(), contact.Message{
		Name:    "Ada Lovelace",
		Email:   "ada@example.com",
		Subject: "Contact form: Ada Lovelace",
		Body:    "Please get in touch about an awkward system.",
	})

	require.NoError(t, err)

	// The domain is in the path, which is what makes one login per sending
	// domain work at all.
	assert.Contains(t, got.path, "mg.example.com")
	assert.Equal(t, "api", got.user)
	assert.Equal(t, "key-secret", got.password)

	assert.Equal(t, "site@mg.example.com", got.fields["from"])
	assert.Equal(t, "info@example.com", got.fields["to"])
	assert.Equal(t, "Contact form: Ada Lovelace", got.fields["subject"])
	assert.Contains(t, got.fields["text"], "awkward system")
	// Same rule as the SMTP adapter: the visitor's address is where a reply
	// goes, never who the mail claims to be from. Sending as them fails SPF for
	// their domain and files the whole thing as spam.
	assert.Equal(t, "ada@example.com", got.fields["h:Reply-To"])
	assert.NotEqual(t, "ada@example.com", got.fields["from"])
}

// The submitter's name and address belong in the body too, or whoever reads the
// inbox has a message with no idea who sent it beyond a header.
func TestSendCarriesWhoSubmittedItInTheBody(t *testing.T) {
	baseURL, got := stubMailgun(t, http.StatusOK, accepted)

	sender := mustSender(t, baseURL)
	require.NoError(t, sender.Send(context.Background(), contact.Message{
		Name:    "Ada Lovelace",
		Email:   "ada@example.com",
		Subject: "Contact form: Ada Lovelace",
		Body:    "Please get in touch.",
	}))

	assert.Contains(t, got.fields["text"], "Ada Lovelace")
	assert.Contains(t, got.fields["text"], "ada@example.com")
}

// A refused send must be reported, not swallowed. A form that says "thanks" and
// drops the message is worse than one that says it failed.
func TestARefusedSendIsAnError(t *testing.T) {
	baseURL, _ := stubMailgun(t, http.StatusUnauthorized, refused)

	sender := mustSender(t, baseURL)
	err := sender.Send(context.Background(), contact.Message{
		Name: "Ada", Email: "ada@example.com", Subject: "s", Body: "b",
	})

	require.Error(t, err)

	// Typed, so the handler can say which provider failed and at which step
	// without parsing a string.
	var de *contact.DeliveryError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "mailgun", de.Provider)
}

// The shared contract can only ask whether something is under the
// DeliveryError, because it does not know what this adapter talks to. Here we
// do: Mailgun's own error carries the status code it got back, and the
// difference between a 401 and a 429 is the difference between a key somebody
// has to replace and a wait. Wrapping has to leave that reachable, so the
// SMTP adapter's textproto reply and this stay equally recoverable.
func TestARefusalKeepsMailgunsOwnStatusReachable(t *testing.T) {
	baseURL, _ := stubMailgun(t, http.StatusUnauthorized, refused)

	err := mustSender(t, baseURL).Send(context.Background(), contact.Message{
		Name: "Ada", Email: "ada@example.com", Subject: "s", Body: "b",
	})

	var ue *mg.UnexpectedResponseError
	require.ErrorAs(t, err, &ue, "Mailgun's own error was flattened into a string")
	assert.Equal(t, http.StatusUnauthorized, ue.Actual)
}

func TestConfigIsCheckedWhenTheSenderIsBuilt(t *testing.T) {
	cases := map[string]mailgun.Config{
		"no domain":  {APIKey: "k", From: "a@example.com", To: "b@example.com"},
		"no api key": {Domain: "mg.example.com", From: "a@example.com", To: "b@example.com"},
		"no from":    {Domain: "mg.example.com", APIKey: "k", To: "b@example.com"},
		"no to":      {Domain: "mg.example.com", APIKey: "k", From: "a@example.com"},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := mailgun.New(cfg)

			require.Error(t, err, "an unusable sender was built anyway")
		})
	}
}

// Sending to the wrong region fails authentication rather than redirecting, so
// a typo here is worth refusing at startup rather than on the first
// submission.
func TestAnUnknownRegionIsRefused(t *testing.T) {
	_, err := mailgun.New(mailgun.Config{
		Domain: "mg.example.com",
		APIKey: "key-secret",
		Region: "antarctica",
		From:   "site@mg.example.com",
		To:     "info@example.com",
	})

	require.ErrorIs(t, err, mailgun.ErrBadRegion)
}

// A timeout is the network having a bad minute, not a rejected key — the
// handler tells the two apart by Op, and this is the only path that produces
// "timeout" rather than "send".
func TestATimeoutIsReportedAsSuchNotAsARefusal(t *testing.T) {
	blocks := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-blocks
	}))
	// Cleanups run LIFO, and Close waits for the handler goroutine above to
	// return - so blocks has to close first, or Close blocks forever waiting
	// on a handler that is waiting right back on it.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(blocks) })

	sender, err := mailgun.New(mailgun.Config{
		Domain:  "mg.example.com",
		APIKey:  "key-secret",
		BaseURL: srv.URL,
		From:    "site@mg.example.com",
		To:      "info@example.com",
		Timeout: 10 * time.Millisecond,
	})
	require.NoError(t, err)

	err = sender.Send(context.Background(), contact.Message{
		Name: "Ada", Email: "ada@example.com", Subject: "s", Body: "b",
	})

	var de *contact.DeliveryError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "timeout", de.Op, "a deadline exceeded was reported as a generic send failure")
}

func mustSender(t *testing.T, baseURL string) *mailgun.Sender {
	t.Helper()
	s, err := mailgun.New(mailgun.Config{
		Domain:  "mg.example.com",
		APIKey:  "key-secret",
		BaseURL: baseURL,
		From:    "site@mg.example.com",
		To:      "info@example.com",
	})
	require.NoError(t, err)

	return s
}
