// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

// The outer test. It runs a real mail server in a container, posts real
// requests at the real composition root, and asserts real messages arrived —
// because the parts that break here are the ones between the pieces: header
// encoding, CRLF handling, whether the visitor's address ends up somewhere a
// reply works, and whether two forms on two domains stay told apart. A fake
// Mailer proves none of that.
package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/alrayyes/form-handler/internal/config"
	"github.com/alrayyes/form-handler/internal/server"
)

// Pinned by digest, like every other image this repo pulls. A tag can be moved
// under you, and a mail server that quietly changed version underneath the one
// test that proves delivery works is a confusing way to spend an afternoon.
const mailpitImage = "axllent/mailpit:v1.21.8@sha256:81370195cd4a0eab9604d17c2617a7525b0486f9365555253b6c5376c6350f1a"

// Two forms on two different sites, which is the whole point of the routing:
// one service, one endpoint prefix, submissions kept apart by where they came
// from and where they go.
const (
	marketingOrigin = "https://www.example.com"
	careersOrigin   = "https://careers.example.org"
)

func testConfig(smtpAddr string) config.Config {
	return config.Config{
		SMTP: config.SMTP{Addr: smtpAddr, Timeout: 10 * time.Second},
		Forms: []config.Form{
			{
				ID:               "marketing",
				Origins:          []string{marketingOrigin},
				From:             "site@example.com",
				To:               "info@example.com",
				Subject:          "Contact form: {{ .Name }}",
				RateLimitPerHour: 100,
			},
			{
				ID:               "careers",
				Origins:          []string{careersOrigin},
				From:             "site@example.com",
				To:               "jobs@example.com",
				Subject:          "Application from {{ .Name }}",
				RateLimitPerHour: 100,
			},
		},
	}
}

func TestEachFormDeliversToItsOwnInbox(t *testing.T) {
	ctx := context.Background()
	smtpAddr, apiURL := startMailpit(t, ctx)
	srv := start(t, testConfig(smtpAddr))

	post(t, ctx, srv.URL+"/contact/marketing", marketingOrigin,
		`{"name":"Ada Lovelace","email":"ada@example.com","message":"Please get in touch about an awkward system.","website":""}`,
		http.StatusAccepted)
	post(t, ctx, srv.URL+"/contact/careers", careersOrigin,
		`{"name":"Grace Hopper","email":"grace@example.com","message":"I would like to apply for the compiler role.","website":""}`,
		http.StatusAccepted)

	marketing := waitForMessageTo(t, ctx, apiURL, "info@example.com")
	careers := waitForMessageTo(t, ctx, apiURL, "jobs@example.com")

	// Each form's own subject template, not one shared line.
	assert.Equal(t, "Contact form: Ada Lovelace", marketing.Subject)
	assert.Equal(t, "Application from Grace Hopper", careers.Subject)

	// The whole point of Reply-To: hitting reply must reach the visitor, not
	// the address the service sends as.
	require.Len(t, marketing.ReplyTo, 1)
	assert.Equal(t, "ada@example.com", marketing.ReplyTo[0].Address)
	require.Len(t, careers.ReplyTo, 1)
	assert.Equal(t, "grace@example.com", careers.ReplyTo[0].Address)

	assert.Equal(t, "site@example.com", marketing.From.Address)
	assert.Contains(t, marketing.Text, "awkward system")
	assert.Contains(t, careers.Text, "compiler role")
}

// The reason origins are per form rather than global: a site that may post to
// its own form must not be able to post to somebody else's.
func TestAFormRefusesAnotherFormsOrigin(t *testing.T) {
	ctx := context.Background()
	smtpAddr, apiURL := startMailpit(t, ctx)
	srv := start(t, testConfig(smtpAddr))

	post(t, ctx, srv.URL+"/contact/marketing", careersOrigin,
		`{"name":"Ada Lovelace","email":"ada@example.com","message":"Posting this at the wrong form entirely.","website":""}`,
		http.StatusForbidden)

	time.Sleep(2 * time.Second)
	assert.Zero(t, messageCount(t, ctx, apiURL), "a refused origin still sent mail")
}

func TestAnUnknownFormIsNotFound(t *testing.T) {
	ctx := context.Background()
	smtpAddr, _ := startMailpit(t, ctx)
	srv := start(t, testConfig(smtpAddr))

	post(t, ctx, srv.URL+"/contact/nonexistent", marketingOrigin,
		`{"name":"Ada Lovelace","email":"ada@example.com","message":"Nobody is listening at this address.","website":""}`,
		http.StatusNotFound)
}

func TestHoneypotIsAcceptedButNotDelivered(t *testing.T) {
	ctx := context.Background()
	smtpAddr, apiURL := startMailpit(t, ctx)
	srv := start(t, testConfig(smtpAddr))

	// Indistinguishable from success, on purpose.
	post(t, ctx, srv.URL+"/contact/marketing", marketingOrigin,
		`{"name":"Bot","email":"bot@example.com","message":"Cheap watches, buy now please.","website":"http://spam.example"}`,
		http.StatusAccepted)

	// Give it long enough that a delivery would have shown up.
	time.Sleep(2 * time.Second)
	assert.Zero(t, messageCount(t, ctx, apiURL), "the honeypot submission was delivered")
}

func TestHealthzAnswersWithoutTouchingSMTP(t *testing.T) {
	ctx := context.Background()
	srv := start(t, testConfig("127.0.0.1:1"))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/healthz", nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()

	// The SMTP address above is deliberately unreachable: liveness must not
	// depend on the mail server being up.
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func start(t *testing.T, cfg config.Config) *httptest.Server {
	t.Helper()
	h, err := server.New(cfg, slog.New(slog.DiscardHandler))
	require.NoError(t, err, "build the server")
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, ctx context.Context, url, origin, body string, want int) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != want {
		got, _ := io.ReadAll(res.Body)
		require.Equalf(t, want, res.StatusCode, "POST %s: body %s", url, got)
	}
}

// startMailpit returns a Mailpit to test against, however this machine can get
// one. Locally that means testcontainers starts a fresh container per test.
//
// CI cannot: testcontainers needs a Docker daemon inside the job, and handing a
// job one means either a privileged sidecar or the host's socket. Neither is a
// trade worth making for a contact form, so the workflow runs Mailpit as an
// ordinary service container and points these variables at it.
//
// The tests are identical either way; only who started the mail server differs.
func startMailpit(t *testing.T, ctx context.Context) (smtpAddr, apiURL string) {
	t.Helper()

	if addr, api := os.Getenv("MAILPIT_SMTP_ADDR"), os.Getenv("MAILPIT_API_URL"); addr != "" && api != "" {
		// One server for the whole run, unlike the container case where each
		// test gets its own. Empty it first, or a test counts the message the
		// previous one sent and reports a leak that isn't.
		deleteAllMessages(t, ctx, api)
		return addr, api
	}

	req := testcontainers.ContainerRequest{
		Image:        mailpitImage,
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor:   wait.ForListeningPort("1025/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "start mailpit")
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	require.NoError(t, err, "container host")
	smtpPort, err := c.MappedPort(ctx, "1025")
	require.NoError(t, err, "smtp port")
	apiPort, err := c.MappedPort(ctx, "8025")
	require.NoError(t, err, "api port")

	return fmt.Sprintf("%s:%s", host, smtpPort.Port()),
		fmt.Sprintf("http://%s:%s", host, apiPort.Port())
}

type mailpitAddress struct {
	Address string `json:"Address"`
}

type mailpitMessage struct {
	Subject string           `json:"Subject"`
	From    mailpitAddress   `json:"From"`
	To      []mailpitAddress `json:"To"`
	ReplyTo []mailpitAddress `json:"ReplyTo"`
	Text    string           `json:"Text"`
}

// waitForMessageTo finds the message delivered to one recipient. Both forms
// deliver to the same server, so "the first message" is not good enough — the
// test has to name which inbox it means.
func waitForMessageTo(t *testing.T, ctx context.Context, apiURL, recipient string) mailpitMessage {
	t.Helper()

	var found mailpitMessage
	require.Eventuallyf(t, func() bool {
		var list struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}
		getJSON(t, ctx, apiURL+"/api/v1/messages", &list)

		for _, m := range list.Messages {
			var msg mailpitMessage
			getJSON(t, ctx, apiURL+"/api/v1/message/"+m.ID, &msg)
			for _, to := range msg.To {
				if to.Address == recipient {
					found = msg
					return true
				}
			}
		}
		return false
	}, 20*time.Second, 250*time.Millisecond, "no message arrived for %s", recipient)

	return found
}

func messageCount(t *testing.T, ctx context.Context, apiURL string) int {
	t.Helper()
	var list struct {
		Total int `json:"total"`
	}
	getJSON(t, ctx, apiURL+"/api/v1/messages", &list)
	return list.Total
}

// deleteAllMessages empties the mailbox, so a shared Mailpit starts each test
// as clean as a fresh container would.
func deleteAllMessages(t *testing.T, ctx context.Context, apiURL string) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL+"/api/v1/messages", nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "empty the mailbox")
	defer func() { _ = res.Body.Close() }()
	require.Equalf(t, http.StatusOK, res.StatusCode, "empty the mailbox")
}

func getJSON(t *testing.T, ctx context.Context, url string, into any) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoErrorf(t, err, "get %s", url)
	defer func() { _ = res.Body.Close() }()
	require.NoErrorf(t, json.NewDecoder(res.Body).Decode(into), "decode %s", url)
}
