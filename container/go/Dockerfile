FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod files from go directory
COPY go/go.mod go/go.sum ./
RUN go mod download

# Copy source code from go directory
COPY go/main.go ./

# Build the binary
RUN go build -o /go-ingress main.go

# Final stage - minimal image
FROM alpine:latest

WORKDIR /

# Copy binary from builder
COPY --from=builder /go-ingress /go-ingress

EXPOSE 8080

CMD ["/go-ingress"]
