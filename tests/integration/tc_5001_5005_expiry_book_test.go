package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xmz-ai/coin/internal/service"
)

func TestTC5001NonExpiryAccountCannotWriteBook(t *testing.T) {
	repo, pool, _, _, creditAccountNo, txnNo := setupPostgresTransferFixture(t, service.TxnStatusPaySuccess, 100)
	expireAt := time.Now().UTC().Add(24 * time.Hour)

	if _, err := pool.Exec(context.Background(), `
	UPDATE account
	SET book_enabled = false, balance = 200
	WHERE account_no = $1
	`, creditAccountNo); err != nil {
		t.Fatalf("set non-book account failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
	UPDATE txn
	SET credit_expire_at = $1::date
	WHERE txn_no = $2::uuid
	`, expireAt, txnNo); err != nil {
		t.Fatalf("set txn credit_expire_at failed: %v", err)
	}

	applied, err := repo.ApplyTransferCreditStage(txnNo, creditAccountNo, 100)
	if err != nil {
		t.Fatalf("apply credit stage failed: %v", err)
	}
	if !applied {
		t.Fatalf("expected credit stage applied")
	}

	credit, _ := repo.GetAccount(creditAccountNo)
	if credit.Balance != 300 {
		t.Fatalf("unexpected credit balance: %d", credit.Balance)
	}
	books := queryAccountBooksByAccount(t, pool, creditAccountNo)
	if len(books) != 0 {
		t.Fatalf("non-book account should not write account_book rows, got %+v", books)
	}
	bookLogs := queryBookChangesByTxnNo(t, pool, txnNo)
	if len(bookLogs) != 0 {
		t.Fatalf("non-book account should not write account_book_change_log rows, got %+v", bookLogs)
	}
}

func TestTC5002FEFODebitOrderAndOnlyExpireAfterNow(t *testing.T) {
	repo, pool, _, debitAccountNo, _, txnNo := setupPostgresTransferFixture(t, service.TxnStatusInit, 150)
	today := time.Now().UTC()
	expiredAt := time.Date(today.Year(), today.Month(), today.Day()-1, 0, 0, 0, 0, time.UTC)
	expire1 := time.Date(today.Year(), today.Month(), today.Day()+1, 0, 0, 0, 0, time.UTC)
	expire2 := time.Date(today.Year(), today.Month(), today.Day()+2, 0, 0, 0, 0, time.UTC)

	if _, err := pool.Exec(context.Background(), `
	UPDATE account
	SET book_enabled = true, balance = 300
	WHERE account_no = $1
	`, debitAccountNo); err != nil {
		t.Fatalf("enable debit book account failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
	INSERT INTO account_book (book_no, account_no, expire_at, balance)
	VALUES
	  ('01956f4e-aaaa-7aaa-8aaa-aaaaaaaaaaa1'::uuid, $1, $2::date, 100),
	  ('01956f4e-bbbb-7bbb-8bbb-bbbbbbbbbbb2'::uuid, $1, $3::date, 100),
	  ('01956f4e-cccc-7ccc-8ccc-ccccccccccc3'::uuid, $1, $4::date, 100)
	`, debitAccountNo, expiredAt, expire1, expire2); err != nil {
		t.Fatalf("insert account_book seed rows failed: %v", err)
	}

	applied, err := repo.ApplyTransferDebitStage(txnNo, debitAccountNo, 150)
	if err != nil {
		t.Fatalf("apply debit stage failed: %v", err)
	}
	if !applied {
		t.Fatalf("expected debit stage applied")
	}

	books := queryAccountBooksByAccount(t, pool, debitAccountNo)
	if len(books) != 3 {
		t.Fatalf("expected 3 account_book rows, got %d", len(books))
	}
	if books[0].Balance != 0 || books[1].Balance != 0 || books[2].Balance != 50 {
		t.Fatalf("unexpected account_book balances: %+v", books)
	}
	if !books[0].ExpireAt.Equal(expiredAt) || !books[1].ExpireAt.Equal(expire1) || !books[2].ExpireAt.Equal(expire2) {
		t.Fatalf("unexpected account_book order/expire_at: %+v", books)
	}
	debit, ok := repo.GetAccount(debitAccountNo)
	if !ok {
		t.Fatalf("debit account not found")
	}
	if debit.Balance != 50 {
		t.Fatalf("unexpected debit balance after writeoff and debit: %d", debit.Balance)
	}
}

func TestTC5003ExpireAtEqualNowExcluded(t *testing.T) {
	repo, pool, _, debitAccountNo, _, txnNo := setupPostgresTransferFixture(t, service.TxnStatusInit, 80)
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	tomorrow := today.Add(24 * time.Hour)

	if _, err := pool.Exec(context.Background(), `
	UPDATE account
	SET book_enabled = true, balance = 200
	WHERE account_no = $1
	`, debitAccountNo); err != nil {
		t.Fatalf("enable debit book account failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
	INSERT INTO account_book (book_no, account_no, expire_at, balance)
	VALUES
	  ('01956f4e-dddd-7ddd-8ddd-ddddddddddd4'::uuid, $1, $2::date, 100),
	  ('01956f4e-eeee-7eee-8eee-eeeeeeeeeee5'::uuid, $1, $3::date, 100)
	`, debitAccountNo, today, tomorrow); err != nil {
		t.Fatalf("insert account_book seed rows failed: %v", err)
	}

	applied, err := repo.ApplyTransferDebitStage(txnNo, debitAccountNo, 80)
	if err != nil {
		t.Fatalf("apply debit stage failed: %v", err)
	}
	if !applied {
		t.Fatalf("expected debit stage applied")
	}

	books := queryAccountBooksByAccount(t, pool, debitAccountNo)
	if len(books) != 2 {
		t.Fatalf("expected 2 account_book rows, got %d", len(books))
	}
	if !books[0].ExpireAt.Equal(today) || books[0].Balance != 0 {
		t.Fatalf("expire_at=now_utc book should be written off before consume: %+v", books[0])
	}
	if !books[1].ExpireAt.Equal(tomorrow) || books[1].Balance != 20 {
		t.Fatalf("future book should be consumed first: %+v", books[1])
	}
	bookLogs := queryBookChangesByTxnNo(t, pool, txnNo)
	if len(bookLogs) != 1 || !bookLogs[0].ExpireAt.Equal(tomorrow) || bookLogs[0].Delta != -80 {
		t.Fatalf("unexpected book change logs: %+v", bookLogs)
	}
}

func TestTC5006ExpiredBooksWriteOffBeforeDebit(t *testing.T) {
	repo, pool, merchant, debitAccountNo, _, txnNo := setupPostgresTransferFixture(t, service.TxnStatusInit, 150)
	today := time.Now().UTC()
	expiredAt := time.Date(today.Year(), today.Month(), today.Day()-1, 0, 0, 0, 0, time.UTC)
	expire1 := time.Date(today.Year(), today.Month(), today.Day()+1, 0, 0, 0, 0, time.UTC)
	expire2 := time.Date(today.Year(), today.Month(), today.Day()+2, 0, 0, 0, 0, time.UTC)

	if _, err := pool.Exec(context.Background(), `
	UPDATE account
	SET book_enabled = true, balance = 300
	WHERE account_no = $1
	`, debitAccountNo); err != nil {
		t.Fatalf("enable debit book account failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
	INSERT INTO account_book (book_no, account_no, expire_at, balance)
	VALUES
	  ('01956f4e-1111-7111-8111-111111111111'::uuid, $1, $2::date, 100),
	  ('01956f4e-2222-7222-8222-222222222222'::uuid, $1, $3::date, 100),
	  ('01956f4e-3333-7333-8333-333333333333'::uuid, $1, $4::date, 100)
	`, debitAccountNo, expiredAt, expire1, expire2); err != nil {
		t.Fatalf("insert account_book seed rows failed: %v", err)
	}

	applied, err := repo.ApplyTransferDebitStage(txnNo, debitAccountNo, 150)
	if err != nil {
		t.Fatalf("apply debit stage failed: %v", err)
	}
	if !applied {
		t.Fatalf("expected debit stage applied")
	}

	merchantCfg, ok := repo.GetMerchantByNo(merchant.MerchantNo)
	if !ok {
		t.Fatalf("merchant not found")
	}
	writeoffAccount, ok := repo.GetAccount(merchantCfg.WriteoffAccountNo)
	if !ok {
		t.Fatalf("writeoff account not found")
	}
	if writeoffAccount.Balance != 100 {
		t.Fatalf("unexpected writeoff account balance: %d", writeoffAccount.Balance)
	}
	debit, ok := repo.GetAccount(debitAccountNo)
	if !ok {
		t.Fatalf("debit account not found")
	}
	if debit.Balance != 50 {
		t.Fatalf("unexpected debit balance: %d", debit.Balance)
	}

	books := queryAccountBooksByAccount(t, pool, debitAccountNo)
	if len(books) != 3 || books[0].Balance != 0 || books[1].Balance != 0 || books[2].Balance != 50 {
		t.Fatalf("unexpected book balances after writeoff: %+v", books)
	}

	var writeoffTxnNo string
	var amount int64
	var debitNo string
	var creditNo string
	if err := pool.QueryRow(context.Background(), `
	SELECT txn_no::text, amount, COALESCE(debit_account_no, ''), COALESCE(credit_account_no, '')
	FROM txn
	WHERE merchant_no = $1
	  AND transfer_scene = $2
	ORDER BY created_at DESC
	LIMIT 1
	`, merchant.MerchantNo, service.SceneExpireWriteoff).Scan(&writeoffTxnNo, &amount, &debitNo, &creditNo); err != nil {
		t.Fatalf("query writeoff txn failed: %v", err)
	}
	if amount != 100 || debitNo != debitAccountNo || creditNo != merchantCfg.WriteoffAccountNo {
		t.Fatalf("unexpected writeoff txn: txn_no=%s amount=%d debit=%s credit=%s", writeoffTxnNo, amount, debitNo, creditNo)
	}
}

func TestTC5007AvailableBalanceExcludesExpiredBeforeWriteoff(t *testing.T) {
	repo, pool, _, debitAccountNo, _, _ := setupPostgresTransferFixture(t, service.TxnStatusInit, 10)
	today := time.Now().UTC()
	expiredAt := time.Date(today.Year(), today.Month(), today.Day()-1, 0, 0, 0, 0, time.UTC)
	expire1 := time.Date(today.Year(), today.Month(), today.Day()+1, 0, 0, 0, 0, time.UTC)

	if _, err := pool.Exec(context.Background(), `
	UPDATE account
	SET book_enabled = true, balance = 200
	WHERE account_no = $1
	`, debitAccountNo); err != nil {
		t.Fatalf("enable debit book account failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
	INSERT INTO account_book (book_no, account_no, expire_at, balance)
	VALUES
	  ('01956f4e-4444-7444-8444-444444444444'::uuid, $1, $2::date, 80),
	  ('01956f4e-5555-7555-8555-555555555555'::uuid, $1, $3::date, 120)
	`, debitAccountNo, expiredAt, expire1); err != nil {
		t.Fatalf("insert account_book seed rows failed: %v", err)
	}

	account, ok := repo.GetAccount(debitAccountNo)
	if !ok {
		t.Fatalf("debit account not found")
	}
	if account.Balance != 200 {
		t.Fatalf("unexpected total balance: %d", account.Balance)
	}
	availableBalance, found, err := repo.GetAvailableBalance(debitAccountNo)
	if err != nil {
		t.Fatalf("get available balance failed: %v", err)
	}
	if !found {
		t.Fatalf("expected account found")
	}
	if availableBalance != 120 {
		t.Fatalf("unexpected available balance: %d", availableBalance)
	}
}

func TestTC5004ExpiryCreditWithoutExpireAtUsesNoExpireBook(t *testing.T) {
	repo, pool, _, _, creditAccountNo, txnNo := setupPostgresTransferFixture(t, service.TxnStatusPaySuccess, 100)
	noExpire := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

	if _, err := pool.Exec(context.Background(), `
	UPDATE account
	SET book_enabled = true, balance = 0
	WHERE account_no = $1
	`, creditAccountNo); err != nil {
		t.Fatalf("enable credit book account failed: %v", err)
	}

	applied, err := repo.ApplyTransferCreditStage(txnNo, creditAccountNo, 100)
	if err != nil {
		t.Fatalf("expected success, got applied=%v err=%v", applied, err)
	}
	if !applied {
		t.Fatalf("credit stage should apply when credit_expire_at is missing")
	}

	account, ok := repo.GetAccount(creditAccountNo)
	if !ok {
		t.Fatalf("credit account not found")
	}
	if account.Balance != 100 {
		t.Fatalf("unexpected account balance: got=%d want=%d", account.Balance, 100)
	}
	books := queryAccountBooksByAccount(t, pool, creditAccountNo)
	if len(books) != 1 {
		t.Fatalf("expected 1 no-expire account_book row, got %d rows=%+v", len(books), books)
	}
	if !books[0].ExpireAt.Equal(noExpire) || books[0].Balance != 100 {
		t.Fatalf("unexpected no-expire account_book row: %+v", books[0])
	}
	bookLogs := queryBookChangesByTxnNo(t, pool, txnNo)
	if len(bookLogs) != 1 {
		t.Fatalf("expected 1 account_book_change_log row, got %d", len(bookLogs))
	}
	if !bookLogs[0].ExpireAt.Equal(noExpire) || bookLogs[0].Delta != 100 || bookLogs[0].BalanceAfter != 100 {
		t.Fatalf("unexpected no-expire account_book_change_log row: %+v", bookLogs[0])
	}
}

func TestTC5005AccountBalanceEqualsBookSum(t *testing.T) {
	repo, pool, _, debitAccountNo, _, txnNo := setupPostgresTransferFixture(t, service.TxnStatusInit, 30)
	now := time.Now().UTC()
	expire1 := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	expire2 := time.Date(now.Year(), now.Month(), now.Day()+2, 0, 0, 0, 0, time.UTC)

	if _, err := pool.Exec(context.Background(), `
	UPDATE account
	SET book_enabled = true, balance = 160
	WHERE account_no = $1
	`, debitAccountNo); err != nil {
		t.Fatalf("enable debit book account failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
	INSERT INTO account_book (book_no, account_no, expire_at, balance)
	VALUES
	  ('01956f4e-ffff-7fff-8fff-fffffffffff6'::uuid, $1, $2::date, 100),
	  ('01956f4e-9999-7999-8999-999999999997'::uuid, $1, $3::date, 60)
	`, debitAccountNo, expire1, expire2); err != nil {
		t.Fatalf("insert account_book seed rows failed: %v", err)
	}

	applied, err := repo.ApplyTransferDebitStage(txnNo, debitAccountNo, 30)
	if err != nil {
		t.Fatalf("apply debit stage failed: %v", err)
	}
	if !applied {
		t.Fatalf("expected debit stage applied")
	}

	account, ok := repo.GetAccount(debitAccountNo)
	if !ok {
		t.Fatalf("account not found: %s", debitAccountNo)
	}
	books := queryAccountBooksByAccount(t, pool, debitAccountNo)
	sum := int64(0)
	for _, book := range books {
		sum += book.Balance
	}
	if account.Balance != sum {
		t.Fatalf("expected account.balance == sum(account_book.balance), got account=%d sum=%d books=%+v", account.Balance, sum, books)
	}
}

func TestTC5006BookDebitFallsBackToNoExpireBookWhenAllowOverdraft(t *testing.T) {
	repo, pool, _, debitAccountNo, _, txnNo := setupPostgresTransferFixture(t, service.TxnStatusInit, 80)
	noExpire := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

	if _, err := pool.Exec(context.Background(), `
	UPDATE account
	SET book_enabled = true, allow_overdraft = true, max_overdraft_limit = 100, balance = 0
	WHERE account_no = $1
	`, debitAccountNo); err != nil {
		t.Fatalf("enable debit book overdraft account failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
	INSERT INTO account_book (book_no, account_no, expire_at, balance)
	VALUES ('01956f4e-5006-7500-8500-500650065006'::uuid, $1, $2::date, 0)
	`, debitAccountNo, noExpire); err != nil {
		t.Fatalf("insert no-expire account_book failed: %v", err)
	}

	applied, err := repo.ApplyTransferDebitStage(txnNo, debitAccountNo, 80)
	if err != nil {
		t.Fatalf("apply debit stage failed: %v", err)
	}
	if !applied {
		t.Fatalf("expected debit stage applied")
	}

	account, ok := repo.GetAccount(debitAccountNo)
	if !ok {
		t.Fatalf("debit account not found")
	}
	if account.Balance != -80 {
		t.Fatalf("unexpected account balance after overdraft debit: got=%d want=%d", account.Balance, -80)
	}
	books := queryAccountBooksByAccount(t, pool, debitAccountNo)
	if len(books) != 1 {
		t.Fatalf("expected only no-expire account_book row, got %d rows=%+v", len(books), books)
	}
	if !books[0].ExpireAt.Equal(noExpire) || books[0].Balance != -80 {
		t.Fatalf("unexpected no-expire book row after overdraft debit: %+v", books[0])
	}
	bookLogs := queryBookChangesByTxnNo(t, pool, txnNo)
	if len(bookLogs) != 1 {
		t.Fatalf("expected 1 account_book_change_log row, got %d", len(bookLogs))
	}
	if !bookLogs[0].ExpireAt.Equal(noExpire) || bookLogs[0].Delta != -80 || bookLogs[0].BalanceAfter != -80 {
		t.Fatalf("unexpected no-expire book change row: %+v", bookLogs[0])
	}
}

func TestTC5007BookDebitInsufficientWithoutOverdraftEvenWithNoExpireBook(t *testing.T) {
	repo, pool, _, debitAccountNo, _, txnNo := setupPostgresTransferFixture(t, service.TxnStatusInit, 1)
	noExpire := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

	if _, err := pool.Exec(context.Background(), `
	UPDATE account
	SET book_enabled = true, allow_overdraft = false, max_overdraft_limit = 0, balance = 0
	WHERE account_no = $1
	`, debitAccountNo); err != nil {
		t.Fatalf("enable debit book non-overdraft account failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
	INSERT INTO account_book (book_no, account_no, expire_at, balance)
	VALUES ('01956f4e-5007-7500-8500-500750075007'::uuid, $1, $2::date, 0)
	`, debitAccountNo, noExpire); err != nil {
		t.Fatalf("insert no-expire account_book failed: %v", err)
	}

	applied, err := repo.ApplyTransferDebitStage(txnNo, debitAccountNo, 1)
	if !errors.Is(err, service.ErrInsufficientBalance) {
		t.Fatalf("expected ErrInsufficientBalance, got applied=%v err=%v", applied, err)
	}
	if applied {
		t.Fatalf("debit stage should not apply")
	}

	account, ok := repo.GetAccount(debitAccountNo)
	if !ok {
		t.Fatalf("debit account not found")
	}
	if account.Balance != 0 {
		t.Fatalf("unexpected account balance after failed debit: %d", account.Balance)
	}
	books := queryAccountBooksByAccount(t, pool, debitAccountNo)
	if len(books) != 1 || books[0].Balance != 0 || !books[0].ExpireAt.Equal(noExpire) {
		t.Fatalf("unexpected no-expire book rows after failed debit: %+v", books)
	}
	bookLogs := queryBookChangesByTxnNo(t, pool, txnNo)
	if len(bookLogs) != 0 {
		t.Fatalf("unexpected account_book_change_log rows after failed debit: %+v", bookLogs)
	}
}
