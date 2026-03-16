FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
COPY tap /tap

# Copy source code and vendor
COPY go/ .

# Build the GraphQL server
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -installsuffix cgo -o /graphql ./cmd/graphql/server.go

# Final stage
FROM alpine:3.19

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

WORKDIR /

COPY --from=builder /graphql /usr/local/bin/graphql

EXPOSE 8080

# Run as non-root user
USER nobody

ENTRYPOINT ["graphql"]
