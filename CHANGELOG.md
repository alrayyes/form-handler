# Changelog

Written by the release job, not by hand. Every entry below comes from the
Conventional Commits that landed on `master`: `feat:` takes the minor, `fix:`
the patch, a `BREAKING CHANGE:` footer the major, and a run of only `docs:` and
`chore:` releases nothing at all.

Editing this file yourself is fine for fixing a typo, but the next release will
insert its section under the marker below and leave whatever else it finds
alone.

<!-- releases below -->

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
