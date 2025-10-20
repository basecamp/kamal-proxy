from golang:1.25.3 as build
workdir /app
copy . .
env CGO_ENABLED=0
run go build -o upstream main.go

from scratch as base
copy --from=build /app/upstream /usr/local/bin/
expose 80

cmd [ "upstream" ]
