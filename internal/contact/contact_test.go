// SPDX-License-Identifier: GPL-3.0-or-later

package contact_test

import (
	"strings"
	"testing"

	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func valid() contact.Submission {
	return contact.Submission{
		Name:    "Ada Lovelace",
		Email:   "ada@example.com",
		Message: "Please get in touch about an awkward system.",
	}
}

func TestValidateAcceptsAGoodSubmission(t *testing.T) {
	msg, err := contact.Validate(valid())

	require.NoError(t, err)
	assert.Equal(t, "ada@example.com", msg.Email)
	assert.Equal(t, "Ada Lovelace", msg.Name)
	// The subject belongs to the form that was posted to, so the Handler fills
	// it in rather than Validate.
	assert.Empty(t, msg.Subject)
}

func TestValidateTrimsBeforeMeasuring(t *testing.T) {
	s := valid()
	s.Name = "   "

	_, err := contact.Validate(s)

	var ve contact.ValidationError
	require.ErrorAs(t, err, &ve)
	assert.Equal(t, "name", ve.Field)
}

func TestValidateRejectsBadInput(t *testing.T) {
	cases := map[string]struct {
		mutate func(*contact.Submission)
		field  string
	}{
		"no name":         {func(s *contact.Submission) { s.Name = "" }, "name"},
		"long name":       {func(s *contact.Submission) { s.Name = strings.Repeat("a", 101) }, "name"},
		"no email":        {func(s *contact.Submission) { s.Email = "" }, "email"},
		"malformed email": {func(s *contact.Submission) { s.Email = "not-an-address" }, "email"},
		"no message":      {func(s *contact.Submission) { s.Message = "" }, "message"},
		"short message":   {func(s *contact.Submission) { s.Message = "hi" }, "message"},
		"long message":    {func(s *contact.Submission) { s.Message = strings.Repeat("a", 5001) }, "message"},
		// ParseAddress accepts this, but taking it would let a sender choose the
		// display name that appears in the reply header.
		"display name": {func(s *contact.Submission) { s.Email = "Ada <ada@example.com>" }, "email"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := valid()
			tc.mutate(&s)

			_, err := contact.Validate(s)

			var ve contact.ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Equal(t, tc.field, ve.Field)
		})
	}
}

func TestValidateCountsRunesNotBytes(t *testing.T) {
	s := valid()
	// 100 runes, well over 100 bytes. A byte count would reject a name that is
	// exactly at the documented limit.
	s.Name = strings.Repeat("é", 100)

	_, err := contact.Validate(s)

	require.NoError(t, err, "a 100-rune name is exactly at the documented limit")
}

func TestValidateCatchesTheHoneypot(t *testing.T) {
	s := valid()
	s.Website = "http://spam.example"

	_, err := contact.Validate(s)

	require.ErrorIs(t, err, contact.ErrSpam)
}
