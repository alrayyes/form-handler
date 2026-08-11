// SPDX-License-Identifier: GPL-3.0-or-later

// Package config assembles what the service needs to run, from a forms file
// where there is one and from the environment where there is not.
//
// It is deliberately the only place that reads os.Getenv or touches the disk.
// Everything downstream takes a Config, which is what makes the whole service
// testable without a filesystem or an environment to arrange.
package config

import (
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/alrayyes/form-handler/internal/contact"
)

// Defaults for the settings a form may leave out.
const (
	DefaultRateLimitPerHour = 5
	DefaultAddr             = ":8080"
	DefaultSMTPAddr         = "localhost:1025"
	DefaultSMTPTimeout      = 10 * time.Second
	// DefaultFormID is the form that /contact resolves to, and the id the
	// environment-configured form is given.
	DefaultFormID = "default"
)

// Config is the whole of what the service was told to do.
type Config struct {
	Addr  string
	Forms []Form
}

// SMTP is how one form's mail leaves. Per form rather than shared, because a
// provider that authenticates per sending domain — Mailgun does — issues a
// separate login for each one, and a service holding two domains therefore
// holds two logins.
type SMTP struct {
	Addr     string
	Username string
	Password string
	Timeout  time.Duration
}

// Form is one configured form: where its submissions may come from, where they
// go, and which login sends them.
type Form struct {
	ID               string
	Origins          []string
	From             string
	To               string
	Subject          string
	RateLimitPerHour int
	SMTP             SMTP
}

// formID is what may appear as the last path segment of the endpoint. Kept
// narrow on purpose: this ends up in a URL and in a log line.
var formID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Load builds the configuration from the environment.
//
// FORMS_FILE, if set, is the whole story — every form comes from that file and
// MAIL_FROM and friends are ignored, because a half-file-half-environment
// configuration is the kind of thing that works locally and surprises you in
// production. Without it, the MAIL_* variables describe a single form served at
// both /contact and /contact/default, which is what a one-site deployment wants
// and what every version before multi-form support did.
func Load() (Config, error) {
	cfg := Config{Addr: env("ADDR", DefaultAddr)}

	if path := os.Getenv("FORMS_FILE"); path != "" {
		forms, err := LoadForms(path)
		if err != nil {
			return Config{}, err
		}
		cfg.Forms = forms
		return cfg, nil
	}

	form, err := formFromEnv()
	if err != nil {
		return Config{}, err
	}
	cfg.Forms = []Form{form}
	return cfg, nil
}

// formFromEnv builds the single form a deployment with no forms file gets.
func formFromEnv() (Form, error) {
	form := Form{
		ID:      DefaultFormID,
		From:    os.Getenv("MAIL_FROM"),
		To:      os.Getenv("MAIL_TO"),
		Subject: contact.DefaultSubject,
		SMTP: SMTP{
			Addr:     env("SMTP_ADDR", DefaultSMTPAddr),
			Username: os.Getenv("SMTP_USERNAME"),
			Password: os.Getenv("SMTP_PASSWORD"),
			Timeout:  DefaultSMTPTimeout,
		},
	}

	// Ordered, not a map: which variable a person is told about first should not
	// change between runs.
	for _, required := range []struct{ name, value string }{
		{"MAIL_FROM", form.From},
		{"MAIL_TO", form.To},
	} {
		if strings.TrimSpace(required.value) == "" {
			return Form{}, fmt.Errorf("%s is required when FORMS_FILE is not set", required.name)
		}
	}

	// No default. There is no origin that is right for everybody, and the
	// consequence of guessing is somebody else's page using this mailbox.
	origins := os.Getenv("ALLOWED_ORIGINS")
	if strings.TrimSpace(origins) == "" {
		return Form{}, errors.New("ALLOWED_ORIGINS is required when FORMS_FILE is not set")
	}
	for _, o := range strings.Split(origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			form.Origins = append(form.Origins, o)
		}
	}

	form.RateLimitPerHour = DefaultRateLimitPerHour
	if raw := os.Getenv("RATE_LIMIT_PER_HOUR"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Form{}, errors.New("RATE_LIMIT_PER_HOUR must be a number")
		}
		form.RateLimitPerHour = n
	}

	if err := validate([]Form{form}); err != nil {
		return Form{}, err
	}
	return form, nil
}

// LoadForms reads and validates a forms file.
func LoadForms(path string) ([]Form, error) {
	f, err := os.Open(path) //nolint:gosec // the path is configuration this service owns, not request input
	if err != nil {
		return nil, fmt.Errorf("forms file: %w", err)
	}
	defer func() { _ = f.Close() }()

	forms, err := ParseForms(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return forms, nil
}

// yamlFile mirrors the file's shape rather than reusing Form, so the file
// format is something this package can change independently of what the rest of
// the service passes around.
type yamlFile struct {
	// Defaults for every form, so a deployment whose forms share a server does
	// not repeat it once per form.
	SMTP  *yamlSMTP  `yaml:"smtp"`
	Forms []yamlForm `yaml:"forms"`
}

type yamlSMTP struct {
	Addr     string `yaml:"addr"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	// PasswordEnv names an environment variable holding the password, which is
	// how a forms file stays committable: it says which secret to use without
	// being a file that contains one.
	PasswordEnv string `yaml:"password_env"`
}

type yamlForm struct {
	ID      string   `yaml:"id"`
	Origins []string `yaml:"origins"`
	From    string   `yaml:"from"`
	To      string   `yaml:"to"`
	Subject string   `yaml:"subject"`
	// A pointer, because an omitted rate limit and an explicit 0 mean different
	// things: take the default, versus turn the limit off.
	RateLimitPerHour *int `yaml:"rate_limit_per_hour"`
	// Overrides the file-level smtp block, field by field. A provider that
	// authenticates per sending domain gives each form its own login here.
	SMTP *yamlSMTP `yaml:"smtp"`
}

// resolveSMTP layers a form's SMTP block over the file's defaults, then reads
// whichever password was named. Field by field, so a form that only overrides
// the username keeps the shared address.
func resolveSMTP(defaults, form *yamlSMTP, where string) (SMTP, error) {
	merged := yamlSMTP{}
	for _, layer := range []*yamlSMTP{defaults, form} {
		if layer == nil {
			continue
		}
		if layer.Addr != "" {
			merged.Addr = layer.Addr
		}
		if layer.Username != "" {
			merged.Username = layer.Username
		}
		if layer.Password != "" {
			merged.Password = layer.Password
			merged.PasswordEnv = ""
		}
		if layer.PasswordEnv != "" {
			merged.PasswordEnv = layer.PasswordEnv
			merged.Password = ""
		}
	}

	// Both set in the same layer is a question about which one wins, and any
	// answer would be somebody's surprise.
	if form != nil && form.Password != "" && form.PasswordEnv != "" {
		return SMTP{}, fmt.Errorf("%s: smtp: set password or password_env, not both", where)
	}
	if defaults != nil && defaults.Password != "" && defaults.PasswordEnv != "" {
		return SMTP{}, fmt.Errorf("smtp: set password or password_env, not both")
	}

	out := SMTP{
		Addr:     merged.Addr,
		Username: merged.Username,
		Password: merged.Password,
		Timeout:  DefaultSMTPTimeout,
	}
	if out.Addr == "" {
		out.Addr = DefaultSMTPAddr
	}

	if merged.PasswordEnv != "" {
		// Fail here rather than on the first submission. A missing secret is a
		// deployment mistake, and the useful moment to hear about it is while
		// the old container is still running.
		out.Password = os.Getenv(merged.PasswordEnv)
		if out.Password == "" {
			return SMTP{}, fmt.Errorf("%s: smtp: %s is empty or unset", where, merged.PasswordEnv)
		}
	}

	// Mailgun and friends refuse the session outright, which would otherwise
	// surface one failed submission at a time.
	if out.Username != "" && out.Password == "" {
		return SMTP{}, fmt.Errorf("%s: smtp: username is set but no password is", where)
	}

	return out, nil
}

// ParseForms reads a forms file and returns the forms it describes.
func ParseForms(r io.Reader) ([]Form, error) {
	dec := yaml.NewDecoder(r)
	// A misspelt key would otherwise be dropped in silence and the form would
	// run with a default nobody chose.
	dec.KnownFields(true)

	var file yamlFile
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse: %w", err)
	}

	forms := make([]Form, 0, len(file.Forms))
	for i, f := range file.Forms {
		where := fmt.Sprintf("form %d", i)
		if f.ID != "" {
			where = fmt.Sprintf("form %q", f.ID)
		}

		smtp, err := resolveSMTP(file.SMTP, f.SMTP, where)
		if err != nil {
			return nil, err
		}

		form := Form{
			ID:               f.ID,
			Origins:          f.Origins,
			From:             f.From,
			To:               f.To,
			Subject:          f.Subject,
			RateLimitPerHour: DefaultRateLimitPerHour,
			SMTP:             smtp,
		}
		if strings.TrimSpace(form.Subject) == "" {
			form.Subject = contact.DefaultSubject
		}
		if f.RateLimitPerHour != nil {
			form.RateLimitPerHour = *f.RateLimitPerHour
		}
		forms = append(forms, form)
	}

	if err := validate(forms); err != nil {
		return nil, err
	}
	return forms, nil
}

// validate says no at startup to everything that would otherwise go wrong at
// the first submission, or worse, not go wrong at all and just deliver
// somewhere nobody is reading.
func validate(forms []Form) error {
	if len(forms) == 0 {
		return errors.New("at least one form must be configured")
	}

	seen := make(map[string]bool, len(forms))
	for i, f := range forms {
		where := fmt.Sprintf("form %d", i)
		if f.ID != "" {
			where = fmt.Sprintf("form %q", f.ID)
		}

		switch {
		case f.ID == "":
			return fmt.Errorf("%s: id is required", where)
		case !formID.MatchString(f.ID):
			return fmt.Errorf("%s: id must match %s", where, formID)
		case seen[f.ID]:
			return fmt.Errorf("%s: duplicate id", where)
		}
		seen[f.ID] = true

		if len(f.Origins) == 0 {
			return fmt.Errorf("%s: at least one origin is required", where)
		}
		for _, o := range f.Origins {
			if err := validOrigin(o); err != nil {
				return fmt.Errorf("%s: origin %q: %w", where, o, err)
			}
		}

		for _, addr := range []struct{ field, value string }{{"from", f.From}, {"to", f.To}} {
			if strings.TrimSpace(addr.value) == "" {
				return fmt.Errorf("%s: %s is required", where, addr.field)
			}
			if _, err := mail.ParseAddress(addr.value); err != nil {
				return fmt.Errorf("%s: %s %q is not a valid address", where, addr.field, addr.value)
			}
		}

		if _, err := template.New("subject").Parse(f.Subject); err != nil {
			return fmt.Errorf("%s: subject template: %w", where, err)
		}
	}

	return nil
}

// validOrigin holds a configured origin to the shape a browser actually sends:
// scheme://host[:port], and nothing after it. A trailing path never matches an
// Origin header, so a form configured with one silently refuses everybody.
func validOrigin(o string) error {
	u, err := url.Parse(o)
	if err != nil {
		return errors.New("not a URL")
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return errors.New("must start with http:// or https://")
	case u.Host == "":
		return errors.New("has no host")
	case u.Path != "" || u.RawQuery != "" || u.Fragment != "":
		return errors.New("must be scheme://host[:port] with nothing after it")
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
