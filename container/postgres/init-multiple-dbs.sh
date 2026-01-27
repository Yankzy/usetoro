#!/bin/bash
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
	CREATE DATABASE svix;
	GRANT ALL PRIVILEGES ON DATABASE svix TO toro;
EOSQL
