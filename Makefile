.PHONY: build test test-race bench docker

build:
	CGO_ENABLED=0 go build -trimpath -o bin/ ./cmd/...

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	golangci-lint run

bench:
	go test -bench=. -benchmem -run=^# ./...

docker:
	docker build -t kamal-proxy .
