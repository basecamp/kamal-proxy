from golang:1.23.2 as build
workdir /app
copy . .
env CGO_ENABLED=0
run go build -o maintenance main.go

from scratch as base
copy --from=build /app/maintenance /usr/local/bin/
expose 80

cmd [ "maintenance" ]
