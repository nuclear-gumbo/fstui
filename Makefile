# SPDX-License-Identifier: GPL-3.0-or-later
#
# Distribution builds (Portage, Nix, Debian) call `make build install` with
# their own GOFLAGS, VERSION, PREFIX and DESTDIR. Nothing here strips the
# binary or reaches the network; packagers own both.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo unknown)
PREFIX  ?= /usr/local
BINDIR  ?= $(PREFIX)/bin
GO      ?= go
GOFLAGS ?= -trimpath

export CGO_ENABLED ?= 0
export GOFLAGS

BIN := bin/fstui
DEMO_BIN := bin/fstui-demo

# The demo build replays recorded OpenZFS output instead of running zfs
# and zpool (internal/demo). It is never installed; it ships only in the
# demo container image.
DEMO_TAGS := fstui_demo

.PHONY: build build-demo install check fmt fmt-check vet staticcheck lint shellcheck vuln test test-variants integration-build test-clean clean

build:
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/fstui

build-demo:
	$(GO) build -tags $(DEMO_TAGS) -ldflags "-X main.version=$(VERSION)" -o $(DEMO_BIN) ./cmd/fstui

# install copies what make build produced and never compiles, so the usual
# `make build` as a user, then `make install` as root, builds unprivileged.
install:
	@test -x $(BIN) || { echo "$(BIN) does not exist; run make build first (as your user, not root)"; exit 1; }
	install -Dm0755 $(BIN) $(DESTDIR)$(BINDIR)/fstui

check: fmt-check vet staticcheck lint shellcheck vuln test test-variants

fmt:
	gofumpt -w .

fmt-check:
	@out=$$(gofumpt -l .); if [ -n "$$out" ]; then echo "gofumpt would change:"; echo "$$out"; exit 1; fi

# vet, staticcheck, lint and test also cover the demo build, whose files
# the default build leaves out; vet and staticcheck also the build
# without the write module.
NOWRITE_TAGS := fstui_nowrite

vet:
	$(GO) vet ./...
	$(GO) vet -tags $(DEMO_TAGS) ./...
	$(GO) vet -tags $(NOWRITE_TAGS) ./...

staticcheck:
	staticcheck ./...
	staticcheck -tags $(DEMO_TAGS) ./...
	staticcheck -tags $(NOWRITE_TAGS) ./...

lint:
	golangci-lint run ./...
	golangci-lint run --build-tags $(DEMO_TAGS) ./...

shellcheck:
	shellcheck scripts/*.sh

vuln:
	govulncheck ./...

# The race detector needs cgo; shipped binaries stay cgo-free.
test:
	CGO_ENABLED=1 $(GO) test -race ./...
	CGO_ENABLED=1 $(GO) test -race -tags $(DEMO_TAGS) ./internal/demo/ ./cmd/fstui/

# Every supported combination of module omit tags builds and reports itself.
test-variants:
	sh scripts/test-build-variants.sh

# Integration tests create real pools: build them here as an unprivileged
# user, then run the binary as root with scripts/integration.sh.
integration-build:
	$(GO) test -c -tags fstui_integration -o bin/integration.test ./internal/zfs

# Destroys leftover fstuitest_ pools and their backing files. Must run as root.
test-clean:
	sh scripts/test-clean.sh

clean:
	rm -rf bin
