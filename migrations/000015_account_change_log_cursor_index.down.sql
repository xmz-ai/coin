DROP INDEX IF EXISTS idx_account_change_log_account_created_change;

CREATE INDEX IF NOT EXISTS idx_account_change_log_account_created
ON account_change_log(account_no, created_at DESC);
