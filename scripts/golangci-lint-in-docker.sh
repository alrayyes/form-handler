#!/usr/bin/env bash
# Runs golangci-lint through its own pinned image rather than whatever a host
# package manager has installed — see scripts/go-in-docker.sh's header for
# the drift this and it together are pinned against.
#
# Two caches, not one, and both need an explicit env var and mount, confirmed
# live rather than assumed (rules/go-lint.md): with no HOME set (which
# --user leaves unset), golangci-lint defaults its own result cache to
# /.cache/golangci-lint and the Go build cache to /.cache/go-build, and a
# non-root user cannot create either at /. GOLANGCI_LINT_CACHE redirects the
# first; GOCACHE redirects the second.
set -euo pipefail

cd "$(dirname "$0")/.."

GOLANGCI_LINT_VERSION=v2.12.2
GOLANGCI_LINT_IMAGE_DIGEST=sha256:5cceeef04e53efe1470638d4b4b4f5ceefd574955ab3941b2d9a68a8c9ad5240

docker run --rm --user "$(id -u):$(id -g)" \
  -v "$(pwd):/src" -w /src \
  -e GOCACHE=/gocache -v "$HOME/.cache/go-build-docker:/gocache" \
  -e GOLANGCI_LINT_CACHE=/cache -v "$HOME/.cache/golangci-lint-docker:/cache" \
  "golangci/golangci-lint:${GOLANGCI_LINT_VERSION}@${GOLANGCI_LINT_IMAGE_DIGEST}" \
  sh -c "$*"
