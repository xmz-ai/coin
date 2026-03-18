ALTER TABLE merchant
  DROP COLUMN IF EXISTS auto_create_account_on_customer_create,
  DROP COLUMN IF EXISTS auto_create_customer_on_credit;
