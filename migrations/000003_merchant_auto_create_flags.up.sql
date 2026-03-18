ALTER TABLE merchant
  ADD COLUMN IF NOT EXISTS auto_create_account_on_customer_create BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS auto_create_customer_on_credit BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE merchant
  ALTER COLUMN auto_create_account_on_customer_create SET DEFAULT TRUE,
  ALTER COLUMN auto_create_customer_on_credit SET DEFAULT TRUE;
