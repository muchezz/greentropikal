FROM golang:1.21 AS builder

WORKDIR /app

# Copy both go.mod and go.sum
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

COPY main.go .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o app main.go

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /app/app .
COPY index.html secrets.html secret_view.html ./

ENV PORT=8080 REDIS_URL=redis:6379

EXPOSE 8080

CMD ["./app"]