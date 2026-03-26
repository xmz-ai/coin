DROP INDEX IF EXISTS idx_merchant_writeoff_account_no;

ALTER TABLE merchant
DROP COLUMN IF EXISTS writeoff_account_no;
