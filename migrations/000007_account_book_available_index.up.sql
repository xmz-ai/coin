CREATE INDEX IF NOT EXISTS idx_account_book_available_by_account_expire
ON account_book(account_no, expire_at)
WHERE balance > 0;
