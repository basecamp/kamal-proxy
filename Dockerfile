from golang:1.23 as build
workdir /app
copy . .
run make

from ubuntu as base
copy --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
copy --from=build /app/bin/kamal-proxy /usr/local/bin/
expose 80 443

run useradd kamal
run mkdir -p /home/kamal && chown kamal:kamal /home/kamal
user kamal:kamal

cmd [ "kamal-proxy", "run" ]
