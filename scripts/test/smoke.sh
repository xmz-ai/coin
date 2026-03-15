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

echo "[smoke] run baseline suite (TC-0001/TC-0002/TC-0003 + iter2/iter3 key cases)"
GOCACHE="$GOCACHE" "$GO_BIN" test -v ./tests/integration -run 'TestTC0001FixtureFactoryCreatesLinkedData|TestTC2001MerchantOnboardingCreatesBudgetAndReceivableAccounts|TestTC2004CustomerUniqueOnMerchantAndOutUserID|TestTC3001DuplicateOutTradeNoReturnsConflict|TestTC3002DuplicateRequestHasNoSideEffects|TestS0SmokeSuiteCoverage' -count=1
GOCACHE="$GOCACHE" "$GO_BIN" test -v ./tests/unit -run TestTC0002ClockAndUUIDInjection -count=1

if [[ "${COIN_SKIP_E2E_SMOKE:-0}" != "1" ]]; then
  GOCACHE="$GOCACHE" "$GO_BIN" test -v ./tests/e2e -run TestTC0003SmokeScriptExecutable -count=1
else
  echo "[smoke] skip e2e recursion guard enabled"
fi

echo "[smoke] done"
