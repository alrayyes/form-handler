# form-handler

[![pipeline](https://github.com/alrayyes/form-handler/actions/workflows/ci.yml/badge.svg)](https://github.com/alrayyes/form-handler/actions)
[![coverage](https://img.shields.io/badge/coverage-go%20test-00ADD8)](https://github.com/alrayyes/form-handler/actions)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![licence](https://img.shields.io/badge/licence-GPL--3.0--or--later-blue)](LICENSE)

Takes the contact form on [andthensome.nl](https://www.andthensome.nl) and turns
it into an email. That is the whole job.

It exists because Cloudflare Workers cannot open an SMTP connection. Workers can
send mail through an HTTP email API, but the mail for this domain is self-hosted,
so a small service that can speak SMTP is the simpler answer than routing the
site's contact form through a third party.

## Requirements

To run it:

- **Go 1.25 or newer**, or Docker if you would rather run the image.
- **An SMTP server** it may send through. In production that is the mail bridge
  on the same host; locally it is a throwaway container, below.
- **A sending address the mail server will accept**, and somewhere to deliver to
  — `MAIL_FROM` and `MAIL_TO`. There are no defaults for these and the service
  refuses to start without them.

To work on it, additionally:

- **Docker**, for the integration test — it starts a real mail server in a
  container. Not needed to build the image: `ko` does that without a daemon.
- **[lefthook](https://lefthook.dev)** for the git hooks, and
  **[bun](https://bun.sh)** to install the two linters that are not Go —
  commitlint and markdownlint. There is a `package.json`, but nothing here is
  JavaScript; it exists only so those two resolve.
- **[golangci-lint](https://golangci-lint.run) v2.12.2**, which the pre-commit
  hook runs from your `PATH` while CI runs it from a pinned image. Install that
  version rather than whichever is current: when the two disagree, the hook
  passes and the pipeline fails, and the reason is not obvious from the failure.

Nothing else needs installing: the Go dependencies come from `go.mod`, and every
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

It listens on `:8080` and needs, at minimum, somewhere to send mail:

| Variable | Default | What it does |
| --- | --- | --- |
| `MAIL_FROM` | *required* | Envelope and header sender. Must be an address the mail server will accept. |
| `MAIL_TO` | *required* | Where submissions land. |
| `SMTP_ADDR` | `localhost:1025` | `host:port` of the mail server. |
| `SMTP_USERNAME` | empty | Omit for a local bridge that does not authenticate. |
| `SMTP_PASSWORD` | empty | |
| `ALLOWED_ORIGINS` | `https://www.andthensome.nl` | Comma-separated. A request from anywhere else is refused. |
| `RATE_LIMIT_PER_HOUR` | `5` | Submissions per client address. `0` disables it. |
| `ADDR` | `:8080` | Listen address. |

For local work, run a throwaway mail server and point at it:

```sh
docker run --rm -p 1025:1025 -p 8025:8025 \
  axllent/mailpit:v1.21.8@sha256:81370195cd4a0eab9604d17c2617a7525b0486f9365555253b6c5376c6350f1a
MAIL_FROM=site@example.com MAIL_TO=info@example.com go run .
```

Mailpit's web interface is on <http://localhost:8025>, and anything the service
sends will appear there instead of the internet.

Two flags, both for asking the binary about itself rather than for running it:

```sh
form-handler -version      # the tag it was built from, or "dev"
form-handler -healthcheck  # probe the local /healthz and exit non-zero if it fails
```

`-healthcheck` exists because the image is distroless. There is no shell and no
curl in there for a container healthcheck to run, so the binary has to be able to
probe itself.

## The endpoint

`POST /contact`, JSON in, JSON out.

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

| Status | Meaning |
| --- | --- |
| `202` | Accepted. Also what a honeypot submission gets — see below. |
| `400` | The body could not be read, or contained fields we do not know. |
| `403` | The `Origin` is not in `ALLOWED_ORIGINS`. |
| `422` | A field is wrong. The body names which one and why. |
| `429` | Too many submissions from this address this hour. |
| `502` | The mail server would not take it. |

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
*from* this service, *about* them, and hitting reply still reaches them.

**Headers are stripped of line breaks before use.** A name containing `\r\n` is
someone trying to add their own headers — `Bcc:`, most likely — and a contact
form is a fine place to try it from.

**Origin is checked, not just answered.** Refusing the CORS header stops a
browser reading the response, but the request still arrived and would still have
sent mail. The check that matters is server-side.

**The rate limiter is in memory and forgets on restart.** One instance, one job.
A shared store would be more moving parts than a contact form justifies, and the
worst case is that a restart forgives someone.

## Testing

Two layers, and the outer one is the one to trust:

```sh
go test ./...                    # unit: validation, honeypot, limits, handler
go test -tags=integration ./...  # runs a real mail server in a container
```

The integration test starts Mailpit with testcontainers, posts a real request at
a real handler, and asserts a real message arrived with the right `Reply-To`,
subject and body. It needs a working Docker. The unit tests use a fake mailer and
prove none of that — header encoding, CRLF handling and whether a reply actually
reaches the sender only show up when something real parses the message.

If you already have a Mailpit running, point the test at it and it will use that
instead of starting one of its own:

```sh
MAILPIT_SMTP_ADDR=127.0.0.1:1025 MAILPIT_API_URL=http://127.0.0.1:8025 \
  go test -tags=integration ./...
```

That is how CI runs it, and the reason is worth knowing before you change it.
Testcontainers needs a Docker daemon *inside* the job, and the only two ways to
give a job one are a privileged `dind` sidecar or a mount of the host's socket —
the first lets any job on the runner escape to the host, the second hands it the
host's Docker outright. Neither is a trade worth making for a contact form, so
the pipeline runs Mailpit as an ordinary service and sets those two variables.
The assertions are the same either way. One difference to keep in mind: a shared
server keeps its messages between tests, so the test empties the mailbox first.

It is behind a build tag so `go test ./...` stays fast; CI runs both.

## Contributing

Branch, push, open a pull request, and let someone else merge it. Link the issue
it answers with `Closes #12`, so merging closes the loop.

Commit messages follow [Conventional Commits](https://www.conventionalcommits.org)
— the `commit-msg` hook will tell you if yours does not, and it is not being
fussy for its own sake: those messages are what pick the next version number and
what ends up in the changelog. `feat:` and `fix:` are the two that release.

The hooks run the same commands CI does, so a green pre-commit means a green
lint stage: `gofmt` and `golangci-lint` on staged Go files, markdownlint on
Markdown, and `go test ./...` before a push. CI runs
commitlint over the whole branch as well, since a hook is something you can skip
and the version number depends on those messages being right.

## Releases

**Paused.** The jobs below are commented out in `.github/workflows/ci.yml` and there are no
releases yet — see [issue #3][releases-issue]. They work: the tag job ran for
real and got as far as computing `v0.1.0` before it stopped at the one thing it
does not have, a `RELEASE_TOKEN`. Rather than leave a job failing on every push to
master — which only teaches people to ignore a red pipeline — they wait there
until the token exists. Until then the deployed image is identified by its digest
and the short SHA, not a version.

The rest of this section describes what happens once they are switched back on.

[releases-issue]: https://github.com/alrayyes/form-handler/issues/3

Nobody picks a version. When something lands on `master`, CI asks
[svu](https://github.com/caarlos0/svu) what the commits since the last tag add up
to — `feat:` takes the minor, `fix:` the patch, a `BREAKING CHANGE:` footer the
major — and tags it if that differs from the current tag. A batch of only `docs:`
and `chore:` releases nothing, which is the intent.

The tag then triggers [goreleaser](https://goreleaser.com), which builds the
Linux binaries, writes the GitHub release with notes grouped by change type, and
pushes the same notes into `CHANGELOG.md` on `master` in a `[skip ci]` commit.
That commit is the one exception to nothing-but-humans-writes-to-`master`.

This needs a `RELEASE_TOKEN` CI variable — a project access token with `api` and
`contents:write` scope, **masked** and **protected**. The pipeline's own job
token cannot push a tag, and a tag it pushed would not start the pipeline that
publishes the release. The `v*` tag pattern is already protected, so that half is
done.

## Deploying

CI builds and pushes an image to this project's registry on every commit to
`master` and on every release tag, and prints the digest. A release build is
tagged with the version; a `master` build with the short SHA. Both also move
`latest`. A branch build runs too, but stops after building — enough to prove
the image still builds before you merge, without publishing anything.

There is no Dockerfile. The image is assembled by **[ko](https://ko.build)**,
which compiles the binary and writes the layers itself, over the registry API.
That is not a preference; it is what the runner allows. A Docker daemon in the
job needs `privileged = true`, and buildah — which needs no daemon — still needs
`unshare(CLONE_NEWUSER)`, which an unprivileged container cannot do either. ko
needs neither. What the Dockerfile used to say now lives in `.ko.yaml`: the same
distroless base, pinned to the same digest it always was.

To build it yourself:

```sh
VERSION=dev KO_DOCKER_REPO=ko.local ko build --bare --local ./
```

**The entrypoint is `/ko-app/form-handler`, not `/form-handler`.** ko puts the
binary under `/ko-app`, so a compose healthcheck that shells the old path will
fail with "no such file" and the container will sit there unhealthy.

Pin the **digest** in the compose file over in `vps-docker`, not the tag. A tag
can be moved; a digest cannot, which is the difference between knowing what is
running and assuming it. `form-handler -version` inside the container tells you
which release that digest is, so pinning a digest no longer means losing the
version.

## Licence

[GPL-3.0-or-later](LICENSE). Every source file carries the SPDX identifier.
