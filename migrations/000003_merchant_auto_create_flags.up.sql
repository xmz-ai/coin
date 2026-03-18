ALTER TABLE merchant
  ADD COLUMN IF NOT EXISTS auto_create_account_on_customer_create BOOLEAN NOT NULL DEFAULT TRUE,
  ADD COLUMN IF NOT EXISTS auto_create_customer_on_credit BOOLEAN NOT NULL DEFAULT TRUE;
