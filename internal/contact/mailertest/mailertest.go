// SPDX-License-Identifier: GPL-3.0-or-later

// Package mailertest holds the contract every contact.Mailer keeps, and a fake
// that keeps it.
//
// The contract is not the interface. contact.Mailer says a Send returns an
// error; delivery.go says rather more than that — every failure leaving a
// Mailer is a *contact.DeliveryError naming the provider and the step, with the
// cause still reachable underneath. That is what the handler reads and what
// ends up in a log somebody is woken by, and until this package existed nothing
// checked it. The SMTP adapter, the one production actually runs, had no tests
// at all.
//
// Contract is run against both real adapters and against Fake. Holding the fake
// to the same suite is the point: a fake that returns a bare error where the
// real thing returns a DeliveryError makes a test pass for a reason production
// will not reproduce, and nothing goes red until someone reads .Op.
package mailertest

import (
	"context"
	"errors"
	"testing"

	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ErrMailServerDown is a cause a Fake can be told to fail with, and the one
// every package testing against a broken Mailer reaches for instead of
// minting its own — named so the contract can check the fake wraps this
// specific error rather than rebuilding it.
var ErrMailServerDown = errors.New("mail server is down")

// Subject is one implementation, and the two states the contract needs it in.
type Subject struct {
	// Provider is the name a DeliveryError from this Mailer carries.
	Provider string

	// Working returns a Mailer that can deliver. Leave it nil where standing up
	// something to deliver to costs more than the test is worth — the delivery
	// cases then skip rather than pass, and say which ones did.
	Working func(t *testing.T) contact.Mailer

	// Failing returns a Mailer whose provider will not take the message. How it
	// is broken — a closed port, a refused key — is the adapter's business. That
	// it reports the break the same way as every other Mailer is not.
	Failing func(t *testing.T) contact.Mailer

	// Cause is the error Failing was told to report, where the caller chose it.
	// Leave it nil where the failure comes from the provider instead — a 550, a
	// 401 — and there is no error object to hold the adapter to.
	//
	// Worth having because the generic check below is only a floor: it asks
	// whether anything is under the DeliveryError, not whether it is the right
	// thing. An adapter rebuilding its cause with errors.New(err.Error())
	// satisfies the floor and still throws away everything errors.Is could have
	// matched. Where a Subject can name its cause, this catches that.
	Cause error
}

// A message that passes validation, so a refusal is the mail server's doing
// rather than the message's.
func message() contact.Message {
	return contact.Message{
		Name:    "Ada Lovelace",
		Email:   "ada@example.com",
		Subject: "Contact form: Ada Lovelace",
		Body:    "Please get in touch about an awkward system.",
	}
}

// Contract runs everything a contact.Mailer has to satisfy. Call it from each
// adapter's own test, so a new adapter is one function call away from being
// held to the same standard as the two that already exist.
func Contract(t *testing.T, s Subject) {
	t.Helper()

	require.NotEmpty(t, s.Provider, "a Subject has to say what its DeliveryError is named")
	require.NotNil(t, s.Failing, "the contract is mostly about how a Mailer fails, so this cannot be skipped")

	contractDelivery(t, s)
	contractFailure(t, s)
}

// contractDelivery checks the paths that need a working Mailer.
func contractDelivery(t *testing.T, s Subject) {
	t.Helper()

	t.Run("a delivered message is not an error", func(t *testing.T) {
		if s.Working == nil {
			t.Skip("no deliverable Mailer supplied: the success path is covered elsewhere")
		}

		err := s.Working(t).Send(context.Background(), message())

		require.NoError(t, err)
	})

	t.Run("a cancelled context is not ignored", func(t *testing.T) {
		if s.Working == nil {
			t.Skip("no deliverable Mailer supplied: cancellation would be indistinguishable from the failure")
		}

		// Send is on the request path. A visitor who has closed the tab should
		// not be holding a connection to a mail server open.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := s.Working(t).Send(ctx, message())

		var de *contact.DeliveryError
		require.ErrorAs(t, err, &de, "a cancelled context was delivered anyway, or failed untyped")
	})
}

// contractFailure checks what a failing Mailer reports.
func contractFailure(t *testing.T, s Subject) {
	t.Helper()

	t.Run("a refused message is an error", func(t *testing.T) {
		// A form that says "thanks" and drops the message is worse than one
		// that says it failed.
		err := s.Failing(t).Send(context.Background(), message())

		require.Error(t, err)
	})

	t.Run("a failure is typed, not a string to parse", func(t *testing.T) {
		err := s.Failing(t).Send(context.Background(), message())

		var de *contact.DeliveryError
		require.ErrorAs(t, err, &de)
	})

	t.Run("a failure names the provider", func(t *testing.T) {
		err := s.Failing(t).Send(context.Background(), message())

		var de *contact.DeliveryError
		require.ErrorAs(t, err, &de)
		// Which provider failed is the difference between one form being down
		// and all of them.
		assert.Equal(t, s.Provider, de.Provider)
	})

	t.Run("a failure names the step it got to", func(t *testing.T) {
		err := s.Failing(t).Send(context.Background(), message())

		var de *contact.DeliveryError
		require.ErrorAs(t, err, &de)
		// "dial" and "auth" are a mail server that is down and a password that
		// is wrong. Both read as "could not send message" without this.
		assert.NotEmpty(t, de.Op, "the failure says which provider but not what it was doing")
	})

	t.Run("a failure has something underneath it", func(t *testing.T) {
		err := s.Failing(t).Send(context.Background(), message())

		var de *contact.DeliveryError
		require.ErrorAs(t, err, &de)
		// Unwrap is what stops the adapter's own typed errors being flattened
		// into a sentence on the way out. Only a floor — see the next case, and
		// Subject.Cause.
		assert.Error(t, errors.Unwrap(de), "the underlying error was thrown away")
	})

	t.Run("a failure still matches the cause it was given", func(t *testing.T) {
		if s.Cause == nil {
			t.Skip("this Subject's failure comes from its provider, so there is no cause object to match")
		}

		err := s.Failing(t).Send(context.Background(), message())

		require.ErrorIs(t, err, s.Cause, "the cause was rebuilt rather than wrapped, so errors.Is no longer finds it")
	})
}
