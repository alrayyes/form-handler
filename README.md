# form-handler

[![ci](https://github.com/alrayyes/form-handler/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/alrayyes/form-handler/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/alrayyes/form-handler?sort=semver)](https://github.com/alrayyes/form-handler/releases/latest)
[![image](https://img.shields.io/badge/ghcr.io-form--handler-2496ED?logo=docker&logoColor=white)](https://github.com/alrayyes/form-handler/pkgs/container/form-handler)
[![go report](https://goreportcard.com/badge/github.com/alrayyes/form-handler)](https://goreportcard.com/report/github.com/alrayyes/form-handler)
[![scorecard](https://api.securityscorecards.dev/projects/github.com/alrayyes/form-handler/badge)](https://scorecard.dev/viewer/?uri=github.com/alrayyes/form-handler)
[![licence](https://img.shields.io/badge/licence-GPL--3.0--or--later-blue)](LICENSE)

Turns a contact form submission into an email. That is the whole job.

It was written for the contact form on
[andthensome.nl](https://www.andthensome.nl), because Cloudflare Workers cannot
open an SMTP connection. Workers can send mail through an HTTP email API, but the
mail for that domain is self-hosted, so a small service that speaks SMTP was the
simpler answer than routing the site's contact form through a third party.

It holds any number of forms across any number of sites. One endpoint per form,
each with its own allowed origins, its own recipient and its own subject line —
so a careers form on one domain and a contact form on another share a deployment
without sharing an inbox, and neither can post to the other.

## Requirements

To run it:

- **Go 1.25 or newer**, or Docker if you would rather run the image.
- **An SMTP server** it may send through. In production that is a mail bridge on
  the same host; locally it is a throwaway container, below.
- **A sending address the mail server will accept, and somewhere to deliver
  to.** There are no defaults and the service refuses to start without them.
- **The origins that may post to it.** Also no default — there is no origin
  that is right for everybody, and the consequence of guessing is somebody
  else's page using your mailbox.

To work on it, additionally:

- **Docker**, for the integration test — it starts a real mail server in a
  container. Not needed to build the image: `ko` does that without a daemon.
- **[lefthook](https://lefthook.dev)** for the git hooks, and
  **[bun](https://bun.sh)** to install the linters that are not Go — commitlint,
  Prettier and markdownlint. There is a `package.json`, but nothing here is
  JavaScript; it exists only so those resolve.
- **[golangci-lint](https://golangci-lint.run) v2.12.2**, which the pre-commit
  hook runs from your `PATH` while CI runs it pinned. Install that version
  rather than whichever is current: when the two disagree, the hook passes and
  the pipeline fails, and the reason is not obvious from the failure.

Nothing else needs installing. The Go dependencies come from `go.mod`, and every
other tool runs from a pinned container image or through `bunx`.

## Installation

```sh
git clone https://github.com/alrayyes/form-handler.git
cd form-handler
go build .
```

Or take the image CI builds, which is what actually runs in production:

```sh
docker pull ghcr.io/alrayyes/form-handler:latest
```

Every image is built by [ko](https://ko.build) from a digest-pinned distroless
base and carries a [build provenance
attestation](https://github.com/alrayyes/form-handler/attestations), so you can
check what built it and from which commit:

```sh
gh attestation verify oci://ghcr.io/alrayyes/form-handler:latest --repo alrayyes/form-handler
```

Working on it? Install the linters and the hooks once — an uninstalled hook
silently does nothing, which is worse than not having one:

```sh
bun install
lefthook install
```

## Running it

```sh
go run .
```

It listens on `:8080`. How you configure it depends on how many forms you have.

### One form

Set three variables and post to `/contact`. This is what a single-site
deployment wants, and what the service did before it could hold more than one:

```sh
MAIL_FROM=site@example.com \
MAIL_TO=info@example.com \
ALLOWED_ORIGINS=https://www.example.com \
  go run .
```

| Variable              | Default          | What it does                                                                    |
| --------------------- | ---------------- | ------------------------------------------------------------------------------- |
| `MAIL_FROM`           | _required_       | Envelope and header sender. Must be an address the mail server will accept.     |
| `MAIL_TO`             | _required_       | Where submissions land.                                                         |
| `ALLOWED_ORIGINS`     | _required_       | Comma-separated. A request from anywhere else is refused.                       |
| `SMTP_ADDR`           | `localhost:1025` | `host:port` of the mail server.                                                 |
| `SMTP_USERNAME`       | empty            | Omit for a local bridge that does not authenticate.                             |
| `SMTP_PASSWORD`       | empty            |                                                                                 |
| `RATE_LIMIT_PER_HOUR` | `5`              | Submissions per client address. `0` disables it.                                |
| `FORMS_FILE`          | unset            | Path to a forms file. Setting it replaces the four variables above — see below. |
| `ADDR`                | `:8080`          | Listen address.                                                                 |

That form is called `default`, which is what makes both `/contact` and
`/contact/default` reach it.

### Several forms

Point `FORMS_FILE` at a YAML file. There is a commented example in
[`forms.example.yaml`](forms.example.yaml):

```yaml
forms:
  - id: marketing
    origins:
      - https://www.example.com
      - https://example.com
    from: site@example.com
    to: info@example.com
    subject: "Contact form: {{ .Name }}"
    rate_limit_per_hour: 5

  - id: careers
    origins:
      - https://careers.example.org
    from: site@example.com
    to: jobs@example.com
    subject: "Application from {{ .Name }}"
```

That serves `POST /contact/marketing` and `POST /contact/careers`. The `id` is
the last path segment, so it has to survive being in a URL — lowercase letters,
digits, `-` and `_`.

`origins` is per form on purpose. A site being allowed to use its own form must
not let it use somebody else's, and the integration test asserts exactly that.
Each entry is `scheme://host[:port]` and nothing after it, which is all a
browser ever sends in an `Origin` header — `www.example.com` and `example.com`
are different origins, so list both if you serve both.

`subject` is a Go [text/template](https://pkg.go.dev/text/template) over
`.Name`, `.Email` and `.Form`. Leave it out for `Contact form: {{ .Name }}`.
Line breaks are stripped from whatever it renders, because the name feeding it
is whatever the visitor typed.

`rate_limit_per_hour` defaults to 5. An explicit `0` turns the limit off, which
is deliberately not the same as leaving the key out.

The forms file is the whole story once it exists: `MAIL_FROM`, `MAIL_TO`,
`ALLOWED_ORIGINS` and `RATE_LIMIT_PER_HOUR` are ignored, because a
half-file-half-environment configuration is the kind of thing that works locally
and surprises you in production. The SMTP settings stay in the environment
either way — a password does not belong in a file that wants to be committed
next to the site it serves.

Everything in that file is checked at startup. An unknown key, a duplicate id, an
origin with a path on it, an address that will not parse, a subject template with
an unclosed brace: all of them refuse to start, rather than waiting for the first
submission to find out.

### Locally

Run a throwaway mail server and point at it:

```sh
docker run --rm -p 1025:1025 -p 8025:8025 \
  axllent/mailpit:v1.21.8@sha256:81370195cd4a0eab9604d17c2617a7525b0486f9365555253b6c5376c6350f1a
```

Mailpit's web interface is on <http://localhost:8025>, and anything the service
sends appears there instead of on the internet.

Two flags, both for asking the binary about itself rather than for running it:

```sh
form-handler -version      # the tag it was built from, or "dev"
form-handler -healthcheck  # probe the local /healthz and exit non-zero if it fails
```

`-healthcheck` exists because the image is distroless. There is no shell and no
curl in there for a container healthcheck to run, so the binary has to be able to
probe itself.

## The endpoint

`POST /contact/{form}`, JSON in, JSON out. `/contact` on its own is an alias for
the form called `default`.

```json
{
  "name": "Ada Lovelace",
  "email": "ada@example.com",
  "message": "Please get in touch about an awkward system.",
  "website": ""
}
```

`website` is the honeypot: it is hidden from people, so anything that fills it in
is automated. Unknown fields are rejected outright.

| Status | Meaning                                                         |
| ------ | --------------------------------------------------------------- |
| `202`  | Accepted. Also what a honeypot submission gets — see below.     |
| `400`  | The body could not be read, or contained fields we do not know. |
| `403`  | The `Origin` is not one of this form's.                         |
| `404`  | No form by that name.                                           |
| `422`  | A field is wrong. The body names which one and why.             |
| `429`  | Too many submissions from this address this hour.               |
| `502`  | The mail server would not take it.                              |

`GET /healthz` answers `{"status":"ok"}` and deliberately does not test SMTP: a
mail server being briefly unreachable is not a reason for an orchestrator to
restart a process that is answering perfectly well.

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

## Testing

Two layers, and the outer one is the one to trust:

```sh
go test ./...                    # unit: validation, honeypot, limits, routing
go test -tags=integration ./...  # runs a real mail server in a container
```

The integration test starts Mailpit with testcontainers, posts real requests at
the real composition root, and asserts real messages arrived in the right
inboxes with the right `Reply-To`, subject and body. It needs a working Docker.
The unit tests use a fake mailer and prove none of that — header encoding, CRLF
handling and whether a reply actually reaches the sender only show up when
something real parses the message.

If you already have a Mailpit running, point the test at it and it will use that
instead of starting one of its own:

```sh
MAILPIT_SMTP_ADDR=127.0.0.1:1025 MAILPIT_API_URL=http://127.0.0.1:8025 \
  go test -tags=integration ./...
```

That is how CI runs it, and the reason is worth knowing before you change it.
Testcontainers needs a Docker daemon _inside_ the job, and the only two ways to
give a job one are a privileged sidecar or a mount of the host's socket. Neither
is a trade worth making for a contact form, so the workflow runs Mailpit as an
ordinary service container and sets those two variables. The assertions are the
same either way. One difference to keep in mind: a shared server keeps its
messages between tests, so the test empties the mailbox first.

The integration test is behind a build tag so `go test ./...` stays fast. CI runs
both.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Short version: write the outer test
first, branch, push, open a pull request, and let someone else merge it. Commit
messages follow [Conventional Commits](https://www.conventionalcommits.org) —
they are what pick the next version number.

Found a security problem? [Report it
privately](https://github.com/alrayyes/form-handler/security/advisories/new)
rather than as an issue. See [SECURITY.md](SECURITY.md).

## Releases

Nobody picks a version. When something lands on `master`, CI asks
[svu](https://github.com/caarlos0/svu) what the commits since the last tag add up
to — `feat:` takes the minor, `fix:` the patch, a `BREAKING CHANGE:` footer the
major — and tags it if that differs from the current tag. A batch of only
`docs:` and `chore:` releases nothing, which is the intent.

The same run then hands the tag to [goreleaser](https://goreleaser.com), which
builds the Linux binaries, writes the GitHub release with notes grouped by change
type, and pushes those same notes into `CHANGELOG.md` on `master` in a
`[skip ci]` commit. That commit is the one exception to nothing-but-humans
writing to `master`; it touches the version and the changelog and nothing else.

It is one workflow rather than two because a tag pushed with `GITHUB_TOKEN` does
not start a new workflow run, so the job that decides the version has to be the
same run that publishes it. No token beyond the built-in `GITHUB_TOKEN` is
needed.

The first tag is the exception: until a `v*` tag exists there is nothing to count
commits since, so the release job says so and stops rather than releasing a
version nobody chose. `v1.0.0` was pushed by hand. Everything after it is
automatic.

Dependency updates come from Dependabot — Go modules, the actions, and the
JavaScript tooling, grouped weekly — and merge themselves once every required
check is green. Two pins it cannot see are updated by hand: the distroless base
in `.ko.yaml` and the Mailpit image the integration test uses.

## Deploying

CI builds and pushes an image to
[ghcr.io/alrayyes/form-handler](https://github.com/alrayyes/form-handler/pkgs/container/form-handler)
on every release, for `linux/amd64` and `linux/arm64`, tagged with the version
and with `latest`. A branch build runs too, but stops after building — enough to
prove the image still builds before you merge, without publishing anything.

There is no Dockerfile. The image is assembled by [ko](https://ko.build), which
compiles the binary and writes the layers itself over the registry API. That
started as a workaround for a runner that would allow neither a Docker daemon nor
`unshare(CLONE_NEWUSER)`, and stayed because it is faster and needs no qemu to
build both architectures. What a Dockerfile would say now lives in `.ko.yaml`:
the same distroless base, pinned to the same digest it always was.

To build it yourself:

```sh
VERSION=dev KO_DOCKER_REPO=ko.local ko build --bare --local ./
```

**The entrypoint is `/ko-app/form-handler`, not `/form-handler`.** ko puts the
binary under `/ko-app`, so a compose healthcheck that shells the old path will
fail with "no such file" and the container will sit there unhealthy. Use
`form-handler -healthcheck` instead; that is what it is for.

Pin the **digest** in your compose file, not the tag. A tag can be moved; a
digest cannot, which is the difference between knowing what is running and
assuming it. `form-handler -version` inside the container tells you which release
a digest is, so pinning a digest no longer means losing the version.

## Licence

[GPL-3.0-or-later](LICENSE). Every source file carries the SPDX identifier.
