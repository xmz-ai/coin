DROP INDEX IF EXISTS idx_customer_default_account;

ALTER TABLE customer
  DROP CONSTRAINT IF EXISTS customer_default_account_no_format_check;

ALTER TABLE customer
  DROP COLUMN IF EXISTS default_account_no;
