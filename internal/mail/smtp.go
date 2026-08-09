// Package mail delivers a contact message over SMTP.
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/alrayyes/form-handler/internal/contact"
)

// SMTP sends through a mail server. Returned as a struct: callers hold it
// through the contact.Mailer interface they declared themselves.
type SMTP struct {
	Addr     string // host:port
	Username string
	Password string
	From     string
	To       string
	Timeout  time.Duration
}

// Send delivers one message.
//
// The visitor's address goes in Reply-To, never in From. Sending as them would
// fail SPF for their domain and get the whole thing filed as spam — the mail is
// from this service, about them.
func (s SMTP) Send(ctx context.Context, m contact.Message) error {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	host, _, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return fmt.Errorf("smtp address %q: %w", s.Addr, err)
	}

	conn, err := (&net.Dialer{Deadline: deadline}).DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", s.Addr, err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = client.Close() }()

	// STARTTLS when the server offers it. Not required, because this may talk to
	// a bridge on localhost where there is nothing between the two processes to
	// listen in on; refusing plaintext there would mean no mail at all.
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}

	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(s.From); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := client.Rcpt(s.To); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write([]byte(s.compose(m))); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close body: %w", err)
	}

	return client.Quit()
}

// compose builds the message. Headers are encoded rather than interpolated:
// a name with a non-ASCII character is ordinary, and a name with a newline in
// it is someone trying to add their own headers.
func (s SMTP) compose(m contact.Message) string {
	var b strings.Builder
	enc := mime.QEncoding

	b.WriteString("From: " + s.From + "\r\n")
	b.WriteString("To: " + s.To + "\r\n")
	b.WriteString("Reply-To: " + sanitiseHeader(m.Email) + "\r\n")
	b.WriteString("Subject: " + enc.Encode("utf-8", sanitiseHeader(m.Subject)) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")

	b.WriteString("Name:  " + m.Name + "\r\n")
	b.WriteString("Email: " + m.Email + "\r\n")
	b.WriteString("\r\n")
	// Normalise line endings: SMTP wants CRLF, and a lone LF inside the body is
	// how a bare "." on its own line ends up terminating the message early.
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(m.Body, "\r\n", "\n"), "\n", "\r\n"))
	b.WriteString("\r\n")

	return b.String()
}

func sanitiseHeader(v string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}
