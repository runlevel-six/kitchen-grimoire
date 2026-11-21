# SPDX-License-Identifier: Apache-2.0
#
# `make check` is the local equivalent of CI (docs/DESIGN.md §18.5).
# Dev tools are pinned here and run with `go run`, so they never become
# module dependencies of the shipped binary.

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.10.0
GOLICENSES  := github.com/google/go-licenses/v2@v2.0.1
# The templ generator must match the runtime version in go.mod.
TEMPL       := github.com/a-h/templ/cmd/templ@$(shell go list -m -f '{{.Version}}' github.com/a-h/templ)
# Use an installed betterleaks if there is one, else the pinned module.
BETTERLEAKS ?= $(shell command -v betterleaks 2>/dev/null || echo go run github.com/betterleaks/betterleaks@v1.10.1)

BIN     := bin/grimoire
IMPORT_BIN := bin/grimoire-import
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/runlevel-six/kitchen-grimoire/internal/product.Version=$(VERSION)

export CGO_ENABLED := 0

.PHONY: all build build-import test vet fmt-check staticcheck vulncheck licenses private betterleaks check test-local image hooks clean generate generate-check dev

all: check build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/grimoire

# The legacy collection converter (D-07). A separate binary, never in the image.
build-import:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(IMPORT_BIN) ./cmd/grimoire-import

test:
	go test ./...

vet:
	go vet ./...

fmt-check:
	@out="$$(gofmt -l internal cmd web)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

staticcheck:
	go run $(STATICCHECK) ./...

vulncheck:
	go run $(GOVULNCHECK) ./...

# No GPL/AGPL/LGPL ("forbidden" and "restricted" classes) in the shipped binary.
licenses:
	go run $(GOLICENSES) check ./cmd/grimoire --disallowed_types=forbidden,restricted,unknown

private:
	scripts/check-private.sh --all

betterleaks:
	$(BETTERLEAKS) git --redact --no-banner .

check: generate-check fmt-check vet staticcheck test vulncheck licenses private betterleaks

# Generated *_templ.go files are committed so `go build` needs no extra tools.
generate:
	go run $(TEMPL) generate

# Fails if the generated code on disk is stale: it regenerates and checks
# that nothing changed. (Comparing with git instead would also fail on
# correct-but-uncommitted output.) In CI the checkout is clean, so this
# catches a .templ edit committed without its generated file.
TEMPL_SUM = find . -name '*_templ.go' -not -path './.claude/*' | sort | xargs cat | cksum
generate-check:
	@before="$$($(TEMPL_SUM))"; go run $(TEMPL) generate >/dev/null 2>&1; after="$$($(TEMPL_SUM))"; \
	if [ "$$before" != "$$after" ]; then echo "generated templ code was stale; run 'make generate' and commit the result"; exit 1; fi

# Run locally with auth disabled, reachable from a phone on the same network.
DEV_LISTEN ?= :8080
dev: generate build
	GRIMOIRE_DEV=1 GRIMOIRE_AUTH_MODE=none GRIMOIRE_LISTEN=$(DEV_LISTEN) \
	GRIMOIRE_BASE_URL=http://localhost$(DEV_LISTEN) GRIMOIRE_DATA_DIR=$(CURDIR)/local/data \
	$(BIN) serve

IMAGE ?= kitchen-grimoire:dev
image:
	docker build --build-arg VERSION=$(VERSION) --build-arg REVISION=$$(git rev-parse HEAD) -t $(IMAGE) .

# Golden tests over real captures in local/fixtures/ (gitignored). No-op when absent.
test-local:
	@if [ -d local/fixtures ]; then GRIMOIRE_LOCAL_FIXTURES=$(CURDIR)/local/fixtures go test ./... -run Local; \
	else echo "local/fixtures not present; nothing to do"; fi

hooks:
	git config core.hooksPath .githooks
	@echo "git hooks enabled from .githooks/"

clean:
	rm -rf bin dist
