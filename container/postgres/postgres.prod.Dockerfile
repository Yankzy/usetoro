FROM timescale/timescaledb:latest-pg18
COPY container/postgres/init-multiple-dbs.sh /docker-entrypoint-initdb.d/init-multiple-dbs.sh
RUN chmod +x /docker-entrypoint-initdb.d/init-multiple-dbs.sh
