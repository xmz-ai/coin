# Credits Ledger - DDL 设计（PostgreSQL 16）

> 版本基线：与 `plan.md` / `domain.md` 当前结论一致
> - 业务租户键：`merchant_id`（不再使用 `app_id`）
> - 幂等键：`uniq(merchant_id, out_trade_no)`
> - Customer 必须归属 Merchant
> - `account_scene` 保留（模板/运营用途），交易能力由 capability 字段决定
> - `support_expiry=true` 才使用 `account_book`
> - `max_overdraft_limit=0` + `allow_overdraft=true` 表示无限透支

---

## 1. 设计约定

1. 金额字段全部使用 `BIGINT`（最小货币单位，禁止浮点）。
2. 时间统一 `TIMESTAMPTZ(3)`，默认 UTC。
3. 内部业务主键（`merchant_id/customer_id/txn_no/event_id/book_no`）使用 UUIDv7 字符串。
4. 所有变更流水均写不可变日志表，不做 update。
5. 使用 PostgreSQL 原生约束（CHECK / FK / UNIQUE / PARTIAL INDEX）。

---

## 2. 核心表结构

## 2.1 merchant

```sql
CREATE TABLE merchant (
  merchant_id UUID PRIMARY KEY,
  merchant_no VARCHAR(32) NOT NULL UNIQUE,
  name VARCHAR(128) NOT NULL,
  status SMALLINT NOT NULL DEFAULT 1 CHECK (status IN (0, 1)), -- 1=ACTIVE,0=INACTIVE
  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK (merchant_no ~ '^[0-9]{16}$')
);

CREATE INDEX idx_merchant_status ON merchant(status);
```

## 2.2 merchant_api_credential

```sql
CREATE TABLE merchant_api_credential (
  id BIGSERIAL PRIMARY KEY,
  merchant_id UUID NOT NULL UNIQUE REFERENCES merchant(merchant_id),
  secret_ciphertext TEXT NOT NULL,
  key_provider VARCHAR(32) NOT NULL DEFAULT 'LOCAL' CHECK (key_provider IN ('LOCAL', 'KMS')),
  kms_key_id VARCHAR(64) NOT NULL DEFAULT 'local_v1',
  secret_version INT NOT NULL DEFAULT 1,
  status SMALLINT NOT NULL DEFAULT 1 CHECK (status IN (0, 1)), -- 1=ACTIVE,0=DISABLED
  last_rotated_at TIMESTAMPTZ(3),
  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

> `merchant_secret` 明文仅开户/轮转时返回一次，库中存可解密密文（`secret_ciphertext`）。
> 当前阶段固定使用 `key_provider=LOCAL` + `kms_key_id=local_v1`。

## 2.3 customer

```sql
CREATE TABLE customer (
  customer_id UUID PRIMARY KEY,
  customer_no VARCHAR(32) NOT NULL UNIQUE,
  merchant_id UUID NOT NULL REFERENCES merchant(merchant_id),
  out_user_id VARCHAR(128) NOT NULL,
  status SMALLINT NOT NULL DEFAULT 1 CHECK (status IN (0, 1)), -- 1=ACTIVE,0=INACTIVE
  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (merchant_id, out_user_id),
  CHECK (customer_no ~ '^[0-9]{16}$')
);

CREATE INDEX idx_customer_merchant ON customer(merchant_id);
CREATE INDEX idx_customer_merchant_out_user ON customer(merchant_id, out_user_id);
```

## 2.4 account

```sql
CREATE TABLE account (
  id BIGSERIAL PRIMARY KEY,
  account_no VARCHAR(32) NOT NULL UNIQUE,
  merchant_id UUID NOT NULL REFERENCES merchant(merchant_id),

  owner_type SMALLINT NOT NULL CHECK (owner_type IN (1, 2)), -- 1=CUSTOMER,2=MERCHANT
  owner_id UUID NOT NULL,

  account_scene VARCHAR(32) NOT NULL CHECK (account_scene IN ('BUDGET', 'RECEIVABLE', 'CUSTOM')),
  currency VARCHAR(16) NOT NULL DEFAULT 'CREDIT',

  balance BIGINT NOT NULL DEFAULT 0,

  allow_overdraft BOOLEAN NOT NULL DEFAULT FALSE,
  max_overdraft_limit BIGINT NOT NULL DEFAULT 0, -- 0+allow_overdraft=true => unlimited
  allow_transfer BOOLEAN NOT NULL DEFAULT FALSE,
  allow_credit_in BOOLEAN NOT NULL DEFAULT TRUE,
  allow_debit_out BOOLEAN NOT NULL DEFAULT TRUE,
  support_expiry BOOLEAN NOT NULL DEFAULT FALSE,

  status SMALLINT NOT NULL DEFAULT 1 CHECK (status IN (0, 1, 2)), -- 1=ACTIVE,0=INACTIVE,2=CLOSED
  version BIGINT NOT NULL DEFAULT 0,

  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

  CHECK (max_overdraft_limit >= 0),
  CHECK (account_no ~ '^[0-9]{19}$')
);

CREATE INDEX idx_account_owner ON account(owner_type, owner_id);
CREATE INDEX idx_account_merchant ON account(merchant_id);
```

## 2.5 merchant_account_binding

```sql
CREATE TABLE merchant_account_binding (
  id BIGSERIAL PRIMARY KEY,
  merchant_id UUID NOT NULL UNIQUE REFERENCES merchant(merchant_id),
  budget_account_no VARCHAR(32) NOT NULL UNIQUE REFERENCES account(account_no),
  receivable_account_no VARCHAR(32) NOT NULL UNIQUE REFERENCES account(account_no),
  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CHECK (budget_account_no <> receivable_account_no)
);
```

## 2.6 account_book（仅 support_expiry=true 账户）

```sql
CREATE TABLE account_book (
  id BIGSERIAL PRIMARY KEY,
  book_no UUID NOT NULL UNIQUE,
  account_no VARCHAR(32) NOT NULL REFERENCES account(account_no),
  expire_at TIMESTAMPTZ(3) NOT NULL,
  balance BIGINT NOT NULL DEFAULT 0,
  status SMALLINT NOT NULL DEFAULT 1 CHECK (status IN (1, 2)), -- 1=ACTIVE,2=EXPIRED
  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (account_no, expire_at)
);

CREATE INDEX idx_book_expire ON account_book(expire_at);
```

## 2.7 txn（交易主单）

```sql
CREATE TABLE txn (
  id BIGSERIAL PRIMARY KEY,
  txn_no UUID NOT NULL UNIQUE,
  merchant_id UUID NOT NULL REFERENCES merchant(merchant_id),
  out_trade_no VARCHAR(64) NOT NULL,

  biz_type VARCHAR(32) NOT NULL CHECK (biz_type IN ('TRANSFER', 'REFUND')),
  transfer_scene VARCHAR(32) CHECK (transfer_scene IN ('ISSUE', 'CONSUME', 'P2P', 'ADJUST')),
  amount BIGINT NOT NULL CHECK (amount > 0),
  status VARCHAR(24) NOT NULL CHECK (status IN ('INIT', 'PROCESSING', 'PAY_SUCCESS', 'RECV_SUCCESS', 'FAILED')),

  refund_of_txn_no UUID,
  refundable_amount BIGINT NOT NULL DEFAULT 0 CHECK (refundable_amount >= 0),

  error_code VARCHAR(64),
  error_msg VARCHAR(512),

  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,

  UNIQUE (merchant_id, out_trade_no),
  CHECK (
    (biz_type = 'TRANSFER' AND transfer_scene IS NOT NULL)
    OR
    (biz_type = 'REFUND' AND transfer_scene IS NULL)
  )
);

CREATE INDEX idx_txn_status_updated ON txn(status, updated_at);
CREATE INDEX idx_txn_refund_of ON txn(refund_of_txn_no);
CREATE INDEX idx_txn_merchant_created ON txn(merchant_id, created_at);
```

## 2.8 txn_detail

```sql
CREATE TABLE txn_detail (
  id BIGSERIAL PRIMARY KEY,
  txn_no UUID NOT NULL REFERENCES txn(txn_no),
  debit_account_no VARCHAR(32) NOT NULL REFERENCES account(account_no),
  credit_account_no VARCHAR(32) NOT NULL REFERENCES account(account_no),
  debit_book_no UUID REFERENCES account_book(book_no),
  credit_book_no UUID REFERENCES account_book(book_no),
  amount BIGINT NOT NULL CHECK (amount > 0),
  status VARCHAR(24) NOT NULL,
  refundable_amount BIGINT NOT NULL DEFAULT 0 CHECK (refundable_amount >= 0),
  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_detail_txn ON txn_detail(txn_no);
CREATE INDEX idx_detail_debit_acct ON txn_detail(debit_account_no);
CREATE INDEX idx_detail_credit_acct ON txn_detail(credit_account_no);
CREATE INDEX idx_detail_debit_book ON txn_detail(debit_book_no);
CREATE INDEX idx_detail_credit_book ON txn_detail(credit_book_no);
```

## 2.9 account_change_log

```sql
CREATE TABLE account_change_log (
  id BIGSERIAL PRIMARY KEY,
  txn_no UUID NOT NULL REFERENCES txn(txn_no),
  account_no VARCHAR(32) NOT NULL REFERENCES account(account_no),
  direction SMALLINT NOT NULL CHECK (direction IN (1, 2)), -- 1=DEBIT,2=CREDIT
  amount BIGINT NOT NULL,
  before_balance BIGINT NOT NULL,
  after_balance BIGINT NOT NULL,
  biz_type VARCHAR(32) NOT NULL,
  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_acl_txn ON account_change_log(txn_no);
CREATE INDEX idx_acl_account_created ON account_change_log(account_no, created_at);
```

## 2.10 account_book_change_log（仅过期账户）

```sql
CREATE TABLE account_book_change_log (
  id BIGSERIAL PRIMARY KEY,
  txn_no UUID NOT NULL REFERENCES txn(txn_no),
  account_no VARCHAR(32) NOT NULL REFERENCES account(account_no),
  book_no UUID NOT NULL REFERENCES account_book(book_no),
  direction SMALLINT NOT NULL CHECK (direction IN (1, 2)), -- 1=DEBIT,2=CREDIT
  amount BIGINT NOT NULL,
  before_balance BIGINT NOT NULL,
  after_balance BIGINT NOT NULL,
  target_book_no UUID,
  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_abcl_txn ON account_book_change_log(txn_no);
CREATE INDEX idx_abcl_account_created ON account_book_change_log(account_no, created_at);
CREATE INDEX idx_abcl_book_created ON account_book_change_log(book_no, created_at);
```

## 2.11 outbox_event

```sql
CREATE TABLE outbox_event (
  id BIGSERIAL PRIMARY KEY,
  event_id UUID NOT NULL UNIQUE,
  event_type VARCHAR(64) NOT NULL,
  aggregate_type VARCHAR(64) NOT NULL,
  aggregate_id VARCHAR(64) NOT NULL,
  payload_json JSONB NOT NULL,
  status SMALLINT NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2, 3)), -- 0=PENDING,1=SENT,2=FAILED,3=DEAD
  retry_count INT NOT NULL DEFAULT 0,
  next_retry_at TIMESTAMPTZ(3),
  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_outbox_status_retry ON outbox_event(status, next_retry_at);
CREATE INDEX idx_outbox_aggregate ON outbox_event(aggregate_type, aggregate_id);
CREATE INDEX idx_outbox_pending_partial ON outbox_event(next_retry_at)
  WHERE status IN (0, 2);
```

## 2.12 notify_log

```sql
CREATE TABLE notify_log (
  id BIGSERIAL PRIMARY KEY,
  txn_no UUID NOT NULL REFERENCES txn(txn_no),
  merchant_id UUID NOT NULL REFERENCES merchant(merchant_id),
  target_url VARCHAR(512) NOT NULL,
  request_body JSONB NOT NULL,
  response_code INT,
  response_body TEXT,
  status SMALLINT NOT NULL DEFAULT 0 CHECK (status IN (0, 1, 2, 3)), -- 0=PENDING,1=SUCCESS,2=FAILED,3=DEAD
  retry_count INT NOT NULL DEFAULT 0,
  next_retry_at TIMESTAMPTZ(3),
  created_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_notify_txn ON notify_log(txn_no);
CREATE INDEX idx_notify_status_retry ON notify_log(status, next_retry_at);
CREATE INDEX idx_notify_merchant_created ON notify_log(merchant_id, created_at);
```

---

## 3. 关键一致性约束（落库 + 应用双重保证）

1. `support_expiry=false` 的账户，不允许落 `account_book` 和 `account_book_change_log`（应用层强校验 + DB 触发器可选）。
2. `support_expiry=true` 的账户：
   - `account.balance` 必须与 `sum(account_book.balance)` 对齐（异步巡检 + 对账修复）。
3. 退款并发：
   - `txn.refundable_amount` 与 `txn_detail.refundable_amount` 通过 CAS 递减。
4. 幂等：
   - DB 唯一键：`UNIQUE (merchant_id, out_trade_no)`。
   - 执行幂等：Redis `processing_key=txn_no+stage`。

---

## 4. 建议的高并发更新 SQL 模板（PostgreSQL）

## 4.1 账户扣减（有限/无限透支）

```sql
UPDATE account
SET balance = balance - $1,
    version = version + 1,
    updated_at = CURRENT_TIMESTAMP
WHERE account_no = $2
  AND status = 1
  AND allow_debit_out = TRUE
  AND (
    (allow_overdraft = FALSE AND balance >= $1)
    OR
    (allow_overdraft = TRUE AND max_overdraft_limit > 0 AND balance + max_overdraft_limit >= $1)
    OR
    (allow_overdraft = TRUE AND max_overdraft_limit = 0)
  );
```

## 4.2 退款可退金额 CAS

```sql
UPDATE txn
SET refundable_amount = refundable_amount - $1,
    updated_at = CURRENT_TIMESTAMP
WHERE txn_no = $2
  AND refundable_amount >= $1;
```

---

## 5. 可选触发器（推荐）

## 5.1 自动维护 updated_at

```sql
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = CURRENT_TIMESTAMP;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
```

可应用于：`merchant`、`merchant_api_credential`、`customer`、`account`、`merchant_account_binding`、`account_book`、`txn`、`txn_detail`、`outbox_event`、`notify_log`。

## 5.2 禁止非过期账户写入 account_book（可选）

```sql
CREATE OR REPLACE FUNCTION guard_account_book_insert()
RETURNS TRIGGER AS $$
DECLARE
  v_support_expiry BOOLEAN;
BEGIN
  SELECT support_expiry INTO v_support_expiry
  FROM account WHERE account_no = NEW.account_no;

  IF v_support_expiry IS DISTINCT FROM TRUE THEN
    RAISE EXCEPTION 'account % does not support expiry, cannot write account_book', NEW.account_no;
  END IF;

  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_guard_account_book_insert
BEFORE INSERT OR UPDATE ON account_book
FOR EACH ROW
EXECUTE FUNCTION guard_account_book_insert();
```

---

## 6. 数据字典建议（枚举）

- `owner_type`: 1 CUSTOMER, 2 MERCHANT
- `account_scene`: BUDGET, RECEIVABLE, CUSTOM
- `txn.status`: INIT, PROCESSING, PAY_SUCCESS, RECV_SUCCESS, FAILED
- `txn.biz_type`: TRANSFER, REFUND
- `txn.transfer_scene`: ISSUE, CONSUME, P2P, ADJUST（仅 TRANSFER 使用）
- `outbox_event.status`: 0 PENDING, 1 SENT, 2 FAILED, 3 DEAD
- `notify_log.status`: 0 PENDING, 1 SUCCESS, 2 FAILED, 3 DEAD

---

## 7. 迁移注意事项

1. 若从 legacy `ledger_book` 命名迁移，建议在迁移脚本阶段统一重命名为 `account_book`。
2. legacy 里由账户类型推断能力的逻辑，迁移时转为 capability 字段初始化。
3. 商户密钥迁移必须只保留可解密密文（`secret_ciphertext`），禁止回填明文。当前版本统一 `kms_key_id=local_v1`。
