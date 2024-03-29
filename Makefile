.PHONY: build test bench docker

build:
	CGO_ENABLED=0 go build -o bin/ ./cmd/...

test:
	go test ./...

bench:
	go test -bench=. -benchmem -run=^# ./...

docker:
	docker build -t parachute .
