FROM golang:1.24-alpine AS builder

WORKDIR /src

# Dependencies first so the module layer is cached across source changes.
COPY go.mod go.sum ./
RUN go mod download

# The web directory is embedded into the binary via go:embed, so it must be
# present at build time — nothing is read from disk at runtime.
COPY *.go ./
COPY web ./web

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/njiralab .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget \
 && adduser -D -H -u 10001 njira

COPY --from=builder /out/njiralab /usr/local/bin/njiralab

USER njira
EXPOSE 8080

ENV PORT=8080 REDIS_URL=redis:6379

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null || exit 1

ENTRYPOINT ["njiralab"]
