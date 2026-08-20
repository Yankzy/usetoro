FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
COPY tap /tap

# Copy source code and vendor
COPY go/vendor ./vendor
COPY go/internal ./internal
COPY go/graph ./graph
COPY go/cmd/gate ./cmd/gate

# Build the API server
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -installsuffix cgo -o /gate ./cmd/gate/main.go

# Final stage
FROM alpine:3.19

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

WORKDIR /

COPY --from=builder /gate /usr/local/bin/gate

EXPOSE 8080

# Run as non-root user
USER nobody

ENTRYPOINT ["gate"]


COPY --chown=nobody:nobody keys /keys
