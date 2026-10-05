# syntax=docker/dockerfile:1

FROM gcr.io/distroless/static-debian13@sha256:58133991db06659feaabe0f4e97a35cebf15ef4ea08f8a4c6d2ee5f75e4aa6a0 AS runtime

FROM --platform=$BUILDPLATFORM golang:1.27.1 AS build

WORKDIR /app

COPY go.mod go.sum ./

RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH make build VERSION=$VERSION

COPY --from=runtime /etc/passwd /etc/group /rootfs/etc/

RUN echo "kamal-proxy:x:1001:1001::/home/kamal-proxy:/sbin/nologin" >> /rootfs/etc/passwd \
    && echo "kamal-proxy:x:1001:" >> /rootfs/etc/group \
    && mkdir -p /rootfs/home/kamal-proxy/.config/kamal-proxy

FROM runtime

COPY --link --from=build /rootfs/etc/passwd /rootfs/etc/group /etc/
COPY --link --from=build --chown=1001:1001 /rootfs/home /home
COPY --link --from=build /app/bin/kamal-proxy /usr/local/bin/

EXPOSE 80 443

USER 1001:1001

CMD ["kamal-proxy", "run"]
