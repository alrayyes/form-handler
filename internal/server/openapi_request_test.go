// SPDX-License-Identifier: GPL-3.0-or-later

package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/alrayyes/form-handler/internal/contact/mailertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The request half of the same job openapi_test.go does for responses.
//
// That file left this open, and the gap was the one the spec exists to close:
// api/openapi.yaml states the limits — 100 characters of name, 10 to 5000 of
// message, no unknown fields — and nothing made the service agree. Change
// MaxNameLen to 200 and the document still said 100, with every test green.
//
// So the numbers here are read out of the spec rather than written down again.
// A case sits a field exactly on its stated bound and expects the submission
// through, then one character past it and expects a refusal. Editing the spec
// moves the tests, which is the only arrangement where the two cannot drift.
type specSchema struct {
	Ref                  string                `yaml:"$ref"`
	Type                 string                `yaml:"type"`
	Format               string                `yaml:"format"`
	AdditionalProperties *bool                 `yaml:"additionalProperties"`
	Required             []string              `yaml:"required"`
	Properties           map[string]specSchema `yaml:"properties"`
	MinLength            *int                  `yaml:"minLength"`
	MaxLength            *int                  `yaml:"maxLength"`
	Enum                 []string              `yaml:"enum"`
}

// requestDoc reads the request body's schema and the error schema the refusals
// are graded against.
type requestDoc struct {
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Schemas map[string]specSchema `yaml:"schemas"`
	} `yaml:"components"`
}

type specRequestOperation struct {
	RequestBody struct {
		Content map[string]struct {
			Schema specSchema `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"requestBody"`
}

const submitPath = "/contact/{form}"

func TestTheEndpointEnforcesTheRequestSchema(t *testing.T) {
	doc := loadRequestDoc(t)
	submission := requestSchema(t, doc)
	invalid := resolveSchema(t, doc, specSchema{Ref: "#/components/schemas/ValidationError"})

	require.NotEmpty(t, submission.Properties, "the spec describes no request body to check")

	// The generator has to produce something the service accepts, or every
	// case below passes for the wrong reason.
	t.Run("the spec's own example of a valid submission is accepted", func(t *testing.T) {
		res := submit(t, validSubmission(t, submission))

		require.Equal(t, http.StatusAccepted, res.Code, res.Body.String())
	})

	for _, field := range sortedKeys(submission.Properties) {
		prop := submission.Properties[field]

		if prop.MaxLength != nil {
			t.Run(field+" at its documented maximum is accepted", func(t *testing.T) {
				body := validSubmission(t, submission)
				body[field] = valueOfLength(prop, *prop.MaxLength)

				res := submit(t, body)

				require.Equal(t, http.StatusAccepted, res.Code, res.Body.String())
			})

			t.Run(field+" past its documented maximum is refused", func(t *testing.T) {
				body := validSubmission(t, submission)
				body[field] = valueOfLength(prop, *prop.MaxLength+1)

				res := submit(t, body)

				assertRefusedField(t, invalid, res, field)
			})
		}

		if prop.MinLength != nil && *prop.MinLength > 0 {
			t.Run(field+" at its documented minimum is accepted", func(t *testing.T) {
				// A length bound is not the only thing a property can ask for.
				// email is minLength 1 and format email, and no one-character
				// string satisfies both — the shortest value that passes is
				// decided by the format, not the number. Skipped rather than
				// fudged, and the format gets its own case below.
				if prop.Format != "" {
					t.Skipf("%s is also %s, which a value of minimum length cannot satisfy", field, prop.Format)
				}

				body := validSubmission(t, submission)
				body[field] = valueOfLength(prop, *prop.MinLength)

				res := submit(t, body)

				require.Equal(t, http.StatusAccepted, res.Code, res.Body.String())
			})

			t.Run(field+" short of its documented minimum is refused", func(t *testing.T) {
				body := validSubmission(t, submission)
				body[field] = valueOfLength(prop, *prop.MinLength-1)

				res := submit(t, body)

				assertRefusedField(t, invalid, res, field)
			})
		}

		if prop.Format != "" {
			t.Run(field+" has to satisfy its documented format", func(t *testing.T) {
				body := validSubmission(t, submission)
				// Inside every length bound the spec states, and still not an
				// address. Without this the format is the one keyword in the
				// schema nothing holds the service to.
				body[field] = malformed(prop)

				res := submit(t, body)

				assertRefusedField(t, invalid, res, field)
			})
		}
	}

	for _, field := range submission.Required {
		t.Run(field+" is required, so leaving it out is refused", func(t *testing.T) {
			body := validSubmission(t, submission)
			delete(body, field)

			res := submit(t, body)

			assertRefusedField(t, invalid, res, field)
		})
	}

	t.Run("a field the spec does not describe is refused", func(t *testing.T) {
		if submission.AdditionalProperties == nil || *submission.AdditionalProperties {
			t.Skip("the spec allows properties it does not name, so there is nothing to enforce")
		}

		body := validSubmission(t, submission)
		body["admin"] = true

		res := submit(t, body)

		// 400 rather than 422: this one is caught by the decoder, before there
		// is a submission to find fault with.
		require.Equal(t, http.StatusBadRequest, res.Code, res.Body.String())
	})
}

// assertRefusedField checks a refusal against what the spec says a refusal
// looks like, rather than against strings copied out of the handler.
func assertRefusedField(t *testing.T, invalid specSchema, res *httptest.ResponseRecorder, field string) {
	t.Helper()

	require.Equal(t, http.StatusUnprocessableEntity, res.Code, res.Body.String())

	var body struct {
		Error  string `json:"error"`
		Field  string `json:"field"`
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &body))

	assert.Equal(t, field, body.Field, "a different field was blamed")
	// The spec enumerates both, so a handler inventing a new reason is a spec
	// change that has not been written down.
	assert.Contains(t, invalid.Properties["field"].Enum, body.Field)
	assert.Contains(t, invalid.Properties["reason"].Enum, body.Reason)
}

// valueOfLength builds a value of exactly n characters that still satisfies
// whatever else the property asks for. The format matters: a schema saying
// `format: email` is asking for something net/mail will parse, and a string of
// 254 letters is not that.
func valueOfLength(prop specSchema, n int) string {
	if prop.Format != "email" {
		return strings.Repeat("a", n)
	}

	const domain = "@example.com"
	if n <= len(domain) {
		// Too short to be an address at all, which is the point at these
		// lengths: the service should refuse it either way.
		return strings.Repeat("a", n)
	}

	return strings.Repeat("a", n-len(domain)) + domain
}

// malformed returns a value that breaks the property's format and nothing else.
func malformed(prop specSchema) string {
	switch prop.Format {
	case "email":
		// Also what a visitor types when they leave the @ out, which is the
		// case this is really protecting.
		return "not-an-address"
	default:
		// A format this test has never seen. Returning something valid would
		// make the case pass by accident, so return something no format
		// accepts and let it fail loudly if that assumption is ever wrong.
		return "\x00 not a valid anything"
	}
}

// validSubmission builds a body that satisfies every constraint the spec
// states, so a case can break exactly one thing.
func validSubmission(t *testing.T, submission specSchema) map[string]any {
	t.Helper()

	body := map[string]any{}
	for field, prop := range submission.Properties {
		switch {
		case prop.Format == "email":
			body[field] = "ada@example.com"
		case prop.MinLength != nil && *prop.MinLength > 0:
			// Comfortably inside both bounds rather than on either.
			body[field] = strings.Repeat("a", *prop.MinLength+1)
		default:
			// The honeypot and anything like it: present and empty.
			body[field] = ""
		}
	}

	return body
}

func submit(t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	raw, err := json.Marshal(body)
	require.NoError(t, err)

	return specSend(specForm(t, mailertest.NewFake(), "", 100),
		http.MethodPost, "/contact", string(raw), specOrigin)
}

func loadRequestDoc(t *testing.T) requestDoc {
	t.Helper()

	raw, err := os.ReadFile(specPath)
	require.NoError(t, err)

	var doc requestDoc
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	return doc
}

// requestSchema digs out the body of the one operation that takes one.
func requestSchema(t *testing.T, doc requestDoc) specSchema {
	t.Helper()

	item, ok := doc.Paths[submitPath]
	require.Truef(t, ok, "the spec no longer documents %s", submitPath)

	node, ok := item["post"]
	require.Truef(t, ok, "%s no longer documents a post", submitPath)

	var op specRequestOperation
	require.NoError(t, node.Decode(&op))

	media, ok := op.RequestBody.Content["application/json"]
	require.True(t, ok, "the request body is no longer JSON")

	return resolveSchema(t, doc, media.Schema)
}

// resolveSchema follows a reference into components/schemas. Narrow on purpose,
// like the response side: anything else fails rather than resolving to an empty
// schema that would quietly assert nothing.
func resolveSchema(t *testing.T, doc requestDoc, s specSchema) specSchema {
	t.Helper()

	if s.Ref == "" {
		return s
	}

	const prefix = "#/components/schemas/"
	require.Truef(t, strings.HasPrefix(s.Ref, prefix), "this test cannot follow %q", s.Ref)

	out, ok := doc.Components.Schemas[strings.TrimPrefix(s.Ref, prefix)]
	require.Truef(t, ok, "%s does not resolve", s.Ref)

	return out
}

// sortedKeys keeps the subtests in a stable order. Ranging a map directly gives
// a different order every run, which makes a failure harder to compare against
// the last one.
func sortedKeys(m map[string]specSchema) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)

	return out
}
