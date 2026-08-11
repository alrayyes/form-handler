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
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alrayyes/form-handler/internal/config"
	"github.com/alrayyes/form-handler/internal/server"
)

// version is stamped in at build time — by goreleaser from the tag, and by ko
// from its VERSION environment variable. "dev" is what you get from a plain
// `go build`, which is the honest answer for a binary built off an unknown tree.
var version = "dev"

func main() {
	// -healthcheck exists because the image is distroless: there is no shell,
	// no wget and no curl for a container healthcheck to run. The binary is the
	// only executable in there, so it has to be able to probe itself.
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if *healthcheck {
		if err := probe(); err != nil {
			log.Error("healthcheck failed", "error", err)
			os.Exit(1)
		}
		return
	}

	if err := run(log); err != nil {
		log.Error("exiting", "error", err)
		os.Exit(1)
	}
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
