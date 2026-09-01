// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/alrayyes/form-handler/internal/contact"
	"gopkg.in/yaml.v3"
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
	Addr string
	// LogLevel is the lowest level that reaches the log. The access log puts
	// health checks at debug, so this is what turns them on.
	LogLevel slog.Level
	// TrustedProxies are the hops between the internet and this service whose
	// X-Forwarded-For contribution can be believed. Empty means none, and the
	// header is then ignored — see internal/clientip for why that is the safe
	// default rather than a limitation.
	TrustedProxies []string
	Forms          []Form
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

	// Exactly one of these is set, and which one is how the composition root
	// knows which adapter to build. A pointer each rather than a provider
	// string plus two structs, so "configured but for the other provider" is
	// not a state that can be represented.
	SMTP    *SMTP
	Mailgun *Mailgun
}

// Mailgun is one form's Mailgun credentials. Per form for the same reason SMTP
// is: a key authenticates one sending domain.
type Mailgun struct {
	Domain  string
	APIKey  string
	Region  string
	BaseURL string
	Timeout time.Duration
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

	level, err := parseLevel(env("LOG_LEVEL", "info"))
	if err != nil {
		return Config{}, err
	}
	cfg.LogLevel = level

	if raw := os.Getenv("TRUSTED_PROXIES"); strings.TrimSpace(raw) != "" {
		for p := range strings.SplitSeq(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfg.TrustedProxies = append(cfg.TrustedProxies, p)
			}
		}
	}

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
		SMTP: &SMTP{
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
			return Form{}, fmt.Errorf("%s %w", required.name, ErrRequiredWhenNoFormsFile)
		}
	}

	// No default. There is no origin that is right for everybody, and the
	// consequence of guessing is somebody else's page using this mailbox.
	origins := os.Getenv("ALLOWED_ORIGINS")
	if strings.TrimSpace(origins) == "" {
		return Form{}, ErrOriginsRequiredWhenNoFormsFile
	}
	for o := range strings.SplitSeq(origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			form.Origins = append(form.Origins, o)
		}
	}

	form.RateLimitPerHour = DefaultRateLimitPerHour
	if raw := os.Getenv("RATE_LIMIT_PER_HOUR"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Form{}, fmt.Errorf("%w: %w", ErrRateLimitNotANumber, err)
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
	// Defaults for every form, so a deployment whose forms share a server or a
	// region does not repeat it once per form.
	SMTP    *yamlSMTP    `yaml:"smtp"`
	Mailgun *yamlMailgun `yaml:"mailgun"`
	Forms   []yamlForm   `yaml:"forms"`
}

type yamlMailgun struct {
	Domain string `yaml:"domain"`
	Region string `yaml:"region"`
	// BaseURL overrides the region outright. Mostly a test seam.
	BaseURL   string `yaml:"base_url"`
	APIKey    string `yaml:"api_key"`
	APIKeyEnv string `yaml:"api_key_env"`
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
	// The other way to send. A form names one or the other, never both.
	Mailgun *yamlMailgun `yaml:"mailgun"`
}

// resolveSMTP layers a form's SMTP block over the file's defaults, then reads
// whichever password was named. Field by field, so a form that only overrides
// the username keeps the shared address.
func resolveSMTP(defaults, form *yamlSMTP, where string) (*SMTP, error) {
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
		return nil, &FormError{Form: where, Field: "smtp", Reason: "set password or password_env, not both"}
	}
	if defaults != nil && defaults.Password != "" && defaults.PasswordEnv != "" {
		return nil, &FormError{Form: where, Field: "smtp", Reason: "file defaults set password and password_env, not both"}
	}

	out := &SMTP{
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
			return nil, &MissingSecretError{Form: where, Variable: merged.PasswordEnv}
		}
	}

	// Mailgun and friends refuse the session outright, which would otherwise
	// surface one failed submission at a time.
	if out.Username != "" && out.Password == "" {
		return nil, &FormError{Form: where, Field: "smtp", Reason: "username is set but no password is"}
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
		where := describe(i, f.ID)

		smtpCfg, mgCfg, err := resolveProvider(&file, f, where)
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
			SMTP:             smtpCfg,
			Mailgun:          mgCfg,
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

// resolveMailgun layers a form's mailgun block over the file's defaults and
// reads the named key.
func resolveMailgun(defaults, form *yamlMailgun, where string) (*Mailgun, error) {
	merged := yamlMailgun{}
	for _, layer := range []*yamlMailgun{defaults, form} {
		if layer == nil {
			continue
		}
		if layer.Domain != "" {
			merged.Domain = layer.Domain
		}
		if layer.Region != "" {
			merged.Region = layer.Region
		}
		if layer.BaseURL != "" {
			merged.BaseURL = layer.BaseURL
		}
		if layer.APIKey != "" {
			merged.APIKey, merged.APIKeyEnv = layer.APIKey, ""
		}
		if layer.APIKeyEnv != "" {
			merged.APIKeyEnv, merged.APIKey = layer.APIKeyEnv, ""
		}
	}

	if form != nil && form.APIKey != "" && form.APIKeyEnv != "" {
		return nil, &FormError{Form: where, Field: "mailgun", Reason: "set api_key or api_key_env, not both"}
	}

	out := &Mailgun{
		Domain:  merged.Domain,
		Region:  merged.Region,
		BaseURL: merged.BaseURL,
		APIKey:  merged.APIKey,
		Timeout: DefaultSMTPTimeout,
	}

	if merged.APIKeyEnv != "" {
		// Same reasoning as the SMTP password: a missing secret should be a
		// failed deploy while the old container is still up, not a form that
		// quietly stops sending.
		out.APIKey = os.Getenv(merged.APIKeyEnv)
		if out.APIKey == "" {
			return nil, &MissingSecretError{Form: where, Variable: merged.APIKeyEnv}
		}
	}

	if strings.TrimSpace(out.Domain) == "" {
		return nil, &FormError{Form: where, Field: "mailgun", Reason: "domain is required"}
	}
	if strings.TrimSpace(out.APIKey) == "" {
		return nil, &FormError{Form: where, Field: "mailgun", Reason: "api_key or api_key_env is required"}
	}
	switch strings.ToLower(out.Region) {
	case "", "us", "eu":
	default:
		return nil, &FormError{Form: where, Field: "mailgun", Reason: fmt.Sprintf("region %q is not \"us\" or \"eu\"", out.Region)}
	}

	return out, nil
}

// resolveProvider decides how one form sends, and refuses anything ambiguous.
//
// A form names smtp or mailgun, never both — two providers configured on one
// form is a question about which one wins, and any answer is somebody's
// surprise. Where a form names neither it inherits whichever the file's
// defaults describe, and where there are no defaults either it falls back to
// SMTP on localhost, which is what this service did before it could speak to
// anything else.
func resolveProvider(file *yamlFile, form yamlForm, where string) (*SMTP, *Mailgun, error) {
	switch {
	case form.SMTP != nil && form.Mailgun != nil:
		return nil, nil, &FormError{Form: where, Field: "provider", Reason: "set smtp or mailgun, not both"}

	case form.Mailgun != nil:
		mg, err := resolveMailgun(file.Mailgun, form.Mailgun, where)

		return nil, mg, err

	case form.SMTP != nil:
		smtp, err := resolveSMTP(file.SMTP, form.SMTP, where)

		return smtp, nil, err

	// Neither named on the form: inherit the file's defaults, so a deployment
	// where every form sends the same way says so once.
	case file.Mailgun != nil && file.SMTP != nil:
		return nil, nil, &FormError{
			Form:   where,
			Field:  "provider",
			Reason: "the file sets both smtp and mailgun defaults, so this form must name which it uses",
		}

	case file.Mailgun != nil:
		mg, err := resolveMailgun(file.Mailgun, nil, where)

		return nil, mg, err

	default:
		smtp, err := resolveSMTP(file.SMTP, nil, where)

		return smtp, nil, err
	}
}

// validate says no at startup to everything that would otherwise go wrong at
// the first submission, or worse, not go wrong at all and just deliver
// somewhere nobody is reading.
func validate(forms []Form) error {
	if len(forms) == 0 {
		return ErrNoFormsConfigured
	}

	seen := make(map[string]bool, len(forms))
	for i, f := range forms {
		where := describe(i, f.ID)

		switch {
		case f.ID == "":
			return &FormError{Form: where, Field: "id", Reason: "is required"}
		case !formID.MatchString(f.ID):
			return &FormError{Form: where, Field: "id", Reason: "must match " + formID.String()}
		case seen[f.ID]:
			return &FormError{Form: where, Field: "id", Reason: "is used by more than one form"}
		}
		seen[f.ID] = true

		if len(f.Origins) == 0 {
			return &FormError{Form: where, Field: "origins", Reason: "at least one is required"}
		}
		for _, o := range f.Origins {
			if err := validOrigin(o); err != nil {
				return &FormError{Form: where, Field: "origins", Reason: fmt.Sprintf("%q: %v", o, err)}
			}
		}

		for _, addr := range []struct{ field, value string }{{"from", f.From}, {"to", f.To}} {
			if strings.TrimSpace(addr.value) == "" {
				return &FormError{Form: where, Field: addr.field, Reason: "is required"}
			}
			if _, err := mail.ParseAddress(addr.value); err != nil {
				return &FormError{Form: where, Field: addr.field, Reason: fmt.Sprintf("%q is not a valid address", addr.value)}
			}
		}

		if _, err := template.New("subject").Parse(f.Subject); err != nil {
			return &FormError{Form: where, Field: "subject", Reason: err.Error()}
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
		return fmt.Errorf("%w: %w", ErrOriginNotAURL, err)
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return ErrOriginBadScheme
	case u.Host == "":
		return ErrOriginNoHost
	case u.Path != "" || u.RawQuery != "" || u.Fragment != "":
		return ErrOriginHasExtra
	}

	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

// describe names a form for an error message: its id where it has one, and its
// position in the file where the id is the thing that is missing.
func describe(index int, id string) string {
	if id != "" {
		return fmt.Sprintf("%q", id)
	}

	return fmt.Sprintf("#%d", index)
}

// parseLevel reads LOG_LEVEL. Named levels rather than slog's numbers, because
// nobody deploying this wants to know that warn is 4.
func parseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrLogLevelInvalid, raw)
	}
}
