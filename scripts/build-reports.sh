#!/usr/bin/env bash
# Assembles site/reports/ from the files the test job writes: junit.xml and
# coverage.out in the current directory. CI runs it on every pull request as
# well as on master, so a broken conversion fails before the merge; only the
# Pages upload and deploy wait for master.
set -euo pipefail

out="${1:-site/reports}"
sha="${GITHUB_SHA:-$(git rev-parse HEAD)}"
date="$(date -u +%Y-%m-%d)"

mkdir -p "$out/tests" "$out/coverage"

cp junit.xml "$out/tests/unit.xml"

# cmd/form-handler/main.go is the composition root. codecov.yml ignores it, so
# the published number leaves it out too, or the two would disagree.
go tool gocover-cobertura -ignore-files 'cmd/form-handler/main\.go' \
  <coverage.out >"$out/coverage/coverage.xml"
cp coverage.out "$out/coverage/coverage.out"
go tool cover -html=coverage.out -o "$out/coverage/index.html"

cat >"$out/index.html" <<HTML
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>form-handler reports</title>
</head>
<body>
<main>
<h1>form-handler reports</h1>
<p>Commit <code>${sha:0:7}</code>, ${date}.</p>
<ul>
<li><a href="tests/unit.xml">Unit test results (JUnit XML)</a></li>
<li><a href="coverage/">Coverage (HTML)</a></li>
<li><a href="coverage/coverage.xml">Coverage (Cobertura XML)</a></li>
<li><a href="coverage/coverage.out">Coverage (Go profile)</a></li>
</ul>
</main>
</body>
</html>
HTML
