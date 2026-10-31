# Common tasks. `make` with no target builds ./bin/telesync.
#
#   make            build the admin panel, regenerate docs/api, then build
#                   ./bin/telesync (the binary carries both: GET /admin and
#                   GET /docs)
#   make run        build, then start the daemon in the foreground
#   make admin      build, then create or reset an administrator (asks for it)
#   make check      gofmt + vet + Go and panel-version tests + panel typecheck
#                   and content tests + documentation current + build, what
#                   AGENTS.md requires (starts no service, network or container)
#   make verify     everything CI runs, in CI's order: the checks above plus the
#                   race tests and a Docker image build (built, never run or
#                   pushed; needs a running Docker engine)
#   make docs       regenerate docs/api from the source (go run ./cmd/docsgen)
#   make docs-check fail if the committed docs/api is out of date
#   make panel      build the panel only and fold it into internal/panel/files
#                   (needs node and the web/ workspace's node_modules)
#   make web-install
#                   install the frontend's dependencies from package-lock.json
#   make web-typecheck
#                   TypeScript check of web/ (needs node_modules; run this
#                   first after adding or changing a dependency)
#   make web-test   the panel's own tests (content parser)
#   make docker     build the image locally: docker build -t telesync:local .
#                   (validates the multi-stage Dockerfile; CI pushes the same
#                   Dockerfile's image to ghcr.io itself)
#   make version v1.0.2
#                   release: write 1.0.2 into the panel's package.json and
#                   create the git tag v1.0.2 at HEAD
#   make clean      remove ./bin and the panel's build output
#
# The version is whatever web/apps/web/package.json holds: `make version v1.0.2`
# writes it there (and makes the tag), the panel imports the file, and a build
# carries it. With no version in the file the nearest git tag is the fallback,
# and with neither the panel shows none. See web/scripts/version.mjs.

BINARY  := bin/telesync
PKG     := ./cmd/telesync
GOFLAGS :=
GO      ?= go

.PHONY: all build release run admin check verify fmt vet test test-race cover docs docs-check panel version test-panel web-install web-typecheck web-test docker clean help

all: build

# Every prerequisite is PHONY, so the build runs in this order on every
# `make build`: the panel that imports the version, then the documentation,
# then the binary. The binary has no file prerequisites of its own: `go build`
# already tracks the source, and the panel's file names change with every
# build.
build: panel docs
	@mkdir -p $(dir $(BINARY))
	$(GO) build $(GOFLAGS) -o $(BINARY) $(PKG)
	@echo "built $(BINARY)"

# make version v1.0.2 releases: package.json gets 1.0.2 and HEAD gets the tag
# v1.0.2 (a checkout that is not a git repository gets the version only, and is
# told so). Without a version it prints the one package.json (or, failing that,
# the git tag) holds.
VERSION_ARG = $(filter-out version,$(MAKECMDGOALS))
version:
	@node web/scripts/version.mjs $(VERSION_ARG)

# The panel is built by the web/ workspace and copied into the tree the Go
# compiler embeds.
panel:
	@if command -v node >/dev/null 2>&1 && [ -d web/node_modules ]; then \
		node web/scripts/build-panel.mjs; \
	elif [ -f internal/panel/files/index.html ]; then \
		echo "panel: keeping the panel embedded in this tree (node or web/node_modules missing)"; \
	else \
		echo "panel: needs node and web/node_modules; run \`cd web && npm install\` once" >&2; \
		exit 1; \
	fi

release: panel docs
	@mkdir -p $(dir $(BINARY))
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BINARY) $(PKG)
	@echo "built $(BINARY)"

run: build
	./$(BINARY) daemon

admin: build
	./$(BINARY) admin

check: fmt vet test test-panel web-typecheck web-test docs-check build

fmt:
	@out=$$(gofmt -l . | grep -v '^bin/'); \
	if [ -n "$$out" ]; then echo "gofmt needs to run on:"; echo "$$out"; exit 1; fi
	@echo "gofmt: clean"

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -count=1

# The panel's build tooling (the version the build stamps).
test-panel:
	node --test web/scripts/version.test.mjs

web-install:
	@cd web && npm ci --no-audit --no-fund

# Needs the workspace's node_modules (run `make web-install` after changing a
# dependency; the lockfile already pins the tree).
web-typecheck:
	@cd web && npm run typecheck

web-test:
	@cd web/apps/web && npm test

# Builds the multi-stage image locally, never runs one and never pushes one.
# CI's workflow is the same check in automation.
docker:
	docker build -t telesync:local .

# verify runs every check the CI workflow runs, in CI's order: the Go checks
# include the race tests, and the image is built but not run or pushed.
verify: fmt vet test test-race test-panel web-typecheck web-test docs-check docker

test-race:
	$(GO) test -race ./internal/auth ./internal/channelstore ./internal/runtime -count=1

cover:
	$(GO) test ./... -coverprofile=coverage.out -count=1
	$(GO) tool cover -func=coverage.out | tail -1

docs:
	$(GO) run ./cmd/docsgen

docs-check:
	$(GO) run ./cmd/docsgen --check

clean:
	rm -rf bin coverage.out web/apps/web/build

help:
	@sed -n '2,31p' Makefile | sed 's/^# \{0,1\}//'

# `make version v1.0.2` names an argument, not a target; without this, make
# would refuse it before the recipe runs.
%:
	@:
