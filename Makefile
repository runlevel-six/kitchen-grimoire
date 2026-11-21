# SPDX-License-Identifier: Apache-2.0
#
# `make check` is the local equivalent of CI (docs/DESIGN.md §18.5).
# Dev tools are pinned here and run with `go run`, so they never become
# module dependencies of the shipped binary.

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.10.0
GOLICENSES  := github.com/google/go-licenses/v2@v2.0.1

BIN     := bin/grimoire
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X kitchen-grimoire/internal/product.Version=$(VERSION)

export CGO_ENABLED := 0

.PHONY: all build test vet fmt-check staticcheck vulncheck licenses private gitleaks check test-local hooks clean

all: check build

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/grimoire

test:
	go test ./...

vet:
	go vet ./...

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

staticcheck:
	go run $(STATICCHECK) ./...

vulncheck:
	go run $(GOVULNCHECK) ./...

# No GPL/AGPL/LGPL ("forbidden" and "restricted" classes) in the shipped binary.
licenses:
	go run $(GOLICENSES) check ./cmd/grimoire --disallowed_types=forbidden,restricted,unknown

private:
	scripts/check-private.sh --all

gitleaks:
	@if command -v gitleaks >/dev/null 2>&1; then gitleaks git --redact --no-banner .; \
	else echo "gitleaks not installed; skipping (CI runs it)"; fi

check: fmt-check vet staticcheck test vulncheck licenses private gitleaks

# Golden tests over real captures in local/fixtures/ (gitignored). No-op when absent.
test-local:
	@if [ -d local/fixtures ]; then GRIMOIRE_LOCAL_FIXTURES=$(CURDIR)/local/fixtures go test ./... -run Local; \
	else echo "local/fixtures not present; nothing to do"; fi

hooks:
	git config core.hooksPath .githooks
	@echo "git hooks enabled from .githooks/"

clean:
	rm -rf bin dist
