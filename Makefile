.PHONY: build test bench docker release

build:
	CGO_ENABLED=0 go build -o bin/ ./cmd/...

test:
	go test ./...

bench:
	go test -bench=. -benchmem -run=^# ./...

docker:
	docker build -t parachute .

release:
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--tag basecamp/parachute:latest \
		--label "org.opencontainers.image.title=parachute" \
		--push .
