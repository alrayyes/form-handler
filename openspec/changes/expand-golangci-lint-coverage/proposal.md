## Why

`.golangci.yml` enables only 6 linters (`bodyclose`, `errorlint`, `gosec`,
`misspell`, `revive`, `unconvert`) plus `gofmt`/`goimports` formatters, well
short of the canonical set the project's own Go linting conventions call for.
Missing linters mean missing classes of bugs going unchecked in CI: `errcheck`
already runs via `standard`, but nothing here catches a copy-pasted function
(`dupl`), a dynamic `errors.New` where a wrapped sentinel belongs (`err113`),
an external error returned unwrapped (`wrapcheck`), a testify assertion with
arguments backwards (`testifylint`), or a helper that doesn't call
`t.Helper()` (`thelper`).

## What Changes

- Add the missing linters to `.golangci.yml`: `dupl`, `err113`, `funlen`,
  `gocognit`, `gocritic` (with `emptyStringTest` enabled), `gocyclo`,
  `makezero`, `modernize`, `nlreturn`, `nolintlint` (with
  `require-explanation`/`require-specific`), `perfsprint`, `sqlclosecheck`,
  `testifylint` (`enable-all`), `thelper`, `unparam`, `usestdlibvars`,
  `whitespace`, `wrapcheck`.
- Add `gofumpt` and `gci` to `formatters.enable` alongside the existing
  `gofmt`/`goimports` — note `gofumpt` is a superset of `gofmt`, so the plain
  `gofmt` formatter comes out rather than sitting beside it.
- Add a `_test.go` exclusion for `dupl` (table-driven tests are legitimately
  repetitive in a way it can't distinguish from a real copy-paste mistake).
- Fix every finding the newly enabled linters surface across the existing
  codebase, or add a `//nolint` with a specific linter name and an explanation
  where a finding is a deliberate false positive (required by
  `nolintlint`'s own settings).
- **BREAKING** for CI, not for the service: any pull request open when this
  change merges may need its own fixes to pass `golangci-lint run`
  afterwards.

## Capabilities

No spec-level behaviour changes — this is CI/lint tooling configuration and
the code clean-up it requires, not a change to what the service does. Any
externally observable behaviour must stay identical; a linter finding that
would change behaviour gets fixed in a way that preserves it, not worked
around by disabling the check.

## Impact

- `.golangci.yml`: linter and formatter config.
- Every `.go` file with a finding under the newly enabled linters (scope
  unknown until the linters actually run — see `design.md`).
- CI's `lint` job and the `golangci-lint` pre-commit hook both already run
  `golangci-lint`, so no workflow or hook wiring changes, only findings to
  fix.
