// SPDX-License-Identifier: GPL-3.0-or-later

// Package http adapts one form's use case to the web.
//
// Everything in here is translation. It turns a request into a
// usecase.SubmitRequest, turns whatever comes back into a status code and a
// JSON body, and logs what it did. There is no rule about contact forms in this
// file — which is the test of whether the layering worked, because the rules
// used to live here.
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/alrayyes/form-handler/internal/clientip"
	"github.com/alrayyes/form-handler/internal/domain"
	"github.com/alrayyes/form-handler/internal/logsafe"
	"github.com/alrayyes/form-handler/internal/usecase"
)

// maxBody caps a submission before it is decoded. Without it the service will
// happily read as much as anyone cares to send before finding out it was too
// long.
const maxBody = 64 << 10

// Handler serves one form's endpoint.
type Handler struct {
	submit   *usecase.Submit
	log      *slog.Logger
	origins  map[string]bool
	clientIP clientip.Resolver
}

// NewHandler wires the adapter for one use case.
func NewHandler(s *usecase.Submit, log *slog.Logger, ip clientip.Resolver) *Handler {
	form := s.Form()
	origins := make(map[string]bool, len(form.Origins))
	for _, o := range form.Origins {
		origins[o] = true
	}
	return &Handler{submit: s, log: log, origins: origins, clientIP: ip}
}

type errorBody struct {
	Error  string `json:"error"`
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	// The CORS header is a browser courtesy. The rule about who may post is
	// enforced by the use case, which is why this can be answered before
	// anything is decided.
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

	var sub domain.Submission
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sub); err != nil {
		h.refuse(w, r, http.StatusBadRequest,
			errorBody{Error: "could not read submission"}, "detail", logsafe.String(err.Error()))
		return
	}

	err := h.submit.Execute(r.Context(), usecase.SubmitRequest{
		Origin:     origin,
		Caller:     h.clientIP.From(r),
		Submission: sub,
	})

	h.respond(w, r, err)
}

// respond turns the use case's answer into a status code.
//
// This mapping is the adapter's whole opinion. The use case says what happened
// in its own terms; which number that is on the wire is a fact about HTTP.
func (h *Handler) respond(w http.ResponseWriter, r *http.Request, err error) {
	var ve domain.ValidationError

	switch {
	case err == nil:
		h.log.Info("sent message", "form", h.submit.Form().ID, "status", http.StatusAccepted)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})

	case errors.Is(err, domain.ErrSpam):
		// Answered exactly as a success is, byte for byte. A bot that can tell
		// the difference learns which field gave it away.
		h.log.Info("dropped submission", "form", h.submit.Form().ID,
			"status", http.StatusAccepted, "reason", "honeypot", "ip", h.clientIP.From(r))
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})

	case errors.Is(err, usecase.ErrOriginNotAllowed):
		h.refuse(w, r, http.StatusForbidden, errorBody{Error: "origin not allowed"})

	case errors.Is(err, usecase.ErrRateLimited):
		h.refuse(w, r, http.StatusTooManyRequests, errorBody{Error: "too many submissions, try later"})

	case errors.As(err, &ve):
		h.refuse(w, r, http.StatusUnprocessableEntity,
			errorBody{Error: "invalid submission", Field: ve.Field, Reason: ve.Reason},
			"field", ve.Field, "why", ve.Reason)

	default:
		// Delivery failed, or the subject would not render. Either way it is
		// ours, not the sender's.
		var de *domain.DeliveryError
		if errors.As(err, &de) {
			h.log.Error("could not send message", "form", h.submit.Form().ID,
				"status", http.StatusBadGateway, "provider", de.Provider, "op", de.Op, "error", err)
			writeJSON(w, http.StatusBadGateway, errorBody{Error: "could not send message"})
			return
		}
		h.log.Error("could not handle submission", "form", h.submit.Form().ID,
			"status", http.StatusInternalServerError, "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "could not send message"})
	}
}

// refuse answers a request this form will not accept, and says so in the log.
//
// Every non-success path goes through here, which is the point: refusals used
// to return in silence, so "nothing in the logs" meant either that the service
// turned a submission away or that it never arrived at all.
func (h *Handler) refuse(w http.ResponseWriter, r *http.Request, status int, body errorBody, extra ...any) {
	level := slog.LevelInfo
	switch status {
	case http.StatusForbidden, http.StatusTooManyRequests:
		level = slog.LevelWarn
	}

	attrs := []any{
		"form", h.submit.Form().ID,
		"status", status,
		"reason", body.Error,
		"ip", h.clientIP.From(r),
		"origin", logsafe.String(r.Header.Get("Origin")),
	}
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
