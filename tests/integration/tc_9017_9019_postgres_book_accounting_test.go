package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xmz-ai/coin/internal/service"
)

type pgBookRow struct {
	BookNo    string
	ExpireAt  time.Time
	Balance   int64
	AccountNo string
}

type pgBookChange struct {
	AccountNo    string
	BookNo       string
	Delta        int64
	BalanceAfter int64
	ExpireAt     time.Time
}

func TestTC9017PostgresCreditStageBookEnabledWritesBookAndLogs(t *testing.T) {
	repo, pool, _, _, creditAccountNo, txnNo := setupPostgresTransferFixture(t, service.TxnStatusPaySuccess, 120)

	expireAt := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `
UPDATE account
SET book_enabled = true, balance = 0
WHERE account_no = $1
`, creditAccountNo); err != nil {
		t.Fatalf("enable book account failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
UPDATE txn
SET credit_expire_at = $1::date
WHERE txn_no = $2::uuid
`, expireAt, txnNo); err != nil {
		t.Fatalf("set txn credit_expire_at failed: %v", err)
	}

	applied, err := repo.ApplyTransferCreditStage(txnNo, creditAccountNo, 120)
	if err != nil {
		t.Fatalf("apply credit stage failed: %v", err)
	}
	if !applied {
		t.Fatalf("expected credit stage applied")
	}

	credit, _ := repo.GetAccount(creditAccountNo)
	if credit.Balance != 120 {
		t.Fatalf("unexpected credit balance: %d", credit.Balance)
	}

	logs := queryAccountChangesByTxnNo(t, pool, txnNo)
	if len(logs) != 1 {
		t.Fatalf("expected 1 account change log, got %d", len(logs))
	}
	if logs[0].AccountNo != creditAccountNo || logs[0].Delta != 120 || logs[0].BalanceAfter != 120 {
		t.Fatalf("unexpected account change log: %+v", logs[0])
	}

	books := queryAccountBooksByAccount(t, pool, creditAccountNo)
	if len(books) != 1 {
		t.Fatalf("expected 1 account_book row, got %d", len(books))
	}
	if books[0].Balance != 120 {
		t.Fatalf("unexpected account_book balance: %+v", books[0])
	}
	if !books[0].ExpireAt.Equal(expireAt) {
		t.Fatalf("unexpected account_book expire_at: got=%s want=%s", books[0].ExpireAt.UTC(), expireAt)
	}

	bookLogs := queryBookChangesByTxnNo(t, pool, txnNo)
	if len(bookLogs) != 1 {
		t.Fatalf("expected 1 account_book_change_log row, got %d", len(bookLogs))
	}
	if bookLogs[0].AccountNo != creditAccountNo || bookLogs[0].Delta != 120 || bookLogs[0].BalanceAfter != 120 {
		t.Fatalf("unexpected account_book_change_log row: %+v", bookLogs[0])
	}
	if !bookLogs[0].ExpireAt.Equal(expireAt) {
		t.Fatalf("unexpected account_book_change_log expire_at: got=%s want=%s", bookLogs[0].ExpireAt.UTC(), expireAt)
	}
}

func TestTC9018PostgresDebitStageBookEnabledConsumesFEFOAndWritesBookLogs(t *testing.T) {
	repo, pool, _, debitAccountNo, _, txnNo := setupPostgresTransferFixture(t, service.TxnStatusProcessing, 150)

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

	debit, _ := repo.GetAccount(debitAccountNo)
	if debit.Balance != 150 {
		t.Fatalf("unexpected debit balance: %d", debit.Balance)
	}

	logs := queryAccountChangesByTxnNo(t, pool, txnNo)
	if len(logs) != 1 {
		t.Fatalf("expected 1 account change log, got %d", len(logs))
	}
	if logs[0].AccountNo != debitAccountNo || logs[0].Delta != -150 || logs[0].BalanceAfter != 150 {
		t.Fatalf("unexpected account change log: %+v", logs[0])
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

	bookLogs := queryBookChangesByTxnNo(t, pool, txnNo)
	if len(bookLogs) != 2 {
		t.Fatalf("expected 2 account_book_change_log rows, got %d", len(bookLogs))
	}
	if bookLogs[0].Delta != -100 || bookLogs[1].Delta != -50 {
		t.Fatalf("unexpected book deltas: %+v", bookLogs)
	}
	if !bookLogs[0].ExpireAt.Equal(expire1) || !bookLogs[1].ExpireAt.Equal(expire2) {
		t.Fatalf("unexpected book expire order: %+v", bookLogs)
	}
}

func TestTC9019PostgresAsyncProcessorBookPathWritesAccountBookData(t *testing.T) {
	repo, pool, _, debitAccountNo, creditAccountNo, txnNo := setupPostgresTransferFixture(t, service.TxnStatusInit, 80)

	expireAt := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `
UPDATE account
SET book_enabled = true, balance = 0
WHERE account_no = $1
`, creditAccountNo); err != nil {
		t.Fatalf("enable book account failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
UPDATE txn
SET credit_expire_at = $1::date
WHERE txn_no = $2::uuid
`, expireAt, txnNo); err != nil {
		t.Fatalf("set txn credit_expire_at failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	if err := processor.Process(txnNo); err != nil {
		t.Fatalf("process txn failed: %v", err)
	}

	txn, ok := repo.GetTransferTxn(txnNo)
	if !ok || txn.Status != service.TxnStatusRecvSuccess {
		t.Fatalf("unexpected txn status: %+v ok=%v", txn, ok)
	}
	debit, _ := repo.GetAccount(debitAccountNo)
	credit, _ := repo.GetAccount(creditAccountNo)
	if debit.Balance != 920 || credit.Balance != 80 {
		t.Fatalf("unexpected balances: debit=%d credit=%d", debit.Balance, credit.Balance)
	}

	changeLogs := queryAccountChangesByTxnNo(t, pool, txnNo)
	if len(changeLogs) != 2 {
		t.Fatalf("expected 2 account change logs, got %d", len(changeLogs))
	}

	books := queryAccountBooksByAccount(t, pool, creditAccountNo)
	if len(books) != 1 || books[0].Balance != 80 {
		t.Fatalf("unexpected account_book rows: %+v", books)
	}
	if !books[0].ExpireAt.Equal(expireAt) {
		t.Fatalf("unexpected account_book expire_at: got=%s want=%s", books[0].ExpireAt.UTC(), expireAt)
	}

	bookLogs := queryBookChangesByTxnNo(t, pool, txnNo)
	if len(bookLogs) != 1 || bookLogs[0].Delta != 80 || bookLogs[0].BalanceAfter != 80 {
		t.Fatalf("unexpected account_book_change_log rows: %+v", bookLogs)
	}
}

func queryAccountBooksByAccount(t *testing.T, pool *pgxpool.Pool, accountNo string) []pgBookRow {
	t.Helper()

	rows, err := pool.Query(context.Background(), `
SELECT book_no::text, account_no, expire_at, balance
FROM account_book
WHERE account_no = $1
ORDER BY expire_at ASC, book_no ASC
`, accountNo)
	if err != nil {
		t.Fatalf("query account_book failed: %v", err)
	}
	defer rows.Close()

	out := make([]pgBookRow, 0)
	for rows.Next() {
		var item pgBookRow
		if err := rows.Scan(&item.BookNo, &item.AccountNo, &item.ExpireAt, &item.Balance); err != nil {
			t.Fatalf("scan account_book failed: %v", err)
		}
		item.ExpireAt = item.ExpireAt.UTC()
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate account_book failed: %v", err)
	}
	return out
}

func queryBookChangesByTxnNo(t *testing.T, pool *pgxpool.Pool, txnNo string) []pgBookChange {
	t.Helper()

	rows, err := pool.Query(context.Background(), `
SELECT account_no, book_no::text, delta, balance_after, expire_at
FROM account_book_change_log
WHERE txn_no = $1::uuid
ORDER BY change_id ASC
`, txnNo)
	if err != nil {
		t.Fatalf("query account_book_change_log failed: %v", err)
	}
	defer rows.Close()

	out := make([]pgBookChange, 0)
	for rows.Next() {
		var item pgBookChange
		if err := rows.Scan(&item.AccountNo, &item.BookNo, &item.Delta, &item.BalanceAfter, &item.ExpireAt); err != nil {
			t.Fatalf("scan account_book_change_log failed: %v", err)
		}
		item.ExpireAt = item.ExpireAt.UTC()
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate account_book_change_log failed: %v", err)
	}
	return out
}
