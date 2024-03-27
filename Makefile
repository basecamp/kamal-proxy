.PHONY: build test bench docker release

build:
	CGO_ENABLED=0 go build -o bin/ ./cmd/...

test:
	go test ./...

bench:
	go test -bench=. -benchmem -run=^# ./...

docker:
	docker build -t kamal-proxy .

soak:
	docker compose exec proxy kamal-proxy deploy main --target kamal-proxy-web-1:3000
	go run ./integration/soak \
		-run-duration=4h \
		-c 8 -rpm 600 \
		-deploy kamal-proxy-web-1:3000,kamal-proxy-web-2:3000,kamal-proxy-web-3:3000,kamal-proxy-web-4:3000 \
		-deploy-interval=30s \
		-url=http://localhost:8000/

release:
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--tag basecamp/kamal-proxy:latest \
		--label "org.opencontainers.image.title=kamal-proxy" \
		--push .
