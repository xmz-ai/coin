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

GOCACHE="${GOCACHE:-${ROOT_DIR}/.cache/go-build}"
mkdir -p "$GOCACHE"

PERF_USE_DOCKER="${PERF_USE_DOCKER:-1}"

PG_CONTAINER="${COIN_PERF_PG_CONTAINER:-coin-perf-pg}"
REDIS_CONTAINER="${COIN_PERF_REDIS_CONTAINER:-coin-perf-redis}"

PG_PORT="${COIN_PERF_PG_PORT:-55432}"
PG_USER="${COIN_PERF_PG_USER:-postgres}"
PG_PASSWORD="${COIN_PERF_PG_PASSWORD:-postgres}"
PG_DB="${COIN_PERF_PG_DB:-coin_perf}"
PG_IMAGE="${COIN_PERF_PG_IMAGE:-postgres:16-alpine}"

REDIS_PORT="${COIN_PERF_REDIS_PORT:-56379}"
REDIS_IMAGE="${COIN_PERF_REDIS_IMAGE:-redis:7-alpine}"

started_pg=0
started_redis=0

if [[ "$PERF_USE_DOCKER" == "1" ]]; then
  if ! command -v docker >/dev/null 2>&1; then
    echo "[perf-core-txn-real] docker not found"
    exit 1
  fi

  if ! docker ps --format '{{.Names}}' | grep -q "^${PG_CONTAINER}$"; then
    if docker ps -a --format '{{.Names}}' | grep -q "^${PG_CONTAINER}$"; then
      docker rm -f "$PG_CONTAINER" >/dev/null
    fi
    echo "[perf-core-txn-real] starting postgres container: ${PG_IMAGE}"
    docker run -d --rm \
      --name "$PG_CONTAINER" \
      -e POSTGRES_USER="$PG_USER" \
      -e POSTGRES_PASSWORD="$PG_PASSWORD" \
      -e POSTGRES_DB="$PG_DB" \
      -p "${PG_PORT}:5432" \
      "$PG_IMAGE" >/dev/null
    started_pg=1
  fi

  if ! docker ps --format '{{.Names}}' | grep -q "^${REDIS_CONTAINER}$"; then
    if docker ps -a --format '{{.Names}}' | grep -q "^${REDIS_CONTAINER}$"; then
      docker rm -f "$REDIS_CONTAINER" >/dev/null
    fi
    echo "[perf-core-txn-real] starting redis container: ${REDIS_IMAGE}"
    docker run -d --rm \
      --name "$REDIS_CONTAINER" \
      -p "${REDIS_PORT}:6379" \
      "$REDIS_IMAGE" >/dev/null
    started_redis=1
  fi

  cleanup() {
    if [[ "$started_pg" == "1" ]]; then
      echo "[perf-core-txn-real] stopping postgres container"
      docker stop "$PG_CONTAINER" >/dev/null || true
    fi
    if [[ "$started_redis" == "1" ]]; then
      echo "[perf-core-txn-real] stopping redis container"
      docker stop "$REDIS_CONTAINER" >/dev/null || true
    fi
  }
  trap cleanup EXIT

  echo "[perf-core-txn-real] waiting for postgres readiness"
  for i in $(seq 1 60); do
    if docker exec "$PG_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
      break
    fi
    sleep 1
    if [[ "$i" == "60" ]]; then
      echo "[perf-core-txn-real] postgres not ready in time"
      exit 1
    fi
  done

  echo "[perf-core-txn-real] waiting for redis readiness"
  for i in $(seq 1 60); do
    if docker exec "$REDIS_CONTAINER" redis-cli ping >/dev/null 2>&1; then
      break
    fi
    sleep 1
    if [[ "$i" == "60" ]]; then
      echo "[perf-core-txn-real] redis not ready in time"
      exit 1
    fi
  done

  export POSTGRES_DSN="${POSTGRES_DSN:-postgres://${PG_USER}:${PG_PASSWORD}@localhost:${PG_PORT}/${PG_DB}?sslmode=disable}"
  export REDIS_ADDR="${REDIS_ADDR:-localhost:${REDIS_PORT}}"
fi

export LOCAL_KMS_KEY_V1="${LOCAL_KMS_KEY_V1:-perf_local_kms_key}"
export GIN_MODE="${GIN_MODE:-release}"

export PERF_DURATION_SECONDS="${PERF_DURATION_SECONDS:-30}"
export PERF_CONCURRENCY="${PERF_CONCURRENCY:-50}"
export PERF_WARMUP="${PERF_WARMUP:-200}"
export PERF_REQUEST_TIMEOUT_MS="${PERF_REQUEST_TIMEOUT_MS:-3000}"
export PERF_MAX_BODY_BYTES="${PERF_MAX_BODY_BYTES:-1048576}"

printf '[perf-core-txn-real] go=%s duration=%ss concurrency=%s warmup=%s timeout_ms=%s\n' \
  "$GO_BIN" "$PERF_DURATION_SECONDS" "$PERF_CONCURRENCY" "$PERF_WARMUP" "$PERF_REQUEST_TIMEOUT_MS"

GOCACHE="$GOCACHE" "$GO_BIN" run ./cmd/perf-core-txn

echo "[perf-core-txn-real] done"
