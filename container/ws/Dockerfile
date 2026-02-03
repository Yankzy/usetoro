# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
RUN go mod download

# Copy source code
COPY go/ .

# Build the WebSocket server
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /ws ./cmd/ws

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
