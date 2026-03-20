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
	if books[0].Balance != 100 || books[1].Balance != 0 || books[2].Balance != 50 {
		t.Fatalf("unexpected account_book balances: %+v", books)
	}
	if !books[0].ExpireAt.Equal(expiredAt) || !books[1].ExpireAt.Equal(expire1) || !books[2].ExpireAt.Equal(expire2) {
		t.Fatalf("unexpected account_book order/expire_at: %+v", books)
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
	if !books[0].ExpireAt.Equal(today) || books[0].Balance != 100 {
		t.Fatalf("expire_at=now_utc book should not be consumed: %+v", books[0])
	}
	if !books[1].ExpireAt.Equal(tomorrow) || books[1].Balance != 20 {
		t.Fatalf("future book should be consumed first: %+v", books[1])
	}
	bookLogs := queryBookChangesByTxnNo(t, pool, txnNo)
	if len(bookLogs) != 1 || !bookLogs[0].ExpireAt.Equal(tomorrow) || bookLogs[0].Delta != -80 {
		t.Fatalf("unexpected book change logs: %+v", bookLogs)
	}
}

func TestTC5004ExpiryCreditRequiresExpireAt(t *testing.T) {
	repo, pool, _, _, creditAccountNo, txnNo := setupPostgresTransferFixture(t, service.TxnStatusPaySuccess, 100)

	if _, err := pool.Exec(context.Background(), `
	UPDATE account
	SET book_enabled = true, balance = 0
	WHERE account_no = $1
	`, creditAccountNo); err != nil {
		t.Fatalf("enable credit book account failed: %v", err)
	}

	applied, err := repo.ApplyTransferCreditStage(txnNo, creditAccountNo, 100)
	if !errors.Is(err, service.ErrExpireAtRequired) {
		t.Fatalf("expected ErrExpireAtRequired, got applied=%v err=%v", applied, err)
	}
	if applied {
		t.Fatalf("credit stage should not apply when credit_expire_at is missing")
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
