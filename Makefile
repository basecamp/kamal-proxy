.PHONY: build test lint check bench docker

VERSION ?= $(shell git describe --tags --always --dirty)
LDFLAGS := -ldflags "-s -w -X 'github.com/basecamp/kamal-proxy/internal/version.Version=$(VERSION)'"

build:
	CGO_ENABLED=0 go build -trimpath $(LDFLAGS) -o bin/ ./cmd/...

test:
	go test ./...

lint:
	golangci-lint run

check:
	go vet ./...
	govulncheck ./...

bench:
	go test -bench=. -benchmem -run=^# ./...

docker:
	docker build -t kamal-proxy .
