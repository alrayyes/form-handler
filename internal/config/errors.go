// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "fmt"

// FormError says which form was wrong and what about it.
//
// Typed rather than a formatted string because these are the errors a person
// reads at deploy time, and the three parts are worth keeping separate: the
// form so they know where to look, the field so they know what to change, and
// the reason so they know why it is not acceptable. It also lets the tests
// assert on the field rather than on the wording of a sentence.
type FormError struct {
	// Form is the form's id, or its position in the file when the id is what is
	// missing.
	Form   string
	Field  string
	Reason string
}

func (e *FormError) Error() string {
	return fmt.Sprintf("form %s: %s: %s", e.Form, e.Field, e.Reason)
}

// MissingSecretError is a named environment variable that turned out to be
// empty.
//
// Separate from FormError because it is the one configuration mistake that is
// not in the configuration file: the file is right, the deployment around it is
// not, and the fix is somewhere else entirely.
//
// The field is called Variable rather than anything with "password" in it. That
// is not squeamishness — it holds a variable name, never a secret, and naming
// it accurately keeps both readers and the taint analysers from concluding that
// a credential is being formatted into an error message.
type MissingSecretError struct {
	Form     string
	Variable string
}

func (e *MissingSecretError) Error() string {
	return fmt.Sprintf("form %s: %s is empty or unset", e.Form, e.Variable)
}
