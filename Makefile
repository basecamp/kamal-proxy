.PHONY: build test bench docker release

build:
	CGO_ENABLED=0 go build -trimpath -o bin/ ./cmd/...

test:
	go test ./...

bench:
	go test -bench=. -benchmem -run=^# ./...

docker:
	docker build -t kamal-proxy .

release:
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--tag basecamp/kamal-proxy:latest \
		--label "org.opencontainers.image.title=kamal-proxy" \
		--push .
