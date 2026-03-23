CREATE TABLE IF NOT EXISTS admin_user (
  user_id BIGSERIAL PRIMARY KEY,
  username VARCHAR(64) NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CHECK (username ~ '^[A-Za-z0-9_.-]{3,64}$')
);

CREATE TABLE IF NOT EXISTS admin_audit_log (
  audit_id BIGSERIAL PRIMARY KEY,
  request_id VARCHAR(64) NOT NULL,
  operator_username VARCHAR(64) NOT NULL,
  action VARCHAR(64) NOT NULL,
  target_type VARCHAR(32) NOT NULL,
  target_id VARCHAR(128) NOT NULL,
  merchant_no VARCHAR(16) NOT NULL DEFAULT '',
  request_payload JSONB,
  result_code VARCHAR(32) NOT NULL,
  result_message TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_admin_audit_log_created_at ON admin_audit_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_audit_log_action ON admin_audit_log(action);
CREATE INDEX IF NOT EXISTS idx_admin_audit_log_merchant_no ON admin_audit_log(merchant_no);
