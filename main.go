// Command form-handler accepts the andthensome.nl contact form and emails it on.
//
// Cloudflare Workers can send mail through an HTTP email API but cannot open an
// SMTP connection, and the mail for this domain is self-hosted. So this is a
// small service rather than a function at the edge.
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alrayyes/form-handler/internal/contact"
	"github.com/alrayyes/form-handler/internal/mail"
)

// version is stamped in at build time — by goreleaser from the tag, and by the
// Dockerfile from its VERSION build argument. "dev" is what you get from a plain
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
	addr := env("ADDR", ":8080")
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("ADDR %q: %w", addr, err)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://127.0.0.1:" + port + "/healthz")
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
	// Everything is wired here and passed explicitly. The service is one
	// handler and one sender; a container would be more ceremony than code.
	sender := mail.SMTP{
		Addr:     env("SMTP_ADDR", "localhost:1025"),
		Username: os.Getenv("SMTP_USERNAME"),
		Password: os.Getenv("SMTP_PASSWORD"),
		From:     env("MAIL_FROM", ""),
		To:       env("MAIL_TO", ""),
		Timeout:  10 * time.Second,
	}

	for name, value := range map[string]string{"MAIL_FROM": sender.From, "MAIL_TO": sender.To} {
		if value == "" {
			return errors.New(name + " is required")
		}
	}

	origins := strings.Split(env("ALLOWED_ORIGINS", "https://www.andthensome.nl"), ",")
	perHour, err := strconv.Atoi(env("RATE_LIMIT_PER_HOUR", "5"))
	if err != nil {
		return errors.New("RATE_LIMIT_PER_HOUR must be a number")
	}

	mux := http.NewServeMux()
	mux.Handle("/contact", contact.NewHandler(sender, log, origins, perHour))
	// Liveness only. It deliberately does not test SMTP: a mail server being
	// briefly unreachable is not a reason for the orchestrator to kill and
	// restart a process that is otherwise answering.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	addr := env("ADDR", ":8080")
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
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
		log.Info("listening", "version", version, "addr", addr, "origins", origins)
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

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
