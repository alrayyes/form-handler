// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// healthyOn starts a server answering /healthz on loopback and points ADDR at
// it, which is where probe() looks. Returns nothing: the flags under test read
// the environment rather than taking an address.
func healthyOn(t *testing.T, status int) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	// httptest listens on 127.0.0.1, which is the only host probe() will talk
	// to; it takes the port from ADDR and fixes the host itself.
	t.Setenv("ADDR", strings.TrimPrefix(srv.URL, "http://"))
}

func TestVersionFlagPrintsBareVersion(t *testing.T) {
	// Bare, with no "form-handler version" prefix: the README documents it as
	// the way to ask which release a digest is, and something is parsing that.
	for _, arg := range []string{"--version", "-version"} {
		t.Run(arg, func(t *testing.T) {
			var out, errOut strings.Builder

			code := cli([]string{arg}, &out, &errOut)

			assert.Equal(t, 0, code)
			assert.Equal(t, version+"\n", out.String())
		})
	}
}

func TestHealthcheckFlagProbesTheLocalService(t *testing.T) {
	// -healthcheck, single dash, is what the deployed container's healthcheck
	// runs. pflag reads that as a cluster of shorthands rather than a long
	// flag, and -h is help - so without the compatibility shim this exits 0
	// having printed usage, and every container looks healthy forever.
	for _, arg := range []string{"--healthcheck", "-healthcheck"} {
		t.Run(arg, func(t *testing.T) {
			healthyOn(t, http.StatusOK)
			var out, errOut strings.Builder

			code := cli([]string{arg}, &out, &errOut)

			assert.Equal(t, 0, code)
			assert.NotContains(t, out.String(), "Usage:", "printed help instead of probing")
		})
	}
}

func TestHealthcheckFlagFailsWhenTheServiceIsUnhealthy(t *testing.T) {
	for _, arg := range []string{"--healthcheck", "-healthcheck"} {
		t.Run(arg, func(t *testing.T) {
			healthyOn(t, http.StatusInternalServerError)
			var out, errOut strings.Builder

			code := cli([]string{arg}, &out, &errOut)

			assert.Equal(t, 1, code)
		})
	}
}

func TestUnknownFlagIsRejected(t *testing.T) {
	var out, errOut strings.Builder

	code := cli([]string{"--nonsense"}, &out, &errOut)

	assert.Equal(t, 1, code)
	assert.Contains(t, errOut.String(), "nonsense")
}

func TestUnknownShorthandIsStillRejected(t *testing.T) {
	// The compatibility shim rewrites the two long flags it knows. It must not
	// turn every single-dash argument into a long one, or a typo becomes a
	// silently accepted flag.
	var out, errOut strings.Builder

	code := cli([]string{"-nonsense"}, &out, &errOut)

	assert.Equal(t, 1, code)
}

func TestHelpListsBothFlags(t *testing.T) {
	var out, errOut strings.Builder

	code := cli([]string{"--help"}, &out, &errOut)

	require.Equal(t, 0, code)
	assert.Contains(t, out.String(), "--healthcheck")
	assert.Contains(t, out.String(), "--version")
}

func TestPositionalArgumentsAreRejected(t *testing.T) {
	// It takes no arguments. Accepting and ignoring one hides a typo'd flag.
	var out, errOut strings.Builder

	code := cli([]string{"serve"}, &out, &errOut)

	assert.Equal(t, 1, code)
}
