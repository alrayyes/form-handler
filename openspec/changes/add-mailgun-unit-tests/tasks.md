## 1. Rename and remove the build tag

- [x] 1.1 `git mv internal/mail/mailgun/mailgun_integration_test.go
internal/mail/mailgun/mailgun_test.go` and remove the
      `//go:build integration` line - verify `go test
./internal/mail/mailgun/...` (no build tags) runs the file's tests.
- [x] 1.2 Reword the file's top doc comment: it currently contrasts itself
      with "every other integration test in this repository," which is no
      longer accurate once it carries no build tag - verify the comment
      still explains why a `httptest.Server` stub is the right choice for
      Mailgun specifically (a hosted API with nothing runnable to test
      against, unlike SMTP's Mailpit).

## 2. Close the two real gaps

- [x] 2.1 Add a case to `New`'s config table (or a new test) for an unknown
      `Region` - verify it returns an error wrapping
      `mailgun.ErrBadRegion`.
- [x] 2.2 Add a test that drives `Send` against a stub that never responds,
      with a short `Config.Timeout` - verify the returned
      `*contact.DeliveryError` has `Op == "timeout"`, not `"send"`,
      exercising the `errors.Is(err, context.DeadlineExceeded)` branch
      already in `Send`.

## 3. Verify

- [x] 3.1 Run `go test -race ./...` (no tags) and confirm non-zero coverage
      for `internal/mail/mailgun`.
- [x] 3.2 Run `go test -race -tags=integration,containertest ./...` and
      `golangci-lint run --build-tags=integration,containertest ./...` to
      confirm nothing else in the repository assumed the old filename or build
      tag, and the linter stays at 0 issues.
