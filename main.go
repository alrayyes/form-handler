// Command form-handler accepts the andthensome.nl contact form and emails it on.
//
// Cloudflare Workers can send mail through an HTTP email API but cannot open an
// SMTP connection, and the mail for this domain is self-hosted. So this is a
// small service rather than a function at the edge.
package main

import (
	"context"
	"errors"
	"log/slog"
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

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(log); err != nil {
		log.Error("exiting", "error", err)
		os.Exit(1)
	}
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
		log.Info("listening", "addr", addr, "origins", origins)
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
