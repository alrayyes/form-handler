// SPDX-License-Identifier: GPL-3.0-or-later

// Package server wires a Config into something that serves HTTP. It is the
// composition root: the one place that knows a form has both a handler and a
// mailer, and pairs them up.
package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/alrayyes/form-handler/internal/config"
	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/alrayyes/form-handler/internal/mail"
)

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

	for _, f := range cfg.Forms {
		sender := mail.SMTP{
			Addr:     f.SMTP.Addr,
			Username: f.SMTP.Username,
			Password: f.SMTP.Password,
			From:     f.From,
			To:       f.To,
			Timeout:  f.SMTP.Timeout,
		}

		h, err := contact.NewHandler(contact.Form{
			ID:          f.ID,
			Origins:     f.Origins,
			Subject:     f.Subject,
			RatePerHour: f.RateLimitPerHour,
		}, sender, log)
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
	mux.HandleFunc("/contact/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown form"})
	})

	// Liveness only. It deliberately does not test SMTP: a mail server being
	// briefly unreachable is not a reason for the orchestrator to kill and
	// restart a process that is otherwise answering.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return mux, nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
