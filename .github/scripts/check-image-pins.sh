#!/usr/bin/env bash
# Finds every image this repository pins by digest and asks the registry whether
# the tag still points at that digest.
#
# The pins are spread across a Go constant, a ko config and a workflow's service
# containers, which is exactly why nothing watched them: Dependabot's docker
# ecosystem reads Dockerfiles and Kubernetes manifests, and there is no
# Dockerfile here at all — ko assembles the image itself.
#
# Writes a Markdown report to stdout. Exits 0 when everything matches, 1 when
# something has moved, 2 when a digest could not be resolved.
set -uo pipefail

# The backticks below are Markdown, not command substitution, and a printf
# format string belongs in single quotes anyway. Without this, shellcheck
# reports SC2016 on every row of the table.
# shellcheck disable=SC2016

# name:tag@sha256:… anywhere in a tracked file. One pattern covers the ko base
# image, the workflow services and the Go constant alike.
readonly PATTERN='[A-Za-z0-9][A-Za-z0-9._/-]*:[A-Za-z0-9][A-Za-z0-9._-]*@sha256:[a-f0-9]{64}'
readonly EXCLUDES=(':!*.md' ':!.github/scripts/*')

short() { printf '%s…' "${1:0:19}"; }

drifted=0
failed=0
rows=""
details=""

# Unique pinned references, so a digest used in four places is resolved once.
refs=$(git grep -hoE "$PATTERN" -- "${EXCLUDES[@]}" | sort -u)

if [ -z "$refs" ]; then
  echo "No digest-pinned images found. That is either good news or a broken pattern."
  exit 2
fi

for ref in $refs; do
  tagged=${ref%@*}
  pinned=${ref#*@}

  # buildx is on the runner already, needs no login for a public image, and
  # reports the digest of the whole index rather than one platform's manifest —
  # which is what a multi-arch tag is pinned to.
  if ! current=$(docker buildx imagetools inspect "$tagged" --format '{{.Manifest.Digest}}' 2>/dev/null); then
    rows+=$(printf '| `%s` | `%s` | — | could not reach the registry |\n' "$tagged" "$(short "$pinned")")
    rows+=$'\n'
    failed=1
    continue
  fi

  if [ "$current" = "$pinned" ]; then
    rows+=$(printf '| `%s` | `%s` | unchanged | up to date |\n' "$tagged" "$(short "$pinned")")
    rows+=$'\n'
    continue
  fi

  drifted=1
  rows+=$(printf '| `%s` | `%s` | `%s` | **moved** |\n' "$tagged" "$(short "$pinned")" "$(short "$current")")
  rows+=$'\n'

  # Everywhere it needs changing, so updating it is a copy and paste rather
  # than a hunt.
  files=$(git grep -lE "$(printf '%s' "$tagged" | sed 's/[.[\*^$]/\\&/g')@sha256:" -- "${EXCLUDES[@]}")
  details+=$(printf '\n### `%s`\n\nReplace `%s`\nwith `%s`\n\nin:\n\n```\n%s\n```\n' \
    "$tagged" "$pinned" "$current" "$files")
  details+=$'\n'
done

printf '| Image | Pinned | Current | |\n| --- | --- | --- | --- |\n%s' "$rows"
[ -n "$details" ] && printf '%s' "$details"

if [ "$failed" = 1 ]; then
  exit 2
fi
exit "$drifted"
