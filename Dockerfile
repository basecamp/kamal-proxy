from golang:1.23 as build
workdir /app
copy . .
run make

from ubuntu as base
copy --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
copy --from=build /app/bin/kamal-proxy /usr/local/bin/
copy --from=build /app/pages /usr/local/share/kamal-proxy/pages
expose 80 443

cmd [ "kamal-proxy", "run" ]
