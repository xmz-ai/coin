DROP INDEX IF EXISTS idx_account_change_log_account_created;

CREATE INDEX IF NOT EXISTS idx_account_change_log_account_created_change
ON account_change_log(account_no, created_at DESC, change_id DESC);
