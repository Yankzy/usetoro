FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
COPY tap /tap

# Copy source code and vendor
COPY go/ .

# Build the runner executable
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -installsuffix cgo -o /agents-runner ./cmd/agents-runner/main.go

# Final stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /agents-runner /app/agents-runner
COPY go/internal/config/defaults.yaml /app/internal/config/defaults.yaml

USER nobody

ENTRYPOINT ["/app/agents-runner"]
