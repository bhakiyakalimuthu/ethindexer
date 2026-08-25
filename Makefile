GO ?= go
PACKAGES ?= ./...
GO_FILES := $(filter-out $(shell git ls-files --deleted '*.go'),$(shell git ls-files --cached --others --exclude-standard '*.go'))

.PHONY: test vet lint check fmt

test:
	$(GO) test $(PACKAGES)

vet:
	$(GO) vet $(PACKAGES)

# Dependency-free lint baseline. This can be replaced or extended with
# golangci-lint when the repository adds development tooling.
lint:
	@unformatted="$$(gofmt -l $(GO_FILES))"; \
	if [ -n "$$unformatted" ]; then \
		echo "Go files need formatting:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	$(GO) mod tidy -diff

fmt:
	gofmt -w $(GO_FILES)

check: lint vet test
