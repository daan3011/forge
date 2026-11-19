GOLANGCI_VERSION := v2.15.0
GOLANGCI := $(CURDIR)/bin/golangci-lint

.PHONY: build run test lint fmt clean

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
	rm -rf bin

$(GOLANGCI):
	GOBIN=$(CURDIR)/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
