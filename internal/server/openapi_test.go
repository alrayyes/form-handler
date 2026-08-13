// SPDX-License-Identifier: GPL-3.0-or-later

package server_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/alrayyes/form-handler/internal/clientip"
	"github.com/alrayyes/form-handler/internal/config"
	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/alrayyes/form-handler/internal/contact/mailertest"
	"github.com/alrayyes/form-handler/internal/ratelimit"
	"github.com/alrayyes/form-handler/internal/server"
)

// The spec is a second description of something the code already describes, and
// the first one of those to go stale takes somebody's afternoon with it. The
// README's status table had been missing three codes for as long as it had
// existed, which is the argument for this file: a document nothing checks is a
// document that is wrong and does not know it.
//
// So this is not a schema validator. It reads the claims the spec actually
// makes — the status, the media type, the body, the headers whose value is
// fixed — and provokes each one out of the real handler. A response the spec
// describes and nothing here provokes fails the test, and so does a response
// provoked here that the spec does not describe.
const specPath = "../../api/openapi.yaml"

const (
	specOrigin = "https://www.example.com"
	specBody   = `{"name":"Ada","email":"ada@example.com","message":"A message long enough to pass."}`
)

// specDoc is as much of an OpenAPI document as this test reads.
type specDoc struct {
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Responses map[string]specResponse `yaml:"responses"`
	} `yaml:"components"`
}

type specOperation struct {
	Responses map[string]specResponse `yaml:"responses"`
}

type specResponse struct {
	// Ref is followed for a response held in components, which is how the two
	// operations that answer 404 share one description of it. Only that one
	// form of reference is resolved — this is a test, not a spec parser, and
	// the day it needs more it should say so by failing.
	Ref     string                `yaml:"$ref"`
	Headers map[string]specHeader `yaml:"headers"`
	Content map[string]specMedia  `yaml:"content"`
}

type specMedia struct {
	Example any `yaml:"example"`
}

// specHeader carries a const where the value never varies — Allow, the CORS
// preflight answers — and a bare string schema where it does, as
// Access-Control-Allow-Origin does when it echoes whoever asked.
type specHeader struct {
	Schema struct {
		Const string `yaml:"const"`
	} `yaml:"schema"`
}

// notAnOperation lists the keys a path item can carry that are not methods.
// Deliberately a list of exclusions rather than a list of the three methods
// this service answers: adding a fourth to the spec should fail here for want
// of a test, not be skipped for want of recognition.
var notAnOperation = map[string]bool{
	"$ref":        true,
	"description": true,
	"parameters":  true,
	"servers":     true,
	"summary":     true,
}

// responseKey identifies one documented response.
type responseKey struct {
	method string
	path   string
	status int
}

func (k responseKey) String() string {
	return k.method + " " + k.path + " -> " + strconv.Itoa(k.status)
}

// specCase is one documented response and the request that provokes it.
type specCase struct {
	responseKey
	send func(t *testing.T) *httptest.ResponseRecorder
}

func TestTheServiceAnswersAsTheSpecSays(t *testing.T) {
	documented := loadSpec(t)

	exercised := make(map[responseKey]bool, len(documented))
	for _, c := range specCases() {
		exercised[c.responseKey] = true

		t.Run(c.String(), func(t *testing.T) {
			want, ok := documented[c.responseKey]
			require.Truef(t, ok, "the spec does not document %s", c)

			res := c.send(t)

			require.Equal(t, c.status, res.Code, res.Body.String())
			assertMatchesSpec(t, want, res)
		})
	}

	// The other direction. Without this the spec could describe anything at all
	// so long as nobody wrote a case for it, which is the failure this file
	// exists to prevent.
	for k := range documented {
		assert.Truef(t, exercised[k], "%s is documented but nothing here provokes it", k)
	}
}

func specCases() []specCase {
	return []specCase{{
		responseKey: responseKey{http.MethodPost, "/contact/{form}", http.StatusAccepted},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			return specSend(specForm(t, mailertest.NewFake(), "", 100), http.MethodPost, "/contact", specBody, specOrigin)
		},
	}, {
		responseKey: responseKey{http.MethodPost, "/contact/{form}", http.StatusBadRequest},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			body := `{"name":"Ada","email":"ada@example.com","message":"long enough here","admin":true}`
			return specSend(specForm(t, mailertest.NewFake(), "", 100), http.MethodPost, "/contact", body, specOrigin)
		},
	}, {
		responseKey: responseKey{http.MethodPost, "/contact/{form}", http.StatusForbidden},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			return specSend(specForm(t, mailertest.NewFake(), "", 100), http.MethodPost, "/contact", specBody, "https://someone-else.example")
		},
	}, {
		responseKey: responseKey{http.MethodPost, "/contact/{form}", http.StatusNotFound},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			mux, _ := serve(t, slog.LevelInfo)
			return specSend(mux, http.MethodPost, "/contact/nosuchform", specBody, specOrigin)
		},
	}, {
		responseKey: responseKey{http.MethodPost, "/contact/{form}", http.StatusUnprocessableEntity},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			body := `{"name":"","email":"ada@example.com","message":"long enough to pass"}`
			return specSend(specForm(t, mailertest.NewFake(), "", 100), http.MethodPost, "/contact", body, specOrigin)
		},
	}, {
		responseKey: responseKey{http.MethodPost, "/contact/{form}", http.StatusTooManyRequests},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			h := specForm(t, mailertest.NewFake(), "", 1)
			require.Equal(t, http.StatusAccepted,
				specSend(h, http.MethodPost, "/contact", specBody, specOrigin).Code,
				"the setup never spent the one submission the limit allows")
			return specSend(h, http.MethodPost, "/contact", specBody, specOrigin)
		},
	}, {
		responseKey: responseKey{http.MethodPost, "/contact/{form}", http.StatusInternalServerError},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			// Parses, so it survives startup, and fails at execution because
			// .Name is a string with no such field. That is the only way this
			// service reaches a 500, and it is why the spec can claim one.
			h := specForm(t, mailertest.NewFake(), "{{ .Name.Missing }}", 100)
			return specSend(h, http.MethodPost, "/contact", specBody, specOrigin)
		},
	}, {
		responseKey: responseKey{http.MethodPost, "/contact/{form}", http.StatusBadGateway},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			h := specForm(t, mailertest.NewFake().Breaks(io.ErrUnexpectedEOF), "", 100)
			return specSend(h, http.MethodPost, "/contact", specBody, specOrigin)
		},
	}, {
		responseKey: responseKey{http.MethodOptions, "/contact/{form}", http.StatusNoContent},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			return specSend(specForm(t, mailertest.NewFake(), "", 100), http.MethodOptions, "/contact", "", specOrigin)
		},
	}, {
		responseKey: responseKey{http.MethodOptions, "/contact/{form}", http.StatusNotFound},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			mux, _ := serve(t, slog.LevelInfo)
			return specSend(mux, http.MethodOptions, "/contact/nosuchform", "", specOrigin)
		},
	}, {
		responseKey: responseKey{http.MethodGet, "/contact/{form}", http.StatusMethodNotAllowed},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			return specSend(specForm(t, mailertest.NewFake(), "", 100), http.MethodGet, "/contact", "", specOrigin)
		},
	}, {
		responseKey: responseKey{http.MethodGet, "/healthz", http.StatusOK},
		send: func(t *testing.T) *httptest.ResponseRecorder {
			mux, _ := serve(t, slog.LevelInfo)
			return specSend(mux, http.MethodGet, "/healthz", "", "")
		},
	}}
}

// The spec templates one path and the service also answers on /contact, so the
// alias is the one part of the contract the table above cannot reach. Proving
// it shares a rate limit proves it is the same handler and not a second one
// that happens to behave alike today.
func TestTheAliasIsTheDefaultFormItself(t *testing.T) {
	mux, err := server.New(config.Config{
		Forms: []config.Form{{
			ID:               config.DefaultFormID,
			Origins:          []string{specOrigin},
			From:             "site@example.com",
			To:               "info@example.com",
			RateLimitPerHour: 1,
			SMTP:             &config.SMTP{Addr: "127.0.0.1:1", Timeout: time.Second},
		}},
	}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	first := specSend(mux, http.MethodPost, "/contact", specBody, specOrigin)
	require.Equal(t, http.StatusBadGateway, first.Code,
		"the setup never reached the mailer, so it never spent the allowance")

	second := specSend(mux, http.MethodPost, "/contact/default", specBody, specOrigin)

	assert.Equal(t, http.StatusTooManyRequests, second.Code,
		"/contact and /contact/default kept separate allowances, so they are not the same form")
}

// loadSpec reads the document into the responses it claims the service returns.
func loadSpec(t *testing.T) map[responseKey]specResponse {
	t.Helper()

	raw, err := os.ReadFile(specPath)
	require.NoError(t, err, "the spec this test holds the service to is missing")

	var doc specDoc
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Paths, "the spec documents no paths")

	out := map[responseKey]specResponse{}
	for path, item := range doc.Paths {
		for method, node := range item {
			if notAnOperation[method] {
				continue
			}

			var op specOperation
			require.NoErrorf(t, node.Decode(&op), "%s %s", method, path)
			require.NotEmptyf(t, op.Responses, "%s %s documents no responses", method, path)

			for status, res := range op.Responses {
				code, err := strconv.Atoi(status)
				require.NoErrorf(t, err, "%s %s: %q is not a status code", method, path, status)
				out[responseKey{strings.ToUpper(method), path, code}] = resolve(t, doc, res)
			}
		}
	}
	return out
}

// resolve follows a reference to a shared response. Anything pointing outside
// components/responses fails rather than being quietly treated as empty, which
// would let a referenced response go unchecked.
func resolve(t *testing.T, doc specDoc, res specResponse) specResponse {
	t.Helper()

	if res.Ref == "" {
		return res
	}

	const prefix = "#/components/responses/"
	require.Truef(t, strings.HasPrefix(res.Ref, prefix), "this test cannot follow %q", res.Ref)

	shared, ok := doc.Components.Responses[strings.TrimPrefix(res.Ref, prefix)]
	require.Truef(t, ok, "%s does not resolve", res.Ref)
	require.Emptyf(t, shared.Ref, "%s points at another reference", res.Ref)

	return shared
}

func assertMatchesSpec(t *testing.T, want specResponse, res *httptest.ResponseRecorder) {
	t.Helper()

	for name, header := range want.Headers {
		got := res.Header().Get(name)
		require.NotEmptyf(t, got, "the spec documents a %s header the response did not carry", name)
		if fixed := header.Schema.Const; fixed != "" {
			assert.Equalf(t, fixed, got, "%s", name)
		}
	}

	if len(want.Content) == 0 {
		assert.Emptyf(t, res.Body.Bytes(), "the spec documents no body, and one came back")
		return
	}

	mediaType, _, err := mime.ParseMediaType(res.Header().Get("Content-Type"))
	require.NoError(t, err)
	media, ok := want.Content[mediaType]
	require.Truef(t, ok, "answered %s, which the spec does not document", mediaType)
	require.NotNilf(t, media.Example, "%s has no example for the body to be checked against", mediaType)

	example, err := json.Marshal(media.Example)
	require.NoError(t, err)
	assert.JSONEq(t, string(example), res.Body.String())
}

func specForm(t *testing.T, m contact.Mailer, subject string, perHour int) *contact.Handler {
	t.Helper()

	h, err := contact.NewHandler(contact.Form{
		ID:      config.DefaultFormID,
		Origins: []string{specOrigin},
		Subject: subject,
	}, m, ratelimit.New(perHour, time.Hour), slog.New(slog.DiscardHandler), clientip.Resolver{})
	require.NoError(t, err)
	return h
}

func specSend(h http.Handler, method, path, body, origin string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}

	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}
