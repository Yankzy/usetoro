# Stage 1: Build Go Brain
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
COPY tap /tap

# Copy source code and vendor
COPY go/vendor ./vendor
COPY go/internal ./internal
COPY go/cmd/protocol ./cmd/protocol

# Build the protocol server
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -installsuffix cgo -o /protocol ./cmd/protocol/main.go

# Stage 2: Runtime (Go Binary Only)
FROM alpine:3.19 AS production

RUN apk --no-cache add ca-certificates

WORKDIR /app

# Copy Go Brain Binary
COPY --from=builder /protocol /usr/local/bin/protocol
COPY go/internal/config/defaults.yml /app/internal/config/defaults.yml
COPY go/internal/erp/ase/dags /app/internal/erp/ase/dags
COPY --from=builder /tap/workflows /app/tap/workflows

USER nobody

ENTRYPOINT ["protocol"]

COPY --chown=nobody:nobody keys /keys
COPY --chown=nobody:nobody tap/workflows /app/tap/workflows
COPY --chown=nobody:nobody go/internal/config/defaults.yml /app/internal/config/defaults.yml
COPY --chown=nobody:nobody go/internal/erp/ase/dags /app/internal/erp/ase/dags

FROM golang:1.25-alpine AS development

RUN apk add --no-cache ca-certificates git \
    && go install github.com/air-verse/air@v1.63.0

WORKDIR /workspace
