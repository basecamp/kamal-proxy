FROM golang:1.22 as build
WORKDIR /app
COPY . .
RUN make

FROM ubuntu as base
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /app/bin/mproxy /usr/local/bin/

ENV HTTP_PORT=80
ENV HTTPS_PORT=443
ENV DEBUG=false

EXPOSE $HTTP_PORT $HTTPS_PORT

CMD mproxy run --http-port=${HTTP_PORT} --https-port=${HTTPS_PORT} --debug=${DEBUG}
