// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/alrayyes/form-handler/internal/logsafe"
)

// recorder remembers the status so it can be logged after the fact.
//
// net/http gives a handler no way to ask what it answered, and a middleware
// that wants to report the outcome has to watch for it. Status defaults to 200
// because a handler that writes a body without calling WriteHeader has sent
// one.
type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	return r.ResponseWriter.Write(b)
}

// logRequests logs every request the service answers.
//
// Before this, only the contact handler said anything, so everything else —
// health checks, preflights, requests for paths nobody serves — happened in
// silence. That matters most exactly when something is wrong: a form that has
// stopped working looks identical to a form nothing is reaching, and without a
// line per request there is nothing to tell them apart.
//
// This is the access log: what happened. The contact handler's own lines are
// why it happened. A refused submission produces both, on purpose — one is for
// counting, the other is for acting on.
func logRequests(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &recorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		if rec.status == 0 {
			rec.status = http.StatusOK
		}

		attrs := []any{
			"method", r.Method,
			"path", logsafe.String(r.URL.Path),
			"status", rec.status,
			"duration_ms", time.Since(started).Milliseconds(),
		}
		// Only when present, so an ordinary request does not carry three empty
		// fields. All three are supplied by the sender.
		if origin := r.Header.Get("Origin"); origin != "" {
			attrs = append(attrs, "origin", logsafe.String(origin))
		}
		if ray := r.Header.Get("CF-Ray"); ray != "" {
			attrs = append(attrs, "cf_ray", logsafe.String(ray))
		}

		log.Log(r.Context(), levelFor(r, rec.status), "request", attrs...)
	})
}

// levelFor grades a request by what became of it, so "show me what is going
// wrong" is a filter rather than a read-through.
func levelFor(r *http.Request, status int) slog.Level {
	// An orchestrator probes this every few seconds. At info it would be the
	// only thing anybody ever saw.
	if r.URL.Path == healthPath {
		return slog.LevelDebug
	}

	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
