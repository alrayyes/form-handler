// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

// The outer test for the Mailgun adapter.
//
// Every other integration test in this repository drives a real server in a
// container, because a real one exists: Mailpit speaks SMTP, so the SMTP
// adapter is tested against the actual protocol. Mailgun is a hosted HTTP API
// and has no runnable equivalent. The options are the live API — real mail,
// real money, real credentials in CI — or something standing in for it, and
// anything standing in for it is code written here whether it runs in a
// container or in this process. A container would add a hop without adding a
// single byte of fidelity.
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alrayyes/form-handler/internal/domain"
	"github.com/alrayyes/form-handler/internal/mail/mailgun"
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

		require.NoError(t, r.ParseMultipartForm(1<<20), "Mailgun sends a multipart form")
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

	err = sender.Send(context.Background(), domain.Message{
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
	require.NoError(t, sender.Send(context.Background(), domain.Message{
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
	baseURL, _ := stubMailgun(t, http.StatusUnauthorized, `{"message":"Invalid private key"}`)

	sender := mustSender(t, baseURL)
	err := sender.Send(context.Background(), domain.Message{
		Name: "Ada", Email: "ada@example.com", Subject: "s", Body: "b",
	})

	require.Error(t, err)

	// Typed, so the handler can say which provider failed and at which step
	// without parsing a string.
	var de *domain.DeliveryError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, "mailgun", de.Provider)
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
