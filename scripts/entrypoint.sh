#!/bin/sh
# maskani-api entrypoint: run migrations (fatal on failure, retried while the database starts),
# then the idempotent seed (non-fatal), then the server.
set -e

MIGRATE_URL="${POSTGRES_MIGRATE_URL:-$POSTGRES_URL}"

echo "maskani-api: running migrations"
attempt=1
until POSTGRES_URL="$MIGRATE_URL" /usr/local/bin/maskani-migrate; do
  if [ "$attempt" -ge 60 ]; then
    echo "maskani-api: migrations failed after $attempt attempts"
    exit 1
  fi
  echo "maskani-api: migration attempt $attempt failed, retrying in 5s"
  attempt=$((attempt + 1))
  sleep 5
done

echo "maskani-api: running seed"
POSTGRES_URL="$MIGRATE_URL" /usr/local/bin/maskani-seed || echo "maskani-api: seed finished with warnings (non-fatal)"

echo "maskani-api: starting server"
exec /usr/local/bin/maskani-api
