// SPDX-License-Identifier: GPL-3.0-or-later

//go:build containertest

// The one test that runs what actually ships. Everything else — the outer
// integration test in internal/server, every unit test — proves the Go code
// works and never touches the compiled binary. There is no Dockerfile; ko
// assembles the image (.ko.yaml), and this is what starts it, sends it a real
// request, and asserts on the running container rather than on server.New.
//
// It stays thin on purpose. Routing, header injection, CORS, per-form SMTP —
// all of that is already proven in-process, faster, in
// internal/server/server_integration_test.go. What only the real artifact can
// break: the entrypoint path, whether the container starts and answers when
// configured purely by environment variables, and whether --healthcheck still
// works against the actual distroless binary.
package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Pinned by digest, same as every other image this repo pulls and the same
// one server_integration_test.go uses.
const mailpitImage = "axllent/mailpit:v1.21.8@sha256:81370195cd4a0eab9604d17c2617a7525b0486f9365555253b6c5376c6350f1a"

const origin = "https://www.example.com"

// A submission through the running container is delivered, using only
// environment variables to configure it — not the YAML forms file, which the
// in-process test already exercises. This is the container's own
// configuration path, and only the container can prove it works.
func TestTheContainerAcceptsAndDeliversASubmission(t *testing.T) {
	ctx := context.Background()
	image := formHandlerImage(ctx, t)
	nw := startNetwork(ctx, t)
	apiURL := startMailpit(ctx, t, nw)

	fh := startFormHandler(ctx, t, image, nw, map[string]string{
		"MAIL_FROM":       "site@example.com",
		"MAIL_TO":         "info@example.com",
		"ALLOWED_ORIGINS": origin,
		"SMTP_ADDR":       "mailpit:1025",
	})

	res := post(ctx, t, fh.baseURL+"/contact", origin,
		`{"name":"Ada Lovelace","email":"ada@example.com","message":"Does the container itself work?","website":""}`)
	require.Equal(t, http.StatusAccepted, res)

	msg := waitForMessageTo(ctx, t, apiURL, "info@example.com")
	assert.Contains(t, msg.Text, "Does the container itself work?")
	require.Len(t, msg.ReplyTo, 1)
	assert.Equal(t, "ada@example.com", msg.ReplyTo[0].Address)

	assertLoggedRequest(ctx, t, fh.container, "/contact", http.StatusAccepted)
}

// --healthcheck exists because the image is distroless — no shell, no curl,
// nothing to exec into but the binary itself — so this is the only way to
// prove it still works against the real thing at /ko-app/form-handler, not
// /form-handler. It must not depend on SMTP: an orchestrator using it to
// decide whether to restart the container should not do so because the mail
// server is briefly unreachable.
func TestTheContainerHealthcheckIgnoresSMTP(t *testing.T) {
	ctx := context.Background()
	image := formHandlerImage(ctx, t)

	fh := startFormHandler(ctx, t, image, nil, map[string]string{
		"MAIL_FROM":       "site@example.com",
		"MAIL_TO":         "info@example.com",
		"ALLOWED_ORIGINS": origin,
		// Deliberately unreachable, the same trick
		// TestHealthzAnswersWithoutTouchingSMTP uses.
		"SMTP_ADDR": "127.0.0.1:1",
	})

	exitCode, out, err := fh.container.Exec(ctx, []string{"/ko-app/form-handler", "--healthcheck"})
	require.NoError(t, err)
	if exitCode != 0 {
		b, _ := io.ReadAll(out)
		require.Equalf(t, 0, exitCode, "healthcheck output: %s", b)
	}
}

// formHandlerImage returns the image these tests run, however this machine
// can get one. Set FORM_HANDLER_IMAGE to point at one already built; CI's
// own image job already proves a plain build works, so these tests build
// their own copy the same way a contributor would locally rather than
// threading one job's output into another's.
func formHandlerImage(ctx context.Context, t *testing.T) string {
	t.Helper()

	if img := os.Getenv("FORM_HANDLER_IMAGE"); img != "" {
		return img
	}

	cmd := exec.CommandContext(ctx, "ko", "build", "--bare", "--local", ".")
	cmd.Env = append(os.Environ(), "KO_DOCKER_REPO=ko.local", "VERSION=containertest")
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		require.NoErrorf(t, err, "ko build: %s", exitErr.Stderr)
	}
	require.NoError(t, err, "ko build")

	image := strings.TrimSpace(string(out))
	require.NotEmpty(t, image, "ko build printed no image reference")
	return image
}

func startNetwork(ctx context.Context, t *testing.T) *testcontainers.DockerNetwork {
	t.Helper()

	nw, err := network.New(ctx)
	require.NoError(t, err, "create network")
	t.Cleanup(func() { _ = nw.Remove(ctx) })
	return nw
}

// startMailpit starts Mailpit reachable from the form-handler container by
// the network alias "mailpit", and returns the API URL reachable from the
// test process itself, on its mapped host port.
func startMailpit(ctx context.Context, t *testing.T, nw *testcontainers.DockerNetwork) (apiURL string) {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        mailpitImage,
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		Networks:     []string{nw.Name},
		NetworkAliases: map[string][]string{
			nw.Name: {"mailpit"},
		},
		WaitingFor: wait.ForListeningPort("1025/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "start mailpit")
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	require.NoError(t, err, "container host")
	apiPort, err := c.MappedPort(ctx, "8025")
	require.NoError(t, err, "api port")

	return fmt.Sprintf("http://%s:%s", host, apiPort.Port())
}

type formHandlerContainer struct {
	container testcontainers.Container
	baseURL   string
}

// startFormHandler starts the actual image these tests are about, configured
// purely by environment variables, and waits for it to answer its own
// healthz before handing back a URL. nw is nil for a test that never needs
// to reach anything else on the network.
func startFormHandler(ctx context.Context, t *testing.T, image string, nw *testcontainers.DockerNetwork, env map[string]string) formHandlerContainer {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        image,
		ExposedPorts: []string{"8080/tcp"},
		Env:          env,
		WaitingFor:   wait.ForHTTP("/healthz").WithPort("8080/tcp").WithStartupTimeout(30 * time.Second),
	}
	if nw != nil {
		req.Networks = []string{nw.Name}
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "start form-handler")
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	require.NoError(t, err, "container host")
	port, err := c.MappedPort(ctx, "8080")
	require.NoError(t, err, "form-handler port")

	return formHandlerContainer{
		container: c,
		baseURL:   fmt.Sprintf("http://%s:%s", host, port.Port()),
	}
}

func post(ctx context.Context, t *testing.T, url, origin, body string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	return res.StatusCode
}

type mailpitAddress struct {
	Address string `json:"Address"`
}

type mailpitMessage struct {
	ReplyTo []mailpitAddress `json:"ReplyTo"`
	Text    string           `json:"Text"`
}

func waitForMessageTo(ctx context.Context, t *testing.T, apiURL, recipient string) mailpitMessage {
	t.Helper()

	var found mailpitMessage
	require.Eventuallyf(t, func() bool {
		var list struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}
		getJSON(ctx, t, apiURL+"/api/v1/messages", &list)

		for _, m := range list.Messages {
			var msg mailpitMessage
			getJSON(ctx, t, apiURL+"/api/v1/message/"+m.ID, &msg)
			found = msg
			return true
		}
		return false
	}, 20*time.Second, 250*time.Millisecond, "no message arrived for %s", recipient)

	return found
}

func getJSON(ctx context.Context, t *testing.T, url string, into any) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	res, err := http.DefaultClient.Do(req)
	require.NoErrorf(t, err, "get %s", url)
	defer func() { _ = res.Body.Close() }()
	require.NoErrorf(t, json.NewDecoder(res.Body).Decode(into), "decode %s", url)
}

// assertLoggedRequest reads the container's stdout and asserts it carries the
// structured JSON line the README documents. It's the only way to check the
// distroless image's own logging: there's no shell inside it to grep with.
func assertLoggedRequest(ctx context.Context, t *testing.T, c testcontainers.Container, path string, status int) {
	t.Helper()

	rc, err := c.Logs(ctx)
	require.NoError(t, err, "container logs")
	defer func() { _ = rc.Close() }()

	logs, err := io.ReadAll(rc)
	require.NoError(t, err, "read logs")

	for line := range strings.Lines(string(logs)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry struct {
			Msg    string `json:"msg"`
			Path   string `json:"path"`
			Status int    `json:"status"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Msg == "request" && entry.Path == path && entry.Status == status {
			return
		}
	}
	require.Failf(t, "no matching log line", "wanted a request line for %s %d", path, status)
}
