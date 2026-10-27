# Not built into anything shipped. It exists so Dependabot's docker ecosystem,
# which reads Dockerfiles and not a workflow's `container:`, can bump the
# gitleaks tag and digest. .github/workflows/gitleaks.yml builds this file and
# runs the result, so this is the only place the version is written.
FROM ghcr.io/gitleaks/gitleaks:v8.30.1@sha256:c00b6bd0aeb3071cbcb79009cb16a60dd9e0a7c60e2be9ab65d25e6bc8abbb7f
