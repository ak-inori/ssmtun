BIN     := ssmtun
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/ak-inori/ssmtun/internal/cli.Version=$(VERSION)

.PHONY: build install test lint fmt clean

build:
	go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/ssmtun

install:
	go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/ssmtun

test:
	go test ./...

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo 'gofmt: files need formatting'; exit 1; }

fmt:
	gofmt -w .

clean:
	rm -rf $(BIN) dist
