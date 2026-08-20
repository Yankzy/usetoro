# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
COPY tap /tap

# Copy source code and vendor directory
COPY go/vendor ./vendor
COPY go/internal ./internal
COPY go/cmd/ws ./cmd/ws

# Build the WebSocket server using the vendor folder
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -installsuffix cgo -o /ws ./cmd/ws

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /

# Copy the binary from builder
COPY --from=builder /ws /usr/local/bin/ws

# Expose WebSocket port
EXPOSE 8080

# Run as non-root user
USER nobody

ENTRYPOINT ["ws"]

COPY --chown=nobody:nobody keys /keys
