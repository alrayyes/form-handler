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

// Two forms pointed at two different mail servers, which is what a provider
// like Mailgun forces: each sending domain has its own login, so "which server
// and which credentials" is a property of the form, not of the service.
func testConfig(marketingSMTP, careersSMTP string) config.Config {
	return config.Config{
		Forms: []config.Form{
			{
				ID:               "marketing",
				Origins:          []string{marketingOrigin},
				From:             "site@example.com",
				To:               "info@example.com",
				Subject:          "Contact form: {{ .Name }}",
				RateLimitPerHour: 100,
				SMTP:             &config.SMTP{Addr: marketingSMTP, Timeout: 10 * time.Second},
			},
			{
				ID:               "careers",
				Origins:          []string{careersOrigin},
				From:             "site@example.com",
				To:               "jobs@example.com",
				Subject:          "Application from {{ .Name }}",
				RateLimitPerHour: 100,
				SMTP:             &config.SMTP{Addr: careersSMTP, Timeout: 10 * time.Second},
			},
		},
	}
}

// The whole point of per-form SMTP: each form's mail leaves through its own
// server, so a message posted at one form cannot turn up on the other's.
func TestEachFormSendsThroughItsOwnServer(t *testing.T) {
	ctx := context.Background()
	marketingSMTP, marketingAPI := startMailpit(ctx, t, 1)
	careersSMTP, careersAPI := startMailpit(ctx, t, 2)
	srv := start(t, testConfig(marketingSMTP, careersSMTP))

	post(ctx, t, srv.URL+"/contact/careers", careersOrigin,
		`{"name":"Grace Hopper","email":"grace@example.com","message":"I would like to apply for the compiler role.","website":""}`,
		http.StatusAccepted)

	careers := waitForMessageTo(ctx, t, careersAPI, "jobs@example.com")
	assert.Equal(t, "Application from Grace Hopper", careers.Subject)

	// The marketing server saw nothing, because that form was not posted to.
	// A shared mailer with a switched recipient would pass every other
	// assertion in this file and fail this one.
	assert.Zero(t, messageCount(ctx, t, marketingAPI),
		"the careers form sent through the marketing server")
}

func TestEachFormDeliversToItsOwnInbox(t *testing.T) {
	ctx := context.Background()
	smtpAddr, apiURL := startMailpit(ctx, t, 1)
	srv := start(t, testConfig(smtpAddr, smtpAddr))

	post(ctx, t, srv.URL+"/contact/marketing", marketingOrigin,
		`{"name":"Ada Lovelace","email":"ada@example.com","message":"Please get in touch about an awkward system.","website":""}`,
		http.StatusAccepted)
	post(ctx, t, srv.URL+"/contact/careers", careersOrigin,
		`{"name":"Grace Hopper","email":"grace@example.com","message":"I would like to apply for the compiler role.","website":""}`,
		http.StatusAccepted)

	marketing := waitForMessageTo(ctx, t, apiURL, "info@example.com")
	careers := waitForMessageTo(ctx, t, apiURL, "jobs@example.com")

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
	smtpAddr, apiURL := startMailpit(ctx, t, 1)
	srv := start(t, testConfig(smtpAddr, smtpAddr))

	post(ctx, t, srv.URL+"/contact/marketing", careersOrigin,
		`{"name":"Ada Lovelace","email":"ada@example.com","message":"Posting this at the wrong form entirely.","website":""}`,
		http.StatusForbidden)

	time.Sleep(2 * time.Second)
	assert.Zero(t, messageCount(ctx, t, apiURL), "a refused origin still sent mail")
}

func TestAnUnknownFormIsNotFound(t *testing.T) {
	ctx := context.Background()
	smtpAddr, _ := startMailpit(ctx, t, 1)
	srv := start(t, testConfig(smtpAddr, smtpAddr))

	post(ctx, t, srv.URL+"/contact/nonexistent", marketingOrigin,
		`{"name":"Ada Lovelace","email":"ada@example.com","message":"Nobody is listening at this address.","website":""}`,
		http.StatusNotFound)
}

func TestHoneypotIsAcceptedButNotDelivered(t *testing.T) {
	ctx := context.Background()
	smtpAddr, apiURL := startMailpit(ctx, t, 1)
	srv := start(t, testConfig(smtpAddr, smtpAddr))

	// Indistinguishable from success, on purpose.
	post(ctx, t, srv.URL+"/contact/marketing", marketingOrigin,
		`{"name":"Bot","email":"bot@example.com","message":"Cheap watches, buy now please.","website":"http://spam.example"}`,
		http.StatusAccepted)

	// Give it long enough that a delivery would have shown up.
	time.Sleep(2 * time.Second)
	assert.Zero(t, messageCount(ctx, t, apiURL), "the honeypot submission was delivered")
}

func TestHealthzAnswersWithoutTouchingSMTP(t *testing.T) {
	ctx := context.Background()
	srv := start(t, testConfig("127.0.0.1:1", "127.0.0.1:1"))

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

func post(ctx context.Context, t *testing.T, url, origin, body string, want int) {
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
// The instance argument is why there are two sets of variables: proving that
// each form sends through its own server needs two servers, and in CI those
// are two service containers rather than two containers this test started.
func startMailpit(ctx context.Context, t *testing.T, instance int) (smtpAddr, apiURL string) {
	t.Helper()

	addrVar, apiVar := "MAILPIT_SMTP_ADDR", "MAILPIT_API_URL"
	if instance > 1 {
		addrVar = fmt.Sprintf("%s_%d", addrVar, instance)
		apiVar = fmt.Sprintf("%s_%d", apiVar, instance)
	}

	if addr, api := os.Getenv(addrVar), os.Getenv(apiVar); addr != "" && api != "" {
		// One server for the whole run, unlike the container case where each
		// test gets its own. Empty it first, or a test counts the message the
		// previous one sent and reports a leak that isn't.
		deleteAllMessages(ctx, t, api)
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
func waitForMessageTo(ctx context.Context, t *testing.T, apiURL, recipient string) mailpitMessage {
	t.Helper()

	var found mailpitMessage
	require.Eventuallyf(t, func() bool {
		var list struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}
		getJSON(ctx, t, apiURL+"/api/v1/messages", &list)

		for _, m := range list.Messages {
			var msg mailpitMessage
			getJSON(ctx, t, apiURL+"/api/v1/message/"+m.ID, &msg)
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

func messageCount(ctx context.Context, t *testing.T, apiURL string) int {
	t.Helper()
	var list struct {
		Total int `json:"total"`
	}
	getJSON(ctx, t, apiURL+"/api/v1/messages", &list)
	return list.Total
}

// deleteAllMessages empties the mailbox, so a shared Mailpit starts each test
// as clean as a fresh container would.
func deleteAllMessages(ctx context.Context, t *testing.T, apiURL string) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL+"/api/v1/messages", nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "empty the mailbox")
	defer func() { _ = res.Body.Close() }()
	require.Equalf(t, http.StatusOK, res.StatusCode, "empty the mailbox")
}

func getJSON(ctx context.Context, t *testing.T, url string, into any) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoErrorf(t, err, "get %s", url)
	defer func() { _ = res.Body.Close() }()
	require.NoErrorf(t, json.NewDecoder(res.Body).Decode(into), "decode %s", url)
}
