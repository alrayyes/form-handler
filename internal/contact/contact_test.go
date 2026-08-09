package contact_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/alrayyes/form-handler/internal/contact"
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
	if err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if msg.Email != "ada@example.com" {
		t.Errorf("email = %q", msg.Email)
	}
	if msg.Subject != "Contact form: Ada Lovelace" {
		t.Errorf("subject = %q", msg.Subject)
	}
}

func TestValidateTrimsBeforeMeasuring(t *testing.T) {
	s := valid()
	s.Name = "   "

	_, err := contact.Validate(s)

	var ve contact.ValidationError
	if !errors.As(err, &ve) || ve.Field != "name" {
		t.Fatalf("Validate() = %v, want a name validation error", err)
	}
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
			if !errors.As(err, &ve) {
				t.Fatalf("Validate() = %v, want a ValidationError", err)
			}
			if ve.Field != tc.field {
				t.Errorf("field = %q, want %q", ve.Field, tc.field)
			}
		})
	}
}

func TestValidateCountsRunesNotBytes(t *testing.T) {
	s := valid()
	// 100 runes, well over 100 bytes. A byte count would reject a name that is
	// exactly at the documented limit.
	s.Name = strings.Repeat("é", 100)

	if _, err := contact.Validate(s); err != nil {
		t.Fatalf("Validate() = %v, want nil for a 100-rune name", err)
	}
}

func TestValidateCatchesTheHoneypot(t *testing.T) {
	s := valid()
	s.Website = "http://spam.example"

	_, err := contact.Validate(s)

	if !errors.Is(err, contact.ErrSpam) {
		t.Fatalf("Validate() = %v, want ErrSpam", err)
	}
}

func TestSubjectCannotCarryAHeaderInjection(t *testing.T) {
	s := valid()
	s.Name = "Ada\r\nBcc: everyone@example.com"

	msg, err := contact.Validate(s)
	if err != nil {
		t.Fatalf("Validate() = %v", err)
	}

	if strings.ContainsAny(msg.Subject, "\r\n") {
		t.Fatalf("subject contains a line break: %q", msg.Subject)
	}
}
