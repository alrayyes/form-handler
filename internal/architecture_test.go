// SPDX-License-Identifier: GPL-3.0-or-later

// The rule that makes the layering real rather than a diagram in a README.
//
// Clean architecture is one constraint — dependencies point inward — and
// nothing in Go enforces it. Without a test, the first import added under
// deadline pressure quietly makes the domain depend on HTTP, and by the time
// anybody notices, undoing it is a project rather than a revert.
package internal_test

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const module = "github.com/alrayyes/form-handler"

// layers, innermost first. A package may import its own layer and anything
// further in; importing outward is what this test exists to catch.
var layers = []struct {
	name     string
	packages []string
	mayNotSee
}{
	{
		name:     "domain",
		packages: []string{module + "/internal/domain"},
		// The centre. It knows nothing about the service around it — not how a
		// submission arrives, not where a message goes, not what time it is.
		mayNotSee: mayNotSee{
			module + "/internal/usecase",
			module + "/internal/adapter",
			module + "/internal/config",
			module + "/internal/server",
			module + "/internal/mail",
			module + "/internal/clientip",
		},
	},
	{
		name:     "usecase",
		packages: []string{module + "/internal/usecase"},
		// It may use the domain and the ports it declares itself. It may not
		// know that HTTP exists, which is the whole reason its tests can call
		// functions instead of building requests.
		mayNotSee: mayNotSee{
			module + "/internal/adapter",
			module + "/internal/config",
			module + "/internal/server",
			module + "/internal/mail",
			module + "/internal/clientip",
		},
	},
	{
		name: "adapters",
		packages: []string{
			module + "/internal/adapter/http",
			module + "/internal/adapter/ratelimit",
			module + "/internal/mail/smtp",
			module + "/internal/mail/mailgun",
			module + "/internal/config",
		},
		// Adapters implement what the inside asked for. One adapter reaching
		// into another means a rule has escaped into the edge, and the
		// composition root is the only place allowed to know about both.
		mayNotSee: mayNotSee{
			module + "/internal/server",
		},
	},
}

type mayNotSee []string

func TestDependenciesPointInward(t *testing.T) {
	for _, layer := range layers {
		t.Run(layer.name, func(t *testing.T) {
			for _, pkg := range layer.packages {
				for _, imported := range importsOf(t, pkg) {
					for _, forbidden := range layer.mayNotSee {
						assert.NotEqual(t, forbidden, imported,
							"%s imports %s — that arrow points outward", pkg, imported)
						assert.False(t, strings.HasPrefix(imported, forbidden+"/"),
							"%s imports %s — that arrow points outward", pkg, imported)
					}
				}
			}
		})
	}
}

// The domain is the one worth stating separately: it should depend on the
// standard library and nothing else at all.
func TestTheDomainDependsOnNothingOfOurs(t *testing.T) {
	for _, imported := range importsOf(t, module+"/internal/domain") {
		assert.False(t, strings.HasPrefix(imported, module),
			"the domain imports %s; it should need only the standard library", imported)
	}
}

// importsOf asks the toolchain rather than parsing imports by hand, so build
// tags, test files and transitive detail are resolved the way a build resolves
// them.
func importsOf(t *testing.T, pkg string) []string {
	t.Helper()

	out, err := exec.Command("go", "list", "-json", pkg).Output()
	require.NoErrorf(t, err, "go list %s", pkg)

	var described struct {
		Imports     []string
		TestImports []string
	}
	require.NoError(t, json.Unmarshal(out, &described))

	return append(described.Imports, described.TestImports...)
}
