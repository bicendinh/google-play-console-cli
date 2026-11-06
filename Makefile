GO ?= go
VERSION ?= dev

.PHONY: build test check release
build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -buildvcs=false -trimpath -ldflags "-s -w -X playops/internal/cli.Version=$(VERSION)" -o bin/playops ./cmd/playops

test:
	$(GO) test -race ./...

check:
	test -z "$$($$($(GO) env GOROOT)/bin/gofmt -l cmd internal integration)"
	$(GO) vet ./...
	$(GO) mod verify

release:
	GO_BINARY=$(GO) python3 scripts/release.py $(VERSION)
