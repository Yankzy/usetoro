# Stage 1: Build Go Brain
FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go/go.mod go/go.sum ./
COPY tap /tap
RUN go mod download

COPY go/ .
RUN go build -o /protocol ./cmd/protocol/main.go

# Stage 2: Runtime (Go Binary Only)
FROM alpine:latest

WORKDIR /

# Copy Go Brain Binary
COPY --from=builder /protocol /usr/local/bin/protocol

ENTRYPOINT ["protocol"]
