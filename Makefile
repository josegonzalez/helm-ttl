.PHONY: build test cover lint clean install

BINARY_NAME=helm-ttl
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/helm-ttl

test:
	go test -v -race ./...

cover:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	@echo ""
	@echo "Checking for 95% coverage..."
	@COVERAGE=$$(go tool cover -func=coverage.out | grep "^total:" | awk '{print $$3}' | sed 's/%//'); \
	if [ $$(echo "$$COVERAGE < 95" | bc) -eq 1 ]; then \
		echo "Coverage is below 95%! Current: $$COVERAGE%"; \
		exit 1; \
	fi; \
	echo "Coverage check passed: $$COVERAGE%"

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/ coverage.out

install: build
	mkdir -p $(HELM_PLUGIN_DIR)/bin
	cp bin/$(BINARY_NAME) $(HELM_PLUGIN_DIR)/bin/
