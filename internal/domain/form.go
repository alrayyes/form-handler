// SPDX-License-Identifier: GPL-3.0-or-later

package domain

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
	// RatePerHour is submissions allowed per client address per hour. Zero
	// disables the limit.
	RatePerHour int
}

// DefaultSubject is what a form that does not name a subject template gets.
