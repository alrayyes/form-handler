// Package contact turns a submitted form into a validated message and hands it
// to something that can deliver it.
package contact

import (
	"context"
	"errors"
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

// Message is what actually gets delivered. Separate from Submission on purpose:
// a submission is untrusted input, a message has been through Validate.
type Message struct {
	Name    string
	Email   string
	Subject string
	Body    string
}

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

// ErrSpam means the submission looked automated. Callers should answer as if it
// succeeded: telling a bot it was caught only teaches it what to change.
var ErrSpam = errors.New("submission looks automated")

// ValidationError names the field that was wrong, so the browser can point at
// it instead of showing a generic failure.
type ValidationError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

// Validate checks a submission and returns the message to deliver.
//
// Trims first, then measures. " " in a required field is absence with extra
// steps, and counting it as present lets an empty message through.
func Validate(s Submission) (Message, error) {
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
		Name:    name,
		Email:   addr.Address,
		Subject: subjectFor(name),
		Body:    body,
	}, nil
}

// subjectFor builds the subject line. The name is stripped of anything that
// could break out of the header — a newline in a subject is how header
// injection works, and the name is attacker-controlled.
func subjectFor(name string) string {
	clean := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, name)
	return "Contact form: " + clean
}
