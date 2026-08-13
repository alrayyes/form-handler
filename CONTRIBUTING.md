# Contributing

Thanks for looking. This is a small service with a small job, so the bar for a
change is mostly "does it still do that job, and can someone tell from the
tests?"

The [README](README.md) is written for whoever runs this. This file is for
whoever changes it.

## Getting set up

- **Go 1.25 or newer.**
- **Docker**, for the integration test — it starts a real mail server in a
  container. Not needed to build the image: `ko` does that without a daemon.
- **[bun](https://bun.sh)** for the tooling that is not Go — commitlint,
  Prettier, markdownlint, Biome, the [Redocly](https://redocly.com/docs/cli)
  that lints the API description, and the [lefthook](https://lefthook.dev) that
  runs the git hooks. There is a `package.json`, but nothing here is
  JavaScript; it exists only so those tools resolve and stay pinned.
- **[golangci-lint](https://golangci-lint.run) v2.12.2**, which the pre-commit
  hook runs from your `PATH` while CI runs it pinned. Install that version
  rather than whichever is current: when the two disagree, the hook passes and
  the pipeline fails, and the reason is not obvious from the failure.

One command installs the linters and the git hooks:

```sh
bun install
```

An uninstalled hook silently does nothing, which is worse than not having one,
so the `prepare` script runs `lefthook install` for you. You find out at the
pipeline otherwise, not at the commit.

Nothing else needs installing. The Go dependencies come from `go.mod`, and
every other tool runs from a pinned container image or through `bunx`.

## How it fits together

Ports and adapters, sized to a service this small rather than as a layer cake.

`internal/contact` is the middle. It holds the domain — `Submission`,
`Message`, `Validate` — and it declares the two things it needs from the
outside world as interfaces of its own: `Mailer` and `RateLimiter`. It imports
no adapter.

`internal/mail/smtp`, `internal/mail/mailgun` and `internal/ratelimit`
implement those interfaces and depend inward on `contact`, never the reverse.
`internal/config` is the only package that reads the environment.

`internal/server.New` is the composition root. It is the one place that knows
both the ports and the adapters exist, and `mailerFor` in that file is the
whole of its knowledge about providers: adding a third means a case in that
switch and a package beside the other two, with nothing in `internal/contact`
changing.

The HTTP handler lives in `internal/contact` beside the domain rather than in a
transport package of its own. Splitting it out would divide the package by
pattern instead of by capability, and would cost more than it returns while
there is one transport.

## The contract

`api/openapi.yaml` describes the endpoint, and it is handwritten rather than
generated. Two tests hold the service to it, and both fail in either direction:

- `internal/server/openapi_test.go` provokes every documented response out of
  the real handler. A response the document describes that nothing produces
  fails just as loudly as a body that moved.
- `internal/server/openapi_request_test.go` reads the limits the request schema
  states and posts a field one character inside each bound and one character
  past it. Change a limit in the code without changing the document and it goes
  red.

`redocly lint` checks the document is valid OpenAPI, from the same
`package.json` script the hook and CI both run. That is the whole point of the
arrangement: the status table in the README quietly went years missing three
codes, because prose has no way of noticing.

## The shape of a change

Write the test first, and write the outer one first.

```sh
go test ./...                    # unit, plus the adapters against local stubs
go test -tags=integration ./...  # the outer test, needs Docker
```

The integration test starts Mailpit with testcontainers, posts real requests at
the real composition root, and asserts real messages arrived in the right
inboxes with the right `Reply-To`, subject and body. It is the one to trust.

Underneath it, every mail adapter is held to one shared contract in
[`internal/contact/mailertest`](internal/contact/mailertest). The `Mailer`
interface says a send returns an error; the contract says what that error is —
a typed `DeliveryError` naming the provider and the step it got to, with the
cause still reachable underneath. It runs against the SMTP adapter, the Mailgun
adapter, and the fake the handler tests use. Holding the fake to it is the
point: a fake that fails differently from the real thing makes tests pass for a
reason production does not reproduce.

The SMTP adapter is driven over a real socket against a stub that speaks the
protocol, so header encoding, the stripped line break and the lone dot in a
body are covered without Docker. What still needs Mailpit is the last claim,
and it is the one worth keeping: that a real mail server accepts what we
compose.

Every new behaviour and every bug fix gets a test. Test behaviour at the
boundaries rather than private internals, and don't chase a coverage number.

If you already have a Mailpit running, point the test at it and it uses that
instead of starting one of its own:

```sh
MAILPIT_SMTP_ADDR=127.0.0.1:1025 MAILPIT_API_URL=http://127.0.0.1:8025 \
  go test -tags=integration ./...
```

That is how CI runs it, and the reason is worth knowing before you change it.
Testcontainers needs a Docker daemon _inside_ the job, and the only two ways to
give a job one are a privileged sidecar or a mount of the host's socket.
Neither is a trade worth making for a contact form, so the workflow runs
Mailpit as an ordinary service container and sets those two variables. The
assertions are the same either way. One difference to keep in mind: a shared
server keeps its messages between tests, so the test empties the mailbox first.

The integration test is behind a build tag so `go test ./...` stays fast. CI
runs both.

## Decisions worth knowing

**A caught bot gets `202`, exactly like a real submission.** Same status, same
body. Telling it that the honeypot fired only teaches whoever wrote it which
field to leave alone next time. There is a test asserting the two responses are
byte-identical, because this is easy to break by adding a helpful error message.

**The visitor's address goes in `Reply-To`, never in `From`.** Sending as them
would fail SPF for their domain and land the whole thing in spam. The mail is
_from_ this service, _about_ them, and hitting reply still reaches them.

**Headers are stripped of line breaks before use.** A name containing `\r\n` is
someone trying to add their own headers — `Bcc:`, most likely — and a contact
form is a fine place to try it from.

**Origin is checked, not just answered.** Refusing the CORS header stops a
browser reading the response, but the request still arrived and would still have
sent mail. The check that matters is server-side.

**A form with no origins refuses everything.** Fail closed: an empty list is a
misconfiguration, and the safe reading of "nobody is allowed" is not "everybody
is". This is why `ALLOWED_ORIGINS` has no default.

**Each form gets its own mailer, not a shared one with a switch.** Choosing the
recipient per request would work right up until two forms were configured
alike, and then it would deliver somebody's job application to the marketing
inbox.

**The rate limiter is in memory and forgets on restart.** One instance, one job,
and per form. A shared store would be more moving parts than a contact form
justifies, and the worst case is that a restart forgives someone.

**`Validate` fails in exactly two ways, and the type says so.** `Rejection` is
an interface with an unexported method, so nothing outside `internal/contact`
can be one. The handler switches on both with no fallback branch — it used to
carry one for a third kind that could not happen, answering a status the spec
had no way to describe.

## Commits

[Conventional Commits](https://www.conventionalcommits.org), checked by
commitlint in the `commit-msg` hook and again over the whole branch in CI. This
is not fussiness for its own sake: those messages pick the next version number
and become the changelog. `feat:` takes the minor, `fix:` the patch, a
`BREAKING CHANGE:` footer the major, and a branch of only `docs:` and `chore:`
releases nothing.

Aim for one logical change per commit, each building and passing its tests on
its own. A refactor and the feature it made room for are two commits.

The body explains _why_. The diff already shows what.

## Pull requests

Branch, push, open a pull request, and let someone else merge it. Keep it small
enough that a human can actually read it — one reviewable change per pull
request. If you cannot say what it does in a sentence without "and", it is two
pull requests, and stacking them is fine: say in the description which one comes
first.

Link the issue it answers with `Closes #12` where there is one. If there is not
and the change is anything someone might later ask "why did this change?" about,
open the issue first — the issue says why and stays findable, while the pull
request only says how.

Tidy the history once CI is green and _before_ asking for a merge, never during
review: rewriting under a review in progress throws away the comments and the
reviewer's place in the diff. Squash fixups into what they were fixing, order
groundwork before the change it made room for, and push with
`--force-with-lease`.

## What CI checks

The same commands the hooks run, so a green pre-commit means a green lint
stage: `gofmt` and `golangci-lint` over the Go, Prettier over the Markdown and
the YAML and then markdownlint over the Markdown, Biome over the JSON,
`redocly lint` over the API description, `go test` with the race detector, the
integration test against a real Mailpit, a CodeQL pass, and a `ko build` to
prove the image still assembles.

Prose gets two more jobs of its own. `mechanics` runs ltex-cli-plus for grammar
and spelling and fails the build, because those have a right answer. `style`
runs Vale for the house voice and mostly warns, because style advice that
blocks a merge teaches people to skip the hooks. Product names go in
`styles/config/vocabularies/House/accept.txt` and in `.ltex.json`, not in an
ignore comment.

Prettier decides Markdown layout and markdownlint judges what Prettier
produced. If the two ever disagree, Prettier wins and the markdownlint rule
comes off — two tools with an opinion about the same character is a hook that
fails twice and fixes nothing.

## Releases

Nobody picks a version, and you do not need to touch `CHANGELOG.md`.
[release-please](https://github.com/googleapis/release-please) reads the
Conventional Commits that land on `master` — `feat:` takes the minor, `fix:`
the patch, a `BREAKING CHANGE:` footer the major — and keeps a pull request
open carrying the next version and the changelog entry it would write. A batch
of only `docs:` and `chore:` releases nothing, which is the intent, and until
you merge that pull request nothing is released at all.

Merging it tags the release and publishes the notes.
[goreleaser](https://goreleaser.com) then builds the Linux binaries and
attaches them, and `ko` pushes the image. Neither does the other's job:
release-please owns the version, the notes and `CHANGELOG.md`, and goreleaser
is told to leave all three alone.

That happens in one workflow rather than two, because a tag pushed with
`GITHUB_TOKEN` starts no further workflow run — GitHub refuses, to stop
recursive runs. So the build is a second job in the same run, gated on
release-please saying it created a release.

**Merge with an empty commit body, or every entry lands twice.**

```sh
gh pr merge <n> --merge --body ""
```

GitHub puts the pull request title into the body of a merge commit, and this
repository's titles are Conventional Commits. release-please then reads the
merge commit and the branch commit as two separate changes and writes both, so
the changelog and the release notes carry each line twice.

The repository setting cannot fix it. GitHub allows only three title-and-body
combinations, and all three put the pull request title somewhere in the merge
commit, so `MERGE_MESSAGE` with a blank body is not offered. Merging from the
web interface means clearing the body by hand before confirming the merge.
Merging with `--body ""` is the reliable way.

Squash merging would also solve it, and is what release-please expects. It is
not what this repository does: a pull request here holds several commits that
each build and pass on their own, and squashing throws that away.

The current version lives in `.release-please-manifest.json`. That is the file
to correct by hand if a release goes wrong, not the tag. To release a version
nobody's commits add up to, put a `Release-As: 1.2.3` footer on a commit.

If duplicates do reach a release pull request, edit `CHANGELOG.md` on that
branch before merging it. release-please only appends, so a correction there
survives.

Two things this needs from repository settings, and both are easy to lose.
**GitHub Actions has to be allowed to create pull requests** (Settings →
Actions → General), or release-please has nowhere to put the release. And the
release pull request passes the same required checks as any other before anyone
can merge it.

Dependency updates come from Dependabot — Go modules, the actions, and the
JavaScript tooling, grouped weekly — and merge themselves once every required
check is green. Two pins it cannot see are updated by hand: the distroless base
in `.ko.yaml` and the Mailpit image the integration test uses.

## Security

Found a security problem? [Report it
privately](https://github.com/alrayyes/form-handler/security/advisories/new)
rather than as an issue. See [SECURITY.md](SECURITY.md).
