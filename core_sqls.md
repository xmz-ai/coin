# Core Transaction SQL 清单（按交易类别）

## 数据来源

- 运行命令（2026-03-20）：
  - `COIN_SQL_DEBUG_TRACE=1 COIN_SQL_DEBUG_SLOW_MS=1 PERF_REQUESTS=1 PERF_CONCURRENCY=1 PERF_WARMUP=0 scripts/test/perf_core_txn_real.sh > /tmp/perf_sql_debug.log 2>&1`
- SQL 耗时日志：
  - `/tmp/perf_sql_debug.log`
- 说明：
  - 本次 `perf` 的 tracer 只会采样首个 `forward`（book_transfer）和首个 `reverse`（book_refund）交易。
  - `无 book` 两类流程的耗时，使用同一条 SQL 在本次采样中的“参考耗时”标注。
  - 下文统计的是成功主路径（不含失败分支/补偿分支）。

## Query 与 SQL 对照

| Query | SQL |
| --- | --- |
| `CreateTransferTxn` | `INSERT INTO txn (...) VALUES (...)` |
| `GetTransferTxnByNo` | `SELECT ... FROM txn WHERE txn_no = ... LIMIT 1` |
| `GetTransferTxnStageForUpdate` | `SELECT ... FROM txn WHERE txn_no = ... FOR UPDATE LIMIT 1` |
| `TryDebitAccountBalanceNonBook` | `UPDATE account SET balance = balance - ? ... RETURNING account_no,balance` |
| `TryCreditAccountBalanceNonBook` | `UPDATE account SET balance = balance + ? ... RETURNING account_no,balance` |
| `DecreaseOriginTxnRefundableIfValid` | `UPDATE txn SET refundable_amount = refundable_amount - ? ... RETURNING ...` |
| `UpdateTransferTxnParties` | `UPDATE txn SET debit_account_no=?, credit_account_no=? ...` |
| `GetAccountForUpdateByNo` | `SELECT ... FROM account WHERE account_no = ? FOR UPDATE LIMIT 1` |
| `ListAvailableAccountBooksForUpdate` | `SELECT ... FROM account_book WHERE account_no=? AND expire_at>today AND balance>0 ORDER BY expire_at,book_no` |
| `BatchUpdateAccountBookBalances` | `WITH payload AS (...) UPDATE account_book ... RETURNING book_no,balance` |
| `UpsertAccountBookBalance` | `INSERT INTO account_book (...) ON CONFLICT(account_no,expire_at) DO UPDATE ... RETURNING ...` |
| `UpdateAccountBalance` | `UPDATE account SET balance=?, updated_at=NOW() WHERE account_no=?` |
| `InsertAccountChange` | `INSERT INTO account_change_log (txn_no, account_no, delta, balance_after) VALUES (...)` |
| `InsertAccountBookChange` | `INSERT INTO account_book_change_log (txn_no, account_no, book_no, delta, balance_after, expire_at) VALUES (...)` |
| `UpdateTransferTxnStatus` | `UPDATE txn SET status=?, error_code=?, error_msg=?, updated_at=NOW() WHERE txn_no=?` |
| `InsertOutboxEvent` | `INSERT INTO outbox_event (...) VALUES (...) ON CONFLICT DO NOTHING` |
| `raw_sql:commit` | `COMMIT` |

## 无 book 转账（transfer）

> SQL 顺序按代码路径（`ApplyTxnStage` + `applyAccountDebitTx` + `applyAccountCreditTx`）列出；耗时为本次采样的同 SQL 参考值。

| # | 阶段 | Query | 参考耗时(ms) | 来源 |
| --- | --- | --- | ---: | --- |
| 1 | submit | `CreateTransferTxn` | 2.185 | forward(book_transfer) |
| 2 | init | `GetTransferTxnStageForUpdate` | 0.468 | forward(book_transfer) |
| 3 | init | `TryDebitAccountBalanceNonBook` | 0.368 | forward(book_transfer) |
| 4 | init | `InsertAccountChange` | 0.459 | forward(book_transfer) |
| 5 | init | `UpdateTransferTxnStatus` | 0.315 | forward(book_transfer) |
| 6 | init | `raw_sql:commit` | 0.677 | forward(book_transfer) |
| 7 | pay_success | `GetTransferTxnStageForUpdate` | 0.126 | forward(book_transfer) |
| 8 | pay_success | `TryCreditAccountBalanceNonBook` | 0.213 | reverse(book_refund) |
| 9 | pay_success | `InsertAccountChange` | 0.087 | reverse(book_refund) |
| 10 | pay_success | `UpdateTransferTxnStatus` | 0.136 | reverse(book_refund) |
| 11 | pay_success | `InsertOutboxEvent` | 0.228 | reverse(book_refund) |
| 12 | pay_success | `raw_sql:commit` | 0.549 | reverse(book_refund) |

## 无 book 退款（refund）

> SQL 顺序按代码路径（`ApplyTxnStage` 退款分支）列出；耗时为本次采样的同 SQL 参考值。

| # | 阶段 | Query | 参考耗时(ms) | 来源 |
| --- | --- | --- | ---: | --- |
| 1 | submit | `CreateTransferTxn` | 0.658 | reverse(book_refund) |
| 2 | route_key | `GetTransferTxnByNo` | 0.329 | reverse(book_refund) |
| 3 | init | `GetTransferTxnStageForUpdate` | 0.225 | reverse(book_refund) |
| 4 | init | `DecreaseOriginTxnRefundableIfValid` | 0.459 | reverse(book_refund) |
| 5 | init | `UpdateTransferTxnParties` | 0.275 | reverse(book_refund) |
| 6 | init | `TryDebitAccountBalanceNonBook` | 0.368 | forward(book_transfer) |
| 7 | init | `InsertAccountChange` | 0.459 | forward(book_transfer) |
| 8 | init | `UpdateTransferTxnStatus` | 0.315 | forward(book_transfer) |
| 9 | init | `raw_sql:commit` | 0.677 | forward(book_transfer) |
| 10 | pay_success | `GetTransferTxnStageForUpdate` | 0.113 | reverse(book_refund) |
| 11 | pay_success | `GetAccountForUpdateByNo` | 0.111 | reverse(book_refund) |
| 12 | pay_success | `TryCreditAccountBalanceNonBook` | 0.213 | reverse(book_refund) |
| 13 | pay_success | `InsertAccountChange` | 0.087 | reverse(book_refund) |
| 14 | pay_success | `UpdateTransferTxnStatus` | 0.136 | reverse(book_refund) |
| 15 | pay_success | `InsertOutboxEvent` | 0.228 | reverse(book_refund) |
| 16 | pay_success | `raw_sql:commit` | 0.549 | reverse(book_refund) |

## 有 book 转账（book_transfer）

> 下表为本次 perf 直接采样的实际执行顺序和单次耗时。

| # | 阶段 | Query | 耗时(ms) |
| --- | --- | --- | ---: |
| 1 | submit | `CreateTransferTxn` | 2.185 |
| 2 | init | `GetTransferTxnStageForUpdate` | 0.468 |
| 3 | init | `TryDebitAccountBalanceNonBook` | 0.368 |
| 4 | init | `InsertAccountChange` | 0.459 |
| 5 | init | `UpdateTransferTxnStatus` | 0.315 |
| 6 | init | `raw_sql:commit` | 0.677 |
| 7 | pay_success | `GetTransferTxnStageForUpdate` | 0.126 |
| 8 | pay_success | `TryCreditAccountBalanceNonBook` (miss, fallback) | 0.250 |
| 9 | pay_success | `GetAccountForUpdateByNo` | 0.275 |
| 10 | pay_success | `UpsertAccountBookBalance` | 0.540 |
| 11 | pay_success | `UpdateAccountBalance` | 0.200 |
| 12 | pay_success | `InsertAccountChange` | 0.104 |
| 13 | pay_success | `InsertAccountBookChange` | 0.457 |
| 14 | pay_success | `UpdateTransferTxnStatus` | 0.488 |
| 15 | pay_success | `InsertOutboxEvent` | 0.458 |
| 16 | pay_success | `raw_sql:commit` | 1.165 |

## 有 book 退款（book_refund）

> 下表为本次 perf 直接采样的实际执行顺序和单次耗时。

| # | 阶段 | Query | 耗时(ms) |
| --- | --- | --- | ---: |
| 1 | submit | `CreateTransferTxn` | 0.658 |
| 2 | route_key | `GetTransferTxnByNo` | 0.329 |
| 3 | init | `GetTransferTxnStageForUpdate` | 0.225 |
| 4 | init | `DecreaseOriginTxnRefundableIfValid` | 0.459 |
| 5 | init | `UpdateTransferTxnParties` | 0.275 |
| 6 | init | `TryDebitAccountBalanceNonBook` (miss, fallback) | 0.389 |
| 7 | init | `GetAccountForUpdateByNo` | 0.224 |
| 8 | init | `ListAvailableAccountBooksForUpdate` | 0.559 |
| 9 | init | `BatchUpdateAccountBookBalances` | 0.555 |
| 10 | init | `UpdateAccountBalance` | 0.191 |
| 11 | init | `InsertAccountChange` | 0.309 |
| 12 | init | `InsertAccountBookChange` | 0.346 |
| 13 | init | `UpdateTransferTxnStatus` | 0.281 |
| 14 | init | `raw_sql:commit` | 0.632 |
| 15 | pay_success | `GetTransferTxnStageForUpdate` | 0.113 |
| 16 | pay_success | `GetAccountForUpdateByNo` | 0.111 |
| 17 | pay_success | `TryCreditAccountBalanceNonBook` | 0.213 |
| 18 | pay_success | `InsertAccountChange` | 0.087 |
| 19 | pay_success | `UpdateTransferTxnStatus` | 0.136 |
| 20 | pay_success | `InsertOutboxEvent` | 0.228 |
| 21 | pay_success | `raw_sql:commit` | 0.549 |
