FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
COPY tap /tap

# Copy source code and vendor
COPY go/vendor ./vendor
COPY go/internal ./internal
COPY go/cmd/sync ./cmd/sync

# Build the API server
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -installsuffix cgo -o /sync ./cmd/sync/main.go

# Final stage
FROM alpine:latest

WORKDIR /

COPY --from=builder /sync /usr/local/bin/sync

# Copy config
COPY go/internal/config/defaults.yml /internal/config/defaults.yml

ENTRYPOINT ["sync"]
