#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

GO_BIN="${GO_BIN:-}"
if [[ -z "$GO_BIN" ]]; then
  if [[ -x "/usr/local/go/bin/go" ]]; then
    GO_BIN="/usr/local/go/bin/go"
  else
    GO_BIN="go"
  fi
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "[pg-test] docker not found"
  exit 1
fi

CONTAINER_NAME="${COIN_TEST_PG_CONTAINER:-coin-test-postgres}"
PG_PORT="${COIN_TEST_PG_PORT:-55432}"
PG_USER="${COIN_TEST_PG_USER:-postgres}"
PG_PASSWORD="${COIN_TEST_PG_PASSWORD:-postgres}"
PG_DB="${COIN_TEST_PG_DB:-coin_test}"
PG_IMAGE="${COIN_TEST_PG_IMAGE:-postgres:16-alpine}"

started_here=0

if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
  if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
    docker rm -f "$CONTAINER_NAME" >/dev/null
  fi

  echo "[pg-test] starting postgres container: ${PG_IMAGE}"
  docker run -d --rm \
    --name "$CONTAINER_NAME" \
    -e POSTGRES_USER="$PG_USER" \
    -e POSTGRES_PASSWORD="$PG_PASSWORD" \
    -e POSTGRES_DB="$PG_DB" \
    -p "${PG_PORT}:5432" \
    "$PG_IMAGE" >/dev/null
  started_here=1
fi

cleanup() {
  if [[ "$started_here" == "1" ]]; then
    echo "[pg-test] stopping postgres container"
    docker stop "$CONTAINER_NAME" >/dev/null || true
  fi
}
trap cleanup EXIT

echo "[pg-test] waiting for postgres readiness"
for i in $(seq 1 60); do
  if docker exec "$CONTAINER_NAME" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  if [[ "$i" == "60" ]]; then
    echo "[pg-test] postgres not ready in time"
    exit 1
  fi
done

export COIN_TEST_POSTGRES_DSN="postgres://${PG_USER}:${PG_PASSWORD}@localhost:${PG_PORT}/${PG_DB}?sslmode=disable"
export GOCACHE="${ROOT_DIR}/.cache/go-build"
mkdir -p "$GOCACHE"

echo "[pg-test] running postgres integration tests"
"$GO_BIN" test -v ./tests/integration -run 'TestTC9001|TestTC9002|TestTC9003|TestTC9004|TestTC9005|TestTC9010|TestTC9011|TestTC9012|TestTC9013|TestTC9014|TestTC9015|TestTC9016|TestTC9017|TestTC9018|TestTC9019' -count=1

echo "[pg-test] done"
