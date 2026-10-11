FROM golang:1.27.2 AS build

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN make build VERSION=$VERSION

FROM ubuntu:noble-20251013 AS base

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /app/bin/kamal-proxy /usr/local/bin/

EXPOSE 80 443

RUN useradd kamal-proxy \
    && mkdir -p /home/kamal-proxy/.config/kamal-proxy \
    && chown -R kamal-proxy:kamal-proxy /home/kamal-proxy

USER kamal-proxy:kamal-proxy

CMD ["kamal-proxy", "run"]
