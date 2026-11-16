# syntax=docker/dockerfile:1
#
# WxAlerts PWS Gateway container image.
#
# Three stages:
#   web    builds the React UI with Node 20 (or writes a placeholder when the
#          UI is not in the tree yet, so the Go embed still has a directory),
#   build  cross-compiles the Go daemon with CGO disabled (SQLite is pure Go
#          through modernc.org/sqlite), running on the build host's own
#          architecture and targeting $TARGETARCH, so arm64 images do not
#          need emulation,
#   final  distroless static image, non-root, nothing but the binary.
#
# Build from the repository root:
#   docker build -f deploy/Dockerfile --build-arg VERSION=dev -t pws-gateway .
# Multi-arch pushes happen in .github/workflows/release.yml.

ARG GO_VERSION=1.26
ARG NODE_VERSION=20

# ---------------------------------------------------------------- web -------
FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-bookworm-slim AS web
WORKDIR /src
# The whole context is copied because web/ may not exist yet and COPY has no
# optional form. deploy/Dockerfile.dockerignore keeps node_modules and build
# output out of the context.
COPY . .
RUN set -eu; \
    if [ -f web/package.json ]; then \
      cd web && npm ci --no-audit --no-fund && npm run build; \
    else \
      mkdir -p web/dist && printf 'web UI not built\n' > web/dist/index.html; \
    fi

# -------------------------------------------------------------- build -------
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
ENV CGO_ENABLED=0
# go.sum is optional until the first dependency is added; the glob keeps the
# COPY valid either way.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -eu; \
    commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown); \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w -buildid= -X main.version=${VERSION} -X main.commit=${commit}" \
      -o /out/pwsgw ./cmd/pwsgw

# -------------------------------------------------------------- final -------
# static-debian12 ships CA certificates and /etc/passwd with the nonroot user
# (uid 65532) and nothing else: no shell, no package manager.
FROM gcr.io/distroless/static-debian12:nonroot AS final
LABEL org.opencontainers.image.title="WxAlerts PWS Gateway" \
      org.opencontainers.image.source="https://github.com/wxalerts/pws-gateway" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/pwsgw /pwsgw
COPY LICENSE NOTICE /usr/share/doc/pws-gateway/
# SQLite database, outbox and update state live here. Mount a volume.
VOLUME ["/var/lib/pwsgw"]
# Web UI. The non-root user cannot bind 80/443 inside the container; map the
# host ports in compose instead.
EXPOSE 8080 8443
USER nonroot:nonroot
ENTRYPOINT ["/pwsgw"]
