// SPDX-License-Identifier: GPL-3.0-or-later

package contact_test

import (
	"errors"
	"fmt"

	"github.com/alrayyes/form-handler/internal/contact"
)

// The ordinary path: an untrusted Submission goes in, a Message that has been
// through validation comes out, trimmed and with the address parsed.
//
// The subject is not filled in here. It belongs to the form that was posted to
// — two forms on one service word it differently — so the Handler renders it
// from that form's template.
func ExampleValidate() {
	msg, err := contact.Validate(contact.Submission{
		Name:    "  Ada Lovelace  ",
		Email:   "ada@example.com",
		Message: "Please get in touch about an awkward system.",
	})
	if err != nil {
		fmt.Println("rejected:", err)

		return
	}

	fmt.Printf("%q\n", msg.Name)
	fmt.Println(msg.Email)
	fmt.Printf("subject: %q\n", msg.Subject)
	// Output:
	// "Ada Lovelace"
	// ada@example.com
	// subject: ""
}

// A caught bot is an error, but not one to show anyone: answer it exactly as
// you would a real submission, because telling it which field gave it away only
// teaches whoever wrote it what to leave alone next time.
func ExampleValidate_honeypot() {
	_, err := contact.Validate(contact.Submission{
		Name:    "Bot",
		Email:   "bot@example.com",
		Message: "Cheap watches, buy now please.",
		Website: "http://spam.example",
	})

	fmt.Println(errors.Is(err, contact.ErrSpam))
	// Output:
	// true
}

// A rejected field says which one and why, so the browser can point at it
// rather than showing a generic failure.
func ExampleValidate_validationError() {
	_, err := contact.Validate(contact.Submission{
		Name:    "Ada Lovelace",
		Email:   "not-an-address",
		Message: "Please get in touch about an awkward system.",
	})

	var ve contact.ValidationError
	if errors.As(err, &ve) {
		fmt.Println(ve.Field, "->", ve.Reason)
	}
	// Output:
	// email -> not a valid address
}
