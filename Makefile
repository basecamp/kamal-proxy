.PHONY: build test bench docker

build:
	CGO_ENABLED=0 go build -trimpath -o bin/ ./cmd/...

test:
	go test -race ./...

bench:
	go test -bench=. -benchmem -run=^# ./...

docker:
	docker build -t kamal-proxy .
