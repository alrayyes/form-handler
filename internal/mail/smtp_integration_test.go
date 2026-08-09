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

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/alrayyes/form-handler/internal/mail"
)

const origin = "https://www.andthensome.nl"

func TestSubmissionArrivesAsEmail(t *testing.T) {
	ctx := context.Background()

	smtpAddr, apiURL, terminate := startMailpit(t, ctx)
	defer terminate()

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
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusAccepted {
		got, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d, want 202; body %s", res.StatusCode, got)
	}

	msg := waitForMessage(t, ctx, apiURL)

	if want := "Contact form: Ada Lovelace"; msg.Subject != want {
		t.Errorf("subject = %q, want %q", msg.Subject, want)
	}
	// The whole point of Reply-To: hitting reply must reach the visitor, not
	// the address the service sends as.
	if len(msg.ReplyTo) != 1 || msg.ReplyTo[0].Address != "ada@example.com" {
		t.Errorf("reply-to = %+v, want ada@example.com", msg.ReplyTo)
	}
	if len(msg.From.Address) == 0 || msg.From.Address != "site@example.com" {
		t.Errorf("from = %q, want site@example.com", msg.From.Address)
	}
	if !strings.Contains(msg.Text, "awkward system") {
		t.Errorf("body did not contain the message; got %q", msg.Text)
	}
}

func TestHoneypotIsAcceptedButNotDelivered(t *testing.T) {
	ctx := context.Background()

	smtpAddr, apiURL, terminate := startMailpit(t, ctx)
	defer terminate()

	sender := mail.SMTP{Addr: smtpAddr, From: "site@example.com", To: "info@example.com"}
	handler := contact.NewHandler(sender, slog.New(slog.DiscardHandler), []string{origin}, 100)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := `{"name":"Bot","email":"bot@example.com","message":"Cheap watches, buy now please.","website":"http://spam.example"}`
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	// Indistinguishable from success, on purpose.
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", res.StatusCode)
	}

	// Give it long enough that a delivery would have shown up.
	time.Sleep(2 * time.Second)
	if n := messageCount(t, ctx, apiURL); n != 0 {
		t.Fatalf("mailbox has %d messages, want 0 — the honeypot submission was delivered", n)
	}
}

func startMailpit(t *testing.T, ctx context.Context) (smtpAddr, apiURL string, terminate func()) {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "axllent/mailpit:v1.21.8",
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor:   wait.ForListeningPort("1025/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start mailpit: %v", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	smtpPort, err := c.MappedPort(ctx, "1025")
	if err != nil {
		t.Fatalf("smtp port: %v", err)
	}
	apiPort, err := c.MappedPort(ctx, "8025")
	if err != nil {
		t.Fatalf("api port: %v", err)
	}

	return fmt.Sprintf("%s:%s", host, smtpPort.Port()),
		fmt.Sprintf("http://%s:%s", host, apiPort.Port()),
		func() { _ = c.Terminate(ctx) }
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

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var list struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}
		if getJSON(t, ctx, apiURL+"/api/v1/messages", &list); len(list.Messages) > 0 {
			var msg mailpitMessage
			getJSON(t, ctx, apiURL+"/api/v1/message/"+list.Messages[0].ID, &msg)
			return msg
		}
		time.Sleep(250 * time.Millisecond)
	}

	t.Fatal("no message arrived within 20s")
	return mailpitMessage{}
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
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer func() { _ = res.Body.Close() }()
	if err := json.NewDecoder(res.Body).Decode(into); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}
