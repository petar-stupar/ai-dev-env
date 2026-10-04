VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
SHELL_SCRIPTS := $(strip $(wildcard internal/stacks/*/entrypoint.d/*.sh) $(wildcard internal/stacks/*/files/*.sh))

.DEFAULT_GOAL := build
.PHONY: build test vet fmt-check lint golden integration install clean

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o aide ./cmd/aide

test:
	go test ./...

vet:
	go vet ./...

fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

lint: fmt-check vet
	@if command -v shellcheck >/dev/null 2>&1; then \
		if [ -n "$(SHELL_SCRIPTS)" ]; then shellcheck $(SHELL_SCRIPTS); fi; \
	else \
		echo "shellcheck not installed; skipping shell script lint"; \
	fi

golden:
	go test ./internal/stacks -run Golden -update

integration:
	AIDE_INTEGRATION=1 go test ./internal/integration -v -timeout 30m

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/aide

clean:
	rm -f aide
