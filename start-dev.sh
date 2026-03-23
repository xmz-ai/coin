#!/bin/sh

set -eu

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Backend defaults for local development.
: "${HTTP_ADDR:=127.0.0.1:8080}"
: "${POSTGRES_DSN:=postgres://postgres:postgres@localhost:55432/coin_test?sslmode=disable}"
: "${LOCAL_KMS_KEY_V1:=dev_local_kms_key}"

# Frontend defaults.
: "${FRONTEND_PORT:=3000}"

node_major_version() {
  node -v 2>/dev/null | sed -n 's/^v\([0-9][0-9]*\).*/\1/p'
}

ensure_node_runtime() {
  major=""
  if command -v node >/dev/null 2>&1; then
    major="$(node_major_version || true)"
  fi

  if [ -n "${major}" ] && [ "${major}" -ge 18 ] 2>/dev/null; then
    return 0
  fi

  for c in \
    "$HOME/.nvm/versions/node/v22.14.0/bin" \
    "$HOME/.nvm/versions/node/v20.18.0/bin" \
    "$HOME/.nvm/versions/node/v18.18.0/bin" \
    "$HOME/.nvm/versions/node/v18.17.0/bin"
  do
    if [ -x "$c/node" ]; then
      PATH="$c:$PATH"
      export PATH
      major="$(node_major_version || true)"
      if [ -n "${major}" ] && [ "${major}" -ge 18 ] 2>/dev/null; then
        return 0
      fi
    fi
  done

  echo "[start-dev] Node.js >= 18 is required. Please install/activate Node 18+." >&2
  exit 1
}

ensure_node_runtime

if [ ! -d "$ROOT_DIR/web/admin/node_modules" ]; then
  echo "[start-dev] Missing web/admin/node_modules. Run 'cd web/admin && npm install' first." >&2
  exit 1
fi

backend_pid=""
frontend_pid=""

cleanup() {
  if [ -n "$backend_pid" ] && kill -0 "$backend_pid" 2>/dev/null; then
    kill "$backend_pid" 2>/dev/null || true
    wait "$backend_pid" 2>/dev/null || true
  fi
  if [ -n "$frontend_pid" ] && kill -0 "$frontend_pid" 2>/dev/null; then
    kill "$frontend_pid" 2>/dev/null || true
    wait "$frontend_pid" 2>/dev/null || true
  fi
}

trap cleanup EXIT INT TERM

(
  cd "$ROOT_DIR" || exit 1
  HTTP_ADDR="$HTTP_ADDR" \
  POSTGRES_DSN="$POSTGRES_DSN" \
  LOCAL_KMS_KEY_V1="$LOCAL_KMS_KEY_V1" \
  go run ./cmd/server
) &
backend_pid=$!

(
  cd "$ROOT_DIR/web/admin" || exit 1
  PORT="$FRONTEND_PORT" npm run dev
) &
frontend_pid=$!

echo "[start-dev] backend:  http://${HTTP_ADDR}"
echo "[start-dev] frontend: http://localhost:${FRONTEND_PORT}"
echo "[start-dev] press Ctrl+C to stop both."

exit_code=0
while :; do
  if ! kill -0 "$backend_pid" 2>/dev/null; then
    wait "$backend_pid" || exit_code=$?
    echo "[start-dev] backend exited." >&2
    break
  fi
  if ! kill -0 "$frontend_pid" 2>/dev/null; then
    wait "$frontend_pid" || exit_code=$?
    echo "[start-dev] frontend exited." >&2
    break
  fi
  sleep 1
done

exit "$exit_code"
