# Repository Guidelines

## Project Structure & Module Organization
- `cmd/server/main.go`: service entrypoint and wiring.
- `cmd/perf-core-txn/main.go`: real-chain core transaction load test runner.
- `internal/api`: Gin routes, auth middleware, HTTP handlers.
- `internal/service`: application services (transfer, refund, async processor, query).
- `internal/domain`: core entities, state machine, and domain errors.
- `internal/db`: repository implementation, SQL queries, and generated sqlc code in `internal/db/sqlc`.
- `migrations`: PostgreSQL schema migrations (`*.up.sql` / `*.down.sql`).
- `tests`: `unit`, `integration`, and `e2e` suites.
- `docs`: source-of-truth specs (`API.md`, `domain.md`, `DDL.md`, `CODE_RULES.md`).

## Build, Test, and Development Commands
- `make test`: run full Go test suite (`go test ./...`).
- `make smoke`: run baseline smoke cases from `scripts/test/smoke.sh`.
- `make test-pg`: run PostgreSQL integration suite via Dockerized Postgres.
- `make sqlc`: regenerate typed DB access code from SQL (`sqlc.yaml`).
- `scripts/test/perf_core_txn_real.sh`: run real-chain load test (Gin + PostgreSQL + Redis).
- Example targeted run: `go test -v ./tests/integration -run 'TestTC1101' -count=1`.

## Coding Style & Naming Conventions
- Language: Go 1.20. Always format with `gofmt` (and `goimports` if used).
- Keep package names lowercase; exported symbols use `CamelCase`.
- Follow domain naming in docs: external identifiers use `merchant_no`, `customer_no`, `account_no`.
- Add migrations in pairs (`up`/`down`) and update sqlc artifacts when SQL changes.
- Keep API behavior aligned with `docs/API.md` and domain rules aligned with `docs/domain.md`.

## Testing Guidelines
- Framework: Go `testing` package; tests are organized by scenario.
- Naming pattern: `TestTC<case_id><Behavior>` (for example, `TestTC9017...`).
- Place tests by scope:
  - `tests/unit`: pure logic
  - `tests/integration`: repository/service flow
  - `tests/e2e`: script-level smoke
- In sandboxed agent environments, default to requesting escalated permissions before running test commands (`make test`, `go test`, `scripts/test/*.sh`) to avoid Docker socket and local port access failures.
- For PG tests, set `COIN_TEST_POSTGRES_DSN`; use `COIN_TEST_PG_KEEP_SCHEMA=1` to inspect schemas after run.
- For accounting changes, assert both balances and change logs.

## Commit & Pull Request Guidelines
- Current history is minimal (`Initial commit`), so no strict legacy format exists.
- Prefer concise Conventional Commit style, e.g. `feat: async transfer stage pipeline`.
- PRs should include:
  - purpose and scope
  - spec/doc updates (if API/domain changed)
  - migration impact
  - executed test commands and results
  - sample request/response for contract changes

## Security & Configuration Tips
- Never log or persist plaintext `merchant_secret`.
- Use environment variables for local config; avoid hardcoding DSNs or secrets.
