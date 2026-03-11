.PHONY: build run clean test help fmt vet lint deps service install-service enable-service uninstall-service

# Variables
BINARY_NAME=slack-bot
BINARY_DIR=bin
BINARY_PATH=$(BINARY_DIR)/$(BINARY_NAME)
GO=go
GOFLAGS=-v
PREFIX?=$(HOME)/slack-bot
BINDIR=$(PREFIX)/bin
SYSTEMD_DIR=$(HOME)/.config/systemd/user
SERVICE_FILE=$(BINARY_NAME).service
SYSTEMCTL=systemctl --user

# OS detection
UNAME_S := $(shell uname -s)

# Check if running on Linux
define check_linux
	@if [ "$(UNAME_S)" != "Linux" ]; then \
		echo "Error: systemd service is only supported on Linux"; \
		echo "Current OS: $(UNAME_S)"; \
		exit 1; \
	fi
endef

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the application
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BINARY_DIR)
	$(GO) build $(GOFLAGS) -o $(BINARY_PATH) ./cmd/$(BINARY_NAME)

run: ## Run the application
	@echo "Running $(BINARY_NAME)..."
	$(GO) run ./cmd/$(BINARY_NAME)

clean: ## Remove build artifacts
	@echo "Cleaning..."
	rm -rf $(BINARY_DIR)
	$(GO) clean

test: ## Run tests
	$(GO) test -v ./...

fmt: ## Format code
	$(GO) fmt ./...

vet: ## Run go vet
	$(GO) vet ./...

lint: fmt vet ## Run linters

deps: ## Download dependencies
	$(GO) mod download
	$(GO) mod tidy

service: ## Generate systemd service file from template
	@echo "Generating $(SERVICE_FILE)..."
	@if [ ! -f .envrc ]; then \
		echo "Error: .envrc not found"; \
		exit 1; \
	fi
	@if [ ! -f slack-bot.service.template ]; then \
		echo "Error: slack-bot.service.template not found"; \
		exit 1; \
	fi
	@set -a && . ./.envrc && set +a && \
	export USER=$(USER) \
	       WORKING_DIR=$(shell pwd) \
	       BINDIR=$(BINDIR) \
	       BINARY_NAME=$(BINARY_NAME) && \
	envsubst < slack-bot.service.template > $(SERVICE_FILE)
	@echo "Generated $(SERVICE_FILE)"

install-service: service ## Install systemd service (Linux only)
	$(call check_linux)
	@echo "Installing $(SERVICE_FILE) to $(SYSTEMD_DIR)..."
	mkdir -p $(SYSTEMD_DIR)
	cp $(SERVICE_FILE) $(SYSTEMD_DIR)/
	$(SYSTEMCTL) daemon-reload
	@echo "Service file installed successfully"

enable-service: install-service ## Enable and start systemd service (Linux only)
	@echo "Enabling $(BINARY_NAME) service..."
	$(SYSTEMCTL) enable $(BINARY_NAME)
	@echo "Starting $(BINARY_NAME) service..."
	$(SYSTEMCTL) restart $(BINARY_NAME)
	@echo "Service started. Check status with: $(SYSTEMCTL) status $(BINARY_NAME)"

enable-linger: ## Enable lingering for current user (Linux only)
	$(call check_linux)
	@echo "Enabling lingering for user $(USER)..."
	loginctl enable-linger $(USER)
	@echo "Lingering enabled. User services will run even after logout."

uninstall-service: ## Uninstall systemd service (Linux only)
	$(call check_linux)
	@echo "Uninstalling $(SERVICE_FILE) from $(SYSTEMD_DIR)..."
	$(SYSTEMCTL) stop $(BINARY_NAME) || true
	$(SYSTEMCTL) disable $(BINARY_NAME) || true
	rm -f $(SYSTEMD_DIR)/$(SERVICE_FILE)
	$(SYSTEMCTL) daemon-reload
	@echo "Service uninstalled successfully"

.DEFAULT_GOAL := help

