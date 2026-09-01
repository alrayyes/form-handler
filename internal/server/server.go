// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/alrayyes/form-handler/internal/clientip"
	"github.com/alrayyes/form-handler/internal/config"
	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/alrayyes/form-handler/internal/logsafe"
	"github.com/alrayyes/form-handler/internal/mail/mailgun"
	"github.com/alrayyes/form-handler/internal/mail/smtp"
	"github.com/alrayyes/form-handler/internal/ratelimit"
)

// ErrNoMailProvider is returned when a Form has neither SMTP nor Mailgun
// configured. config.Load and config.LoadForms both refuse to produce a Form
// like this, so reaching it means the two have drifted apart rather than
// that somebody misconfigured something.
var ErrNoMailProvider = errors.New("no mail provider configured")

// New builds the handler for every configured form.
//
// Each form gets its own contact.Handler — its own origins, subject, and rate
// limit — and its own mail.SMTP carrying that form's server, login, From and
// To. Sharing one mailer and switching the recipient per request would work
// right up until two forms were configured with the same id by accident, and
// then it would deliver somebody's application to the marketing inbox. It also
// could not hold two logins at once, which a provider that authenticates per
// sending domain requires.
func New(cfg config.Config, log *slog.Logger) (http.Handler, error) {
	mux := http.NewServeMux()

	// One resolver for the whole service: how many proxies sit in front is a
	// property of the deployment, not of a form.
	resolver, err := clientip.NewResolver(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}

	for _, f := range cfg.Forms {
		sender, err := mailerFor(f)
		if err != nil {
			return nil, err
		}

		// Per form, not shared: two forms on one deployment have their own
		// allowances, so a busy careers page cannot use up what the contact
		// form was going to give its visitors.
		limiter := ratelimit.New(f.RateLimitPerHour, time.Hour)

		h, err := contact.NewHandler(contact.Form{
			ID:      f.ID,
			Origins: f.Origins,
			Subject: f.Subject,
		}, sender, limiter, log, resolver)
		if err != nil {
			return nil, fmt.Errorf("form %q: %w", f.ID, err)
		}

		mux.Handle("/contact/"+f.ID, h)
		// /contact without an id is the single-form deployment's endpoint, and
		// what every version before multi-form support answered on. It stays
		// pointed at the form called "default" so upgrading changes nothing for
		// a site that only ever had one form.
		if f.ID == config.DefaultFormID {
			mux.Handle("/contact", h)
		}
	}

	// Without this, a post to a form that does not exist gets net/http's plain
	// text 404, which is a surprise for a client that has only ever been sent
	// JSON by this service.
	//
	// It is logged for the same reason the handler logs its refusals: a site
	// posting at the wrong path looks exactly like a site whose requests never
	// arrive, and the two are fixed in different places.
	mux.HandleFunc("/contact/", func(w http.ResponseWriter, r *http.Request) {
		log.Warn("refused submission",
			"status", http.StatusNotFound,
			"reason", "unknown form",
			// Both are supplied by whoever sent the request, and both end up in
			// a line somebody reads.
			"path", logsafe.String(r.URL.Path),
			"origin", logsafe.String(r.Header.Get("Origin")),
		)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown form"})
	})

	// Liveness only. It deliberately does not test SMTP: a mail server being
	// briefly unreachable is not a reason for the orchestrator to kill and
	// restart a process that is otherwise answering.
	mux.HandleFunc(healthPath, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Everything the service answers goes through here, including the paths no
	// handler above claims.
	return logRequests(mux, log), nil
}

// healthPath is named because two places care: the mux that serves it and the
// access log that keeps it quiet.
const healthPath = "/healthz"

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// mailerFor builds the adapter one form sends through.
//
// This is the whole of the composition root's knowledge about providers: the
// domain declared the port, each adapter implements it, and the only place that
// knows both exist is here. Adding a third provider means a case in this switch
// and a package beside the other two, and nothing in internal/contact changes.
func mailerFor(f config.Form) (contact.Mailer, error) {
	switch {
	case f.Mailgun != nil:
		sender, err := mailgun.New(mailgun.Config{
			Domain:  f.Mailgun.Domain,
			APIKey:  f.Mailgun.APIKey,
			Region:  f.Mailgun.Region,
			BaseURL: f.Mailgun.BaseURL,
			From:    f.From,
			To:      f.To,
			Timeout: f.Mailgun.Timeout,
		})
		if err != nil {
			return nil, fmt.Errorf("form %q: %w", f.ID, err)
		}

		return sender, nil

	case f.SMTP != nil:
		return smtp.Sender{
			Addr:     f.SMTP.Addr,
			Username: f.SMTP.Username,
			Password: f.SMTP.Password,
			From:     f.From,
			To:       f.To,
			Timeout:  f.SMTP.Timeout,
		}, nil

	default:
		return nil, fmt.Errorf("form %q: %w", f.ID, ErrNoMailProvider)
	}
}
