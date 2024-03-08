FROM golang:1.22 as build

WORKDIR /app
COPY . .
ENV CGO_ENABLED=0
RUN go build -o upstream main.go


FROM scratch as base

COPY --from=build /app/upstream /usr/local/bin/
EXPOSE 3000
CMD [ "upstream" ]
