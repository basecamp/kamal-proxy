FROM golang:1.22 as build

WORKDIR /app
COPY . .
RUN make


FROM ubuntu as base

COPY --from=build /app/bin/mproxy /usr/local/bin/
EXPOSE 80
CMD [ "mproxy", "run" ]
