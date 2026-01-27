FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
RUN go mod download

# Copy source
COPY go/ .

# Build Sync
RUN go build -o /sync ./cmd/sync/main.go

# Final stage
FROM alpine:latest

WORKDIR /

COPY --from=builder /sync /sync

CMD ["/sync"]
