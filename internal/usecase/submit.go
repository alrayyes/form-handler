// SPDX-License-Identifier: GPL-3.0-or-later

// Package usecase holds what the service does, as opposed to what it is made
// of. One type per thing a user can ask for; this one is "submit a contact
// form".
//
// It knows nothing about HTTP. There is no request, no status code and no
// header in here — an origin arrives as a string and a caller arrives as an
// identifier, because whether they came from a browser, a queue or a test is
// not this layer's business. The ports it needs are declared here and
// implemented further out, so the arrow always points inward.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/alrayyes/form-handler/internal/domain"
)

// Mailer is somewhere to deliver a message. Declared here rather than beside
// an implementation, because the consumer is what knows the shape it wants.
type Mailer interface {
	Send(ctx context.Context, m domain.Message) error
}

// RateLimiter decides whether one caller has had enough for now.
//
// A port rather than a struct field, which is the difference this refactor
// makes to it: it used to be an unexported type inside the HTTP handler, so
// testing "what happens at the limit" meant driving HTTP, and swapping it for
// something shared between instances meant editing the handler.
type RateLimiter interface {
	Allow(key string) bool
}

// Refusals this use case can produce that are not about the submission itself.
// The submission's own problems come back as domain.ValidationError or
// domain.ErrSpam.
var (
	ErrOriginNotAllowed = errors.New("origin not allowed")
	ErrRateLimited      = errors.New("too many submissions")
)

// SubmitRequest is one attempt at a form, in the terms the rules care about.
type SubmitRequest struct {
	// Origin is where the submission claims to come from. A string, not a
	// header: the rule is "only these sites may use this form", and that is
	// true whether or not HTTP is involved.
	Origin string
	// Caller identifies who to hold to the rate limit. The work of deciding
	// what that means — a connection, a proxy header — belongs further out.
	Caller     string
	Submission domain.Submission
}

// Submit accepts a submission for one form and sends it on.
type Submit struct {
	form    domain.Form
	origins map[string]bool
	subject *template.Template
	mailer  Mailer
	limiter RateLimiter
}

// NewSubmit wires the use case for one form.
//
// It returns an error rather than panicking on a bad subject template, because
// that template comes from a config file a person edits and the useful moment
// to hear about a typo in it is at startup.
func NewSubmit(f domain.Form, m Mailer, l RateLimiter) (*Submit, error) {
	origins := make(map[string]bool, len(f.Origins))
	for _, o := range f.Origins {
		if o = strings.TrimSpace(o); o != "" {
			origins[o] = true
		}
	}

	pattern := f.Subject
	if strings.TrimSpace(pattern) == "" {
		pattern = domain.DefaultSubject
	}
	subject, err := template.New("subject").Parse(pattern)
	if err != nil {
		return nil, fmt.Errorf("form %q: subject template: %w", f.ID, err)
	}

	return &Submit{form: f, origins: origins, subject: subject, mailer: m, limiter: l}, nil
}

// Form is what this use case was built for. The composition root needs it to
// know which endpoint to hang the use case off.
func (s *Submit) Form() domain.Form { return s.form }

// Execute runs the whole rule set in the order the rules apply.
//
// The order is deliberate and is itself a business rule: origin before rate
// limit, because a submission from a site that may not post here should not
// consume anybody's allowance; rate limit before validation, because counting
// only the well-formed attempts is not a limit.
func (s *Submit) Execute(ctx context.Context, req SubmitRequest) error {
	// Fail closed: a form with no origins configured accepts nothing. An empty
	// list is a misconfiguration, and the safe reading of "nobody is allowed"
	// is not "everybody is".
	if !s.origins[req.Origin] {
		return ErrOriginNotAllowed
	}

	if !s.limiter.Allow(req.Caller) {
		return ErrRateLimited
	}

	msg, err := domain.Validate(req.Submission)
	if err != nil {
		// Including domain.ErrSpam, which the caller answers as a success —
		// telling a bot it was caught teaches whoever wrote it what to change.
		return err
	}

	if msg.Subject, err = s.subjectFor(msg); err != nil {
		return err
	}

	return s.mailer.Send(ctx, msg)
}

// subjectFor renders the form's subject template. Line breaks come out, because
// the name feeding it is attacker-controlled and a newline in a header is how
// injection works.
func (s *Submit) subjectFor(m domain.Message) (string, error) {
	var b strings.Builder
	data := struct{ Name, Email, Form string }{Name: m.Name, Email: m.Email, Form: s.form.ID}
	if err := s.subject.Execute(&b, data); err != nil {
		return "", fmt.Errorf("render subject: %w", err)
	}
	return domain.StripBreaks(b.String()), nil
}
