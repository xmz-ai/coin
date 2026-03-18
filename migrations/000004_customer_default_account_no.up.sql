ALTER TABLE customer
  ADD COLUMN IF NOT EXISTS default_account_no VARCHAR(19);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'customer_default_account_no_format_check'
  ) THEN
    ALTER TABLE customer
      ADD CONSTRAINT customer_default_account_no_format_check
      CHECK (default_account_no IS NULL OR default_account_no ~ '^[0-9]{19}$');
  END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_customer_default_account
  ON customer(merchant_no, default_account_no);
