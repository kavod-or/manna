# syntax=docker/dockerfile:1

FROM golang:1.26.8-alpine AS build
WORKDIR /src
RUN apk upgrade --no-cache

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mana .

FROM alpine:3.22.5
RUN apk upgrade --no-cache \
    && addgroup -S mana \
    && adduser -S -G mana -u 10001 mana

WORKDIR /app
COPY --from=build /out/mana /usr/local/bin/mana
COPY content /app/content

USER mana
EXPOSE 8080

ENV PORT=8080
ENV CONTENT_DIR=/app/content

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/mana"]
