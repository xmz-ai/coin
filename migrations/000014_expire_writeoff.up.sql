ALTER TABLE merchant
ADD COLUMN IF NOT EXISTS writeoff_account_no VARCHAR(19);

CREATE UNIQUE INDEX IF NOT EXISTS idx_merchant_writeoff_account_no
ON merchant(writeoff_account_no)
WHERE writeoff_account_no IS NOT NULL;
