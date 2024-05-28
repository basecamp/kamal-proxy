FROM golang:1.22 as build
WORKDIR /app
COPY . .
RUN make

FROM ubuntu as base
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /app/bin/kamal-proxy /usr/local/bin/
EXPOSE 80 443

CMD [ "kamal-proxy", "run" ]
