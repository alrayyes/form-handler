<!--
One reviewable change per pull request. If you cannot say what this does in a
sentence without "and", it is two — split it and say which one comes first.
-->

## What this changes, and why

<!-- Prose, not a bullet list of the diff. The diff already shows what. -->

## How it was tested

<!--
Which layer, and why that one. "go test ./..." on its own is not an answer for
anything that touches delivery — the integration test is the one that proves a
message actually arrives.
-->

## Anything a reviewer should look at twice

<!--
Trade-offs you made, things you were unsure about, scope you left out on purpose.
-->

---

- [ ] Tests cover the new behaviour, and were written before it
- [ ] Commit messages follow Conventional Commits — they pick the version number
- [ ] `README.md` updated in this pull request if it is now false
- [ ] Deliberately deferred work has an issue, not just a `TODO`

Closes #
