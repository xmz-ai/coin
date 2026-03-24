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
  0
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

-- name: GetAccountByMerchantOutUserID :one
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
JOIN customer c
  ON c.customer_no = a.customer_no
 AND c.merchant_no = a.merchant_no
WHERE c.merchant_no = sqlc.arg(merchant_no)
  AND c.out_user_id = sqlc.arg(out_user_id)
  AND c.default_account_no = a.account_no
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
  title, remark, debit_account_no, credit_account_no, credit_expire_at, amount, status,
  refund_of_txn_no, refundable_amount, error_code, error_msg
) VALUES (
  NULLIF(sqlc.arg(txn_no), '')::uuid,
  sqlc.arg(merchant_no),
  sqlc.arg(out_trade_no),
  sqlc.arg(biz_type),
  NULLIF(sqlc.arg(transfer_scene), ''),
  NULLIF(sqlc.arg(title), ''),
  NULLIF(sqlc.arg(remark), ''),
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
  COALESCE(t.title, '') AS title,
  COALESCE(t.remark, '') AS remark,
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
WHERE t.txn_no = NULLIF(sqlc.arg(txn_no), '')::uuid
LIMIT 1;

-- name: GetTransferTxnStageForUpdate :one
SELECT
  t.txn_no,
  COALESCE(t.status, '') AS status,
  COALESCE(t.debit_account_no, '') AS debit_account_no,
  COALESCE(t.credit_account_no, '') AS credit_account_no,
  t.credit_expire_at,
  t.amount,
  t.merchant_no,
  t.out_trade_no,
  COALESCE(t.biz_type, '') AS biz_type,
  COALESCE(t.transfer_scene, '') AS transfer_scene,
  t.refund_of_txn_no
FROM txn t
WHERE t.txn_no = NULLIF(sqlc.arg(txn_no), '')::uuid
FOR UPDATE
LIMIT 1;

-- name: GetTransferTxnByOutTradeNo :one
SELECT
  t.txn_no::text AS txn_no,
  t.merchant_no,
  t.out_trade_no,
  COALESCE(t.title, '') AS title,
  COALESCE(t.remark, '') AS remark,
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
  COALESCE(t.title, '') AS title,
  COALESCE(t.remark, '') AS remark,
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
WHERE (sqlc.arg(merchant_no)::text = '' OR t.merchant_no = sqlc.arg(merchant_no)::text)
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
  COALESCE(t.title, '') AS title,
  COALESCE(t.remark, '') AS remark,
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

-- name: ListAccountChangeLogs :many
SELECT
  acl.change_id,
  acl.txn_no::text AS txn_no,
  acl.account_no,
  acl.delta,
  acl.balance_before,
  acl.balance_after,
  COALESCE(t.title, '') AS title,
  COALESCE(t.remark, '') AS remark,
  acl.created_at
FROM account_change_log acl
JOIN account a
  ON a.account_no = acl.account_no
LEFT JOIN txn t
  ON t.txn_no = acl.txn_no
WHERE a.merchant_no = sqlc.arg(merchant_no)
  AND acl.account_no = sqlc.arg(account_no)
  AND (
    NOT sqlc.arg(has_cursor)::bool
    OR (
      acl.created_at < sqlc.arg(cursor_created_at)::timestamptz
      OR (acl.created_at = sqlc.arg(cursor_created_at)::timestamptz AND acl.change_id < sqlc.arg(cursor_change_id))
    )
  )
ORDER BY acl.created_at DESC, acl.change_id DESC
LIMIT sqlc.arg(page_limit);

-- name: UpdateTransferTxnStatus :exec
UPDATE txn
SET status = sqlc.arg(status),
    error_code = NULLIF(sqlc.arg(error_code), ''),
    error_msg = NULLIF(sqlc.arg(error_msg), ''),
    updated_at = NOW()
WHERE txn_no = NULLIF(sqlc.arg(txn_no), '')::uuid;

-- name: UpdateTransferTxnStatusFrom :execrows
UPDATE txn
SET status = sqlc.arg(next_status),
    error_code = NULLIF(sqlc.arg(error_code), ''),
    error_msg = NULLIF(sqlc.arg(error_msg), ''),
    updated_at = NOW()
WHERE txn_no = NULLIF(sqlc.arg(txn_no), '')::uuid
  AND status = sqlc.arg(from_status);

-- name: UpdateTransferTxnParties :exec
UPDATE txn
SET debit_account_no = sqlc.arg(debit_account_no),
    credit_account_no = sqlc.arg(credit_account_no),
    updated_at = NOW()
WHERE txn_no = NULLIF(sqlc.arg(txn_no), '')::uuid;

-- name: TryDecreaseTxnRefundable :one
UPDATE txn
SET refundable_amount = refundable_amount - sqlc.arg(amount),
    updated_at = NOW()
WHERE txn_no = NULLIF(sqlc.arg(txn_no), '')::uuid
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

-- name: TryCreditAccountBalanceNonBookRefund :one
UPDATE account a
SET balance = a.balance + sqlc.arg(amount),
    updated_at = NOW()
WHERE a.account_no = sqlc.arg(account_no)
  AND a.book_enabled = false
RETURNING
  a.account_no,
  a.balance;

-- name: UpdateAccountBalance :exec
UPDATE account
SET balance = sqlc.arg(balance),
    updated_at = NOW()
WHERE account_no = sqlc.arg(account_no);

-- name: InsertAccountChange :exec
INSERT INTO account_change_log (txn_no, account_no, delta, balance_before, balance_after)
VALUES (
  sqlc.arg(txn_no)::uuid,
  sqlc.arg(account_no),
  sqlc.arg(delta),
  sqlc.arg(balance_before),
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
  AND (
    (b.expire_at > sqlc.arg(now_utc)::date AND b.balance > 0)
    OR b.expire_at = sqlc.arg(no_expire_at)::date
  )
ORDER BY
  CASE WHEN b.expire_at = sqlc.arg(no_expire_at)::date THEN 1 ELSE 0 END ASC,
  b.expire_at ASC,
  b.book_no ASC
FOR UPDATE OF b;

-- name: GetAccountBookForUpdateByAccountExpire :one
SELECT
  b.book_no,
  b.account_no,
  b.expire_at,
  b.balance
FROM account_book b
WHERE b.account_no = sqlc.arg(account_no)
  AND b.expire_at = sqlc.arg(expire_at)::date
FOR UPDATE OF b
LIMIT 1;

-- name: UpsertAccountBookBalance :one
INSERT INTO account_book (book_no, account_no, expire_at, balance)
VALUES (
  NULLIF(sqlc.arg(book_no), '')::uuid,
  sqlc.arg(account_no),
  sqlc.arg(expire_at)::date,
  sqlc.arg(delta)
)
ON CONFLICT (account_no, expire_at)
DO UPDATE SET balance = account_book.balance + EXCLUDED.balance
RETURNING book_no, account_no, expire_at, balance;

-- name: BatchUpdateAccountBookBalances :many
WITH payload AS (
  SELECT
    bn.book_no::uuid AS book_no,
    bl.delta::bigint AS delta
  FROM UNNEST(CAST(sqlc.arg(book_nos) AS text[])) WITH ORDINALITY AS bn(book_no, ord)
  JOIN UNNEST(CAST(sqlc.arg(deltas) AS bigint[])) WITH ORDINALITY AS bl(delta, ord)
    ON bn.ord = bl.ord
)
UPDATE account_book b
SET balance = b.balance + payload.delta
FROM payload
WHERE b.book_no = payload.book_no
RETURNING b.book_no, b.balance;

-- name: InsertAccountBookChange :exec
INSERT INTO account_book_change_log (txn_no, account_no, book_no, delta, balance_before, balance_after, expire_at)
VALUES (
  sqlc.arg(txn_no)::uuid,
  sqlc.arg(account_no),
  sqlc.arg(book_no)::uuid,
  sqlc.arg(delta),
  sqlc.arg(balance_before),
  sqlc.arg(balance_after),
  sqlc.arg(expire_at)::date
);

-- name: DecreaseOriginTxnRefundableIfValid :one
UPDATE txn
SET refundable_amount = refundable_amount - sqlc.arg(amount),
    updated_at = NOW()
WHERE txn_no = sqlc.arg(origin_txn_no)
  AND merchant_no = sqlc.arg(merchant_no)
  AND biz_type = 'TRANSFER'
  AND status = 'RECV_SUCCESS'
  AND refundable_amount >= sqlc.arg(amount)
RETURNING
  COALESCE(debit_account_no, '') AS debit_account_no,
  COALESCE(credit_account_no, '') AS credit_account_no,
  refundable_amount;

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

-- name: GetRefundDebitStatsByOrigin :one
SELECT
  COALESCE(SUM(t.amount), 0)::bigint AS total_debited,
  COALESCE(SUM(CASE WHEN t.txn_no = sqlc.arg(refund_txn_no)::uuid THEN t.amount ELSE 0 END), 0)::bigint AS current_debited
FROM txn t
JOIN account_change_log acl
  ON acl.txn_no = t.txn_no
WHERE t.merchant_no = sqlc.arg(merchant_no)
  AND t.refund_of_txn_no = sqlc.arg(origin_txn_no)::uuid
  AND t.biz_type = 'REFUND'
  AND acl.account_no = sqlc.arg(origin_credit_account_no)
  AND acl.delta < 0;

-- name: TxnCount :one
SELECT COUNT(*)::bigint AS txn_count
FROM txn;

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_event (
  event_id, txn_no, merchant_no, out_trade_no, status, retry_count, next_retry_at, created_at, updated_at
) SELECT
  sqlc.arg(event_id)::uuid,
  sqlc.arg(txn_no)::uuid,
  sqlc.arg(merchant_no),
  NULLIF(sqlc.arg(out_trade_no), ''),
  sqlc.arg(status),
  0,
  NULL,
  NOW(),
  NOW()
WHERE EXISTS (
  SELECT 1
  FROM webhook_config wc
  WHERE wc.merchant_no = sqlc.arg(webhook_merchant_no)
    AND wc.enabled = TRUE
    AND BTRIM(wc.url) <> ''
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
WITH picked AS (
  SELECT
    e.id,
    e.created_at
  FROM outbox_event e
  WHERE (
      e.status = 'PENDING'
      AND (e.next_retry_at IS NULL OR e.next_retry_at <= sqlc.arg(now_at)::timestamptz)
    ) OR (
      e.status = 'PROCESSING'
      AND e.updated_at <= NOW() - INTERVAL '30 minutes'
    )
  ORDER BY e.created_at ASC, e.id ASC
  LIMIT sqlc.arg(page_limit)
  FOR UPDATE SKIP LOCKED
),
claimed AS (
  UPDATE outbox_event e
  SET status = 'PROCESSING',
      updated_at = NOW()
  FROM picked p
  WHERE e.id = p.id
  RETURNING
    e.id,
    e.event_id,
    e.txn_no,
    e.merchant_no,
    COALESCE(e.out_trade_no, '') AS out_trade_no,
    e.retry_count,
    p.created_at
)
SELECT
  c.event_id::text AS event_id,
  c.txn_no::text AS txn_no,
  c.merchant_no,
  c.out_trade_no,
  COALESCE(t.biz_type, '') AS biz_type,
  COALESCE(t.transfer_scene, '') AS transfer_scene,
  t.amount,
  COALESCE(t.status, '') AS status,
  c.retry_count
FROM claimed c
JOIN txn t ON t.txn_no = c.txn_no
ORDER BY c.created_at ASC, c.id ASC;

-- name: ClaimDueOutboxEventsByTxnNo :many
WITH picked AS (
  SELECT
    e.id,
    e.created_at
  FROM outbox_event e
  WHERE e.txn_no = NULLIF(sqlc.arg(txn_no), '')::uuid
    AND (
      (
        e.status = 'PENDING'
        AND (e.next_retry_at IS NULL OR e.next_retry_at <= sqlc.arg(now_at)::timestamptz)
      ) OR (
        e.status = 'PROCESSING'
        AND e.updated_at <= NOW() - INTERVAL '30 minutes'
      )
    )
  ORDER BY e.created_at ASC, e.id ASC
  LIMIT sqlc.arg(page_limit)
  FOR UPDATE SKIP LOCKED
),
claimed AS (
  UPDATE outbox_event e
  SET status = 'PROCESSING',
      updated_at = NOW()
  FROM picked p
  WHERE e.id = p.id
  RETURNING
    e.id,
    e.event_id,
    e.txn_no,
    e.merchant_no,
    COALESCE(e.out_trade_no, '') AS out_trade_no,
    e.retry_count,
    p.created_at
)
SELECT
  c.event_id::text AS event_id,
  c.txn_no::text AS txn_no,
  c.merchant_no,
  c.out_trade_no,
  COALESCE(t.biz_type, '') AS biz_type,
  COALESCE(t.transfer_scene, '') AS transfer_scene,
  t.amount,
  COALESCE(t.status, '') AS status,
  c.retry_count
FROM claimed c
JOIN txn t ON t.txn_no = c.txn_no
ORDER BY c.created_at ASC, c.id ASC;

-- name: MarkOutboxEventSuccess :exec
UPDATE outbox_event
SET status = 'SUCCESS',
    updated_at = NOW()
WHERE event_id = NULLIF(sqlc.arg(event_id), '')::uuid;

-- name: MarkOutboxEventRetry :exec
UPDATE outbox_event
SET retry_count = sqlc.arg(retry_count),
    next_retry_at = sqlc.arg(next_retry_at)::timestamptz,
    status = CASE WHEN sqlc.arg(mark_dead)::bool THEN 'DEAD' ELSE 'PENDING' END,
    updated_at = NOW()
WHERE event_id = NULLIF(sqlc.arg(event_id), '')::uuid;

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
