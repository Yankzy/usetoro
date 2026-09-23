#!/bin/bash
set -eo pipefail

echo "==> [ledger] Initializing Production Django Container..."

# Ensure runtime directories exist
mkdir -p /run/supervisor /var/log/supervisor /app/.logs /app/staticfiles

# Default environment variables
export GUNICORN_WORKERS=${GUNICORN_WORKERS:-4}
export GUNICORN_THREADS=${GUNICORN_THREADS:-2}
export GUNICORN_TIMEOUT=${GUNICORN_TIMEOUT:-120}
export DJANGO_SETTINGS_MODULE=${DJANGO_SETTINGS_MODULE:-config.settings}
export PYTHONPATH="/app:/app/ledger:${PYTHONPATH}"

# Collect static files
echo "==> [ledger] Collecting static files..."
python /app/ledger/manage.py collectstatic --noinput || {
    echo "Warning: collectstatic reported an issue, continuing startup..."
}

# Optional DB migration check
if [ "${SKIP_MIGRATIONS:-0}" != "1" ]; then
    echo "==> [ledger] Running database migrations if needed..."
    python /app/ledger/manage.py migrate --noinput || {
        echo "Warning: Database migration failed or database is not yet ready, continuing..."
    }
fi

echo "==> [ledger] Starting Supervisor..."
exec /usr/bin/supervisord -n -c /etc/supervisor/conf.d/supervisord.conf
