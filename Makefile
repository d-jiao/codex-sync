.PHONY: build install clean test fmt lint release-dry-run setup-hooks check install-launchd uninstall-launchd

BINARY_NAME=codex-sync
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_DIR=bin
GO=go

# Build flags
LDFLAGS=-ldflags "-s -w -X main.version=$(VERSION)"

# Default target
all: build

# Build the binary
build:
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/codex-sync

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)

# Run tests
test:
	$(GO) test -v ./...

# Format code
fmt:
	$(GO) fmt ./...

# Lint code (requires golangci-lint)
lint:
	golangci-lint run

# Build for multiple platforms
build-all: build-darwin build-linux build-windows
	cd $(BUILD_DIR) && shasum -a 256 $(BINARY_NAME)-* > checksums.txt

build-darwin:
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd/codex-sync
	GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd/codex-sync

build-linux:
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/codex-sync
	GOOS=linux GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 ./cmd/codex-sync

build-windows:
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd/codex-sync
	GOOS=windows GOARCH=arm64 $(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-arm64.exe ./cmd/codex-sync

# Development: build and run
run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

# Download dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy

# Setup git hooks
setup-hooks:
	git config core.hooksPath .githooks
	@echo "Git hooks installed. Pre-commit will run tests and lint."

INSTALL_DIR ?= $(HOME)/.local/bin
LAUNCHD_LABEL = com.codex-sync.daily
LAUNCHD_PLIST = $(HOME)/Library/LaunchAgents/$(LAUNCHD_LABEL).plist
# launchd jobs do not inherit the shell environment: bake CODEX_HOME into the
# agent when it is set at install time, so the job syncs the same home.
LAUNCHD_ENV = $(if $(CODEX_HOME),<key>EnvironmentVariables</key><dict><key>CODEX_HOME</key><string>$(CODEX_HOME)</string></dict>,)

# Install the binary for the current user
install: build
	mkdir -p $(INSTALL_DIR)
	install -m 755 $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Installed $(INSTALL_DIR)/$(BINARY_NAME)"

# Install the daily launchd agent (pull then push: immediately on install,
# daily at 03:00, and at login). Honors CODEX_HOME if set when running this.
install-launchd: install
	mkdir -p $(HOME)/Library/LaunchAgents $(HOME)/Library/Logs
	sed -e 's#__BIN__#$(INSTALL_DIR)/$(BINARY_NAME)#g' -e 's#__HOME__#$(HOME)#g' \
		-e 's#__ENV__#$(LAUNCHD_ENV)#' \
		scripts/launchd/$(LAUNCHD_LABEL).plist.template > $(LAUNCHD_PLIST)
	plutil -lint $(LAUNCHD_PLIST)
	launchctl bootout gui/$$(id -u) $(LAUNCHD_PLIST) 2>/dev/null || true
	launchctl bootstrap gui/$$(id -u) $(LAUNCHD_PLIST)
	@echo "Installed $(LAUNCHD_LABEL): runs now, daily at 03:00 and at login; log: ~/Library/Logs/codex-sync.log"

uninstall-launchd:
	launchctl bootout gui/$$(id -u) $(LAUNCHD_PLIST) 2>/dev/null || true
	rm -f $(LAUNCHD_PLIST)
	@echo "Removed $(LAUNCHD_LABEL)"

# Run all checks (same as pre-commit)
check:
	@echo "Checking formatting..."
	@test -z "$$(gofmt -l .)" || (echo "Run 'make fmt' to fix formatting" && exit 1)
	@echo "Running go vet..."
	$(GO) vet ./...
	@echo "Running tests..."
	$(GO) test ./... -short
	@echo "All checks passed!"
