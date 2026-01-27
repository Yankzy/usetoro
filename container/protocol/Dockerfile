# Stage 1: Build Go Brain
FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go/go.mod go/go.sum ./
RUN go mod download

COPY go/ .
RUN go build -o /protocol ./cmd/protocol/main.go

# Stage 2: Runtime (Python + Go Binary)
FROM python:3.11-slim

WORKDIR /app

# Install system dependencies if needed
# RUN apt-get update && apt-get install -y ...

# Copy Go Brain Binary
COPY --from=builder /protocol /protocol

# Setup Python Environment
COPY python-worker/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy Python Code
COPY python-worker/ .

# Copy Start Script
COPY container/brain/start.sh /start.sh
RUN chmod +x /start.sh

# Environment variables
ENV PYTHONUNBUFFERED=1

CMD ["/start.sh"]
