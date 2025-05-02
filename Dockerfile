FROM golang:1.24.2-alpine3.21 AS build

RUN apk --no-cache upgrade
RUN apk --no-cache add tzdata ca-certificates
RUN apk --no-cache add make

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN make


FROM alpine:3.21 AS app

RUN apk --no-cache upgrade
RUN apk --no-cache add tzdata ca-certificates

COPY --from=build /app/bin/kamal-proxy /usr/local/bin/

EXPOSE 80 443

RUN adduser -D kamal-proxy \
    && mkdir -p /home/kamal-proxy/.config/kamal-proxy \
    && chown -R kamal-proxy:kamal-proxy /home/kamal-proxy

USER kamal-proxy:kamal-proxy

WORKDIR /home/kamal-proxy

CMD ["kamal-proxy", "run"]
