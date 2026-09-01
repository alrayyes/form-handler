## Why

Issue #91: `internal/mail/mailgun` has no coverage in CI's `test` job (`go
test -race -covermode=atomic -coverprofile=coverage.out ./...`, no build
tags). The obvious read is "write unit tests for it," but
`mailgun_integration_test.go` turns out to already be exactly that: a
`httptest.Server` stub with no real network dependency, no container, no
external service. It only carries `//go:build integration` because it
followed `smtp`'s file-naming convention, where the equivalent file genuinely
does need a tag (it drives a real Mailpit container). Mailgun has no runnable
service to test against in CI at all, so its own file header already says as
much: "a container would add a hop without adding a single byte of fidelity."

The fix is smaller than the issue assumed: drop the tag, and the file joins
the default test run with no other change needed.

## What Changes

- Rename `mailgun_integration_test.go` to `mailgun_test.go` and remove its
  `//go:build integration` tag, so it runs under a plain `go test ./...`.
- Reword the file's doc comment: it currently frames itself against "every
  other integration test in this repository," which stops being true once
  it isn't one.
- Add the two real gaps this exposes once it's read as a unit-test file
  rather than an integration one: `New` rejecting an unknown `Region`, and
  `Send` reporting a timeout (`Op: "timeout"`) rather than a generic
  `"send"` failure when the context deadline is what ends the call.
- No production code changes.

## Capabilities

No spec-level behaviour changes — this adds test coverage for existing
behaviour and corrects a build tag; it does not change what the service
does.

## Impact

- `internal/mail/mailgun/mailgun_integration_test.go` → renamed to
  `mailgun_test.go`, build tag removed, two new test cases.
- CI: `go test ./...` (no tags) now covers this package; no workflow
  changes needed — nothing else in the repository referenced the old filename or
  build tag.
