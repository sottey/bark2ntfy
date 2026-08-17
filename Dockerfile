# syntax=docker/dockerfile:1

FROM golang:1.26.5-alpine AS build

WORKDIR /src

COPY go.mod ./
COPY main.go ./

RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/bark2ntfy .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
    && addgroup -S bark2ntfy \
    && adduser -S -D -H -G bark2ntfy bark2ntfy

COPY --from=build /out/bark2ntfy /usr/local/bin/bark2ntfy

USER bark2ntfy

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/bark2ntfy"]
