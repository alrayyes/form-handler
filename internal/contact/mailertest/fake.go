// SPDX-License-Identifier: GPL-3.0-or-later

package mailertest

import (
	"context"
	"sync"

	"github.com/alrayyes/form-handler/internal/contact"
)

// Provider is what a DeliveryError from a Fake calls itself. Not "smtp" or
// "mailgun": a test that asserts on the provider should not be able to pass
// against something that never opened a connection.
const Provider = "fake"

// Fake is an in-memory contact.Mailer: somewhere for a handler test to send,
// with no mail server behind it.
//
// Held to Contract like the real adapters are, which is the whole reason it
// lives here rather than being hand-rolled per test file. Two hand-rolled ones
// used to exist, both returning bare errors, so every test asserting on a
// failure was asserting against something no adapter does.
//
// Safe for concurrent use: the handler hands one Mailer to every request.
type Fake struct {
	mu    sync.Mutex
	sent  []contact.Message
	cause error
}

// NewFake returns a Fake that delivers.
func NewFake() *Fake { return &Fake{} }

// Breaks returns a Fake that refuses every Send, reporting cause the way an
// adapter that could not reach its provider would. Returns the receiver so it
// reads as one expression at a call site.
func (f *Fake) Breaks(cause error) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cause = cause

	return f
}

// Send records the message, or fails the way a real adapter fails.
func (f *Fake) Send(ctx context.Context, m contact.Message) error {
	// Checked first and for the same reason a real adapter checks it: the
	// caller has gone, and there is no point doing the work.
	if err := ctx.Err(); err != nil {
		return contact.Undeliverable(Provider, "context", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if f.cause != nil {
		return contact.Undeliverable(Provider, "send", f.cause)
	}

	f.sent = append(f.sent, m)

	return nil
}

// Sent is every message delivered so far, oldest first. A copy, so a caller
// ranging over it cannot be tripped by a concurrent Send.
func (f *Fake) Sent() []contact.Message {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]contact.Message, len(f.sent))
	copy(out, f.sent)

	return out
}

// Count is how many messages were delivered.
func (f *Fake) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.sent)
}
