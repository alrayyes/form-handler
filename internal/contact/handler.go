// SPDX-License-Identifier: GPL-3.0-or-later

package contact

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Handler serves the contact endpoint.
type Handler struct {
	mailer  Mailer
	log     *slog.Logger
	origins map[string]bool
	limiter *limiter
}

// NewHandler wires a handler. Origins are the sites allowed to post here; a
// browser will not send the form from anywhere else once this is set, and
// anything that is not a browser was never going to respect CORS anyway — so
// this is about keeping other people's pages from using our mailbox, not about
// authentication.
func NewHandler(m Mailer, log *slog.Logger, allowedOrigins []string, perHour int) *Handler {
	origins := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if o = strings.TrimSpace(o); o != "" {
			origins[o] = true
		}
	}
	return &Handler{mailer: m, log: log, origins: origins, limiter: newLimiter(perHour, time.Hour)}
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
		writeJSON(w, http.StatusMethodNotAllowed, errorBody{Error: "method not allowed"})
		return
	}

	// A browser that was refused the CORS header above would never see the
	// response anyway, but the request still arrived and would still send mail.
	// Refusing it here is what actually stops another site posting to us.
	if len(h.origins) > 0 && !h.origins[origin] {
		writeJSON(w, http.StatusForbidden, errorBody{Error: "origin not allowed"})
		return
	}

	if !h.limiter.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, errorBody{Error: "too many submissions, try later"})
		return
	}

	// Cap the body before decoding. Without this the service will happily read
	// as much as anyone cares to send before finding out it was too long.
	var sub Submission
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sub); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "could not read submission"})
		return
	}

	msg, err := Validate(sub)
	switch {
	case errors.Is(err, ErrSpam):
		// Answer exactly as a success does. A bot that can tell the difference
		// learns which field gave it away.
		h.log.Info("dropped submission", "reason", "honeypot", "ip", clientIP(r))
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
		return
	case err != nil:
		var ve ValidationError
		if errors.As(err, &ve) {
			writeJSON(w, http.StatusUnprocessableEntity, errorBody{
				Error: "invalid submission", Field: ve.Field, Reason: ve.Reason,
			})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "invalid submission"})
		return
	}

	if err := h.mailer.Send(r.Context(), msg); err != nil {
		// The sender's address is theirs, not ours to log in full on a failure
		// path that gets read by whoever is on call.
		h.log.Error("could not send message", "error", err)
		writeJSON(w, http.StatusBadGateway, errorBody{Error: "could not send message"})
		return
	}

	h.log.Info("sent message", "from", msg.Email)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
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
