package integration

import (
	"context"
	"testing"
	"time"

	"github.com/xmz-ai/coin/internal/service"
)

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

	debit, _ := repo.GetAccount(debitAccountNo)
	if debit.Balance != 50 {
		t.Fatalf("unexpected debit balance: %d", debit.Balance)
	}

	logs := queryAccountChangesByTxnNo(t, pool, txnNo)
	if len(logs) != 1 {
		t.Fatalf("expected 1 account change log, got %d", len(logs))
	}
	if logs[0].AccountNo != debitAccountNo || logs[0].Delta != -150 || logs[0].BalanceAfter != 50 {
		t.Fatalf("unexpected account change log: %+v", logs[0])
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
	processor.Enqueue(txnNo)
	waitTxnStatusRepo(t, repo, txnNo, service.TxnStatusRecvSuccess, 2*time.Second)

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

func TestTC9021PostgresRefundBookEnabledTargetRestoresByRemainingDays(t *testing.T) {
	repo, pool, merchant, debitAccountNo, creditAccountNo, _ := setupPostgresTransferFixture(t, service.TxnStatusInit, 200)

	today := time.Now().UTC()
	originBookExpire := time.Date(today.Year(), today.Month(), today.Day()+5, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `
UPDATE account
SET book_enabled = true, balance = 200
WHERE account_no = $1
`, debitAccountNo); err != nil {
		t.Fatalf("enable debit account book failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO account_book (book_no, account_no, expire_at, balance)
VALUES ('01956f4e-9221-7922-8922-922192219221'::uuid, $1, $2::date, 200)
`, debitAccountNo, originBookExpire); err != nil {
		t.Fatalf("seed origin debit account book failed: %v", err)
	}

	originTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a9221"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            originTxnNo,
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_9021_origin",
		BizType:          service.BizTypeTransfer,
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           200,
		RefundableAmount: 200,
		Status:           service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create origin txn failed: %v", err)
	}
	applied, err := repo.ApplyTransferDebitStage(originTxnNo, debitAccountNo, 200)
	if err != nil || !applied {
		t.Fatalf("apply origin debit stage failed: applied=%v err=%v", applied, err)
	}
	applied, err = repo.ApplyTransferCreditStage(originTxnNo, creditAccountNo, 200)
	if err != nil || !applied {
		t.Fatalf("apply origin credit stage failed: applied=%v err=%v", applied, err)
	}

	originBookLogs := queryBookChangesByTxnNo(t, pool, originTxnNo)
	if len(originBookLogs) != 1 || originBookLogs[0].AccountNo != debitAccountNo || originBookLogs[0].Delta != -200 {
		t.Fatalf("unexpected origin book logs: %+v", originBookLogs)
	}

	originDebitAt := time.Date(today.Year(), today.Month(), today.Day()-10, 13, 0, 0, 0, time.UTC)
	originExpireAt := time.Date(today.Year(), today.Month(), today.Day()-2, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `
UPDATE account_book_change_log
SET created_at = $1::timestamptz, expire_at = $2::date
WHERE txn_no = $3::uuid
  AND account_no = $4
`, originDebitAt, originExpireAt, originTxnNo, debitAccountNo); err != nil {
		t.Fatalf("override origin book change timing failed: %v", err)
	}

	if _, err := pool.Exec(context.Background(), `
UPDATE account
SET book_enabled = false
WHERE account_no = $1
`, creditAccountNo); err != nil {
		t.Fatalf("ensure origin credit account non-book failed: %v", err)
	}

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a9222"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            refundTxnNo,
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_9021_refund",
		BizType:          service.BizTypeRefund,
		TransferScene:    "",
		DebitAccountNo:   creditAccountNo,
		CreditAccountNo:  debitAccountNo,
		Amount:           50,
		RefundOfTxnNo:    originTxnNo,
		RefundableAmount: 0,
		Status:           service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	processor.Enqueue(refundTxnNo)
	waitTxnStatusRepo(t, repo, refundTxnNo, service.TxnStatusRecvSuccess, 2*time.Second)

	refund, ok := repo.GetTransferTxn(refundTxnNo)
	if !ok {
		t.Fatalf("refund txn not found")
	}
	if refund.Status != service.TxnStatusRecvSuccess {
		t.Fatalf("expected RECV_SUCCESS, got %s", refund.Status)
	}

	bookLogs := queryBookChangesByTxnNo(t, pool, refundTxnNo)
	if len(bookLogs) != 1 {
		t.Fatalf("expected 1 refund account_book_change_log row, got %d", len(bookLogs))
	}
	if bookLogs[0].AccountNo != debitAccountNo || bookLogs[0].Delta != 50 {
		t.Fatalf("unexpected refund book log row: %+v", bookLogs[0])
	}

	remainingDays := int64(originExpireAt.Sub(time.Date(originDebitAt.Year(), originDebitAt.Month(), originDebitAt.Day(), 0, 0, 0, 0, time.UTC)) / (24 * time.Hour))
	if remainingDays <= 0 {
		t.Fatalf("test setup invalid remainingDays=%d", remainingDays)
	}
	wantExpireAt := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC).Add(time.Duration(remainingDays) * 24 * time.Hour)
	if !bookLogs[0].ExpireAt.Equal(wantExpireAt) {
		t.Fatalf("unexpected refund expire_at: got=%s want=%s", bookLogs[0].ExpireAt.UTC(), wantExpireAt.UTC())
	}
	if !bookLogs[0].ExpireAt.After(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("refund expire_at should be after today, got=%s", bookLogs[0].ExpireAt.UTC())
	}
	if !originExpireAt.Before(time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("test setup invalid: originExpireAt should be in past, got=%s", originExpireAt.UTC())
	}
}

func TestTC9022PostgresRefundBookEnabledTargetMissingOriginBookTraceFailed(t *testing.T) {
	repo, pool, merchant, debitAccountNo, creditAccountNo, _ := setupPostgresTransferFixture(t, service.TxnStatusInit, 200)

	originTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a9223"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            originTxnNo,
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_9022_origin",
		BizType:          service.BizTypeTransfer,
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           200,
		RefundableAmount: 200,
		Status:           service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create origin txn failed: %v", err)
	}
	applied, err := repo.ApplyTransferDebitStage(originTxnNo, debitAccountNo, 200)
	if err != nil || !applied {
		t.Fatalf("apply origin debit stage failed: applied=%v err=%v", applied, err)
	}
	applied, err = repo.ApplyTransferCreditStage(originTxnNo, creditAccountNo, 200)
	if err != nil || !applied {
		t.Fatalf("apply origin credit stage failed: applied=%v err=%v", applied, err)
	}

	if _, err := pool.Exec(context.Background(), `
UPDATE account
SET book_enabled = true
WHERE account_no = $1
`, debitAccountNo); err != nil {
		t.Fatalf("enable origin debit account book failed: %v", err)
	}

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a9224"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            refundTxnNo,
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_9022_refund",
		BizType:          service.BizTypeRefund,
		TransferScene:    "",
		DebitAccountNo:   creditAccountNo,
		CreditAccountNo:  debitAccountNo,
		Amount:           50,
		RefundOfTxnNo:    originTxnNo,
		RefundableAmount: 0,
		Status:           service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	processor.Enqueue(refundTxnNo)
	waitTxnStatusRepo(t, repo, refundTxnNo, service.TxnStatusPaySuccess, 2*time.Second)

	refund, ok := repo.GetTransferTxn(refundTxnNo)
	if !ok {
		t.Fatalf("refund txn not found")
	}
	if refund.Status != service.TxnStatusPaySuccess {
		t.Fatalf("expected PAY_SUCCESS status for compensatable refund, got %s", refund.Status)
	}
	if refund.ErrorCode != "" {
		t.Fatalf("expected empty error_code while staying in PAY_SUCCESS, got %s", refund.ErrorCode)
	}

	bookLogs := queryBookChangesByTxnNo(t, pool, refundTxnNo)
	if len(bookLogs) != 0 {
		t.Fatalf("expected no account_book_change_log on blocked refund credit, got %d", len(bookLogs))
	}
}

func TestTC9023PostgresTransferBookToBookWritesBidirectionalBookLogs(t *testing.T) {
	repo, pool, _, debitAccountNo, creditAccountNo, txnNo := setupPostgresTransferFixture(t, service.TxnStatusInit, 120)

	today := time.Now().UTC()
	debitExpire1 := time.Date(today.Year(), today.Month(), today.Day()+1, 0, 0, 0, 0, time.UTC)
	debitExpire2 := time.Date(today.Year(), today.Month(), today.Day()+2, 0, 0, 0, 0, time.UTC)
	creditSeedExpire := time.Date(today.Year(), today.Month(), today.Day()+3, 0, 0, 0, 0, time.UTC)
	creditExpire := time.Date(today.Year(), today.Month(), today.Day()+9, 0, 0, 0, 0, time.UTC)

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
  ('01956f4e-9223-7923-8923-922392239231'::uuid, $1, $2::date, 70),
  ('01956f4e-9223-7923-8923-922392239232'::uuid, $1, $3::date, 90)
`, debitAccountNo, debitExpire1, debitExpire2); err != nil {
		t.Fatalf("seed debit account books failed: %v", err)
	}

	if _, err := pool.Exec(context.Background(), `
UPDATE account
SET book_enabled = true, balance = 50
WHERE account_no = $1
`, creditAccountNo); err != nil {
		t.Fatalf("enable credit book account failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO account_book (book_no, account_no, expire_at, balance)
VALUES ('01956f4e-9223-7923-8923-922392239233'::uuid, $1, $2::date, 50)
`, creditAccountNo, creditSeedExpire); err != nil {
		t.Fatalf("seed credit account books failed: %v", err)
	}

	if _, err := pool.Exec(context.Background(), `
UPDATE txn
SET credit_expire_at = $1::date
WHERE txn_no = $2::uuid
`, creditExpire, txnNo); err != nil {
		t.Fatalf("set txn credit_expire_at failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	processor.Enqueue(txnNo)
	waitTxnStatusRepo(t, repo, txnNo, service.TxnStatusRecvSuccess, 2*time.Second)

	debit, _ := repo.GetAccount(debitAccountNo)
	credit, _ := repo.GetAccount(creditAccountNo)
	if debit.Balance != 40 {
		t.Fatalf("unexpected debit balance: %d", debit.Balance)
	}
	if credit.Balance != 170 {
		t.Fatalf("unexpected credit balance: %d", credit.Balance)
	}

	debitBooks := queryAccountBooksByAccount(t, pool, debitAccountNo)
	if len(debitBooks) != 2 {
		t.Fatalf("expected 2 debit account_book rows, got %d", len(debitBooks))
	}
	if debitBooks[0].Balance != 0 || debitBooks[1].Balance != 40 {
		t.Fatalf("unexpected debit account_book balances: %+v", debitBooks)
	}
	if !debitBooks[0].ExpireAt.Equal(debitExpire1) || !debitBooks[1].ExpireAt.Equal(debitExpire2) {
		t.Fatalf("unexpected debit account_book expire_at: %+v", debitBooks)
	}

	creditBooks := queryAccountBooksByAccount(t, pool, creditAccountNo)
	if len(creditBooks) != 3 {
		t.Fatalf("expected 3 credit account_book rows, got %d", len(creditBooks))
	}
	if creditBooks[0].Balance != 70 || creditBooks[1].Balance != 50 || creditBooks[2].Balance != 50 {
		t.Fatalf("unexpected credit account_book balances: %+v", creditBooks)
	}
	if !creditBooks[0].ExpireAt.Equal(debitExpire1) || !creditBooks[1].ExpireAt.Equal(debitExpire2) || !creditBooks[2].ExpireAt.Equal(creditSeedExpire) {
		t.Fatalf("unexpected credit account_book expire_at: %+v", creditBooks)
	}
	for _, item := range creditBooks {
		if item.ExpireAt.Equal(creditExpire) {
			t.Fatalf("book->book transfer should ignore credit_expire_at, got credit account_book rows=%+v", creditBooks)
		}
	}

	bookLogs := queryBookChangesByTxnNo(t, pool, txnNo)
	if len(bookLogs) != 4 {
		t.Fatalf("expected 4 account_book_change_log rows, got %d", len(bookLogs))
	}

	var debitBookDelta, creditBookDelta int64
	var debitBookRows, creditBookRows int
	creditExpiries := make([]time.Time, 0, 2)
	for _, item := range bookLogs {
		if item.AccountNo == debitAccountNo {
			debitBookRows++
			debitBookDelta += item.Delta
			continue
		}
		if item.AccountNo == creditAccountNo {
			creditBookRows++
			creditBookDelta += item.Delta
			creditExpiries = append(creditExpiries, item.ExpireAt)
		}
	}
	if debitBookRows != 2 || debitBookDelta != -120 {
		t.Fatalf("unexpected debit book logs rows=%d delta=%d logs=%+v", debitBookRows, debitBookDelta, bookLogs)
	}
	if creditBookRows != 2 || creditBookDelta != 120 {
		t.Fatalf("unexpected credit book logs rows=%d delta=%d logs=%+v", creditBookRows, creditBookDelta, bookLogs)
	}
	if len(creditExpiries) != 2 || !creditExpiries[0].Equal(debitExpire1) || !creditExpiries[1].Equal(debitExpire2) {
		t.Fatalf("unexpected credit book log expire_at list=%+v", creditExpiries)
	}
}

func TestTC9024PostgresRefundBookToBookWritesBidirectionalBookLogs(t *testing.T) {
	repo, pool, merchant, debitAccountNo, creditAccountNo, _ := setupPostgresTransferFixture(t, service.TxnStatusInit, 120)

	today := time.Now().UTC()
	originDebitExpire := time.Date(today.Year(), today.Month(), today.Day()+5, 0, 0, 0, 0, time.UTC)
	refundDebitExpire := time.Date(today.Year(), today.Month(), today.Day()+3, 0, 0, 0, 0, time.UTC)
	originCreditExpire := time.Date(today.Year(), today.Month(), today.Day()+7, 0, 0, 0, 0, time.UTC)

	if _, err := pool.Exec(context.Background(), `
UPDATE account
SET book_enabled = true, balance = 200
WHERE account_no = $1
`, debitAccountNo); err != nil {
		t.Fatalf("enable debit account book failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO account_book (book_no, account_no, expire_at, balance)
VALUES ('01956f4e-9224-7924-8924-922492249241'::uuid, $1, $2::date, 200)
`, debitAccountNo, originDebitExpire); err != nil {
		t.Fatalf("seed debit account book failed: %v", err)
	}

	if _, err := pool.Exec(context.Background(), `
UPDATE account
SET book_enabled = true, balance = 40
WHERE account_no = $1
`, creditAccountNo); err != nil {
		t.Fatalf("enable credit account book failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
INSERT INTO account_book (book_no, account_no, expire_at, balance)
VALUES ('01956f4e-9224-7924-8924-922492249242'::uuid, $1, $2::date, 40)
`, creditAccountNo, refundDebitExpire); err != nil {
		t.Fatalf("seed credit account book failed: %v", err)
	}

	originTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a9225"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            originTxnNo,
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_9024_origin",
		BizType:          service.BizTypeTransfer,
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           120,
		RefundableAmount: 120,
		Status:           service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create origin txn failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
UPDATE txn
SET credit_expire_at = $1::date
WHERE txn_no = $2::uuid
`, originCreditExpire, originTxnNo); err != nil {
		t.Fatalf("set origin credit_expire_at failed: %v", err)
	}

	applied, err := repo.ApplyTransferDebitStage(originTxnNo, debitAccountNo, 120)
	if err != nil || !applied {
		t.Fatalf("apply origin debit stage failed: applied=%v err=%v", applied, err)
	}
	applied, err = repo.ApplyTransferCreditStage(originTxnNo, creditAccountNo, 120)
	if err != nil || !applied {
		t.Fatalf("apply origin credit stage failed: applied=%v err=%v", applied, err)
	}

	originDebitAt := time.Date(today.Year(), today.Month(), today.Day()-2, 13, 0, 0, 0, time.UTC)
	originDebitLogExpire := time.Date(today.Year(), today.Month(), today.Day()+3, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(context.Background(), `
UPDATE account_book_change_log
SET created_at = $1::timestamptz, expire_at = $2::date
WHERE txn_no = $3::uuid
  AND account_no = $4
  AND delta < 0
`, originDebitAt, originDebitLogExpire, originTxnNo, debitAccountNo); err != nil {
		t.Fatalf("override origin debit book trace failed: %v", err)
	}

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a9226"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            refundTxnNo,
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_9024_refund",
		BizType:          service.BizTypeRefund,
		TransferScene:    "",
		DebitAccountNo:   creditAccountNo,
		CreditAccountNo:  debitAccountNo,
		Amount:           70,
		RefundOfTxnNo:    originTxnNo,
		RefundableAmount: 0,
		Status:           service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	processor.Enqueue(refundTxnNo)
	waitTxnStatusRepo(t, repo, refundTxnNo, service.TxnStatusRecvSuccess, 2*time.Second)

	refund, ok := repo.GetTransferTxn(refundTxnNo)
	if !ok {
		t.Fatalf("refund txn not found")
	}
	if refund.Status != service.TxnStatusRecvSuccess {
		t.Fatalf("expected RECV_SUCCESS, got %s", refund.Status)
	}

	origin, ok := repo.GetTransferTxn(originTxnNo)
	if !ok {
		t.Fatalf("origin txn not found")
	}
	if origin.RefundableAmount != 50 {
		t.Fatalf("expected origin refundable_amount=50, got %d", origin.RefundableAmount)
	}

	debit, _ := repo.GetAccount(debitAccountNo)
	credit, _ := repo.GetAccount(creditAccountNo)
	if debit.Balance != 150 {
		t.Fatalf("unexpected debit balance after refund: %d", debit.Balance)
	}
	if credit.Balance != 90 {
		t.Fatalf("unexpected credit balance after refund: %d", credit.Balance)
	}

	bookLogs := queryBookChangesByTxnNo(t, pool, refundTxnNo)
	if len(bookLogs) < 2 {
		t.Fatalf("expected refund to write both-side book logs, got %d", len(bookLogs))
	}

	var debitBookDelta, creditBookDelta int64
	var debitBookRows, creditBookRows int
	var debitCreditExpireAt time.Time
	for _, item := range bookLogs {
		if item.AccountNo == debitAccountNo {
			debitBookRows++
			debitBookDelta += item.Delta
			debitCreditExpireAt = item.ExpireAt
			continue
		}
		if item.AccountNo == creditAccountNo {
			creditBookRows++
			creditBookDelta += item.Delta
		}
	}
	if debitBookRows == 0 || debitBookDelta != 70 {
		t.Fatalf("unexpected refund credit-side book logs rows=%d delta=%d logs=%+v", debitBookRows, debitBookDelta, bookLogs)
	}
	if creditBookRows == 0 || creditBookDelta != -70 {
		t.Fatalf("unexpected refund debit-side book logs rows=%d delta=%d logs=%+v", creditBookRows, creditBookDelta, bookLogs)
	}

	remainingDays := int64(originDebitLogExpire.Sub(time.Date(originDebitAt.Year(), originDebitAt.Month(), originDebitAt.Day(), 0, 0, 0, 0, time.UTC)) / (24 * time.Hour))
	if remainingDays <= 0 {
		t.Fatalf("test setup invalid remainingDays=%d", remainingDays)
	}
	wantExpireAt := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC).Add(time.Duration(remainingDays) * 24 * time.Hour)
	if !debitCreditExpireAt.Equal(wantExpireAt) {
		t.Fatalf("unexpected refund credit expire_at: got=%s want=%s", debitCreditExpireAt.UTC(), wantExpireAt.UTC())
	}
}
