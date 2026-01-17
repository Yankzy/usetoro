# Base Image
FROM python:3.11-slim

# Set environment variables
ENV PYTHONDONTWRITEBYTECODE 1
ENV PYTHONUNBUFFERED 1

WORKDIR /app

# Install system dependencies
RUN apt-get update && apt-get install -y \
    gcc \
    libpq-dev \
    supervisor \
    && rm -rf /var/lib/apt/lists/*

# Install Python dependencies
# We assume requirements.txt exists or we'll create a minimal one.
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy supervisord config
COPY container/django/supervisord.conf /etc/supervisor/conf.d/supervisord.conf

# Copy project
COPY . .

# Ensure entrypoint is executable
RUN chmod +x container/django/start

# Expose port
EXPOSE 8000

# Default command
CMD ["./container/django/start"]
