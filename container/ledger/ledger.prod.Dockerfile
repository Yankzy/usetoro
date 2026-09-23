# Production Dockerfile for Ledger (Django managed by Supervisor)
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    IN_DOCKER=1 \
    PYTHONPATH="/app:/app/ledger" \
    DJANGO_SETTINGS_MODULE="config.settings"

WORKDIR /app

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    supervisor \
    gcc \
    libpq-dev \
    curl \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Install Python production dependencies
COPY container/ledger/requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir --upgrade pip && \
    pip install --no-cache-dir -r /app/requirements.txt

# Copy supervisor config and entrypoint
COPY container/ledger/supervisord.conf /etc/supervisor/conf.d/supervisord.conf
COPY container/ledger/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Create required runtime directories
RUN mkdir -p /run/supervisor /var/log/supervisor /app/.logs /app/staticfiles

# Copy application source code
COPY ledger /app/ledger

EXPOSE 8000

# HEALTHCHECK --interval=10s --timeout=5s --start-period=30s --retries=3 \
#     CMD curl -f http://127.0.0.1:8000/healthz || exit 1

ENTRYPOINT ["/entrypoint.sh"]
