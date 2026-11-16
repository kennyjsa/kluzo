# Single entry point for build and quality checks (docs/development.md).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build dist test test-integration lint arch vuln check clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/kluzo ./cmd/kluzo

dist:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/kluzo-$$os-$$arch ./cmd/kluzo || exit 1; \
	done

test:
	go test -race -count=1 ./...

test-integration:
	go test -tags integration -count=1 ./...

lint:
	go vet ./...
	golangci-lint run

arch:
	go test -count=1 ./internal/archtest/

vuln:
	govulncheck ./...

check: lint arch test

clean:
	rm -rf bin dist
