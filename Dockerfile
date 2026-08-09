# Both stages pinned by digest, not just tag: a tag is repointable, and an image
# that changes underneath a deploy is the kind of thing you debug for a day.
FROM golang:1.26-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build

WORKDIR /src

# Dependencies first, so a source-only change does not re-download the module
# cache on every build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off so the binary is static and the final stage can be distroless.
# -trimpath keeps build machine paths out of the binary, which also makes the
# build reproducible enough to compare two of them.
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/form-handler .

# Distroless: no shell, no package manager, nothing to exec into if this is ever
# reached from outside. nonroot rather than root, and the port is above 1024 so
# it does not need privileges to bind.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35

COPY --from=build /out/form-handler /form-handler

EXPOSE 8080
USER nonroot:nonroot

# No HEALTHCHECK: there is no shell and no curl to run one with. The orchestrator
# polls /healthz over HTTP instead, which is what compose is configured to do.
ENTRYPOINT ["/form-handler"]
