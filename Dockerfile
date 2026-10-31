# syntax=docker/dockerfile:1

# The admin panel, built by the web/ workspace and copied into the tree the Go
# compiler embeds. The panel build ends by rendering its one page through a
# preview server; build-panel.mjs writes that page from the build's manifest
# when no listener can be opened, so the step needs no port either way.
#
# The version is the git tag, handed in by the caller (the release workflow
# names it): a build context carries no .git, so git cannot answer here. It is
# written into the panel's package.json, which the panel imports. No tag is no
# version, not a guess.
FROM node:24-bookworm AS panel

ARG VERSION=""
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
COPY web/apps/web/package.json apps/web/
COPY web/packages/ui/package.json packages/ui/
RUN npm ci --no-audit --no-fund
COPY web/ ./
WORKDIR /src
COPY . .
RUN [ -z "$VERSION" ] || node web/scripts/version.mjs --stamp "$VERSION" \
 && node web/scripts/build-panel.mjs

# Go 1.27 matches the go directive in go.mod (go 1.27.3).
FROM golang:1.27-bookworm AS build

WORKDIR /src

# Dependencies first so layer caching works across source-only changes.
COPY go.mod go.sum ./
RUN go mod download

# The source, then the panel the previous stage built, so `go build` folds the
# current panel into the binary.
COPY . .
COPY --from=panel /src/internal/panel/files ./internal/panel/files

RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w" \
        -o /out/telesync ./cmd/telesync

# Minimal runtime. distroless static has no shell, no package manager and a
# non-root user (65532) by default.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/telesync /usr/local/bin/telesync

# The daemon spawns `telesync worker` processes; both come from the same binary.
# The configuration is mounted read-only, and run/session dirs are writable.
VOLUME ["/data"]

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/telesync"]
CMD ["daemon"]
