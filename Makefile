GO ?= go
PACKAGES ?= ./...
TOOLS_BIN ?= $(CURDIR)/bin
GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT_VERSION_NUMBER := $(patsubst v%,%,$(GOLANGCI_LINT_VERSION))
GOLANGCI_LINT ?= $(TOOLS_BIN)/golangci-lint

.PHONY: test vet lint lint-config lint-fix lint-install lint-tool check fmt

test:
	$(GO) test $(PACKAGES)

vet:
	$(GO) vet $(PACKAGES)

lint-tool:
	@if [ ! -x "$(GOLANGCI_LINT)" ]; then \
		echo "golangci-lint is not installed; run 'make lint-install'"; \
		exit 1; \
	fi
	@"$(GOLANGCI_LINT)" version | grep -q "version $(GOLANGCI_LINT_VERSION_NUMBER)" || { \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) is required; run 'make lint-install'"; \
		exit 1; \
	}

lint-install:
	@mkdir -p "$(TOOLS_BIN)"
	@if [ -x "$(GOLANGCI_LINT)" ] && "$(GOLANGCI_LINT)" version | grep -q "version $(GOLANGCI_LINT_VERSION_NUMBER)"; then \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) is already installed"; \
	else \
		curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b "$(TOOLS_BIN)" "$(GOLANGCI_LINT_VERSION)"; \
	fi

lint-config: lint-tool
	"$(GOLANGCI_LINT)" config verify

lint: lint-config
	$(GO) mod tidy -diff
	"$(GOLANGCI_LINT)" run $(PACKAGES)

lint-fix: lint-config
	"$(GOLANGCI_LINT)" run --fix $(PACKAGES)

fmt: lint-tool
	"$(GOLANGCI_LINT)" fmt

check: lint vet test
