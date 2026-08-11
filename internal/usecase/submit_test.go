// SPDX-License-Identifier: GPL-3.0-or-later

// These are the tests the refactor was for.
//
// Every rule below used to be reachable only through an HTTP handler: to find
// out what happened at the rate limit you built a request, set a header, and
// read a status code back. The rules had nothing to do with HTTP, but the only
// door to them was. Here they are called as functions.
package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alrayyes/form-handler/internal/domain"
	"github.com/alrayyes/form-handler/internal/usecase"
)

const origin = "https://www.example.com"

// recorder is the fake for the Mailer port.
type recorder struct {
	sent []domain.Message
	err  error
}

func (r *recorder) Send(_ context.Context, m domain.Message) error {
	if r.err != nil {
		return r.err
	}
	r.sent = append(r.sent, m)
	return nil
}

// allowN lets the first n through and refuses the rest, which is all the use
// case needs from a limiter.
type allowN struct{ left int }

func (a *allowN) Allow(string) bool {
	if a.left <= 0 {
		return false
	}
	a.left--
	return true
}

type unlimited struct{}

func (unlimited) Allow(string) bool { return true }

func valid() domain.Submission {
	return domain.Submission{
		Name:    "Ada Lovelace",
		Email:   "ada@example.com",
		Message: "Please get in touch about an awkward system.",
	}
}

func submit(t *testing.T, m usecase.Mailer, l usecase.RateLimiter, subject string) *usecase.Submit {
	t.Helper()
	s, err := usecase.NewSubmit(domain.Form{
		ID:      "marketing",
		Origins: []string{origin},
		Subject: subject,
	}, m, l)
	require.NoError(t, err)
	return s
}

func request(s domain.Submission, org string) usecase.SubmitRequest {
	return usecase.SubmitRequest{Origin: org, Caller: "198.51.100.1", Submission: s}
}

func TestAGoodSubmissionIsSent(t *testing.T) {
	mailer := &recorder{}

	err := submit(t, mailer, unlimited{}, "").Execute(context.Background(), request(valid(), origin))

	require.NoError(t, err)
	require.Len(t, mailer.sent, 1)
	assert.Equal(t, "ada@example.com", mailer.sent[0].Email)
	assert.Equal(t, "Contact form: Ada Lovelace", mailer.sent[0].Subject)
}

func TestAnotherSitesOriginIsRefused(t *testing.T) {
	mailer := &recorder{}

	err := submit(t, mailer, unlimited{}, "").
		Execute(context.Background(), request(valid(), "https://someone-else.example"))

	require.ErrorIs(t, err, usecase.ErrOriginNotAllowed)
	assert.Empty(t, mailer.sent, "a refused origin still sent mail")
}

// Fail closed: "nobody is allowed" must not be read as "everybody is".
func TestAFormWithNoOriginsAcceptsNothing(t *testing.T) {
	mailer := &recorder{}
	s, err := usecase.NewSubmit(domain.Form{ID: "misconfigured"}, mailer, unlimited{})
	require.NoError(t, err)

	err = s.Execute(context.Background(), request(valid(), origin))

	require.ErrorIs(t, err, usecase.ErrOriginNotAllowed)
}

func TestTheRateLimitStopsRepeatedSubmissions(t *testing.T) {
	mailer := &recorder{}
	s := submit(t, mailer, &allowN{left: 1}, "")

	require.NoError(t, s.Execute(context.Background(), request(valid(), origin)))
	err := s.Execute(context.Background(), request(valid(), origin))

	require.ErrorIs(t, err, usecase.ErrRateLimited)
	assert.Len(t, mailer.sent, 1)
}

// The order is a rule in itself: a site that may not post here should not be
// able to spend somebody else's allowance just by trying.
func TestARefusedOriginDoesNotConsumeTheRateLimit(t *testing.T) {
	mailer := &recorder{}
	limiter := &allowN{left: 1}
	s := submit(t, mailer, limiter, "")

	_ = s.Execute(context.Background(), request(valid(), "https://someone-else.example"))
	err := s.Execute(context.Background(), request(valid(), origin))

	require.NoError(t, err, "the refused attempt was counted against the limit")
}

func TestTheHoneypotIsReportedAsSpamAndNotSent(t *testing.T) {
	mailer := &recorder{}
	s := valid()
	s.Website = "http://spam.example"

	err := submit(t, mailer, unlimited{}, "").Execute(context.Background(), request(s, origin))

	require.ErrorIs(t, err, domain.ErrSpam)
	assert.Empty(t, mailer.sent, "a honeypot submission was delivered")
}

func TestAnInvalidFieldIsNamed(t *testing.T) {
	mailer := &recorder{}
	s := valid()
	s.Email = "not-an-address"

	err := submit(t, mailer, unlimited{}, "").Execute(context.Background(), request(s, origin))

	var ve domain.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "email", ve.Field)
	assert.Empty(t, mailer.sent)
}

func TestTheFormsSubjectTemplateIsRendered(t *testing.T) {
	mailer := &recorder{}

	err := submit(t, mailer, unlimited{}, "{{ .Form }}: {{ .Name }} <{{ .Email }}>").
		Execute(context.Background(), request(valid(), origin))

	require.NoError(t, err)
	require.Len(t, mailer.sent, 1)
	assert.Equal(t, "marketing: Ada Lovelace <ada@example.com>", mailer.sent[0].Subject)
}

// The name reaches the subject line and the name is whatever the visitor typed.
func TestTheSubjectCannotCarryAHeaderInjection(t *testing.T) {
	mailer := &recorder{}
	s := valid()
	s.Name = "Ada\r\nBcc: everyone@example.com"

	err := submit(t, mailer, unlimited{}, "").Execute(context.Background(), request(s, origin))

	require.NoError(t, err)
	require.Len(t, mailer.sent, 1)
	assert.NotContains(t, mailer.sent[0].Subject, "\r")
	assert.NotContains(t, mailer.sent[0].Subject, "\n")
}

func TestABadSubjectTemplateIsRefusedAtStartup(t *testing.T) {
	_, err := usecase.NewSubmit(domain.Form{
		ID: "broken", Origins: []string{origin}, Subject: "Contact form: {{ .Name",
	}, &recorder{}, unlimited{})

	require.Error(t, err, "a template that cannot parse was accepted")
}

func TestADeliveryFailureIsReportedNotSwallowed(t *testing.T) {
	mailer := &recorder{err: errors.New("mail server is down")}

	err := submit(t, mailer, unlimited{}, "").Execute(context.Background(), request(valid(), origin))

	require.Error(t, err, "a failed send was reported as success")
}
