# Stage 1: Build Go Brain
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
COPY tap /tap

# Copy source code and vendor
COPY go/ .

# Build the protocol server
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -installsuffix cgo -o /protocol ./cmd/protocol/main.go

# Stage 2: Runtime (Go Binary Only)
FROM alpine:latest

WORKDIR /

# Copy Go Brain Binary
COPY --from=builder /protocol /usr/local/bin/protocol

ENTRYPOINT ["protocol"]
