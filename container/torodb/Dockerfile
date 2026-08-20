# Stage 1: Build the Go execution harness (ToroDB Stream Engine)
FROM golang:1.25 AS builder

WORKDIR /app
# Copy the Go workspace and source files
COPY go/go.mod go/go.sum ./
COPY tap ./tap
COPY go ./go

WORKDIR /app/go
# Build the ToroDB Stream Engine binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /go/bin/torodb ./cmd/torodb/main.go

# Stage 2: Bundle with AlloyDB Omni
FROM google/alloydbomni:17.7-ubi9

# We need to switch to root to install or copy files if needed, 
# but copying our binary usually works fine.
USER root

# Copy the compiled Go binary
COPY --from=builder /go/bin/torodb /usr/local/bin/torodb
RUN chmod +x /usr/local/bin/torodb

# Copy the custom entrypoint
COPY container/torodb/entrypoint.sh /usr/local/bin/torodb-entrypoint.sh
RUN chmod +x /usr/local/bin/torodb-entrypoint.sh

# Use the ToroDB entrypoint which will spawn both Postgres and the Go Harness
ENTRYPOINT ["/usr/local/bin/torodb-entrypoint.sh"]
