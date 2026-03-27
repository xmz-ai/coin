# Credits Ledger - DDL 设计（PostgreSQL 16）

> 与 `docs/domain.md` 对齐（V1）
> - 交易租户键：`merchant_no`
> - 幂等键：`uniq(merchant_no, out_trade_no)`
> - Customer/Account/Txn/Outbox/Webhook 均以 `merchant_no` 关联商户
> - V1 不引入 `txn_detail`
> - 不配置数据库外键（`FOREIGN KEY/REFERENCES`）

---

## 1. 设计约束

1. 金额使用 `BIGINT`（最小货币单位）。
2. 时间使用 `TIMESTAMPTZ`（UTC）。
3. 主业务键：
   - 内部标识：`merchant_id/customer_id/txn_no/event_id/book_no`（UUID）
   - 业务租户键：`merchant_no`
4. 数据关联完整性由应用层与任务巡检保障（DDL 不配置外键约束）。

---

## 2. 核心表结构（与 migrations/000001_init.up.sql 一致）

## 2.1 merchant

```sql
CREATE TABLE IF NOT EXISTS merchant (
  merchant_id UUID PRIMARY KEY,
  merchant_no VARCHAR(16) NOT NULL UNIQUE,
  name TEXT NOT NULL,
  budget_account_no VARCHAR(19) NOT NULL,
  receivable_account_no VARCHAR(19) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (merchant_no ~ '^[0-9]{16}$'),
  CHECK (budget_account_no ~ '^[0-9]{19}$'),
  CHECK (receivable_account_no ~ '^[0-9]{19}$')
);
```

## 2.2 merchant_api_credential

```sql
CREATE TABLE IF NOT EXISTS merchant_api_credential (
  credential_id BIGSERIAL PRIMARY KEY,
  merchant_no VARCHAR(16) NOT NULL,
  secret_ciphertext TEXT NOT NULL,
  key_provider VARCHAR(32) NOT NULL DEFAULT 'LOCAL',
  kms_key_id VARCHAR(64) NOT NULL DEFAULT 'LOCAL_KMS_KEY_V1',
  secret_version INTEGER NOT NULL DEFAULT 1,
  active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (merchant_no, secret_version)
);
```

## 2.2.1 code_sequence（编码发号序列表）

```sql
CREATE TABLE IF NOT EXISTS code_sequence (
  code_type VARCHAR(32) NOT NULL,
  scope_key VARCHAR(32) NOT NULL,
  next_value BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (code_type, scope_key),
  CHECK (next_value >= 0)
);
```

## 2.3 customer

```sql
CREATE TABLE IF NOT EXISTS customer (
  customer_id UUID PRIMARY KEY,
  customer_no VARCHAR(16) NOT NULL UNIQUE,
  merchant_no VARCHAR(16) NOT NULL,
  out_user_id VARCHAR(128) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (merchant_no, out_user_id),
  CHECK (customer_no ~ '^[0-9]{16}$'),
  CHECK (merchant_no ~ '^[0-9]{16}$')
);
```

## 2.4 account

```sql
CREATE TABLE IF NOT EXISTS account (
  account_no VARCHAR(19) PRIMARY KEY,
  merchant_no VARCHAR(16) NOT NULL,
  customer_no VARCHAR(16),
  account_type VARCHAR(32) NOT NULL,
  allow_overdraft BOOLEAN NOT NULL DEFAULT FALSE,
  max_overdraft_limit BIGINT NOT NULL DEFAULT 0,
  allow_debit_out BOOLEAN NOT NULL DEFAULT TRUE,
  allow_credit_in BOOLEAN NOT NULL DEFAULT TRUE,
  allow_transfer BOOLEAN NOT NULL DEFAULT TRUE,
  book_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  balance BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (max_overdraft_limit >= 0),
  CHECK (account_no ~ '^[0-9]{19}$'),
  CHECK (merchant_no ~ '^[0-9]{16}$'),
  CHECK (customer_no IS NULL OR customer_no ~ '^[0-9]{16}$')
);

CREATE INDEX IF NOT EXISTS idx_account_merchant ON account(merchant_no);
CREATE INDEX IF NOT EXISTS idx_account_customer ON account(merchant_no, customer_no);
```

## 2.5 txn

```sql
CREATE TABLE IF NOT EXISTS txn (
  txn_no UUID PRIMARY KEY,
  merchant_no VARCHAR(16) NOT NULL,
  out_trade_no VARCHAR(64) NOT NULL,
  title VARCHAR(128),
  remark VARCHAR(512),
  biz_type VARCHAR(32) NOT NULL CHECK (biz_type IN ('TRANSFER', 'REFUND')),
  transfer_scene VARCHAR(32),
  debit_account_no VARCHAR(19),
  credit_account_no VARCHAR(19),
  amount BIGINT NOT NULL CHECK (amount > 0),
  status VARCHAR(24) NOT NULL CHECK (status IN ('INIT', 'PAY_SUCCESS', 'RECV_SUCCESS', 'FAILED')),
  refund_of_txn_no UUID,
  refundable_amount BIGINT NOT NULL DEFAULT 0 CHECK (refundable_amount >= 0),
  error_code VARCHAR(64),
  error_msg VARCHAR(512),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (merchant_no, out_trade_no),
  CHECK (
    (biz_type = 'TRANSFER' AND transfer_scene IS NOT NULL)
    OR
    (biz_type = 'REFUND' AND transfer_scene IS NULL)
  ),
  CHECK (merchant_no ~ '^[0-9]{16}$'),
  CHECK (out_trade_no ~ '^[A-Za-z0-9_\\-]{1,64}$'),
  CHECK (debit_account_no IS NULL OR debit_account_no ~ '^[0-9]{19}$'),
  CHECK (credit_account_no IS NULL OR credit_account_no ~ '^[0-9]{19}$')
);

CREATE INDEX IF NOT EXISTS idx_txn_status_updated ON txn(status, updated_at);
CREATE INDEX IF NOT EXISTS idx_txn_refund_of ON txn(refund_of_txn_no);
CREATE INDEX IF NOT EXISTS idx_txn_merchant_created ON txn(merchant_no, created_at DESC, txn_no DESC);
```

## 2.6 account_change_log

```sql
CREATE TABLE IF NOT EXISTS account_change_log (
  change_id BIGSERIAL PRIMARY KEY,
  txn_no UUID NOT NULL,
  account_no VARCHAR(19) NOT NULL,
  delta BIGINT NOT NULL,
  balance_before BIGINT NOT NULL,
  balance_after BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (account_no ~ '^[0-9]{19}$')
);

CREATE INDEX IF NOT EXISTS idx_account_change_log_txn_no ON account_change_log(txn_no);
CREATE INDEX IF NOT EXISTS idx_account_change_log_account_created_change ON account_change_log(account_no, created_at DESC, change_id DESC);
```

## 2.7 account_book

```sql
CREATE TABLE IF NOT EXISTS account_book (
  book_no UUID PRIMARY KEY,
  account_no VARCHAR(19) NOT NULL,
  expire_at TIMESTAMPTZ NOT NULL,
  balance BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (account_no, expire_at),
  CHECK (account_no ~ '^[0-9]{19}$')
);

CREATE INDEX IF NOT EXISTS idx_account_book_available_by_account_expire
ON account_book(account_no, expire_at)
WHERE balance > 0;
```

## 2.8 account_book_change_log

```sql
CREATE TABLE IF NOT EXISTS account_book_change_log (
  change_id BIGSERIAL PRIMARY KEY,
  txn_no UUID NOT NULL,
  account_no VARCHAR(19) NOT NULL,
  book_no UUID NOT NULL,
  delta BIGINT NOT NULL,
  balance_before BIGINT NOT NULL,
  balance_after BIGINT NOT NULL,
  expire_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (account_no ~ '^[0-9]{19}$')
);

CREATE INDEX IF NOT EXISTS idx_account_book_change_log_txn_no ON account_book_change_log(txn_no);
CREATE INDEX IF NOT EXISTS idx_account_book_change_log_account_created ON account_book_change_log(account_no, created_at DESC);
```

## 2.9 outbox_event

```sql
CREATE TABLE IF NOT EXISTS outbox_event (
  event_id UUID PRIMARY KEY,
  txn_no UUID NOT NULL,
  merchant_no VARCHAR(16) NOT NULL,
  out_trade_no VARCHAR(64),
  status VARCHAR(16) NOT NULL,
  retry_count INTEGER NOT NULL DEFAULT 0,
  next_retry_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (merchant_no ~ '^[0-9]{16}$'),
  CHECK (out_trade_no IS NULL OR out_trade_no ~ '^[A-Za-z0-9_\\-]{1,64}$')
);

CREATE INDEX IF NOT EXISTS idx_outbox_event_status_retry ON outbox_event(status, next_retry_at);
```

## 2.10 notify_log

```sql
CREATE TABLE IF NOT EXISTS notify_log (
  notify_id BIGSERIAL PRIMARY KEY,
  txn_no UUID NOT NULL,
  status VARCHAR(16) NOT NULL,
  retries INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

## 2.11 webhook_config

```sql
CREATE TABLE IF NOT EXISTS webhook_config (
  merchant_no VARCHAR(16) PRIMARY KEY,
  url TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (merchant_no ~ '^[0-9]{16}$')
);
```

---

## 3. 与 domain.md 的一致性说明

1. `txn` 仅记录交易语义（双方账户、金额、状态、可退金额），不承载 book 路由细节。
2. V1 不引入 `txn_detail`，book 级路径由流水表表达。
3. `book_enabled=false` 不应产生 `account_book*` 数据（应用层与测试保障）。
4. 幂等键为 `(merchant_no, out_trade_no)`。
