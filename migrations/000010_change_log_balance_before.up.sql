ALTER TABLE account_change_log
ADD COLUMN IF NOT EXISTS balance_before BIGINT NOT NULL DEFAULT 0;

ALTER TABLE account_change_log
ALTER COLUMN balance_before DROP DEFAULT;

ALTER TABLE account_book_change_log
ADD COLUMN IF NOT EXISTS balance_before BIGINT NOT NULL DEFAULT 0;

ALTER TABLE account_book_change_log
ALTER COLUMN balance_before DROP DEFAULT;
