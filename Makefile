.PHONY: build run clean test help fmt vet lint deps image

# Variables
BINARY_NAME=slack-bot
BINARY_DIR=bin
BINARY_PATH=$(BINARY_DIR)/$(BINARY_NAME)
GO=go
GOFLAGS=-v
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS=-X main.version=$(VERSION)
IMAGE ?= ghcr.io/takanao14/$(BINARY_NAME)

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the application
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BINARY_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY_PATH) ./cmd/$(BINARY_NAME)

run: ## Run the application
	@echo "Running $(BINARY_NAME)..."
	$(GO) run ./cmd/$(BINARY_NAME)

image: ## Build the container image
	@echo "Building $(IMAGE):$(VERSION)..."
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

clean: ## Remove build artifacts
	@echo "Cleaning..."
	rm -rf $(BINARY_DIR)
	$(GO) clean

test: ## Run tests
	$(GO) test -race ./...

fmt: ## Format code
	$(GO) fmt ./...

vet: ## Run go vet
	$(GO) vet ./...

lint: fmt vet ## Run linters

deps: ## Download dependencies
	$(GO) mod download
	$(GO) mod tidy

.DEFAULT_GOAL := help
