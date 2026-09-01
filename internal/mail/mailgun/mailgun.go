// SPDX-License-Identifier: GPL-3.0-or-later

// Package mailgun delivers a contact message through Mailgun's HTTP API.
//
// One of the two adapters behind contact.Mailer. It exists because Mailgun
// authenticates per sending domain, so a service holding several domains holds
// several of these — one per form — each with its own key.
package mailgun

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/alrayyes/form-handler/internal/contact"
	mg "github.com/mailgun/mailgun-go/v5"
)

// provider names this adapter in a DeliveryError.
const provider = "mailgun"

// Regions Mailgun serves from. Which one you get is decided when the domain is
// created, and sending to the wrong one fails authentication rather than
// redirecting, which is a confusing way to spend an afternoon.
const (
	RegionUS = "us"
	RegionEU = "eu"
)

// DefaultTimeout is how long a send may take before it is abandoned. Mailgun is
// a network call on the request path, so this is what stops a slow API holding
// a visitor's browser open.
const DefaultTimeout = 10 * time.Second

// Config is everything one form needs to send through Mailgun.
type Config struct {
	// Domain is the sending domain as Mailgun knows it, usually mg.example.com
	// rather than example.com.
	Domain string
	APIKey string
	// Region selects Mailgun's US or EU API. Empty means US, which is
	// Mailgun's own default.
	Region string
	// BaseURL overrides the API address outright. Region covers what anyone
	// actually needs; this is the seam the tests drive.
	BaseURL string

	From string
	To   string

	Timeout time.Duration
}

// Sender delivers through Mailgun. Returned as a struct: callers hold it
// through the contact.Mailer interface they declared themselves.
type Sender struct {
	client  *mg.Client
	domain  string
	from    string
	to      string
	timeout time.Duration
}

// New checks the configuration and builds a Sender.
//
// It refuses rather than defaulting, because every one of these is something
// only the deployment knows. A Sender built without a key would start happily
// and fail one submission at a time, which is the failure mode this whole
// service is arranged to avoid.
func New(cfg Config) (*Sender, error) {
	var problems []string
	if strings.TrimSpace(cfg.Domain) == "" {
		problems = append(problems, "domain is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		problems = append(problems, "api key is required")
	}
	for _, addr := range []struct{ field, value string }{{"from", cfg.From}, {"to", cfg.To}} {
		if strings.TrimSpace(addr.value) == "" {
			problems = append(problems, addr.field+" is required")

			continue
		}
		if _, err := mail.ParseAddress(addr.value); err != nil {
			problems = append(problems, fmt.Sprintf("%s %q is not a valid address", addr.field, addr.value))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("mailgun: %s", strings.Join(problems, "; "))
	}

	client := mg.NewMailgun(cfg.APIKey)

	base := cfg.BaseURL
	if base == "" {
		switch strings.ToLower(cfg.Region) {
		case RegionEU:
			base = mg.APIBaseEU
		case RegionUS, "":
			base = mg.APIBaseUS
		default:
			return nil, fmt.Errorf("mailgun: region %q is not %q or %q", cfg.Region, RegionUS, RegionEU)
		}
	}
	if err := client.SetAPIBase(base); err != nil {
		return nil, fmt.Errorf("mailgun: api base %q: %w", base, err)
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	return &Sender{
		client:  client,
		domain:  cfg.Domain,
		from:    cfg.From,
		to:      cfg.To,
		timeout: timeout,
	}, nil
}

// Send delivers one message.
//
// The visitor's address goes in Reply-To, never in From — the same rule the
// SMTP adapter follows and for the same reason. Sending as them would fail SPF
// for their domain and file the whole thing as spam; the mail is from this
// service, about them, and hitting reply still reaches them.
func (s *Sender) Send(ctx context.Context, m contact.Message) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	msg := mg.NewMessage(s.domain, s.from, m.Subject, body(m), s.to)
	msg.SetReplyTo(m.Email)

	if _, err := s.client.Send(ctx, msg); err != nil {
		op := "send"
		// Worth separating: a rejected key is a deployment mistake somebody has
		// to fix, and a timeout is the network having a bad minute.
		if errors.Is(err, context.DeadlineExceeded) {
			op = "timeout"
		}

		return contact.Undeliverable(provider, op, err)
	}

	return nil
}

// body puts the submitter above their message. Whoever reads the inbox should
// not have to open the headers to find out who wrote in.
func body(m contact.Message) string {
	var b strings.Builder
	b.WriteString("Name:  " + m.Name + "\n")
	b.WriteString("Email: " + m.Email + "\n\n")
	b.WriteString(m.Body)
	b.WriteString("\n")

	return b.String()
}
