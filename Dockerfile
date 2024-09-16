from golang:1.23 as build
workdir /app
copy . .
run make

from ubuntu as base
copy --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
copy --from=build /app/bin/kamal-proxy /usr/local/bin/
expose 80 443

run useradd kamal-proxy
run mkdir -p /home/kamal-proxy && chown kamal-proxy:kamal-proxy /home/kamal-proxy
user kamal-proxy:kamal-proxy

cmd [ "kamal-proxy", "run" ]
