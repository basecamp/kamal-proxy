FROM golang:1.22 as build
WORKDIR /app
COPY . .
RUN make

FROM ubuntu as base
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /app/bin/parachute /usr/local/bin/
EXPOSE 80 443

CMD [ "parachute", "run" ]
