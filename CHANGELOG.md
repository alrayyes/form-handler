# Changelog

## [2.1.6](https://github.com/alrayyes/form-handler/compare/v2.1.5...v2.1.6) (2026-08-17)


### Bug Fixes

* **deps:** commit-message prefix for the shipped ecosystem(s) ([#51](https://github.com/alrayyes/form-handler/issues/51)) ([cfa58bf](https://github.com/alrayyes/form-handler/commit/cfa58bf7d07c6d0525da17c86c90ad1ecbe20cc4))

## [2.1.5](https://github.com/alrayyes/form-handler/compare/v2.1.4...v2.1.5) (2026-08-13)


### Bug Fixes

* **ci:** exclude the changelog from the vale hook ([633bcd8](https://github.com/alrayyes/form-handler/commit/633bcd821b6421a70ab649f9333d229aeb76cfda))
* **ci:** run the grammar check at push, not only in CI ([5d9cdb8](https://github.com/alrayyes/form-handler/commit/5d9cdb8b9d439507bba9dadd20696a494ce9b1b0))
* **ci:** run vale at push, from the same script as CI ([5499014](https://github.com/alrayyes/form-handler/commit/5499014efab2a77e6fd14cea799faa355a085672))
* **ci:** widen the glob that triggers the spec linter ([da96289](https://github.com/alrayyes/form-handler/commit/da9628900ecebf88a24639385538af3e41572d32))
* stop merge commits duplicating every changelog entry ([c457433](https://github.com/alrayyes/form-handler/commit/c457433f953d98c7df5ee9dd3ea86c04340f4f1c))

## [2.1.4](https://github.com/alrayyes/form-handler/compare/v2.1.3...v2.1.4) (2026-08-13)


### Bug Fixes

* **ci:** let image-pins file the issue it found ([6c2aeba](https://github.com/alrayyes/form-handler/commit/6c2aeba0d5dc1c55c613b73a2f4ef44af9446d73)), closes [#30](https://github.com/alrayyes/form-handler/issues/30)

## v2.1.3

### Fixes
*  fix(ci): let Vale's errors fail the style job

## v2.1.2

### Fixes
*  fix(ci): make the prose jobs actually run
*  fix(ci): run ltex against the JDK it ships with
### Everything else
*  ci: check prose for mechanics and style, not only structure

## v2.1.1

### Fixes
*  fix(build): drop the commit hashes from the changelog

## v2.1.0

### Features
* feat: log every request, not only the ones a form handled

## v2.0.0

### Fixes
* fix!: believe X-Forwarded-For only from proxies you have named
### Everything else
* ci: watch the image digests nothing was watching

## v1.3.2

### Fixes
* fix(build): report the same version from the binary and the image

## v1.3.1

### Fixes
* fix(security): strip line breaks by name so the sanitiser is recognised

## v1.3.0

### Features
* feat: log refused submissions, not just accepted ones
### Fixes
* fix(security): clean every user-supplied value that reaches a log
* fix(security): strip line breaks from logged values

## v1.2.0

### Features
* feat(cli): parse arguments with cobra
* feat: send through Mailgun as well as SMTP
### Fixes
* fix(cli): report a failure once rather than twice
### Everything else
* ci: lint the integration tests as well
* test(cli): pin the single-dash flags against a real probe

## v1.1.0

### Features
* feat: give every form its own mail server login
### Fixes
* fix(ci): give the changelog commit a key it can push with
### Everything else
* docs: record v1.0.0 in the changelog, and drop a retired badge

## v1.0.0

### Features
* feat!: serve any number of forms across any number of domains
* feat: accept the contact form and send it as email
* feat: probe itself with -healthcheck
* feat: report the version it was built from
### Fixes
* fix(ci): build the image with buildah, and build it on branches too
* fix(ci): build the image with ko, and drop the Dockerfile
* fix(ci): let the release job fetch the toolchain svu needs
* fix(ci): run the integration test without a Docker daemon in the job
* fix(ci): stop linting the linters' own READMEs
* fix(ci): target Go 1.25 rather than the toolchain that happened to be installed
* fix: make the commit-msg hook able to run at all
### Everything else
* build: format the Markdown with Prettier before linting it
* build: keep the release output out of the build context
* build: pin the mail server image by digest
* chore: let Renovate see the pins it cannot find on its own
* ci: build, test and analyse on GitHub Actions
* ci: check the whole branch's commit messages
* ci: let Dependabot keep the dependencies current
* ci: lint the Markdown this repo owns
* ci: name the analysis jobs after what they analyse
* ci: pause releases until there is a token to make them with
* ci: release itself from the commits that landed
* ci: release itself from the commits that landed
* ci: run the golangci-lint version people actually have
* docs: describe the multi-form service this has become
* docs: give the README the sections a newcomer needs
* docs: license the code under GPL-3.0-or-later
* docs: say how to contribute, and how to report a hole in it
* docs: show Validate's contract as runnable examples
