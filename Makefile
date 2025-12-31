# Variables
BINARY_NAME := netns-exporter
DOCKER_IMAGE := netns-exporter
GO_VERSION := 1.23

# Updated to v1.62.2 to resolve "unsupported version: 2" with Go 1.23+
GOLANGCI_LINT_VERSION := 1.62.2

# Get the GOPATH for binary installation paths
GOPATH := $(shell go env GOPATH)

.PHONY: all help build build-linux build-docker image test lint lint-install clean deps

# Default target
all: lint test build

help:
	@echo "Available commands:"
	@echo "  make build         - Build the binary for host OS"
	@echo "  make build-linux   - Build the binary for Linux (amd64)"
	@echo "  make image         - Build the Docker image"
	@echo "  make test          - Run tests"
	@echo "  make lint          - Run linters"
	@echo "  make clean         - Cleanup"

deps:
	go mod download
	go mod tidy

build:
	go build -o $(BINARY_NAME) .

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BINARY_NAME) .

build-docker:
	docker run --rm \
		-v "$(PWD)":/app \
		-w /app \
		-e CGO_ENABLED=0 \
		-e GOOS=linux \
		golang:$(GO_VERSION) \
		go build -ldflags="-s -w" -o $(BINARY_NAME) .

image:
	docker build -t $(DOCKER_IMAGE) .

test:
	go test -v -race ./...

lint-install:
	@# Check if installed version matches the requested version
	@if [ ! -f "$(GOPATH)/bin/golangci-lint" ] || [ "$$($(GOPATH)/bin/golangci-lint --version | awk '{print $$4}')" != "$(GOLANGCI_LINT_VERSION)" ]; then \
		echo "Installing golangci-lint v$(GOLANGCI_LINT_VERSION)..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GOPATH)/bin v$(GOLANGCI_LINT_VERSION); \
		$(GOPATH)/bin/golangci-lint cache clean; \
	fi

# Removed deprecated linters (structcheck, varcheck)
# Added 'cache clean' to ensure no old artifacts cause version conflicts
lint: lint-install
	$(GOPATH)/bin/golangci-lint run \
		--timeout=5m \
		--disable-all \
		--enable=govet \
		--enable=staticcheck \
		--enable=unused \
		--enable=gosimple \
		--enable=ineffassign \
		--enable=typecheck \
		--enable=revive \
		--enable=gocritic \
		--enable=gosec

clean:
	rm -f $(BINARY_NAME)
	go clean
