ALTER TABLE account_book_change_log
DROP COLUMN IF EXISTS balance_before;

ALTER TABLE account_change_log
DROP COLUMN IF EXISTS balance_before;
