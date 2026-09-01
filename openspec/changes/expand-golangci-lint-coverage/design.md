## Context

See `proposal.md` - Why. `.golangci.yml` currently enables 6 linters against
golangci-lint's `standard` preset baseline; the project's own Go linting
convention specifies a larger set. This touches every `.go` file in the
module, cross-cutting by nature, and the size of what surfaces is unknown
until the linters actually run.

## Goals / Non-Goals

**Goals:**

- Bring `.golangci.yml` in line with the target linter set.
- Fix every real finding without changing observable behaviour.
- Keep each pull request small enough to review — `pull-request` conventions
  put ~300 changed lines as the point to stop and ask whether it's really one
  change.

**Non-Goals:**

- Not a general refactor. A linter finding gets the smallest fix that
  satisfies it, not a rewrite of the surrounding code.
- Not a change to CI/hook wiring - `golangci-lint run` and the pre-commit
  hook already invoke the full config; only what they find changes.

## Decisions

- **Land the config change and the fixes together, split by linter or by
  package rather than as one pull request.** Enabling a linter with no fix
  for what it immediately finds leaves CI red on `master` for anyone who
  pulls between the two; splitting keeps each pull request reviewable and
  each one leaves the tree green.
- **Run `golangci-lint run --build-tags=integration,containertest ./...`
  locally first to see the actual finding count and shape before deciding
  how many pull requests it takes.** The proposal can't size this precisely
  ahead of time (see Open Questions).
- **A false positive gets `//nolint:<linter> // <reason>`, not a settings
  change that weakens the linter repo-wide.** `nolintlint`'s own
  `require-explanation`/`require-specific` settings enforce this once
  enabled, so this decision also makes the config's own gate pass.
- **`gofmt` comes out of `formatters.enable` when `gofumpt` goes in.**
  `gofumpt` is a strict superset; keeping both is a formatter arguing with
  itself over the same bytes, and `go-lint.md` calls this out explicitly.

## Risks / Trade-offs

- [Finding count is large enough that fixing all of them stalls other work]
  → Land the config change with the full target set from the start (so CI
  is checking the real thing from commit one), and work through findings in
  batches by linter or package, each in its own pull request, rather than
  gating the config change on every finding being fixed first.
- [A `//nolint` gets reached for out of convenience rather than because the
  finding is genuinely a false positive] → `nolintlint`'s
  `require-explanation`/`require-specific` settings force a named linter and
  a reason on every one, which at least makes each one visible in review;
  the reviewer still has to judge whether the reason holds.
- [`testifylint`'s `enable-all` or `gocritic`/`revive` surface a large volume
  of low-value style findings] → Each is still gated by its own linter name
  in a `//nolint` where it's genuinely not worth fixing; nothing here commits
  to fixing 100% of findings, only to not silently disabling a linter that's
  mostly right.

## Migration Plan

1. Update `.golangci.yml`: add the linters and formatters listed in the
   proposal, plus the `dupl` test exclusion.
2. Run `golangci-lint run --build-tags=integration,containertest ./...`
   locally and record the finding count per linter.
3. Fix findings in batches (by linter or by package, whichever groups more
   naturally given what actually surfaces), each batch its own pull request.
4. Once findings are clear, confirm `golangci-lint run` is green in CI on
   `master`.

No rollback beyond reverting the config change - nothing here is a runtime
behaviour change, only what's checked in CI and at commit time.

## Open Questions

- Exact finding count and its distribution across linters/packages - won't
  be known until `golangci-lint run` is executed with the new config, which
  is the first implementation task rather than something to guess at here.
