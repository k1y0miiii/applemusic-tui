MACOSX_DEPLOYMENT_TARGET := 14.2
export MACOSX_DEPLOYMENT_TARGET

GO ?= go

# VERSION labels the artifacts. Defaults to the current tag, else the short SHA.
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || git rev-parse --short HEAD 2>/dev/null || echo dev)
DIST ?= dist
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: test test-race vet build build-nocgo verify verify-minos dist dist-clean

test:
	CGO_ENABLED=1 $(GO) test ./...

test-race:
	CGO_ENABLED=1 $(GO) test -race ./...

vet:
	CGO_ENABLED=1 $(GO) vet ./...

build:
	CGO_ENABLED=1 $(GO) build ./...

build-nocgo:
	CGO_ENABLED=0 $(GO) build ./...

verify: vet test test-race build build-nocgo verify-minos

dist-clean:
	rm -rf "$(DIST)"

# dist builds every release target into $(DIST) and writes SHA256SUMS.
#
# darwin needs CGO_ENABLED=1 so the visualizer keeps its CoreAudio process tap;
# without cgo the macOS build silently degrades to the simulated animation. That
# makes macOS the only host that can produce a complete release — linux and
# windows are pure Go and cross-compile from anywhere, so one macOS runner
# covers the whole matrix.
#
# linux keeps CGO_ENABLED=0 on purpose: its capture backend speaks the
# PulseAudio protocol in pure Go, so a static binary loses nothing and runs on
# any glibc/musl distro.
dist: dist-clean
	@mkdir -p "$(DIST)"
	@host="$$($(GO) env GOOS)"; \
	for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
	  os="$${target%%/*}"; arch="$${target##*/}"; cgo=0; ext=""; \
	  if [ "$$os" = "darwin" ]; then \
	    if [ "$$host" != "darwin" ]; then \
	      echo "SKIP  $$target — needs a macOS host for the CoreAudio visualizer"; \
	      continue; \
	    fi; \
	    cgo=1; \
	  fi; \
	  if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
	  name="amtui-$(VERSION)-$$os-$$arch"; \
	  echo "BUILD $$target (cgo=$$cgo)"; \
	  CGO_ENABLED=$$cgo GOOS=$$os GOARCH=$$arch \
	    $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(DIST)/$$name/amtui$$ext" . || exit 1; \
	  cp README.md LICENSE docs/config.example.toml "$(DIST)/$$name/"; \
	  if [ "$$os" = "windows" ]; then \
	    (cd "$(DIST)" && zip -qr "$$name.zip" "$$name"); \
	  else \
	    tar -czf "$(DIST)/$$name.tar.gz" -C "$(DIST)" "$$name"; \
	  fi; \
	  rm -rf "$(DIST)/$$name"; \
	done
	@cd "$(DIST)" && shasum -a 256 * > SHA256SUMS && cat SHA256SUMS

verify-minos:
	@artifact="$${TMPDIR:-/tmp}/amtui-visualizer-minos-$$$$"; \
	trap 'rm -f "$$artifact"' EXIT; \
	CGO_ENABLED=1 $(GO) test -c -o "$$artifact" ./visualizer; \
	vtool -show-build "$$artifact"
