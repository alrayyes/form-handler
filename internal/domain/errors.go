// SPDX-License-Identifier: GPL-3.0-or-later

package domain

import "fmt"

// DeliveryError is what a Mailer returns when it could not deliver.
//
// It lives here rather than beside either adapter because it belongs to the
// port: the handler is what reads it, and the handler must not have to know
// whether the form behind it speaks SMTP or HTTP. Once two providers can fail,
// "could not send message" in a log line stops being enough to act on — which
// one, and at which step, is the difference between a wrong password and a mail
// server that is briefly down.
type DeliveryError struct {
	// Provider is the adapter that failed: "smtp" or "mailgun".
	Provider string
	// Op is the step it failed at — "dial", "auth", "send". Named for what the
	// adapter was doing rather than for the function it was in, because this
	// ends up in a log somebody reads at three in the morning.
	Op  string
	Err error
}

func (e *DeliveryError) Error() string {
	return fmt.Sprintf("%s: %s: %v", e.Provider, e.Op, e.Err)
}

// Unwrap keeps errors.Is and errors.As working through this, so an adapter
// wrapping a provider's own typed error does not hide it.
func (e *DeliveryError) Unwrap() error { return e.Err }

// Undeliverable wraps err as a delivery failure. Adapters use it so every
// failure that leaves a Mailer looks the same from the outside.
func Undeliverable(provider, op string, err error) error {
	if err == nil {
		return nil
	}
	return &DeliveryError{Provider: provider, Op: op, Err: err}
}
