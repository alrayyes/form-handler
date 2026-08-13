// SPDX-License-Identifier: GPL-3.0-or-later

package contact

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"text/template"

	"github.com/alrayyes/form-handler/internal/clientip"
	"github.com/alrayyes/form-handler/internal/logsafe"
)

// Handler serves one form's endpoint. One per configured form, each with its
// own origins, its own subject line, its own rate limit and its own Mailer —
// which is what keeps two forms on one service from leaking into each other.
type Handler struct {
	form    Form
	mailer  Mailer
	log     *slog.Logger
	origins map[string]bool
	subject *template.Template
	limiter RateLimiter
	// clientIP decides who a request is from. Its zero value ignores
	// X-Forwarded-For entirely, which is the right answer for a service
	// reached directly.
	clientIP clientip.Resolver
}

// subjectData is what a subject template is rendered against. Deliberately not
// the whole Message: the body has no business in a header.
type subjectData struct {
	Name  string
	Email string
	Form  string
}

// NewHandler wires a handler for one form.
//
// Origins are the sites allowed to post here; a browser will not send the form
// from anywhere else once this is set, and anything that is not a browser was
// never going to respect CORS anyway — so this is about keeping other people's
// pages from using our mailbox, not about authentication.
//
// It returns an error rather than panicking on a bad subject template, because
// that template comes from a config file a person edits, and the useful moment
// to hear about a typo in it is at startup.
func NewHandler(f Form, m Mailer, l RateLimiter, log *slog.Logger, ip clientip.Resolver) (*Handler, error) {
	origins := make(map[string]bool, len(f.Origins))
	for _, o := range f.Origins {
		if o = strings.TrimSpace(o); o != "" {
			origins[o] = true
		}
	}

	pattern := f.Subject
	if strings.TrimSpace(pattern) == "" {
		pattern = DefaultSubject
	}
	subject, err := template.New("subject").Parse(pattern)
	if err != nil {
		return nil, fmt.Errorf("form %q: subject template: %w", f.ID, err)
	}

	return &Handler{
		form:     f,
		mailer:   m,
		log:      log,
		origins:  origins,
		subject:  subject,
		limiter:  l,
		clientIP: ip,
	}, nil
}

// subjectFor renders the form's subject template. Line breaks come out, because
// the name feeding it is attacker-controlled and a newline in a header is how
// injection works.
func (h *Handler) subjectFor(m Message) (string, error) {
	var b strings.Builder
	if err := h.subject.Execute(&b, subjectData{Name: m.Name, Email: m.Email, Form: h.form.ID}); err != nil {
		return "", fmt.Errorf("render subject: %w", err)
	}
	return stripBreaks(b.String()), nil
}

type errorBody struct {
	Error  string `json:"error"`
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if h.origins[origin] {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST, OPTIONS")
		h.refuse(w, r, http.StatusMethodNotAllowed, errorBody{Error: "method not allowed"}, "method", r.Method)
		return
	}

	// A browser that was refused the CORS header above would never see the
	// response anyway, but the request still arrived and would still send mail.
	// Refusing it here is what actually stops another site posting to us.
	//
	// Fail closed: a form with no origins configured accepts nothing. An empty
	// list is a misconfiguration, and the safe reading of "nobody is allowed"
	// is not "everybody is".
	if !h.origins[origin] {
		h.refuse(w, r, http.StatusForbidden, errorBody{Error: "origin not allowed"})
		return
	}

	if !h.limiter.Allow(h.clientIP.From(r)) {
		h.refuse(w, r, http.StatusTooManyRequests, errorBody{Error: "too many submissions, try later"})
		return
	}

	// Cap the body before decoding. Without this the service will happily read
	// as much as anyone cares to send before finding out it was too long.
	var sub Submission
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sub); err != nil {
		// The decoder's own message names the offending field for an unknown
		// one, which is the difference between "their form is out of date" and
		// "somebody is poking at this".
		h.refuse(w, r, http.StatusBadRequest, errorBody{Error: "could not read submission"}, "detail", logsafe.String(err.Error()))
		return
	}

	// Every way this can be turned down is a case here, because Rejection is a
	// closed set. There is no fallback branch, and there is nothing for one to
	// catch.
	msg, rejected := Validate(sub)
	switch rejected := rejected.(type) {
	case nil:
		// Accepted. The send is below.
	case spam:
		// Answer exactly as a success does. A bot that can tell the difference
		// learns which field gave it away.
		h.log.Info("dropped submission", "form", h.form.ID, "status", http.StatusAccepted,
			"reason", "honeypot", "ip", h.clientIP.From(r), "origin", logsafe.String(origin))
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
		return
	case ValidationError:
		h.refuse(w, r, http.StatusUnprocessableEntity, errorBody{
			Error: "invalid submission", Field: rejected.Field, Reason: rejected.Reason,
		}, "field", rejected.Field, "why", rejected.Reason)
		return
	}

	subject, err := h.subjectFor(msg)
	if err != nil {
		h.log.Error("could not build subject", "form", h.form.ID,
			"status", http.StatusInternalServerError, "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "could not send message"})
		return
	}
	msg.Subject = subject

	if err := h.mailer.Send(r.Context(), msg); err != nil {
		// The sender's address is theirs, not ours to log in full on a failure
		// path that gets read by whoever is on call.
		h.log.Error("could not send message", "form", h.form.ID,
			"status", http.StatusBadGateway, "error", err)
		writeJSON(w, http.StatusBadGateway, errorBody{Error: "could not send message"})
		return
	}

	h.log.Info("sent message", "form", h.form.ID, "status", http.StatusAccepted,
		"from", logsafe.String(msg.Email))
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

// refuse answers a request this form will not accept, and says so in the log.
//
// Every non-success path goes through here, which is the point: refusals used
// to return in silence, so "nothing in the logs" meant either that the service
// turned a submission away or that it never arrived at all — two very different
// problems with identical evidence. Routing them all through one place also
// means a new refusal cannot be added without one.
func (h *Handler) refuse(w http.ResponseWriter, r *http.Request, status int, body errorBody, extra ...any) {
	// Somebody else's page posting here, or one address flooding the form, is
	// worth noticing. A visitor mistyping their email address is not.
	level := slog.LevelInfo
	switch status {
	case http.StatusForbidden, http.StatusTooManyRequests:
		level = slog.LevelWarn
	}

	attrs := []any{
		"form", h.form.ID,
		"status", status,
		"reason", body.Error,
		"ip", h.clientIP.From(r),
		"origin", logsafe.String(r.Header.Get("Origin")),
	}
	// Cloudflare stamps every request it forwards. Carrying it means a line
	// here can be matched against Cloudflare's own log for the same request,
	// which is how you find out whether something in front of this service is
	// answering on its behalf.
	if ray := r.Header.Get("CF-Ray"); ray != "" {
		attrs = append(attrs, "cf_ray", logsafe.String(ray))
	}
	attrs = append(attrs, extra...)

	h.log.Log(r.Context(), level, "refused submission", attrs...)
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
