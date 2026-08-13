// SPDX-License-Identifier: GPL-3.0-or-later

package contact

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// Mailer is what this package needs from the outside world: somewhere to send a
// message. Declared here rather than beside the SMTP implementation, because
// the consumer is what knows the shape it wants.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// RateLimiter decides whether one caller has had enough for now, and counts the
// submission when it says yes. The key is whatever identifies a caller — this
// package hands it the client address and does not care how it is counted.
//
// Declared here for the same reason Mailer is, and kept to one method so that
// counting submissions somewhere other than this process is a new type rather
// than an edit to this one.
type RateLimiter interface {
	Allow(key string) bool
}

// Message is what actually gets delivered. Separate from Submission on purpose:
// a submission is untrusted input, a message has been through Validate.
//
// Subject is left empty by Validate and filled in by the Handler, because the
// subject line belongs to the form that was posted to rather than to the
// submission: two forms on the same service word it differently.
type Message struct {
	Name    string
	Email   string
	Subject string
	Body    string
}

// Form is one configured form, in the terms this package needs: who may post to
// it, how often, and what the resulting subject line says. Where the mail goes
// is not here — that belongs to the Mailer the form is wired to, so a form and
// its destination are chosen together at the composition root.
type Form struct {
	// ID is the last path segment of the endpoint, so it has to survive being
	// in a URL. Validated by whatever builds the Form.
	ID string
	// Origins are the sites allowed to post to this form. Per form rather than
	// global: one site being allowed to use its own form must not let it use
	// somebody else's.
	Origins []string
	// Subject is a text/template rendered with .Name, .Email and .Form. Empty
	// means DefaultSubject.
	Subject string
}

// DefaultSubject is what a form that does not name a subject template gets.
const DefaultSubject = "Contact form: {{ .Name }}"

// Submission is the raw form, exactly as posted.
type Submission struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Message string `json:"message"`
	// Website is a honeypot. It is hidden from people and left empty by them;
	// a bot that fills every field it finds will populate it. Named something
	// a bot would want to fill rather than "honeypot".
	Website string `json:"website"`
}

// Field limits. Generous for a human, mean for anything pasting a payload: the
// message cap is the one that matters, since an unbounded body is how a form
// becomes a way to post arbitrary volumes of text into someone's inbox.
const (
	MaxNameLen    = 100
	MaxEmailLen   = 254 // RFC 5321 maximum for a forward path
	MaxMessageLen = 5000
	MinMessageLen = 10
)

// Rejection is why a submission was not accepted, and the set of answers is
// closed: an unexported method means nothing outside this package can be one.
// There are two, ErrSpam and ValidationError, and a caller that has handled
// both has handled everything.
//
// Worth the extra type because the alternative was a promise in a comment. The
// handler used to carry a branch for an error kind Validate could not return,
// answering a 400 nothing could provoke — dead on the day it was written, and
// impossible to describe in the API spec, since a documented response that
// never happens fails the conformance test. Closing the set is what lets that
// branch go.
type Rejection interface {
	error
	// rejection is unexported on purpose. It is the whole mechanism: a type in
	// another package cannot implement it, so this file lists every Rejection
	// there is.
	rejection()
}

// spam exists so ErrSpam can be a Rejection. errors.New returns a type from
// somewhere else, and a method cannot be hung on that.
type spam struct{}

func (spam) Error() string { return "submission looks automated" }
func (spam) rejection()    {}

// ErrSpam means the submission looked automated. Callers should answer as if it
// succeeded: telling a bot it was caught only teaches it what to change.
var ErrSpam Rejection = spam{}

// ValidationError names the field that was wrong, so the browser can point at
// it instead of showing a generic failure.
type ValidationError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func (ValidationError) rejection() {}

// Validate checks a submission and returns the message to deliver.
//
// The Rejection rather than a bare error is the signature carrying its own
// promise: this fails in exactly two ways, and both are in this file.
//
// Trims first, then measures. " " in a required field is absence with extra
// steps, and counting it as present lets an empty message through.
func Validate(s Submission) (Message, Rejection) {
	if strings.TrimSpace(s.Website) != "" {
		return Message{}, ErrSpam
	}

	name := strings.TrimSpace(s.Name)
	email := strings.TrimSpace(s.Email)
	body := strings.TrimSpace(s.Message)

	switch {
	case name == "":
		return Message{}, ValidationError{Field: "name", Reason: "required"}
	case utf8.RuneCountInString(name) > MaxNameLen:
		return Message{}, ValidationError{Field: "name", Reason: "too long"}
	case email == "":
		return Message{}, ValidationError{Field: "email", Reason: "required"}
	case len(email) > MaxEmailLen:
		return Message{}, ValidationError{Field: "email", Reason: "too long"}
	case body == "":
		return Message{}, ValidationError{Field: "message", Reason: "required"}
	case utf8.RuneCountInString(body) < MinMessageLen:
		return Message{}, ValidationError{Field: "message", Reason: "too short"}
	case utf8.RuneCountInString(body) > MaxMessageLen:
		return Message{}, ValidationError{Field: "message", Reason: "too long"}
	}

	// net/mail rather than a regular expression. Every hand-rolled email regex
	// is wrong in one direction or the other, and this is the parser the mail
	// packages themselves use.
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return Message{}, ValidationError{Field: "email", Reason: "not a valid address"}
	}
	// ParseAddress accepts "Name <addr>". Only the address part is wanted, or a
	// sender could smuggle a display name into the header.
	if addr.Address != email {
		return Message{}, ValidationError{Field: "email", Reason: "not a valid address"}
	}

	return Message{
		Name:  name,
		Email: addr.Address,
		Body:  body,
	}, nil
}

// stripBreaks removes anything that could break out of a header. A newline in a
// subject is how header injection works, and the name it is built from is
// attacker-controlled.
func stripBreaks(v string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, v)
}
