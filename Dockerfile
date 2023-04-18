FROM golang:1.20 as build

WORKDIR /app
COPY . .
RUN make


FROM scratch as base

COPY --from=build /app/bin/mproxy /usr/local/bin/
CMD [ "mproxy", "-p", "80", "run" ]
