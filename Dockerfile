# syntax=docker/dockerfile:1

FROM golang:1.26.8-alpine AS build
WORKDIR /src
RUN apk upgrade --no-cache

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/manna .

FROM alpine:3.22.5
RUN apk upgrade --no-cache \
    && addgroup -S manna \
    && adduser -S -G manna -u 10001 manna

WORKDIR /app
COPY --from=build /out/manna /usr/local/bin/manna
COPY content /app/content

USER manna
EXPOSE 8080

ENV PORT=8080
ENV CONTENT_DIR=/app/content

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/manna"]
