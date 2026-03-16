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

CREATE TABLE IF NOT EXISTS code_sequence (
  code_type VARCHAR(32) NOT NULL,
  scope_key VARCHAR(32) NOT NULL,
  next_value BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (code_type, scope_key),
  CHECK (next_value >= 0)
);

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

CREATE TABLE IF NOT EXISTS txn (
  txn_no UUID PRIMARY KEY,
  merchant_no VARCHAR(16) NOT NULL,
  out_trade_no VARCHAR(64) NOT NULL,
  biz_type VARCHAR(32) NOT NULL CHECK (biz_type IN ('TRANSFER', 'REFUND')),
  transfer_scene VARCHAR(32),
  debit_account_no VARCHAR(19),
  credit_account_no VARCHAR(19),
  amount BIGINT NOT NULL CHECK (amount > 0),
  status VARCHAR(24) NOT NULL CHECK (status IN ('INIT', 'PROCESSING', 'PAY_SUCCESS', 'RECV_SUCCESS', 'FAILED')),
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

CREATE TABLE IF NOT EXISTS account_change_log (
  change_id BIGSERIAL PRIMARY KEY,
  txn_no UUID NOT NULL,
  account_no VARCHAR(19) NOT NULL,
  delta BIGINT NOT NULL,
  balance_after BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (account_no ~ '^[0-9]{19}$')
);

CREATE INDEX IF NOT EXISTS idx_account_change_log_txn_no ON account_change_log(txn_no);
CREATE INDEX IF NOT EXISTS idx_account_change_log_account_created ON account_change_log(account_no, created_at DESC);

CREATE TABLE IF NOT EXISTS account_book (
  book_no UUID PRIMARY KEY,
  account_no VARCHAR(19) NOT NULL,
  expire_at DATE NOT NULL,
  balance BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (account_no, expire_at),
  CHECK (account_no ~ '^[0-9]{19}$')
);

CREATE TABLE IF NOT EXISTS account_book_change_log (
  change_id BIGSERIAL PRIMARY KEY,
  txn_no UUID NOT NULL,
  account_no VARCHAR(19) NOT NULL,
  book_no UUID NOT NULL,
  delta BIGINT NOT NULL,
  balance_after BIGINT NOT NULL,
  expire_at DATE NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (account_no ~ '^[0-9]{19}$')
);

CREATE INDEX IF NOT EXISTS idx_account_book_change_log_txn_no ON account_book_change_log(txn_no);
CREATE INDEX IF NOT EXISTS idx_account_book_change_log_account_created ON account_book_change_log(account_no, created_at DESC);

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

CREATE TABLE IF NOT EXISTS notify_log (
  notify_id BIGSERIAL PRIMARY KEY,
  txn_no UUID NOT NULL,
  status VARCHAR(16) NOT NULL,
  retries INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS webhook_config (
  merchant_no VARCHAR(16) PRIMARY KEY,
  url TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (merchant_no ~ '^[0-9]{16}$')
);

CREATE TABLE IF NOT EXISTS applied_change_counter (
  id SMALLINT PRIMARY KEY CHECK (id = 1),
  value BIGINT NOT NULL DEFAULT 0
);

INSERT INTO applied_change_counter(id, value)
VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;
