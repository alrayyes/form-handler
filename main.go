// SPDX-License-Identifier: GPL-3.0-or-later

// Command form-handler accepts contact form submissions and emails them on.
//
// It was written for the contact form on andthensome.nl, because Cloudflare
// Workers can send mail through an HTTP email API but cannot open an SMTP
// connection, and the mail for that domain is self-hosted. It serves any number
// of forms across any number of sites: one endpoint per form, each with its own
// allowed origins and its own recipient.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/alrayyes/form-handler/internal/config"
	"github.com/alrayyes/form-handler/internal/server"
)

// version is stamped in at build time — by goreleaser from the tag, and by ko
// from its VERSION environment variable. "dev" is what you get from a plain
// `go build`, which is the honest answer for a binary built off an unknown tree.
var version = "dev"

func main() {
	os.Exit(cli(os.Args[1:], os.Stdout, os.Stderr))
}

// cli parses args and does what they ask, returning the process exit code. It
// exists as a seam: main() may not return, so everything worth asserting on
// lives here instead.
func cli(args []string, stdout, stderr io.Writer) int {
	cmd := newRootCommand(stdout)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(legacySingleDash(args))

	if err := cmd.Execute(); err != nil {
		// Nothing useful to do if even stderr will not take it.
		_, _ = fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

// legacyFlags are the two the standard library's flag package accepted behind a
// single dash, before this command was parsed by pflag.
var legacyFlags = []string{"-healthcheck", "-version"}

// legacySingleDash rewrites those two into the double-dash form pflag needs.
//
// It is a compatibility shim rather than a nicety. `flag` treats a single dash
// and a double dash alike, so `-healthcheck` is what the README documented and,
// more to the point, what the healthcheck baked into already-deployed compose
// files runs. pflag reads a single dash as a cluster of shorthands instead, and
// the first letter of `-healthcheck` is `h` — help. Without this, a container
// probing itself prints usage, exits 0 and reports healthy no matter what the
// service is doing.
//
// Only these two exact arguments are rewritten. Anything else keeps its
// meaning, so `-nonsense` is still an error rather than being promoted into a
// long flag nobody defined.
func legacySingleDash(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i, arg := range out {
		if slices.Contains(legacyFlags, arg) {
			out[i] = "-" + arg
		}
	}
	return out
}

func newRootCommand(stdout io.Writer) *cobra.Command {
	var healthcheck bool

	cmd := &cobra.Command{
		Use:   "form-handler",
		Short: "Turn contact form submissions into email",
		Long: "Accepts contact form submissions over HTTP and emails them on.\n\n" +
			"Configured from the environment, or from a forms file when FORMS_FILE\n" +
			"is set. See the README for the variables it reads.",
		Args: cobra.NoArgs,
		// A failure from run() is not a usage error, and printing the whole
		// usage block after "could not reach the mail server" buries it. cli()
		// prints the error itself.
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
		RunE: func(_ *cobra.Command, _ []string) error {
			log := slog.New(slog.NewJSONHandler(stdout, nil))

			if healthcheck {
				if err := probe(); err != nil {
					log.Error("healthcheck failed", "error", err)
					return err
				}
				return nil
			}

			if err := run(log); err != nil {
				log.Error("exiting", "error", err)
				return err
			}
			return nil
		},
	}

	// -healthcheck exists because the image is distroless: there is no shell,
	// no wget and no curl for a container healthcheck to run. The binary is the
	// only executable in there, so it has to be able to probe itself.
	cmd.Flags().BoolVar(&healthcheck, "healthcheck", false, "probe the local /healthz and exit")
	// Bare version output, not cobra's "form-handler version X" sentence: it is
	// documented as the way to ask a running container which release its digest
	// is, so something may well be reading it.
	cmd.SetVersionTemplate("{{.Version}}\n")

	return cmd
}

// probe asks the running process whether it is serving. Talks to itself over
// the loopback rather than the configured ADDR, which may be a wildcard bind.
func probe() error {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = config.DefaultAddr
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("ADDR %q: %w", addr, err)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	// The host is fixed to loopback and only the port is interpolated, from
	// this service's own ADDR rather than from anything a request carries.
	res, err := client.Get("http://127.0.0.1:" + port + "/healthz") //nolint:gosec // G704: loopback only, port from own config
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return nil
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	handler, err := server.New(cfg, log)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: handler,
		// A public endpoint with no read timeout is a slow-loris waiting to
		// happen: a handful of sockets dribbling headers holds the server open.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		ids := make([]string, 0, len(cfg.Forms))
		for _, f := range cfg.Forms {
			ids = append(ids, f.ID)
		}
		log.Info("listening", "version", version, "addr", cfg.Addr, "forms", ids)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		// Long enough to finish a message already being delivered, short enough
		// that a deploy is not held up by one.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
