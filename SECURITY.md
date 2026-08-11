# Security

## Reporting a vulnerability

Please report it privately, through
[GitHub's private vulnerability reporting](https://github.com/alrayyes/form-handler/security/advisories/new)
— the Security tab of this repository, "Report a vulnerability". That opens a
draft advisory only you and the maintainers can see.

Not a public issue, please. This service holds an SMTP credential and sends mail
as a domain somebody owns, so a working exploit posted in the open is a working
exploit for every deployment of it at once.

Tell me what you did, what happened, and what you expected. A request that
reproduces it is worth more than a description of one.

I will acknowledge within a week and tell you what I think, whether or not I
agree it is a vulnerability. If it is one, the fix ships as a `fix:` commit and
the advisory is published with the release that carries it.

## Supported versions

The latest release, and only the latest. This is a single-binary service that
releases on every fix, so "upgrade" is the patch.

## What this service is exposed to

Worth knowing before you go looking, because these are deliberate:

- **The endpoint is public and unauthenticated.** It is a contact form. The
  defences are the origin allowlist, the honeypot field and the per-address rate
  limit, and none of them are authentication.
- **Origin is checked server-side, not just answered with a CORS header.**
  Refusing the header stops a browser reading the response, but the request
  still arrived and would still have sent mail. A form with no origins
  configured refuses everything rather than accepting everything.
- **The rate limiter is in memory and forgets on restart.** One instance, one
  job. The worst case is that a restart forgives someone.
- **The rate limiter counts by client address, and that address is only as
  trustworthy as `TRUSTED_PROXIES` makes it.** `X-Forwarded-For` is ignored
  unless the connection came from a proxy listed there, because the header is
  set by the sender. A deployment that lists a range wider than its actual
  proxies is back to letting callers pick their own bucket.
- **Header injection is the interesting attack.** The visitor controls the name
  and the address that reach `Subject` and `Reply-To`, so line breaks are
  stripped before either is written and there is a test asserting it. If you
  find a way past that, it is worth reporting.
- **The visitor's address goes in `Reply-To`, never in `From`.** Sending as them
  would fail SPF for their domain.

## What is not a vulnerability here

- That a caught honeypot submission gets `202` exactly like a real one. That is
  the point: telling a bot which field gave it away only teaches whoever wrote
  it what to leave alone next time.
- That `/healthz` answers without testing SMTP. A mail server being briefly
  unreachable is not a reason for an orchestrator to restart a process that is
  answering perfectly well.
- Rate limits being per instance rather than shared. A shared store would be
  more moving parts than a contact form justifies.
