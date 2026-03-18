-- name: CreateMerchant :exec
INSERT INTO merchant (
  merchant_id, merchant_no, name, budget_account_no, receivable_account_no
) VALUES (
  sqlc.arg(merchant_id)::uuid,
  sqlc.arg(merchant_no),
  sqlc.arg(name),
  sqlc.arg(budget_account_no),
  sqlc.arg(receivable_account_no)
);

-- name: GetMerchantByNo :one
SELECT merchant_id::text, merchant_no, name, budget_account_no, receivable_account_no
FROM merchant
WHERE merchant_no = sqlc.arg(merchant_no)
LIMIT 1;

-- name: GetMerchantByID :one
SELECT merchant_id::text, merchant_no, name, budget_account_no, receivable_account_no
FROM merchant
WHERE merchant_id = sqlc.arg(merchant_id)
LIMIT 1;

-- name: CreateAccount :exec
INSERT INTO account (
  account_no, merchant_no, customer_no, account_type,
  allow_overdraft, max_overdraft_limit,
  allow_debit_out, allow_credit_in, allow_transfer,
  book_enabled, balance
) VALUES (
  sqlc.arg(account_no),
  sqlc.arg(merchant_no),
  sqlc.narg(customer_no),
  sqlc.arg(account_type),
  sqlc.arg(allow_overdraft),
  sqlc.arg(max_overdraft_limit),
  sqlc.arg(allow_debit_out),
  sqlc.arg(allow_credit_in),
  sqlc.arg(allow_transfer),
  sqlc.arg(book_enabled),
  sqlc.arg(balance)
);

-- name: InitCodeSequence :exec
INSERT INTO code_sequence (code_type, scope_key, next_value)
VALUES (
  sqlc.arg(code_type),
  sqlc.arg(scope_key),
  0
)
ON CONFLICT (code_type, scope_key)
DO NOTHING;

-- name: LeaseCodeRange :one
UPDATE code_sequence
SET next_value = next_value + sqlc.arg(batch_size),
    updated_at = NOW()
WHERE code_type = sqlc.arg(code_type)
  AND scope_key = sqlc.arg(scope_key)
RETURNING
  (next_value - sqlc.arg(batch_size))::bigint AS start_value,
  (next_value - 1)::bigint AS end_value;

-- name: GetAccountByNo :one
SELECT
  a.account_no,
  a.merchant_no,
  COALESCE(a.customer_no, '') AS customer_no,
  a.account_type,
  a.allow_overdraft,
  a.max_overdraft_limit,
  a.allow_debit_out,
  a.allow_credit_in,
  a.allow_transfer,
  a.book_enabled,
  a.balance
FROM account a
WHERE a.account_no = sqlc.arg(account_no)
LIMIT 1;

-- name: GetAccountNoByCustomerNo :one
SELECT c.default_account_no
FROM customer c
WHERE c.merchant_no = sqlc.arg(merchant_no)
  AND c.customer_no = sqlc.arg(customer_no)
  AND c.default_account_no IS NOT NULL
LIMIT 1;

-- name: UpdateAccountCapabilities :exec
UPDATE account
SET allow_debit_out = sqlc.arg(allow_debit_out),
    allow_credit_in = sqlc.arg(allow_credit_in),
    allow_transfer = sqlc.arg(allow_transfer),
    updated_at = NOW()
WHERE account_no = sqlc.arg(account_no);

-- name: CreateCustomer :exec
INSERT INTO customer (customer_id, customer_no, merchant_no, out_user_id, default_account_no)
VALUES (
  sqlc.arg(customer_id)::uuid,
  sqlc.arg(customer_no),
  sqlc.arg(merchant_no),
  sqlc.arg(out_user_id),
  sqlc.narg(default_account_no)
);

-- name: GetCustomerByOutUserID :one
SELECT
  c.customer_id::text AS customer_id,
  c.customer_no,
  c.merchant_no,
  c.out_user_id,
  COALESCE(c.default_account_no, '') AS default_account_no
FROM customer c
WHERE c.merchant_no = sqlc.arg(merchant_no)
  AND c.out_user_id = sqlc.arg(out_user_id)
LIMIT 1;

-- name: SetCustomerDefaultAccountIfEmpty :execrows
UPDATE customer c
SET default_account_no = sqlc.arg(default_account_no),
    updated_at = NOW()
WHERE c.merchant_no = sqlc.arg(merchant_no)
  AND c.customer_no = sqlc.arg(customer_no)
  AND c.default_account_no IS NULL
  AND EXISTS (
    SELECT 1
    FROM account a
    WHERE a.account_no = sqlc.arg(default_account_no)
      AND a.merchant_no = c.merchant_no
      AND a.customer_no = c.customer_no
  );

-- name: CreateTransferTxn :exec
INSERT INTO txn (
  txn_no, merchant_no, out_trade_no, biz_type, transfer_scene,
  debit_account_no, credit_account_no, credit_expire_at, amount, status,
  refund_of_txn_no, refundable_amount, error_code, error_msg
) VALUES (
  sqlc.arg(txn_no)::uuid,
  sqlc.arg(merchant_no),
  sqlc.arg(out_trade_no),
  sqlc.arg(biz_type),
  NULLIF(sqlc.arg(transfer_scene), ''),
  NULLIF(sqlc.arg(debit_account_no), ''),
  NULLIF(sqlc.arg(credit_account_no), ''),
  sqlc.narg(credit_expire_at)::date,
  sqlc.arg(amount),
  sqlc.arg(status),
  NULLIF(sqlc.arg(refund_of_txn_no), '')::uuid,
  sqlc.arg(refundable_amount),
  NULLIF(sqlc.arg(error_code), ''),
  NULLIF(sqlc.arg(error_msg), '')
);

-- name: GetTransferTxnByNo :one
SELECT
  t.txn_no::text AS txn_no,
  t.merchant_no,
  t.out_trade_no,
  COALESCE(t.biz_type, '') AS biz_type,
  COALESCE(t.transfer_scene, '') AS transfer_scene,
  COALESCE(t.debit_account_no, '') AS debit_account_no,
  COALESCE(t.credit_account_no, '') AS credit_account_no,
  t.credit_expire_at,
  t.amount,
  COALESCE(t.refund_of_txn_no::text, '')::text AS refund_of_txn_no,
  t.refundable_amount,
  COALESCE(t.status, '') AS status,
  COALESCE(t.error_code, '') AS error_code,
  COALESCE(t.error_msg, '') AS error_msg,
  t.created_at
FROM txn t
WHERE t.txn_no = sqlc.arg(txn_no)
LIMIT 1;

-- name: GetTransferTxnStageForUpdate :one
SELECT
  COALESCE(t.status, '') AS status,
  COALESCE(t.debit_account_no, '') AS debit_account_no,
  COALESCE(t.credit_account_no, '') AS credit_account_no,
  t.credit_expire_at,
  t.amount,
  t.merchant_no,
  t.out_trade_no,
  COALESCE(t.biz_type, '') AS biz_type,
  COALESCE(t.transfer_scene, '') AS transfer_scene,
  COALESCE(t.refund_of_txn_no::text, '')::text AS refund_of_txn_no
FROM txn t
WHERE t.txn_no = sqlc.arg(txn_no)
FOR UPDATE
LIMIT 1;

-- name: GetTransferTxnByOutTradeNo :one
SELECT
  t.txn_no::text AS txn_no,
  t.merchant_no,
  t.out_trade_no,
  COALESCE(t.biz_type, '') AS biz_type,
  COALESCE(t.transfer_scene, '') AS transfer_scene,
  COALESCE(t.debit_account_no, '') AS debit_account_no,
  COALESCE(t.credit_account_no, '') AS credit_account_no,
  t.credit_expire_at,
  t.amount,
  COALESCE(t.refund_of_txn_no::text, '')::text AS refund_of_txn_no,
  t.refundable_amount,
  COALESCE(t.status, '') AS status,
  COALESCE(t.error_code, '') AS error_code,
  COALESCE(t.error_msg, '') AS error_msg,
  t.created_at
FROM txn t
WHERE t.merchant_no = sqlc.arg(merchant_no)
  AND t.out_trade_no = sqlc.arg(out_trade_no)
LIMIT 1;

-- name: ListTransferTxns :many
SELECT
  t.txn_no::text AS txn_no,
  t.merchant_no,
  t.out_trade_no,
  COALESCE(t.biz_type, '') AS biz_type,
  COALESCE(t.transfer_scene, '') AS transfer_scene,
  COALESCE(t.debit_account_no, '') AS debit_account_no,
  COALESCE(t.credit_account_no, '') AS credit_account_no,
  t.credit_expire_at,
  t.amount,
  COALESCE(t.refund_of_txn_no::text, '')::text AS refund_of_txn_no,
  t.refundable_amount,
  COALESCE(t.status, '') AS status,
  COALESCE(t.error_code, '') AS error_code,
  COALESCE(t.error_msg, '') AS error_msg,
  t.created_at
FROM txn t
WHERE t.merchant_no = sqlc.arg(merchant_no)
  AND (
    NOT sqlc.arg(has_out_user_id)::bool
    OR EXISTS (
      SELECT 1
      FROM account a
      JOIN customer c
        ON c.customer_no = a.customer_no
       AND c.merchant_no = t.merchant_no
      WHERE c.out_user_id = sqlc.arg(out_user_id)
        AND (
          a.account_no = t.debit_account_no
          OR a.account_no = t.credit_account_no
        )
    )
  )
  AND (NOT sqlc.arg(has_scene)::bool OR t.transfer_scene = sqlc.arg(scene))
  AND (NOT sqlc.arg(has_status)::bool OR t.status = sqlc.arg(status))
  AND (NOT sqlc.arg(has_start_time)::bool OR t.created_at >= sqlc.arg(start_time)::timestamptz)
  AND (NOT sqlc.arg(has_end_time)::bool OR t.created_at <= sqlc.arg(end_time)::timestamptz)
  AND (
    NOT sqlc.arg(has_cursor)::bool
    OR (
      t.created_at < sqlc.arg(cursor_created_at)::timestamptz
      OR (t.created_at = sqlc.arg(cursor_created_at)::timestamptz AND t.txn_no < sqlc.arg(cursor_txn_no))
    )
  )
ORDER BY t.created_at DESC, t.txn_no DESC
LIMIT sqlc.arg(page_limit);

-- name: ListTransferTxnsByStatus :many
SELECT
  t.txn_no::text AS txn_no,
  t.merchant_no,
  t.out_trade_no,
  COALESCE(t.biz_type, '') AS biz_type,
  COALESCE(t.transfer_scene, '') AS transfer_scene,
  COALESCE(t.debit_account_no, '') AS debit_account_no,
  COALESCE(t.credit_account_no, '') AS credit_account_no,
  t.credit_expire_at,
  t.amount,
  COALESCE(t.refund_of_txn_no::text, '')::text AS refund_of_txn_no,
  t.refundable_amount,
  COALESCE(t.status, '') AS status,
  COALESCE(t.error_code, '') AS error_code,
  COALESCE(t.error_msg, '') AS error_msg,
  t.created_at
FROM txn t
WHERE t.status = sqlc.arg(status)
ORDER BY t.updated_at ASC, t.txn_no ASC
LIMIT sqlc.arg(page_limit);

-- name: UpdateTransferTxnStatus :exec
UPDATE txn
SET status = sqlc.arg(status),
    error_code = NULLIF(sqlc.arg(error_code), ''),
    error_msg = NULLIF(sqlc.arg(error_msg), ''),
    updated_at = NOW()
WHERE txn_no = sqlc.arg(txn_no);

-- name: UpdateTransferTxnStatusFrom :execrows
UPDATE txn
SET status = sqlc.arg(next_status),
    error_code = NULLIF(sqlc.arg(error_code), ''),
    error_msg = NULLIF(sqlc.arg(error_msg), ''),
    updated_at = NOW()
WHERE txn_no = sqlc.arg(txn_no)
  AND status = sqlc.arg(from_status);

-- name: UpdateTransferTxnParties :exec
UPDATE txn
SET debit_account_no = sqlc.arg(debit_account_no),
    credit_account_no = sqlc.arg(credit_account_no),
    updated_at = NOW()
WHERE txn_no = sqlc.arg(txn_no);

-- name: TryDecreaseTxnRefundable :one
UPDATE txn
SET refundable_amount = refundable_amount - sqlc.arg(amount),
    updated_at = NOW()
WHERE txn_no = sqlc.arg(txn_no)
  AND refundable_amount >= sqlc.arg(amount)
RETURNING refundable_amount;

-- name: GetAccountForUpdateByNo :one
SELECT
  a.account_no,
  a.merchant_no,
  COALESCE(a.customer_no, '') AS customer_no,
  a.account_type,
  a.allow_overdraft,
  a.max_overdraft_limit,
  a.allow_debit_out,
  a.allow_credit_in,
  a.allow_transfer,
  a.book_enabled,
  a.balance
FROM account a
WHERE a.account_no = sqlc.arg(account_no)
FOR UPDATE OF a
LIMIT 1;

-- name: UpdateAccountBalance :exec
UPDATE account
SET balance = sqlc.arg(balance),
    updated_at = NOW()
WHERE account_no = sqlc.arg(account_no);

-- name: InsertAccountChangePair :exec
INSERT INTO account_change_log (txn_no, account_no, delta, balance_after)
VALUES
  (
    sqlc.arg(txn_no)::uuid,
    sqlc.arg(debit_account_no),
    sqlc.arg(debit_delta),
    sqlc.arg(debit_balance_after)
  ),
  (
    sqlc.arg(txn_no)::uuid,
    sqlc.arg(credit_account_no),
    sqlc.arg(credit_delta),
    sqlc.arg(credit_balance_after)
  );

-- name: InsertAccountChange :exec
INSERT INTO account_change_log (txn_no, account_no, delta, balance_after)
VALUES (
  sqlc.arg(txn_no)::uuid,
  sqlc.arg(account_no),
  sqlc.arg(delta),
  sqlc.arg(balance_after)
);

-- name: ListAvailableAccountBooksForUpdate :many
SELECT
  b.book_no,
  b.account_no,
  b.expire_at,
  b.balance
FROM account_book b
WHERE b.account_no = sqlc.arg(account_no)
  AND b.expire_at > sqlc.arg(now_utc)::date
  AND b.balance > 0
ORDER BY b.expire_at ASC
FOR UPDATE OF b;

-- name: UpsertAccountBookBalance :one
INSERT INTO account_book (book_no, account_no, expire_at, balance)
VALUES (
  sqlc.arg(book_no)::uuid,
  sqlc.arg(account_no),
  sqlc.arg(expire_at)::date,
  sqlc.arg(delta)
)
ON CONFLICT (account_no, expire_at)
DO UPDATE SET balance = account_book.balance + EXCLUDED.balance
RETURNING book_no, account_no, expire_at, balance;

-- name: UpdateAccountBookBalance :exec
UPDATE account_book
SET balance = sqlc.arg(balance)
WHERE book_no = sqlc.arg(book_no)::uuid;

-- name: InsertAccountBookChange :exec
INSERT INTO account_book_change_log (txn_no, account_no, book_no, delta, balance_after, expire_at)
VALUES (
  sqlc.arg(txn_no)::uuid,
  sqlc.arg(account_no),
  sqlc.arg(book_no)::uuid,
  sqlc.arg(delta),
  sqlc.arg(balance_after),
  sqlc.arg(expire_at)::date
);

-- name: GetOriginTxnForUpdate :one
SELECT
  COALESCE(debit_account_no, '') AS debit_account_no,
  COALESCE(credit_account_no, '') AS credit_account_no,
  refundable_amount,
  merchant_no
FROM txn
WHERE txn_no = sqlc.arg(origin_txn_no)
FOR UPDATE
LIMIT 1;

-- name: DecreaseOriginTxnRefundable :exec
UPDATE txn
SET refundable_amount = refundable_amount - sqlc.arg(amount),
    updated_at = NOW()
WHERE txn_no = sqlc.arg(origin_txn_no);

-- name: ListOriginDebitBookChanges :many
SELECT
  delta,
  expire_at,
  created_at
FROM account_book_change_log
WHERE txn_no = sqlc.arg(txn_no)::uuid
  AND account_no = sqlc.arg(account_no)
  AND delta < 0
ORDER BY change_id ASC;

-- name: ListRefundDebitsByOrigin :many
SELECT
  t.txn_no::text AS txn_no,
  t.amount
FROM txn t
JOIN account_change_log acl
  ON acl.txn_no = t.txn_no
WHERE t.merchant_no = sqlc.arg(merchant_no)
  AND t.refund_of_txn_no = sqlc.arg(origin_txn_no)::uuid
  AND t.biz_type = 'REFUND'
  AND acl.account_no = sqlc.arg(origin_credit_account_no)
  AND acl.delta < 0
ORDER BY acl.change_id ASC, t.txn_no ASC;

-- name: TxnCount :one
SELECT COUNT(*)::bigint AS txn_count
FROM txn;

-- name: IncAppliedChange :exec
INSERT INTO applied_change_counter(id, value)
VALUES (1, 1)
ON CONFLICT (id)
DO UPDATE SET value = applied_change_counter.value + 1;

-- name: AppliedChangeCount :one
SELECT value
FROM applied_change_counter
WHERE id = 1;

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_event (
  event_id, txn_no, merchant_no, out_trade_no, status, retry_count, next_retry_at, created_at, updated_at
) VALUES (
  sqlc.arg(event_id)::uuid,
  sqlc.arg(txn_no)::uuid,
  sqlc.arg(merchant_no),
  NULLIF(sqlc.arg(out_trade_no), ''),
  sqlc.arg(status),
  0,
  NULL,
  NOW(),
  NOW()
)
ON CONFLICT DO NOTHING;

-- name: UpsertWebhookConfig :exec
INSERT INTO webhook_config (merchant_no, url, enabled, created_at, updated_at)
VALUES (
  sqlc.arg(merchant_no),
  sqlc.arg(url),
  sqlc.arg(enabled),
  NOW(),
  NOW()
)
ON CONFLICT (merchant_no)
DO UPDATE SET
  url = EXCLUDED.url,
  enabled = EXCLUDED.enabled,
  updated_at = NOW();

-- name: GetWebhookConfig :one
SELECT merchant_no, url, enabled, created_at, updated_at
FROM webhook_config
WHERE merchant_no = sqlc.arg(merchant_no)
LIMIT 1;

-- name: ClaimDueOutboxEvents :many
SELECT
  e.event_id::text AS event_id,
  e.txn_no::text AS txn_no,
  e.merchant_no,
  COALESCE(e.out_trade_no, '') AS out_trade_no,
  COALESCE(t.biz_type, '') AS biz_type,
  COALESCE(t.transfer_scene, '') AS transfer_scene,
  t.amount,
  COALESCE(t.status, '') AS status,
  e.retry_count
FROM outbox_event e
JOIN txn t ON t.txn_no = e.txn_no
WHERE e.status = 'PENDING'
  AND (e.next_retry_at IS NULL OR e.next_retry_at <= sqlc.arg(now_at)::timestamptz)
ORDER BY e.created_at ASC, e.event_id ASC
LIMIT sqlc.arg(page_limit)
FOR UPDATE SKIP LOCKED;

-- name: ClaimDueOutboxEventsByTxnNo :many
SELECT
  e.event_id::text AS event_id,
  e.txn_no::text AS txn_no,
  e.merchant_no,
  COALESCE(e.out_trade_no, '') AS out_trade_no,
  COALESCE(t.biz_type, '') AS biz_type,
  COALESCE(t.transfer_scene, '') AS transfer_scene,
  t.amount,
  COALESCE(t.status, '') AS status,
  e.retry_count
FROM outbox_event e
JOIN txn t ON t.txn_no = e.txn_no
WHERE e.status = 'PENDING'
  AND e.txn_no = sqlc.arg(txn_no)::uuid
  AND (e.next_retry_at IS NULL OR e.next_retry_at <= sqlc.arg(now_at)::timestamptz)
ORDER BY e.created_at ASC, e.event_id ASC
LIMIT sqlc.arg(page_limit)
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxEventSuccess :exec
UPDATE outbox_event
SET status = 'SUCCESS',
    updated_at = NOW()
WHERE event_id = sqlc.arg(event_id)::uuid;

-- name: MarkOutboxEventRetry :exec
UPDATE outbox_event
SET retry_count = sqlc.arg(retry_count),
    next_retry_at = sqlc.arg(next_retry_at)::timestamptz,
    status = CASE WHEN sqlc.arg(mark_dead)::bool THEN 'DEAD' ELSE 'PENDING' END,
    updated_at = NOW()
WHERE event_id = sqlc.arg(event_id)::uuid;

-- name: InsertNotifyLog :exec
INSERT INTO notify_log (txn_no, status, retries, created_at)
VALUES (
  sqlc.arg(txn_no)::uuid,
  sqlc.arg(status),
  sqlc.arg(retries),
  NOW()
);

-- name: GetActiveSecretCiphertext :one
SELECT secret_ciphertext
FROM merchant_api_credential
WHERE merchant_no = sqlc.arg(merchant_no)
  AND active = true
ORDER BY secret_version DESC
LIMIT 1;

-- name: LockMerchantNoForUpdate :one
SELECT merchant_no
FROM merchant
WHERE merchant_no = sqlc.arg(merchant_no)
FOR UPDATE
LIMIT 1;

-- name: GetMaxSecretVersion :one
SELECT COALESCE(MAX(secret_version), 0)::int AS max_secret_version
FROM merchant_api_credential
WHERE merchant_no = sqlc.arg(merchant_no);

-- name: DeactivateActiveMerchantSecrets :exec
UPDATE merchant_api_credential
SET active = false,
    updated_at = NOW()
WHERE merchant_no = sqlc.arg(merchant_no)
  AND active = true;

-- name: InsertMerchantSecretCredential :exec
INSERT INTO merchant_api_credential (
  merchant_no, secret_ciphertext, secret_version, active, created_at, updated_at
) VALUES (
  sqlc.arg(merchant_no),
  sqlc.arg(secret_ciphertext),
  sqlc.arg(secret_version),
  true,
  NOW(),
  NOW()
);
