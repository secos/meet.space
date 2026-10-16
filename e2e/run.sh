#!/usr/bin/env bash
# Runs the Playwright suite against a server built from this checkout,
# using a dedicated e2e database (never the dev database). Called by
# `make e2e`.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT="${E2E_PORT:-8090}"
DB=meetspace_e2e
psql() { docker compose exec -T db psql -v ON_ERROR_STOP=1 -q -U meetspace -d postgres "$@"; }

export ENV=development
export DATABASE_URL="postgres://meetspace:devpassword@127.0.0.1:5432/${DB}?sslmode=disable"
export PLATFORM_NAME=meet.space
export PUBLIC_HOST="localhost:${PORT}"
export PUBLIC_BASE_URL="http://localhost:${PORT}"
export ORIGIN_HOST="origin.localhost:${PORT}"
export HOST_SOURCE=host
export APP_SECRET=e2e-only-secret-e2e-only-secret-0000
export SES_FROM_AUTH=login@auth.example.test
export SES_FROM_NOTIFY=notifications@mail.example.test
export LISTEN_ADDR="127.0.0.1:${PORT}"
export E2E_PORT="${PORT}"

if curl -s -o /dev/null "http://127.0.0.1:${PORT}/" 2>/dev/null; then
  echo "e2e: port ${PORT} is already in use; set E2E_PORT to a free port" >&2
  exit 1
fi

psql -c "DROP DATABASE IF EXISTS ${DB} WITH (FORCE)" -c "CREATE DATABASE ${DB}"
go build -o bin/server-e2e ./cmd/server
bin/server-e2e migrate up
bin/server-e2e seed-dev

mkdir -p e2e/test-results
bin/server-e2e >e2e/test-results/server.log 2>&1 &
PID=$!
cleanup() {
  kill "$PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null || true
  psql -c "DROP DATABASE IF EXISTS ${DB} WITH (FORCE)" || true
}
trap cleanup EXIT

for _ in $(seq 1 50); do
  if curl -fs -H "Host: ${ORIGIN_HOST}" "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then break; fi
  if ! kill -0 "$PID" 2>/dev/null; then cat e2e/test-results/server.log >&2; exit 1; fi
  sleep 0.2
done

cd e2e
if [ ! -d node_modules/@playwright/test ]; then npm install --no-audit --no-fund --silent; fi
npx playwright install chromium >/dev/null
npx playwright test "$@"
