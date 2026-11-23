GOLANGCI_VERSION := v2.15.0
GOLANGCI := $(CURDIR)/bin/golangci-lint

.PHONY: build run test lint fmt clean release-snapshot release-check shellcheck gif secrets

build:
	go build -o bin/forge ./cmd/forge

run: build
	./bin/forge

test:
	go test -race ./...

lint: $(GOLANGCI)
	$(GOLANGCI) run

fmt: $(GOLANGCI)
	$(GOLANGCI) fmt

clean:
	rm -rf bin dist

# Builds every release archive into dist/ without publishing.
release-snapshot:
	goreleaser release --snapshot --clean

release-check:
	goreleaser check

shellcheck:
	shellcheck install.sh

# Scans the whole git history for secrets (run before tagging a release).
secrets:
	gitleaks git --redact .

# Records the README GIF from `forge demo app` (fixture data only).
gif: build
	PATH="$(CURDIR)/bin:$$PATH" vhs docs/demo.tape

$(GOLANGCI):
	GOBIN=$(CURDIR)/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
