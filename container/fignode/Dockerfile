FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go/go.mod go/go.sum ./
COPY tap /tap
RUN go mod download

# Copy source
COPY go/ .

# Build Fignode with static linking
RUN CGO_ENABLED=0 GOOS=linux go build -o /fignode ./cmd/fignode/main.go

# Final stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /

COPY --from=builder /fignode /usr/local/bin/fignode

EXPOSE 8083

USER nobody

ENTRYPOINT ["fignode"]
