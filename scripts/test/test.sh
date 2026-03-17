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
export GOCACHE

CONTAINER_NAME="${COIN_TEST_PG_CONTAINER:-coin-test-postgres}"
PG_PORT="${COIN_TEST_PG_PORT:-55432}"
PG_USER="${COIN_TEST_PG_USER:-postgres}"
PG_PASSWORD="${COIN_TEST_PG_PASSWORD:-postgres}"
PG_DB="${COIN_TEST_PG_DB:-coin_test}"
PG_IMAGE="${COIN_TEST_PG_IMAGE:-postgres:16-alpine}"

started_here=0

if [[ -z "${COIN_TEST_POSTGRES_DSN:-}" ]]; then
  if ! command -v docker >/dev/null 2>&1; then
    echo "[test] docker not found and COIN_TEST_POSTGRES_DSN not set"
    exit 1
  fi

  if ! docker ps --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
    if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
      docker rm -f "$CONTAINER_NAME" >/dev/null
    fi

    echo "[test] starting postgres container: ${PG_IMAGE}"
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
      echo "[test] stopping postgres container"
      docker stop "$CONTAINER_NAME" >/dev/null || true
    fi
  }
  trap cleanup EXIT

  echo "[test] waiting for postgres readiness"
  for i in $(seq 1 60); do
    if docker exec "$CONTAINER_NAME" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
      break
    fi
    sleep 1
    if [[ "$i" == "60" ]]; then
      echo "[test] postgres not ready in time"
      exit 1
    fi
  done

  export COIN_TEST_POSTGRES_DSN="postgres://${PG_USER}:${PG_PASSWORD}@localhost:${PG_PORT}/${PG_DB}?sslmode=disable"
else
  echo "[test] using COIN_TEST_POSTGRES_DSN from environment"
fi

if [[ "${COIN_SKIP_E2E_SMOKE:-0}" == "1" ]]; then
  echo "[test] COIN_SKIP_E2E_SMOKE=1, running full suite except tests/e2e"
  PKGS="$("$GO_BIN" list ./... | grep -v '/tests/e2e$')"
else
  echo "[test] running full suite"
  PKGS="$("$GO_BIN" list ./...)"
fi

# shellcheck disable=SC2086
"$GO_BIN" test -v $PKGS -count=1

echo "[test] done"
