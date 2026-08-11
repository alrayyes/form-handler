# Architecture

This branch is a comparison, not a decision. It rebuilds the service as clean
architecture so the trade can be judged from the diff rather than argued about
in the abstract. `master` is ports and adapters, which is what
`rules/architecture.md` asks for; this is the same service with the layers made
explicit.

Read this, then `git diff master...refactor/clean-architecture --stat`.

## The layers

Dependencies point inward. Nothing in Go enforces that, so
`internal/architecture_test.go` does.

```text
                    ┌─────────────────────────────────┐
   inward  ───────► │            domain               │  entities and rules
                    │  Submission, Message, Validate  │  imports: stdlib only
                    └─────────────────────────────────┘
                    ┌─────────────────────────────────┐
                    │            usecase              │  what the service does
                    │  Submit + the ports it needs    │  imports: domain
                    │  Mailer, RateLimiter            │
                    └─────────────────────────────────┘
                    ┌─────────────────────────────────┐
                    │            adapters             │  how the world reaches it
                    │  http, ratelimit, smtp,         │  imports: usecase, domain
                    │  mailgun, config                │
                    └─────────────────────────────────┘
                    ┌─────────────────────────────────┐
                    │            server               │  composition root
                    │  the only place that knows      │  imports: everything
                    │  all of the above exist         │
                    └─────────────────────────────────┘
```

## What actually moved

| Was                                                           | Is                                                      | Why                                                                                      |
| ------------------------------------------------------------- | ------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `contact.Validate`, `Submission`, `Message`                   | `domain`                                                | Facts about a contact form. They needed nothing else and now import nothing else.        |
| The rules inside `contact.Handler.ServeHTTP`                  | `usecase.Submit.Execute`                                | They were reachable only through HTTP. Now they are a function.                          |
| `contact.limiter`, unexported, inside the handler             | `adapter/ratelimit.Memory` behind `usecase.RateLimiter` | It was an implementation detail of a web handler. It is a policy with an implementation. |
| `contact.Handler` (281 lines, rules and translation together) | `adapter/http.Handler` (translation only)               | The status-code table is the whole of its opinion now.                                   |

`smtp`, `mailgun` and `config` did not move. They were already adapters — which
is the honest headline of this comparison.

## What it buys

**The rules are testable without HTTP.** `internal/usecase/submit_test.go` calls
functions and asserts on errors. No request is built, no status code is read, no
header is set. One test in there is new and could not easily have been written
before:

```go
func TestARefusedOriginDoesNotConsumeTheRateLimit(t *testing.T)
```

That ordering — origin checked before the limiter, so a site that may not post
here cannot spend somebody else's allowance — was already true on `master`. It
was just tangled up in `ServeHTTP` with the CORS headers and the JSON decoder,
where nobody would think to look for it.

**The rate limiter became swappable.** Sharing counts between instances is now a
new type implementing one method, rather than surgery on a web handler.

**The direction is enforced.** `TestDependenciesPointInward` fails if the domain
ever imports the adapter layer. Verified by making it fail.

## What it costs

**More packages for the same behaviour.** Four layers where there were two, and
the service is about 2,000 lines. `rules/go.md` says not to build a
domain/application/infrastructure tree just to have one, and a fair reading is
that this branch does exactly that.

**A hop to follow.** Reading a submission end to end now means `adapter/http` →
`usecase` → `domain` → back out to an adapter. On `master` it is one file.

**Two names for one idea.** `domain.Form` and `config.Form` are different types
that mean the same thing, mapped at the composition root. That is the price of
the config layer not reaching inward, and it is real.

## The honest summary

The refactor pays for itself in exactly one place: the rules that used to be
trapped inside `ServeHTTP` are now callable and separately tested. Everything
else it does, ports and adapters was already doing — the ports existed, the
adapters existed, the composition root existed.

If the service grows another entry point — a queue consumer, a CLI that replays
a submission, a second transport — this shape is already the right one. If it
stays a contact form with one endpoint, `master` is less to hold in your head
for the same behaviour.

Same tests, both branches. The `internal/server` integration suite is unchanged
here and passes, which is the evidence that the behaviour did not move while the
code did.
