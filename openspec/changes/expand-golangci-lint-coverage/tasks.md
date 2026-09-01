## 1. Config

- [ ] 1.1 Add `dupl`, `err113`, `funlen`, `gocognit`, `gocritic` (with
      `emptyStringTest` enabled), `gocyclo` (`min-complexity: 15`),
      `gocognit` (`min-complexity: 15`), `makezero`, `modernize`, `nlreturn`,
      `nolintlint` (`require-explanation`/`require-specific`), `perfsprint`,
      `sqlclosecheck`, `testifylint` (`enable-all`), `thelper`, `unparam`,
      `usestdlibvars`, `whitespace`, `wrapcheck` to `.golangci.yml`'s
      `linters.enable`, and verify `golangci-lint run` (with the project's
      usual `--build-tags=integration,containertest ./...`) loads the config
      without error.
- [ ] 1.2 Add `gofumpt` and `gci` to `formatters.enable`, remove `gofmt` (a
      strict subset of `gofumpt`), and verify `golangci-lint fmt` runs clean
      with no config error.
- [ ] 1.3 Add a `dupl` exclusion for `_test.go` under `exclusions.rules`, and
      verify `golangci-lint run` no longer flags table-driven test
      repetition that already passed review.

## 2. Baseline

- [ ] 2.1 Run `golangci-lint run --build-tags=integration,containertest ./...`
      with the updated config and record the finding count per linter -
      this sizes tasks 3.x and confirms whether they split by linter or by
      package.

## 3. Fix findings

- [ ] 3.1 Fix or `//nolint:<linter> // <reason>` every finding from task 2.1,
      grouped into one or more pull requests by whatever the baseline showed
      groups more naturally (by linter or by package) - keep to
      `pull-request` conventions' ~300-line-per-PR guideline, splitting
      further where the baseline is larger than that. Verify with
      `golangci-lint run --build-tags=integration,containertest ./...`
      after each batch, and `go test ./...` to confirm no behaviour changed.

## 4. Verify

- [ ] 4.1 Confirm `golangci-lint run --build-tags=integration,containertest ./...`
      is green in CI on `master` with every linter from the target set
      enabled and no outstanding findings.
