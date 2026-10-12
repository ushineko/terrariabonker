default: help

.PHONY: help
help: ## Show this help
	@echo
	@echo "Available commands:"
	@echo
	@awk -F ':|##' '/^[^\t].+?:.*?##/ {printf "\033[36m%-24s\033[0m %s\n", $$1, $$NF}' $(MAKEFILE_LIST)

MODULE=github.com/ushineko/terrariabonker
VERSION?=$(shell tr -d 'v[:space:]' < .tag 2>/dev/null || echo dev)
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS=-w -s -X $(MODULE)/internal/buildinfo.Version=$(VERSION) -X $(MODULE)/internal/buildinfo.Commit=$(COMMIT)

# migrated_fynedo tells Fyne this app has been through the fyne.Do migration, so
# it stops asking which goroutine it is on. Without it, Fyne answers that
# question with runtime.Stack -- a full traceback of the goroutine -- on every
# Canvas.Refresh. Profiled during a window drag: 52% of the process's CPU was
# runtime.traceback, and dragging the window stalled for seconds at a time.
# Every UI mutation off the main goroutine here goes through fyne.Do, which is
# what the tag asserts.
FYNE_TAGS?=migrated_fynedo

# On Windows the GUI must link as a GUI subsystem binary, or Windows gives it a
# console window that sits behind the Fyne window for its whole life. -H
# windowsgui sets the PE subsystem to GUI; it is a PE-only flag, so it is added
# only for a Windows build (it is not a valid header type for an ELF link).
#
# EXE is the suffix a program has there. `go build -o` writes exactly the name it
# is given, so without it a Windows build leaves bin/terrariabonker, which
# Windows will not run and the installer's manifest does not name.
GUI_LDFLAGS=$(LDFLAGS)
EXE=
FYNSTALL_FLAGS?=--with-cli-only
ifeq ($(shell go env GOOS),windows)
GUI_LDFLAGS+= -H windowsgui
EXE=.exe
# --windowed: the wizard's installer and the uninstaller never show a console,
# on any Windows. The command-line installer beside it is the one for a terminal.
FYNSTALL_FLAGS+= --windowed
endif

# Forward slashes: on Windows GOPATH is C:\Users\..., and a recipe's shell reads
# a backslash as an escape.
BINDIR=$(subst \,/,$(shell go env GOPATH))

# The installer is built by fynstall (github.com/ushineko/fynstall) from
# fynstall.yaml, and an installer is pinned to the fynstall that built it. The
# pin lives here, as the linter's does: fynstall is a build tool, not a
# dependency of either binary, so go.mod does not name it.
FYNSTALL_VERSION?=v0.2.2
FYNSTALL?=$(BINDIR)/bin/fynstall-$(FYNSTALL_VERSION)$(EXE)
LINT_NAME?=golangci-lint
LINT_VERSION?=v2.12.2
LINT_PROGRAM=$(LINT_NAME)-$(LINT_VERSION)
LINT_VERSION_NUM=$(LINT_VERSION:v%=%)
LINT_BASE_URL=https://github.com/golangci/golangci-lint/releases/download/$(LINT_VERSION)

# golangci-lint type-checks against the standard library of whichever Go it
# finds, using a go/types built into the linter binary -- so a linter built
# with an older Go panics on a newer GOROOT. go.mod carries no toolchain line,
# so the pin lives here. Bump it together with LINT_VERSION.
LINT_GO_TOOLCHAIN?=go1.26.0

.PHONY: install-lint
install-lint: $(BINDIR)/bin/$(LINT_PROGRAM) ## Install the pinned linter

$(BINDIR)/bin/$(LINT_PROGRAM):
	@echo "Setting up $(LINT_PROGRAM) ..."
	@set -e; \
	os=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	arch=$$(uname -m); \
	case "$$arch" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; esac; \
	dist="$(LINT_NAME)-$(LINT_VERSION_NUM)-$$os-$$arch"; \
	tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	curl -fsSL "$(LINT_BASE_URL)/$$dist.tar.gz" -o "$$tmp/$$dist.tar.gz"; \
	curl -fsSL "$(LINT_BASE_URL)/$(LINT_NAME)-$(LINT_VERSION_NUM)-checksums.txt" -o "$$tmp/checksums.txt"; \
	want=$$(awk -v f="$$dist.tar.gz" '$$2 == f {print $$1}' "$$tmp/checksums.txt"); \
	got=$$( (sha256sum "$$tmp/$$dist.tar.gz" 2>/dev/null || shasum -a 256 "$$tmp/$$dist.tar.gz") | awk '{print $$1}'); \
	if [ -z "$$want" ] || [ "$$want" != "$$got" ]; then echo "checksum mismatch for $$dist.tar.gz"; exit 1; fi; \
	tar -C "$$tmp" -xzf "$$tmp/$$dist.tar.gz"; \
	mkdir -p "$(BINDIR)/bin"; \
	mv -v "$$tmp/$$dist/$(LINT_NAME)" "$(BINDIR)/bin/$(LINT_PROGRAM)"

.PHONY: lint
lint: export GOTOOLCHAIN = $(LINT_GO_TOOLCHAIN)
lint: install-lint ## Lint every package
	@go version
	$(BINDIR)/bin/$(LINT_PROGRAM) run --timeout 5m0s --config config/.golangci-$(LINT_VERSION).yml ./...

.PHONY: test
test: ## Run the tests with the race detector
	@go test -race ./...

.PHONY: coverage
coverage: ## Run the tests and open a coverage report
	@go test -coverprofile coverage.out ./...
	@go tool cover -html=coverage.out
	@rm -f coverage.out

.PHONY: build
build: ## Build the CLI (no CGO: it needs no display)
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags='$(LDFLAGS)' -trimpath -o bin/terrariabonker$(EXE) ./cmd/terrariabonker

.PHONY: build-gui
build-gui: ## Build the window (needs CGO, OpenGL and X11/Wayland headers)
	@mkdir -p bin
	CGO_ENABLED=1 go build -tags $(FYNE_TAGS) -ldflags='$(GUI_LDFLAGS)' -trimpath -o bin/terrariabonker-gui$(EXE) ./cmd/terrariabonker-gui

.PHONY: release
release: build build-gui ## Package both binaries into dist/ with a checksum file
	@set -e; \
	rm -rf dist; mkdir -p dist; \
	stage=$$(mktemp -d); trap 'rm -rf "$$stage"' EXIT; \
	dir="$$stage/terrariabonker-$(VERSION)-linux-amd64"; \
	mkdir -p "$$dir"; \
	cp bin/terrariabonker bin/terrariabonker-gui "$$dir/"; \
	cp README.md install.sh uninstall.sh io.ushineko.terrariabonker.desktop "$$dir/"; \
	mkdir -p "$$dir/assets"; cp assets/terrariabonker.svg "$$dir/assets/"; \
	tar -C "$$stage" -czf "dist/terrariabonker-$(VERSION)-linux-amd64.tar.gz" \
		"terrariabonker-$(VERSION)-linux-amd64"; \
	cd dist && (sha256sum *.tar.gz 2>/dev/null || shasum -a 256 *.tar.gz) > SHA256SUMS
	@ls -l dist/

.PHONY: install-fynstall
install-fynstall: $(FYNSTALL) ## Install the pinned installer builder

$(FYNSTALL):
	@echo "Setting up fynstall $(FYNSTALL_VERSION) ..."
	@set -e; \
	mkdir -p bin "$(BINDIR)/bin"; \
	rm -rf bin/.fynstall; \
	GOBIN="$(CURDIR)/bin/.fynstall" GOFLAGS= go install github.com/ushineko/fynstall/cmd/fynstall@$(FYNSTALL_VERSION); \
	mv -v "bin/.fynstall/fynstall$(EXE)" "$(FYNSTALL)"; \
	rmdir bin/.fynstall

.PHONY: installer
installer: build build-gui install-fynstall ## Build the installers for this platform into dist/
	@mkdir -p dist
	$(FYNSTALL) validate
	$(FYNSTALL) build $(FYNSTALL_FLAGS)
	@ls -l dist/

.PHONY: install
install: ## Install both binaries, the desktop entry and the icon into ~/.local
	./install.sh

.PHONY: uninstall
uninstall: ## Remove what install put in ~/.local, leaving your data alone
	./uninstall.sh

.PHONY: clean
clean: ## Remove build artifacts
	@rm -rf bin/ dist/ coverage.out
