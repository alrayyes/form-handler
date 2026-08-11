// SPDX-License-Identifier: GPL-3.0-or-later

package contact

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"text/template"
	"time"
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
	limiter *limiter
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
func NewHandler(f Form, m Mailer, log *slog.Logger) (*Handler, error) {
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
		form:    f,
		mailer:  m,
		log:     log,
		origins: origins,
		subject: subject,
		limiter: newLimiter(f.RatePerHour, time.Hour),
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

	if !h.limiter.allow(clientIP(r)) {
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
		h.refuse(w, r, http.StatusBadRequest, errorBody{Error: "could not read submission"}, "detail", clip(err.Error()))
		return
	}

	msg, err := Validate(sub)
	switch {
	case errors.Is(err, ErrSpam):
		// Answer exactly as a success does. A bot that can tell the difference
		// learns which field gave it away.
		h.log.Info("dropped submission", "form", h.form.ID, "status", http.StatusAccepted,
			"reason", "honeypot", "ip", clientIP(r), "origin", clip(origin))
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
		return
	case err != nil:
		var ve ValidationError
		if errors.As(err, &ve) {
			h.refuse(w, r, http.StatusUnprocessableEntity, errorBody{
				Error: "invalid submission", Field: ve.Field, Reason: ve.Reason,
			}, "field", ve.Field, "why", ve.Reason)
			return
		}
		h.refuse(w, r, http.StatusBadRequest, errorBody{Error: "invalid submission"})
		return
	}

	if msg.Subject, err = h.subjectFor(msg); err != nil {
		h.log.Error("could not build subject", "form", h.form.ID,
			"status", http.StatusInternalServerError, "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "could not send message"})
		return
	}

	if err := h.mailer.Send(r.Context(), msg); err != nil {
		// The sender's address is theirs, not ours to log in full on a failure
		// path that gets read by whoever is on call.
		h.log.Error("could not send message", "form", h.form.ID,
			"status", http.StatusBadGateway, "error", err)
		writeJSON(w, http.StatusBadGateway, errorBody{Error: "could not send message"})
		return
	}

	h.log.Info("sent message", "form", h.form.ID, "status", http.StatusAccepted, "from", msg.Email)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

// maxLoggedHeader bounds a header before it reaches a log line. Origin and
// CF-Ray are whatever the sender put in them, and an unbounded one turns a log
// file into somewhere to write a novel.
const maxLoggedHeader = 200

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
		"ip", clientIP(r),
		"origin", clip(r.Header.Get("Origin")),
	}
	// Cloudflare stamps every request it forwards. Carrying it means a line
	// here can be matched against Cloudflare's own log for the same request,
	// which is how you find out whether something in front of this service is
	// answering on its behalf.
	if ray := r.Header.Get("CF-Ray"); ray != "" {
		attrs = append(attrs, "cf_ray", clip(ray))
	}
	attrs = append(attrs, extra...)

	h.log.Log(r.Context(), level, "refused submission", attrs...)
	writeJSON(w, status, body)
}

// clip bounds an attacker-controlled string on its way to a log line.
func clip(v string) string {
	if len(v) <= maxLoggedHeader {
		return v
	}
	return v[:maxLoggedHeader] + "…"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// clientIP prefers X-Forwarded-For's first entry, because this runs behind
// Traefik and RemoteAddr would otherwise be the proxy for every visitor —
// which would rate-limit the whole internet as one client.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, found := strings.Cut(xff, ","); found {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	host, _, found := strings.Cut(r.RemoteAddr, ":")
	if !found {
		return r.RemoteAddr
	}
	return host
}

// limiter is a fixed window per client. Deliberately in memory and deliberately
// simple: this service has one instance and one job, and a shared store would be
// more moving parts than the problem deserves. Restarting forgives everyone,
// which is an acceptable trade for a contact form.
type limiter struct {
	mu     sync.Mutex
	seen   map[string]*window
	limit  int
	period time.Duration
}

type window struct {
	count int
	reset time.Time
}

func newLimiter(limit int, period time.Duration) *limiter {
	return &limiter{seen: make(map[string]*window), limit: limit, period: period}
}

func (l *limiter) allow(key string) bool {
	if l.limit <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	// Drop expired entries while we hold the lock. Without this the map grows
	// by one key per unique address, forever.
	for k, w := range l.seen {
		if now.After(w.reset) {
			delete(l.seen, k)
		}
	}

	w, ok := l.seen[key]
	if !ok || now.After(w.reset) {
		l.seen[key] = &window{count: 1, reset: now.Add(l.period)}
		return true
	}
	if w.count >= l.limit {
		return false
	}
	w.count++
	return true
}
