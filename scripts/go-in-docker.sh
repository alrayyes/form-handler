#!/usr/bin/env bash
# Runs a Go toolchain command inside the exact golang image go.mod's own `go`
# directive names, pinned by digest, rather than whatever `go` a host package
# manager happens to have installed.
#
# The reason: a host's `go` and golangci-lint (run through its own image,
# scripts/golangci-lint-in-docker.sh) update independently of each other and
# of this repository's pins. Hit live on a real machine: golangci-lint built
# against go1.26 failing to type-check the stdlib of go1.27, installed
# alongside it by the same package manager on the same day — every Go hook on
# that machine broken until one of the two packages caught up, with nothing
# to fix in any repo (rules/go-mod.md, account-wide Claude Code conventions).
#
# --user leaves HOME unset, and go's own defaults for GOCACHE and GOMODCACHE
# both resolve under /root, which a non-root UID cannot write to — go test
# fails outright, not slowly, without the two mounts below.
set -euo pipefail

cd "$(dirname "$0")/.."

GO_VERSION=1.25.14
GO_IMAGE_DIGEST=sha256:3b4a11519ad929d1e1d261a12cff056f0c85b735253d7d861346b9c6f8b36437

docker run --rm --user "$(id -u):$(id -g)" \
  -v "$(pwd):/src" -w /src \
  -e GOCACHE=/gocache -v "$HOME/.cache/go-build-docker:/gocache" \
  -e GOMODCACHE=/gomod -v "$HOME/.cache/go-mod-docker:/gomod" \
  "golang:${GO_VERSION}-bookworm@${GO_IMAGE_DIGEST}" \
  sh -c "$*"
