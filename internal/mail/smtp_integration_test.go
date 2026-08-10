// SPDX-License-Identifier: GPL-3.0-or-later

//go:build integration

// The outer test. It runs a real mail server in a container, posts a real
// request at a real handler, and asserts a real message arrived — because the
// parts that break here are the ones between the pieces: header encoding,
// CRLF handling, whether the visitor's address ends up somewhere a reply works.
// A fake Mailer proves none of that.
package mail_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/alrayyes/form-handler/internal/mail"
)

const origin = "https://www.andthensome.nl"

// Pinned by digest, like every other image this repo pulls. A tag can be moved
// under you, and a mail server that quietly changed version underneath the one
// test that proves delivery works is a confusing way to spend an afternoon.
const mailpitImage = "axllent/mailpit:v1.21.8@sha256:81370195cd4a0eab9604d17c2617a7525b0486f9365555253b6c5376c6350f1a"

func TestSubmissionArrivesAsEmail(t *testing.T) {
	ctx := context.Background()

	smtpAddr, apiURL := startMailpit(t, ctx)

	sender := mail.SMTP{
		Addr:    smtpAddr,
		From:    "site@example.com",
		To:      "info@example.com",
		Timeout: 10 * time.Second,
	}

	handler := contact.NewHandler(sender, slog.New(slog.DiscardHandler), []string{origin}, 100)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := `{"name":"Ada Lovelace","email":"ada@example.com","message":"Please get in touch about an awkward system.","website":""}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusAccepted {
		got, _ := io.ReadAll(res.Body)
		require.Equalf(t, http.StatusAccepted, res.StatusCode, "body %s", got)
	}

	msg := waitForMessage(t, ctx, apiURL)

	assert.Equal(t, "Contact form: Ada Lovelace", msg.Subject)
	// The whole point of Reply-To: hitting reply must reach the visitor, not
	// the address the service sends as.
	require.Len(t, msg.ReplyTo, 1)
	assert.Equal(t, "ada@example.com", msg.ReplyTo[0].Address)
	assert.Equal(t, "site@example.com", msg.From.Address)
	assert.Contains(t, msg.Text, "awkward system")
}

func TestHoneypotIsAcceptedButNotDelivered(t *testing.T) {
	ctx := context.Background()

	smtpAddr, apiURL := startMailpit(t, ctx)

	sender := mail.SMTP{Addr: smtpAddr, From: "site@example.com", To: "info@example.com"}
	handler := contact.NewHandler(sender, slog.New(slog.DiscardHandler), []string{origin}, 100)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := `{"name":"Bot","email":"bot@example.com","message":"Cheap watches, buy now please.","website":"http://spam.example"}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()

	// Indistinguishable from success, on purpose.
	require.Equal(t, http.StatusAccepted, res.StatusCode)

	// Give it long enough that a delivery would have shown up.
	time.Sleep(2 * time.Second)
	assert.Zero(t, messageCount(t, ctx, apiURL), "the honeypot submission was delivered")
}

func startMailpit(t *testing.T, ctx context.Context) (smtpAddr, apiURL string) {
	t.Helper()

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
	ReplyTo []mailpitAddress `json:"ReplyTo"`
	Text    string           `json:"Text"`
}

func waitForMessage(t *testing.T, ctx context.Context, apiURL string) mailpitMessage {
	t.Helper()

	var msg mailpitMessage
	require.Eventually(t, func() bool {
		var list struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}
		getJSON(t, ctx, apiURL+"/api/v1/messages", &list)
		if len(list.Messages) == 0 {
			return false
		}
		getJSON(t, ctx, apiURL+"/api/v1/message/"+list.Messages[0].ID, &msg)
		return true
	}, 20*time.Second, 250*time.Millisecond, "no message arrived")

	return msg
}

func messageCount(t *testing.T, ctx context.Context, apiURL string) int {
	t.Helper()
	var list struct {
		Total int `json:"total"`
	}
	getJSON(t, ctx, apiURL+"/api/v1/messages", &list)
	return list.Total
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
