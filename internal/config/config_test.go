// SPDX-License-Identifier: GPL-3.0-or-later

package config_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alrayyes/form-handler/internal/config"
	"github.com/alrayyes/form-handler/internal/contact"
)

func parse(t *testing.T, yaml string) ([]config.Form, error) {
	t.Helper()
	return config.ParseForms(strings.NewReader(yaml))
}

const twoForms = `
forms:
  - id: marketing
    origins:
      - https://www.example.com
      - https://example.com
    from: site@example.com
    to: info@example.com
    subject: "Contact form: {{ .Name }}"
    rate_limit_per_hour: 20
  - id: careers
    origins:
      - https://careers.example.org
    from: site@example.com
    to: jobs@example.com
`

func TestParseReadsEveryForm(t *testing.T) {
	forms, err := parse(t, twoForms)

	require.NoError(t, err)
	require.Len(t, forms, 2)

	assert.Equal(t, "marketing", forms[0].ID)
	assert.Equal(t, []string{"https://www.example.com", "https://example.com"}, forms[0].Origins)
	assert.Equal(t, "info@example.com", forms[0].To)
	assert.Equal(t, 20, forms[0].RateLimitPerHour)

	assert.Equal(t, "careers", forms[1].ID)
	assert.Equal(t, "jobs@example.com", forms[1].To)
}

// An omitted rate limit is not "no rate limit". Zero is a value someone may
// mean, so it has to be distinguishable from the field being absent.
func TestAnOmittedRateLimitTakesTheDefault(t *testing.T) {
	forms, err := parse(t, twoForms)

	require.NoError(t, err)
	assert.Equal(t, config.DefaultRateLimitPerHour, forms[1].RateLimitPerHour)
}

func TestAnExplicitZeroRateLimitDisablesIt(t *testing.T) {
	forms, err := parse(t, `
forms:
  - id: open
    origins: ["https://www.example.com"]
    from: site@example.com
    to: info@example.com
    rate_limit_per_hour: 0
`)

	require.NoError(t, err)
	assert.Zero(t, forms[0].RateLimitPerHour, "an explicit 0 was overwritten by the default")
}

func TestAnOmittedSubjectTakesTheDefault(t *testing.T) {
	forms, err := parse(t, twoForms)

	require.NoError(t, err)
	assert.Equal(t, contact.DefaultSubject, forms[1].Subject)
}

func TestParseRejectsBadConfig(t *testing.T) {
	cases := map[string]struct {
		yaml string
		want string
	}{
		"no forms at all": {`forms: []`, "at least one form"},
		"missing id": {`
forms:
  - origins: ["https://www.example.com"]
    from: site@example.com
    to: info@example.com
`, "id"},
		// The id is the last segment of the URL, so it has to survive being in
		// one. A slash would silently register a different route.
		"id is not url safe": {`
forms:
  - id: "marketing/live"
    origins: ["https://www.example.com"]
    from: site@example.com
    to: info@example.com
`, "id"},
		"duplicate ids": {`
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: site@example.com
    to: info@example.com
  - id: marketing
    origins: ["https://other.example.com"]
    from: site@example.com
    to: other@example.com
`, "more than one form"},
		// Fail closed. A form nobody may post to is a mistake worth hearing
		// about at startup, not a form that quietly accepts everybody.
		"no origins": {`
forms:
  - id: marketing
    origins: []
    from: site@example.com
    to: info@example.com
`, "origin"},
		"origin is not an origin": {`
forms:
  - id: marketing
    origins: ["www.example.com"]
    from: site@example.com
    to: info@example.com
`, "origin"},
		// An Origin header is scheme://host[:port] and nothing else, so a
		// configured origin carrying a path would never match one.
		"origin has a path": {`
forms:
  - id: marketing
    origins: ["https://www.example.com/contact"]
    from: site@example.com
    to: info@example.com
`, "origin"},
		"missing recipient": {`
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: site@example.com
`, "to"},
		"sender is not an address": {`
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: not-an-address
    to: info@example.com
`, "from"},
		// A typo in a key would otherwise be silently ignored, and the form
		// would run with a default nobody chose.
		"unknown key": {`
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: site@example.com
    to: info@example.com
    recipients: everyone@example.com
`, "recipients"},
		"bad subject template": {`
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: site@example.com
    to: info@example.com
    subject: "Contact form: {{ .Name"
`, "subject"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parse(t, tc.yaml)

			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), tc.want)
		})
	}
}

// Mailgun gives every sending domain its own login, so the credentials are a
// property of the form rather than of the service.
func TestEachFormCanHaveItsOwnLogin(t *testing.T) {
	t.Setenv("MAILGUN_EXAMPLE_COM", "secret-for-com")
	t.Setenv("MAILGUN_EXAMPLE_ORG", "secret-for-org")

	forms, err := parse(t, `
smtp:
  addr: smtp.eu.mailgun.org:587

forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: postmaster@mg.example.com
    to: info@example.com
    smtp:
      username: postmaster@mg.example.com
      password_env: MAILGUN_EXAMPLE_COM
  - id: careers
    origins: ["https://careers.example.org"]
    from: postmaster@mg.example.org
    to: jobs@example.org
    smtp:
      addr: smtp.mailgun.org:587
      username: postmaster@mg.example.org
      password_env: MAILGUN_EXAMPLE_ORG
`)

	require.NoError(t, err)
	require.Len(t, forms, 2)

	// The top-level block is the default, so a form that does not name a
	// server inherits it rather than repeating it.
	assert.Equal(t, "smtp.eu.mailgun.org:587", forms[0].SMTP.Addr)
	assert.Equal(t, "postmaster@mg.example.com", forms[0].SMTP.Username)
	assert.Equal(t, "secret-for-com", forms[0].SMTP.Password)

	// And a form that does name one overrides it.
	assert.Equal(t, "smtp.mailgun.org:587", forms[1].SMTP.Addr)
	assert.Equal(t, "postmaster@mg.example.org", forms[1].SMTP.Username)
	assert.Equal(t, "secret-for-org", forms[1].SMTP.Password)
}

// The point of password_env: the forms file describes which secret to use
// without being a file that holds secrets, so it can live next to the site.
func TestAMissingPasswordVariableIsRefusedAtStartup(t *testing.T) {
	_, err := parse(t, `
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: postmaster@mg.example.com
    to: info@example.com
    smtp:
      username: postmaster@mg.example.com
      password_env: MAILGUN_NOT_SET
`)

	require.Error(t, err, "an unset password variable started anyway")
	assert.Contains(t, err.Error(), "MAILGUN_NOT_SET")
}

func TestSMTPConfigIsRejectedWhenItCannotWork(t *testing.T) {
	t.Setenv("MAILGUN_EXAMPLE_COM", "secret")

	cases := map[string]struct {
		smtp string
		want string
	}{
		// Two sources for one secret is a question about which one won.
		"both password and password_env": {`
      username: postmaster@mg.example.com
      password: inline-secret
      password_env: MAILGUN_EXAMPLE_COM`, "password"},
		// Mailgun refuses the session, and the failure arrives per submission
		// rather than at startup where it belongs.
		"username with no password": {`
      username: postmaster@mg.example.com`, "password"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parse(t, `
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: postmaster@mg.example.com
    to: info@example.com
    smtp:`+tc.smtp+"\n")

			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), tc.want)
		})
	}
}

// A form that says nothing about SMTP still has to end up with somewhere to
// send, or the service starts and then fails one submission at a time.
func TestAFormWithNoSMTPBlockTakesTheDefaultServer(t *testing.T) {
	forms, err := parse(t, twoForms)

	require.NoError(t, err)
	assert.Equal(t, config.DefaultSMTPAddr, forms[0].SMTP.Addr)
	assert.Empty(t, forms[0].SMTP.Username, "a default server needs no login")
}

func TestFormsFromEnvironmentBecomeTheDefaultForm(t *testing.T) {
	t.Setenv("MAIL_FROM", "site@example.com")
	t.Setenv("MAIL_TO", "info@example.com")
	t.Setenv("ALLOWED_ORIGINS", "https://www.example.com, https://example.com")

	t.Setenv("SMTP_USERNAME", "postmaster@mg.example.com")
	t.Setenv("SMTP_PASSWORD", "secret")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Len(t, cfg.Forms, 1)
	// "default" is the id, which is what makes /contact an alias for it.
	assert.Equal(t, config.DefaultFormID, cfg.Forms[0].ID)
	assert.Equal(t, []string{"https://www.example.com", "https://example.com"}, cfg.Forms[0].Origins)
	assert.Equal(t, "info@example.com", cfg.Forms[0].To)
	// One form, one login, still carried on the form itself.
	assert.Equal(t, "postmaster@mg.example.com", cfg.Forms[0].SMTP.Username)
	assert.Equal(t, "secret", cfg.Forms[0].SMTP.Password)
}

// There is no sensible default for "who may post to this", and guessing one
// wrong means an open relay for spam.
func TestLoadRequiresOriginsWhenConfiguredFromTheEnvironment(t *testing.T) {
	t.Setenv("MAIL_FROM", "site@example.com")
	t.Setenv("MAIL_TO", "info@example.com")

	_, err := config.Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ALLOWED_ORIGINS")
}

func TestLoadRequiresTheMailAddresses(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "https://www.example.com")

	_, err := config.Load()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "MAIL_FROM")
}

func TestAFormsFileWinsOverTheEnvironmentForm(t *testing.T) {
	path := writeTemp(t, twoForms)
	t.Setenv("FORMS_FILE", path)
	// Deliberately set too: the file is the whole story once it exists, and a
	// leftover MAIL_TO must not quietly add a form nobody asked for.
	t.Setenv("MAIL_FROM", "site@example.com")
	t.Setenv("MAIL_TO", "leftover@example.com")
	t.Setenv("ALLOWED_ORIGINS", "https://www.example.com")

	cfg, err := config.Load()

	require.NoError(t, err)
	require.Len(t, cfg.Forms, 2)
	assert.Equal(t, "marketing", cfg.Forms[0].ID)
}

func TestAMissingFormsFileIsAnError(t *testing.T) {
	t.Setenv("FORMS_FILE", "/nonexistent/forms.yaml")

	_, err := config.Load()

	require.Error(t, err)
}

func writeTemp(t *testing.T, contents string) string {
	t.Helper()
	path := t.TempDir() + "/forms.yaml"
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}
