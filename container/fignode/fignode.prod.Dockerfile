FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
COPY tap /tap

# Copy source code and vendor
COPY go/vendor ./vendor
COPY go/internal ./internal
COPY go/cmd/fignode ./cmd/fignode

# Build the API server
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -a -installsuffix cgo -o /fignode ./cmd/fignode/main.go

# Final stage
FROM alpine:3.19 AS production

RUN apk --no-cache add ca-certificates

WORKDIR /

COPY --from=builder /fignode /usr/local/bin/fignode

EXPOSE 8083

USER nobody

ENTRYPOINT ["fignode"]

COPY --chown=nobody:nobody keys /keys

FROM golang:1.25-alpine AS development

RUN apk add --no-cache ca-certificates git \
    && go install github.com/air-verse/air@v1.63.0

WORKDIR /workspace
