// SPDX-License-Identifier: GPL-3.0-or-later

package config_test

import (
	"strings"
	"testing"

	"github.com/alrayyes/form-handler/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAFormCanSendThroughMailgun(t *testing.T) {
	t.Setenv("MAILGUN_EXAMPLE_COM", "key-secret")

	forms, err := parse(t, `
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: postmaster@mg.example.com
    to: info@example.com
    mailgun:
      domain: mg.example.com
      region: eu
      api_key_env: MAILGUN_EXAMPLE_COM
`)

	require.NoError(t, err)
	require.Len(t, forms, 1)
	require.NotNil(t, forms[0].Mailgun)
	assert.Nil(t, forms[0].SMTP, "a Mailgun form must not also carry SMTP settings")
	assert.Equal(t, "mg.example.com", forms[0].Mailgun.Domain)
	assert.Equal(t, "eu", forms[0].Mailgun.Region)
	assert.Equal(t, "key-secret", forms[0].Mailgun.APIKey)
}

// The point of the whole exercise: one service, one form per domain, and each
// domain's mail leaving through its own provider account.
func TestFormsCanUseDifferentProvidersFromEachOther(t *testing.T) {
	t.Setenv("MAILGUN_EXAMPLE_COM", "key-secret")

	forms, err := parse(t, `
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: postmaster@mg.example.com
    to: info@example.com
    mailgun:
      domain: mg.example.com
      api_key_env: MAILGUN_EXAMPLE_COM
  - id: careers
    origins: ["https://careers.example.org"]
    from: site@example.org
    to: jobs@example.org
    smtp:
      addr: smtp.example.org:587
`)

	require.NoError(t, err)
	require.Len(t, forms, 2)
	assert.NotNil(t, forms[0].Mailgun)
	assert.Nil(t, forms[0].SMTP)
	assert.NotNil(t, forms[1].SMTP)
	assert.Nil(t, forms[1].Mailgun)
}

func TestFileLevelMailgunDefaultsAreInherited(t *testing.T) {
	t.Setenv("MAILGUN_KEY", "key-secret")

	forms, err := parse(t, `
mailgun:
  region: eu
  api_key_env: MAILGUN_KEY

forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: postmaster@mg.example.com
    to: info@example.com
    mailgun:
      domain: mg.example.com
`)

	require.NoError(t, err)
	require.NotNil(t, forms[0].Mailgun)
	assert.Equal(t, "eu", forms[0].Mailgun.Region)
	assert.Equal(t, "key-secret", forms[0].Mailgun.APIKey)
	assert.Equal(t, "mg.example.com", forms[0].Mailgun.Domain)
}

func TestMailgunConfigIsRejectedWhenItCannotWork(t *testing.T) {
	t.Setenv("MAILGUN_KEY", "key-secret")

	cases := map[string]struct {
		yaml string
		want string
	}{
		// Both is a question about which one wins, and any answer surprises
		// somebody.
		"both providers on one form": {`
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: a@example.com
    to: b@example.com
    smtp:
      addr: smtp.example.com:587
    mailgun:
      domain: mg.example.com
      api_key_env: MAILGUN_KEY
`, "not both"},
		"no domain": {`
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: a@example.com
    to: b@example.com
    mailgun:
      api_key_env: MAILGUN_KEY
`, "domain"},
		"no key at all": {`
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: a@example.com
    to: b@example.com
    mailgun:
      domain: mg.example.com
`, "api_key"},
		// Sending to the wrong region fails authentication rather than
		// redirecting, so a typo here is worth catching at startup.
		"unknown region": {`
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: a@example.com
    to: b@example.com
    mailgun:
      domain: mg.example.com
      region: antarctica
      api_key_env: MAILGUN_KEY
`, "region"},
		"both key forms": {`
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: a@example.com
    to: b@example.com
    mailgun:
      domain: mg.example.com
      api_key: inline
      api_key_env: MAILGUN_KEY
`, "not both"},
		// Ambiguous rather than wrong: the file says both are available and the
		// form says nothing, so nobody has decided.
		"both file defaults and a silent form": {`
smtp:
  addr: smtp.example.com:587
mailgun:
  domain: mg.example.com
  api_key_env: MAILGUN_KEY

forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: a@example.com
    to: b@example.com
`, "must name which"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parse(t, tc.yaml)

			require.Error(t, err)
			assert.Contains(t, strings.ToLower(err.Error()), strings.ToLower(tc.want))
		})
	}
}

// The forms file names the secret; the deployment supplies it. A file that
// names one nobody set has to fail at startup, not at the first submission.
func TestAMissingMailgunKeyIsATypedError(t *testing.T) {
	_, err := parse(t, `
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: a@example.com
    to: b@example.com
    mailgun:
      domain: mg.example.com
      api_key_env: MAILGUN_NOT_SET
`)

	require.Error(t, err)

	var missing *config.MissingSecretError
	require.ErrorAs(t, err, &missing, "callers should not have to grep the message for the variable")
	assert.Equal(t, "MAILGUN_NOT_SET", missing.Variable)
}

func TestABrokenFormIsATypedError(t *testing.T) {
	_, err := parse(t, `
forms:
  - id: marketing
    origins: ["https://www.example.com"]
    from: a@example.com
    to: b@example.com
    smtp:
      username: postmaster@example.com
`)

	require.Error(t, err)

	var fe *config.FormError
	require.ErrorAs(t, err, &fe)
	assert.Equal(t, "smtp", fe.Field)
}
