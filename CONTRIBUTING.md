# Contributing

Thanks for looking. This is a small service with a small job, so the bar for a
change is mostly "does it still do that job, and can someone tell from the
tests?"

## Getting set up

You need Go, [bun](https://bun.sh) for the linters that are not Go, and
[golangci-lint](https://golangci-lint.run) v2.12.2 — that version specifically,
because CI runs it pinned and a newer one locally will disagree with the
pipeline in ways the failure does not explain.

```sh
bun install
```

That installs the git hooks too. [lefthook](https://lefthook.dev) is pinned in
`package.json` like everything else here, and the `prepare` script runs
`lefthook install` for you. An uninstalled hook silently does nothing, which is
worse than not having one — you find out at the pipeline instead of at the
commit.

## The shape of a change

Write the test first, and write the outer one first. The integration test in
`internal/server` posts real requests at the real composition root with a real
mail server behind it, and it is the one to trust: header encoding, CRLF
handling and whether a reply actually reaches the sender only show up when
something real parses the message. The unit tests use a fake mailer and prove
none of that.

```sh
go test ./...                    # unit: validation, honeypot, limits, routing
go test -tags=integration ./...  # the outer test, needs Docker
```

Every new behaviour and every bug fix gets a test. Test behaviour at the
boundaries rather than private internals, and don't chase a coverage number.

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

## What CI will check

The same commands the hooks run, so a green pre-commit means a green lint stage:
`gofmt` and `golangci-lint` over the Go, Prettier over the Markdown and the YAML
and then markdownlint over the Markdown, Biome over the JSON, `go test` with the
race detector, the integration test against a real Mailpit, a CodeQL pass, and a
`ko build` to prove the image still assembles.

Prettier decides Markdown layout and markdownlint judges what Prettier produced.
If the two ever disagree, Prettier wins and the markdownlint rule comes off —
two tools with an opinion about the same character is a hook that fails twice
and fixes nothing.

## Releases

Nobody picks a version, and you do not need to touch `CHANGELOG.md`. Once your
work is on `master`,
[release-please](https://github.com/googleapis/release-please) folds it into a
release pull request carrying the next version and the changelog entry. Merging
that pull request tags the release, and
[goreleaser](https://goreleaser.com) attaches the binaries while `ko` pushes the
image.
