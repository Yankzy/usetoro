# Build Stage
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy dependency files
# Context will be set to ../../go, so we copy from root of context
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -o toro-muscle main.go

# Runtime Stage
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/toro-muscle .

# Expose port
EXPOSE 8080

CMD ["./toro-muscle"]
