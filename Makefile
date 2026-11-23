BINARY := usage-cli
PKG := ./cmd/usage-cli
DIST_DIR := dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo unknown)

LDFLAGS := -s -w \
	-X github.com/gm64x/usage-cli/internal/cli.Version=$(VERSION) \
	-X github.com/gm64x/usage-cli/internal/cli.Commit=$(COMMIT) \
	-X github.com/gm64x/usage-cli/internal/cli.Date=$(DATE)

.PHONY: test test-race bench lint fmt vet build-win build-wsl build-all

test:
	go test ./...

test-race:
	go test -race ./...

bench:
	go test -bench=. -benchmem ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

build-win:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe $(PKG)

build-wsl:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-linux-amd64 $(PKG)

build-all: build-win build-wsl
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-windows-arm64.exe $(PKG)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-linux-arm64 $(PKG)
