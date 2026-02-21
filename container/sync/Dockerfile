FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
COPY tap /tap
RUN go mod download

# Copy source
COPY go/ .

# Build Sync
RUN go build -o /sync ./cmd/sync/main.go

# Final stage
FROM alpine:latest

WORKDIR /

COPY --from=builder /sync /usr/local/bin/sync

# Copy config
COPY go/internal/config/defaults.yaml /internal/config/defaults.yaml

ENTRYPOINT ["sync"]
