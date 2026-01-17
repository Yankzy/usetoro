#!/bin/zsh
set -e

# git pull origin staging --recurse-submodules --force
# Uncomment above in real prod

export PRODUCTION_SERVER=1
export DO_CD_SUDO_PASSWORD="${DO_CD_SUDO_PASSWORD}"

echo "Deploying to Production..."

# Run Docker Compose with Production Override
# We use 'make upd' which is defined in Makefile
make upd

# Fix Postgres Permissions
if [ -n "$DO_CD_SUDO_PASSWORD" ]; then
    echo "$DO_CD_SUDO_PASSWORD" | sudo -S chown -R 999:999 container/postgres/db_data
fi

# Ensure SSL
make ssl
