# form-handler

[![pipeline](https://github.com/alrayyes/form-handler/actions/workflows/ci.yml/badge.svg)](https://github.com/alrayyes/form-handler/actions)
[![coverage](https://img.shields.io/badge/coverage-go%20test-00ADD8)](https://github.com/alrayyes/form-handler/actions)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)

Takes the contact form on [andthensome.nl](https://www.andthensome.nl) and turns
it into an email. That is the whole job.

It exists because Cloudflare Workers cannot open an SMTP connection. Workers can
send mail through an HTTP email API, but the mail for this domain is self-hosted,
so a small service that can speak SMTP is the simpler answer than routing the
site's contact form through a third party.

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

It is behind a build tag so `go test ./...` stays fast; CI runs both.

## Deploying

CI builds and pushes an image to this project's registry on every commit to
`master`, tagged with the short SHA and `latest`, and prints the digest.

Pin the **digest** in the compose file over in `vps-docker`, not the tag. A tag
can be moved; a digest cannot, which is the difference between knowing what is
running and assuming it.
