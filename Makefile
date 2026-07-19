MACOSX_DEPLOYMENT_TARGET := 14.2
export MACOSX_DEPLOYMENT_TARGET

GO ?= go

.PHONY: test test-race vet build build-nocgo verify verify-minos

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

verify-minos:
	@artifact="$${TMPDIR:-/tmp}/amtui-visualizer-minos-$$$$"; \
	trap 'rm -f "$$artifact"' EXIT; \
	CGO_ENABLED=1 $(GO) test -c -o "$$artifact" ./visualizer; \
	vtool -show-build "$$artifact"
