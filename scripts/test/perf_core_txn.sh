#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

GO_BIN="${GO_BIN:-}"
if [[ -z "$GO_BIN" ]]; then
  if [[ -x "/usr/local/go/bin/go" ]]; then
    GO_BIN="/usr/local/go/bin/go"
  else
    GO_BIN="go"
  fi
fi

BENCH_PATTERN="${BENCH_PATTERN:-BenchmarkCoreTxn}"
BENCH_TIME="${BENCH_TIME:-2s}"
BENCH_COUNT="${BENCH_COUNT:-3}"
BENCH_CPU="${BENCH_CPU:-1,2,4}"

GOCACHE="${GOCACHE:-${ROOT_DIR}/.cache/go-build}"
mkdir -p "$GOCACHE"

cd "$ROOT_DIR"

echo "[perf-core-txn] go=$GO_BIN pattern=$BENCH_PATTERN benchtime=$BENCH_TIME count=$BENCH_COUNT cpu=$BENCH_CPU"
GOCACHE="$GOCACHE" "$GO_BIN" test ./tests/integration \
  -run '^$' \
  -bench "$BENCH_PATTERN" \
  -benchmem \
  -benchtime "$BENCH_TIME" \
  -count "$BENCH_COUNT" \
  -cpu "$BENCH_CPU"

echo "[perf-core-txn] done"
