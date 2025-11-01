GO ?= go
PKG_DIRS := ./cmd/... ./internal/...
TEST_PKGS := ./...
BIN := bin/vs-exporter

.PHONY: help fmt vet tidy test coverage build run clean all

help:
	@echo "Available targets:"
	@echo "  fmt       - Run gofmt/go fmt on project packages"
	@echo "  vet       - Run go vet on project packages"
	@echo "  tidy      - Ensure go.mod/go.sum are tidy"
	@echo "  test      - Execute unit tests"
	@echo "  coverage  - Run tests with coverage report"
	@echo "  build     - Build vs-exporter binary"
	@echo "  run       - Build and execute vs-exporter with config.yaml"
	@echo "  clean     - Remove build artifacts"
	@echo "  all       - fmt + vet + test"

fmt:
	$(GO) fmt $(PKG_DIRS)
	$(GO) run golang.org/x/tools/cmd/goimports@latest -w $$(find cmd internal -name '*.go')

vet:
	$(GO) vet $(PKG_DIRS)

tidy:
	$(GO) mod tidy

test:
	$(GO) test $(TEST_PKGS)

coverage:
	$(GO) test -coverprofile=coverage.out $(TEST_PKGS)
	$(GO) tool cover -func=coverage.out

build:
	$(GO) build -o $(BIN) ./cmd/vs-exporter

run: build
	./$(BIN) --config=config.yaml

clean:
	rm -rf $(BIN) coverage.out

all: fmt vet test
