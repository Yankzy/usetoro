FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
RUN go mod download

# Copy source
COPY go/ .

# Build Gate with static linking
RUN CGO_ENABLED=0 GOOS=linux go build -o /gate ./cmd/gate/main.go

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

