#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ! -s "$PGDATA/PG_VERSION" ]]; then
  mkdir -p "$PGDATA"
  chown -R postgres:postgres "$PGDATA"
  gosu postgres initdb -D "$PGDATA" --auth-local=trust --auth-host=trust --no-locale --encoding=UTF8 >/dev/null
fi

gosu postgres pg_ctl -D "$PGDATA" -o "-c listen_addresses='' -c unix_socket_directories=/var/run/postgresql -c max_connections=100" -w start >/dev/null
if ! gosu postgres psql -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='tablekeeper'" | grep -q 1; then
  gosu postgres createdb tablekeeper
fi
until gosu postgres pg_isready -q -d tablekeeper; do sleep 0.1; done

/app/tablekeeper &
api_pid=$!
shutdown() {
  kill -TERM "$api_pid" 2>/dev/null || true
  wait "$api_pid" 2>/dev/null || true
  gosu postgres pg_ctl -D "$PGDATA" -m fast -w stop >/dev/null 2>&1 || true
}
trap shutdown TERM INT
set +e
wait "$api_pid"
status=$?
set -e
gosu postgres pg_ctl -D "$PGDATA" -m fast -w stop >/dev/null 2>&1 || true
exit "$status"
