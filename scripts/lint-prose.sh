#!/usr/bin/env bash
# Holds the prose this repository owns to house style.
#
# One script rather than a block in the workflow and a copy in the hook. There
# used to be no copy in the hook at all, which is how Vale ended up the only
# linter here with no whole-tree local run, and how its exclusion list came to
# exist on one side only: correcting a generated changelog line by hand failed
# the commit, because the hook did not know the changelog was generated.
#
# Not under .github/ despite the script that lives there. A git hook is the
# main caller, and filing hook infrastructure under .github/ is what suggests
# it only runs in CI — which is the misunderstanding this file exists to end.
set -uo pipefail

cd "$(dirname "$0")/.."

# Vale ships no opinions of its own; the styles it checks against are
# downloaded rather than committed. Without them it reports nothing at all and
# exits 0, which reads exactly like a pass.
if [ ! -d styles/Google ] || [ ! -d styles/proselint ]; then
  echo "Fetching Vale's style packages"
  vale sync
fi

# The prose this repository wrote. A bare `vale .` also reads the Contributor
# Covenant, the generated changelog, OpenSpec's vendored Claude Code docs and
# the README of every downloaded style package, and holds all of them to
# house rules they were never written to.
files=$(git ls-files '*.md' | grep -v '^CODE_OF_CONDUCT.md$' | grep -v '^CHANGELOG.md$' | grep -v '^\.claude/')

echo "Checking:"
echo "$files"

# VALE_REPORT is where CI wants a copy for the job summary. Locally there is
# nowhere to put one, and the terminal already has it.
# shellcheck disable=SC2086
vale --output=line $files | tee "${VALE_REPORT:-/dev/null}"

# Errors are the tier worth failing on — a misspelt product name, or a term
# this repository has already decided how to spell. Warnings are advice, and
# style advice that blocks a commit teaches people to skip the hooks.
#
# Decided by a second run rather than by reading the output above: the line
# format prints file, position, rule and message and no severity at all, so
# grepping it for ":error:" matched nothing and this could not fail however
# wrong the prose was.
# shellcheck disable=SC2086
if ! vale --minAlertLevel=error --output=line $files >/dev/null; then
  echo "Vale reported errors"
  exit 1
fi
