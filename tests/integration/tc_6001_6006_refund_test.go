package integration

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

func TestTC6001OriginTxnNotFoundRejected(t *testing.T) {
	repo, _, processor, merchantNo, _, debitAccountNo, creditAccountNo := setupRefundAsyncFixture(t)

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6001"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:           refundTxnNo,
		MerchantNo:      merchantNo,
		OutTradeNo:      "ord_6001_refund",
		BizType:         service.BizTypeRefund,
		DebitAccountNo:  creditAccountNo,
		CreditAccountNo: debitAccountNo,
		RefundOfTxnNo:   "01956f4e-9d22-73bc-8e11-3f5e9c7a6fff",
		Amount:          10,
		Status:          service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor.Enqueue(refundTxnNo)
	waitTxnStatusRepo(t, repo, refundTxnNo, service.TxnStatusFailed, 2*time.Second)

	refund, ok := repo.GetTransferTxn(refundTxnNo)
	if !ok {
		t.Fatalf("refund txn not found")
	}
	if refund.Status != service.TxnStatusFailed {
		t.Fatalf("expected FAILED, got %s", refund.Status)
	}
	if refund.ErrorCode != "REFUND_ORIGIN_NOT_FOUND" {
		t.Fatalf("expected REFUND_ORIGIN_NOT_FOUND, got %s", refund.ErrorCode)
	}
	events, err := repo.ClaimDueOutboxEventsByTxnNo(refundTxnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim refund outbox events failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 refund outbox event on failed refund, got %+v", events)
	}
}

func TestTC6002RefundAmountExceededRejected(t *testing.T) {
	repo, _, processor, merchantNo, originTxnNo, debitAccountNo, creditAccountNo := setupRefundAsyncFixture(t)

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6002"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:           refundTxnNo,
		MerchantNo:      merchantNo,
		OutTradeNo:      "ord_6002_refund",
		BizType:         service.BizTypeRefund,
		DebitAccountNo:  creditAccountNo,
		CreditAccountNo: debitAccountNo,
		RefundOfTxnNo:   originTxnNo,
		Amount:          120,
		Status:          service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor.Enqueue(refundTxnNo)
	waitTxnStatusRepo(t, repo, refundTxnNo, service.TxnStatusFailed, 2*time.Second)

	refund, ok := repo.GetTransferTxn(refundTxnNo)
	if !ok {
		t.Fatalf("refund txn not found")
	}
	if refund.Status != service.TxnStatusFailed {
		t.Fatalf("expected FAILED, got %s", refund.Status)
	}
	if refund.ErrorCode != "REFUND_AMOUNT_EXCEEDED" {
		t.Fatalf("expected REFUND_AMOUNT_EXCEEDED, got %s", refund.ErrorCode)
	}

	origin, ok := repo.GetTransferTxn(originTxnNo)
	if !ok {
		t.Fatalf("origin txn not found")
	}
	if origin.RefundableAmount != 100 {
		t.Fatalf("expected origin refundable_amount=100, got %d", origin.RefundableAmount)
	}
	events, err := repo.ClaimDueOutboxEventsByTxnNo(refundTxnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim refund outbox events failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 refund outbox event on failed refund, got %+v", events)
	}
}

func TestTC6003RefundStagesToRecvSuccess(t *testing.T) {
	repo, _, processor, merchantNo, originTxnNo, debitAccountNo, creditAccountNo := setupRefundAsyncFixture(t)

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6003"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:           refundTxnNo,
		MerchantNo:      merchantNo,
		OutTradeNo:      "ord_6003_refund",
		BizType:         service.BizTypeRefund,
		DebitAccountNo:  creditAccountNo,
		CreditAccountNo: debitAccountNo,
		RefundOfTxnNo:   originTxnNo,
		Amount:          30,
		Status:          service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor.Enqueue(refundTxnNo)
	waitTxnStatusRepo(t, repo, refundTxnNo, service.TxnStatusRecvSuccess, 2*time.Second)

	refund, ok := repo.GetTransferTxn(refundTxnNo)
	if !ok {
		t.Fatalf("refund txn not found")
	}
	if refund.Status != service.TxnStatusRecvSuccess {
		t.Fatalf("expected RECV_SUCCESS, got %s", refund.Status)
	}
	if refund.DebitAccountNo != creditAccountNo || refund.CreditAccountNo != debitAccountNo {
		t.Fatalf("unexpected refund parties: debit=%s credit=%s", refund.DebitAccountNo, refund.CreditAccountNo)
	}

	origin, ok := repo.GetTransferTxn(originTxnNo)
	if !ok {
		t.Fatalf("origin txn not found")
	}
	if origin.RefundableAmount != 70 {
		t.Fatalf("expected origin refundable_amount=70, got %d", origin.RefundableAmount)
	}

	events, err := repo.ClaimDueOutboxEventsByTxnNo(refundTxnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim refund outbox events failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event, got %+v", events)
	}
}

func TestTC6004RefundCrossMerchantOriginRejected(t *testing.T) {
	repo, _, processor, merchantNo, _, debitAccountNo, creditAccountNo := setupRefundAsyncFixture(t)

	otherOriginTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6400"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            otherOriginTxnNo,
		MerchantNo:       "1000000000006999",
		OutTradeNo:       "ord_6004_other_origin",
		BizType:          service.BizTypeTransfer,
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   "6217701201600000691",
		CreditAccountNo:  "6217701201600000692",
		Amount:           50,
		RefundableAmount: 50,
		Status:           service.TxnStatusRecvSuccess,
	}); err != nil {
		t.Fatalf("create other origin txn failed: %v", err)
	}

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6004"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:           refundTxnNo,
		MerchantNo:      merchantNo,
		OutTradeNo:      "ord_6004_refund",
		BizType:         service.BizTypeRefund,
		DebitAccountNo:  creditAccountNo,
		CreditAccountNo: debitAccountNo,
		RefundOfTxnNo:   otherOriginTxnNo,
		Amount:          10,
		Status:          service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor.Enqueue(refundTxnNo)
	waitTxnStatusRepo(t, repo, refundTxnNo, service.TxnStatusFailed, 2*time.Second)

	refund, ok := repo.GetTransferTxn(refundTxnNo)
	if !ok {
		t.Fatalf("refund txn not found")
	}
	if refund.Status != service.TxnStatusFailed {
		t.Fatalf("expected FAILED, got %s", refund.Status)
	}
	if refund.ErrorCode != "REFUND_ORIGIN_NOT_FOUND" {
		t.Fatalf("expected REFUND_ORIGIN_NOT_FOUND, got %s", refund.ErrorCode)
	}
	events, err := repo.ClaimDueOutboxEventsByTxnNo(refundTxnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim refund outbox events failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 refund outbox event on failed refund, got %+v", events)
	}
}

func TestTC6007RefundOriginStatusInvalidRejected(t *testing.T) {
	repo, _, processor, merchantNo, originTxnNo, debitAccountNo, creditAccountNo := setupRefundAsyncFixture(t)

	if err := repo.UpdateTransferTxnStatus(originTxnNo, service.TxnStatusPaySuccess, "", ""); err != nil {
		t.Fatalf("set origin status failed: %v", err)
	}

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6007"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:           refundTxnNo,
		MerchantNo:      merchantNo,
		OutTradeNo:      "ord_6007_refund",
		BizType:         service.BizTypeRefund,
		DebitAccountNo:  creditAccountNo,
		CreditAccountNo: debitAccountNo,
		RefundOfTxnNo:   originTxnNo,
		Amount:          10,
		Status:          service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor.Enqueue(refundTxnNo)
	waitTxnStatusRepo(t, repo, refundTxnNo, service.TxnStatusFailed, 2*time.Second)

	refund, ok := repo.GetTransferTxn(refundTxnNo)
	if !ok {
		t.Fatalf("refund txn not found")
	}
	if refund.Status != service.TxnStatusFailed {
		t.Fatalf("expected FAILED, got %s", refund.Status)
	}
	if refund.ErrorCode != "REFUND_ORIGIN_INVALID" {
		t.Fatalf("expected REFUND_ORIGIN_INVALID, got %s", refund.ErrorCode)
	}

	origin, ok := repo.GetTransferTxn(originTxnNo)
	if !ok {
		t.Fatalf("origin txn not found")
	}
	if origin.RefundableAmount != 100 {
		t.Fatalf("expected origin refundable_amount=100, got %d", origin.RefundableAmount)
	}
	events, err := repo.ClaimDueOutboxEventsByTxnNo(refundTxnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim refund outbox events failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 refund outbox event on failed refund, got %+v", events)
	}
}

func TestTC6008RefundOriginBizTypeInvalidRejected(t *testing.T) {
	repo, _, processor, merchantNo, originTxnNo, debitAccountNo, creditAccountNo := setupRefundAsyncFixture(t)

	invalidOriginTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6008"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            invalidOriginTxnNo,
		MerchantNo:       merchantNo,
		OutTradeNo:       "ord_6008_origin",
		BizType:          service.BizTypeRefund,
		RefundOfTxnNo:    originTxnNo,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           50,
		RefundableAmount: 50,
		Status:           service.TxnStatusRecvSuccess,
	}); err != nil {
		t.Fatalf("create invalid origin txn failed: %v", err)
	}

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6009"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:           refundTxnNo,
		MerchantNo:      merchantNo,
		OutTradeNo:      "ord_6008_refund",
		BizType:         service.BizTypeRefund,
		DebitAccountNo:  creditAccountNo,
		CreditAccountNo: debitAccountNo,
		RefundOfTxnNo:   invalidOriginTxnNo,
		Amount:          10,
		Status:          service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor.Enqueue(refundTxnNo)
	waitTxnStatusRepo(t, repo, refundTxnNo, service.TxnStatusFailed, 2*time.Second)

	refund, ok := repo.GetTransferTxn(refundTxnNo)
	if !ok {
		t.Fatalf("refund txn not found")
	}
	if refund.Status != service.TxnStatusFailed {
		t.Fatalf("expected FAILED, got %s", refund.Status)
	}
	if refund.ErrorCode != "REFUND_ORIGIN_INVALID" {
		t.Fatalf("expected REFUND_ORIGIN_INVALID, got %s", refund.ErrorCode)
	}

	origin, ok := repo.GetTransferTxn(invalidOriginTxnNo)
	if !ok {
		t.Fatalf("invalid origin txn not found")
	}
	if origin.RefundableAmount != 50 {
		t.Fatalf("expected invalid origin refundable_amount=50, got %d", origin.RefundableAmount)
	}
	events, err := repo.ClaimDueOutboxEventsByTxnNo(refundTxnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim refund outbox events failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 refund outbox event on failed refund, got %+v", events)
	}
}

func TestTC6005ConcurrentRefundDoesNotExceed(t *testing.T) {
	repo, _, _, merchantNo, originTxnNo, debitAccountNo, creditAccountNo := setupRefundAsyncFixture(t)

	refundTxnA := "01956f4e-9d22-73bc-8e11-3f5e9c7a6501"
	refundTxnB := "01956f4e-9d22-73bc-8e11-3f5e9c7a6502"
	for _, item := range []service.TransferTxn{
		{
			TxnNo:           refundTxnA,
			MerchantNo:      merchantNo,
			OutTradeNo:      "ord_6005_refund_a",
			BizType:         service.BizTypeRefund,
			DebitAccountNo:  creditAccountNo,
			CreditAccountNo: debitAccountNo,
			RefundOfTxnNo:   originTxnNo,
			Amount:          80,
			Status:          service.TxnStatusInit,
		},
		{
			TxnNo:           refundTxnB,
			MerchantNo:      merchantNo,
			OutTradeNo:      "ord_6005_refund_b",
			BizType:         service.BizTypeRefund,
			DebitAccountNo:  creditAccountNo,
			CreditAccountNo: debitAccountNo,
			RefundOfTxnNo:   originTxnNo,
			Amount:          80,
			Status:          service.TxnStatusInit,
		},
	} {
		if err := repo.CreateTransferTxn(item); err != nil {
			t.Fatalf("create refund txn failed: %v", err)
		}
	}

	processor := service.NewTransferAsyncProcessor(repo)
	var wg sync.WaitGroup
	for _, txnNo := range []string{refundTxnA, refundTxnB} {
		wg.Add(1)
		go func(txnNo string) {
			defer wg.Done()
			processor.Enqueue(txnNo)
		}(txnNo)
	}
	wg.Wait()

	successByStatus := 0
	exceededByStatus := 0
	for _, txnNo := range []string{refundTxnA, refundTxnB} {
		txn := waitTxnTerminalRepo(t, repo, txnNo, 2*time.Second)
		if txn.Status == service.TxnStatusRecvSuccess {
			successByStatus++
		}
		if txn.Status == service.TxnStatusFailed && txn.ErrorCode == "REFUND_AMOUNT_EXCEEDED" {
			exceededByStatus++
		}
	}
	if successByStatus != 1 || exceededByStatus != 1 {
		t.Fatalf("expected status success=1 exceeded=1, got success=%d exceeded=%d", successByStatus, exceededByStatus)
	}

	origin, ok := repo.GetTransferTxn(originTxnNo)
	if !ok {
		t.Fatalf("origin txn not found")
	}
	if origin.RefundableAmount != 20 {
		t.Fatalf("expected origin refundable_amount=20, got %d", origin.RefundableAmount)
	}
}

func TestTC6006RefundReverseAccounting(t *testing.T) {
	repo, pool, processor, merchantNo, originTxnNo, debitAccountNo, creditAccountNo := setupRefundAsyncFixture(t)

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6006"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:           refundTxnNo,
		MerchantNo:      merchantNo,
		OutTradeNo:      "ord_6006_refund",
		BizType:         service.BizTypeRefund,
		DebitAccountNo:  creditAccountNo,
		CreditAccountNo: debitAccountNo,
		RefundOfTxnNo:   originTxnNo,
		Amount:          30,
		Status:          service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor.Enqueue(refundTxnNo)
	waitTxnStatusRepo(t, repo, refundTxnNo, service.TxnStatusRecvSuccess, 2*time.Second)

	debit, _ := repo.GetAccount(debitAccountNo)
	credit, _ := repo.GetAccount(creditAccountNo)
	if debit.Balance != 1030 {
		t.Fatalf("expected debit balance 1030, got %d", debit.Balance)
	}
	if credit.Balance != 170 {
		t.Fatalf("expected credit balance 170, got %d", credit.Balance)
	}

	changes := queryAccountChangesByTxnNo(t, pool, refundTxnNo)
	if len(changes) != 2 {
		t.Fatalf("expected 2 account changes, got %+v", changes)
	}
	deltas := map[string]int64{}
	for _, item := range changes {
		deltas[item.AccountNo] += item.Delta
	}
	if deltas[creditAccountNo] != -30 || deltas[debitAccountNo] != 30 {
		t.Fatalf("unexpected refund deltas: %+v", deltas)
	}
}

func TestTC6009RefundCreditDoesNotCheckAllowCreditIn(t *testing.T) {
	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)
	processor := service.NewTransferAsyncProcessor(repo)

	merchantNo := "1000000000006009"
	debitAccountNo := "6217701201600000901"
	creditAccountNo := "6217701201600000902"
	if err := repo.CreateAccount(service.Account{
		AccountNo:     debitAccountNo,
		MerchantNo:    merchantNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: false,
		AllowTransfer: true,
	}); err != nil {
		t.Fatalf("create debit account failed: %v", err)
	}
	if err := repo.CreateAccount(service.Account{
		AccountNo:     creditAccountNo,
		MerchantNo:    merchantNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
	}); err != nil {
		t.Fatalf("create credit account failed: %v", err)
	}
	seedAccountBalanceByCredit(t, repo, merchantNo, debitAccountNo, 1000, nil)
	seedAccountBalanceByCredit(t, repo, merchantNo, creditAccountNo, 200, nil)

	originTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6009"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            originTxnNo,
		MerchantNo:       merchantNo,
		OutTradeNo:       "ord_6009_origin",
		BizType:          service.BizTypeTransfer,
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           100,
		RefundableAmount: 100,
		Status:           service.TxnStatusRecvSuccess,
	}); err != nil {
		t.Fatalf("create origin txn failed: %v", err)
	}

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6010"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:           refundTxnNo,
		MerchantNo:      merchantNo,
		OutTradeNo:      "ord_6009_refund",
		BizType:         service.BizTypeRefund,
		DebitAccountNo:  creditAccountNo,
		CreditAccountNo: debitAccountNo,
		RefundOfTxnNo:   originTxnNo,
		Amount:          30,
		Status:          service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor.Enqueue(refundTxnNo)
	waitTxnStatusRepo(t, repo, refundTxnNo, service.TxnStatusRecvSuccess, 2*time.Second)

	debit, ok := repo.GetAccount(debitAccountNo)
	if !ok {
		t.Fatalf("debit account not found")
	}
	credit, ok := repo.GetAccount(creditAccountNo)
	if !ok {
		t.Fatalf("credit account not found")
	}
	if debit.Balance != 1030 {
		t.Fatalf("expected debit balance 1030, got %d", debit.Balance)
	}
	if credit.Balance != 170 {
		t.Fatalf("expected credit balance 170, got %d", credit.Balance)
	}

	origin, ok := repo.GetTransferTxn(originTxnNo)
	if !ok {
		t.Fatalf("origin txn not found")
	}
	if origin.RefundableAmount != 70 {
		t.Fatalf("expected origin refundable_amount=70, got %d", origin.RefundableAmount)
	}
}

func TestTC6010RefundSubmitChecksDebitCapability(t *testing.T) {
	repo, _, _, merchantNo, originTxnNo, _, originCreditAccountNo := setupRefundAsyncFixture(t)
	repo.UpdateAccountCapabilities(originCreditAccountNo, false, true, true)

	transferSvc := service.NewTransferService(repo, idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-9d22-73bc-8e11-3f5e9c7a6011",
	}))
	_, err := transferSvc.Submit(service.TransferRequest{
		MerchantNo:    merchantNo,
		OutTradeNo:    "ord_6010_refund_submit",
		BizType:       service.BizTypeRefund,
		RefundOfTxnNo: originTxnNo,
		Amount:        20,
		Status:        service.TxnStatusInit,
	})
	if !errors.Is(err, service.ErrAccountForbidDebit) {
		t.Fatalf("expected ErrAccountForbidDebit, got %v", err)
	}
}

func TestTC6011RefundAsyncIgnoresCapabilitiesAfterSubmit(t *testing.T) {
	repo, _, processor, merchantNo, originTxnNo, originDebitAccountNo, originCreditAccountNo := setupRefundAsyncFixture(t)

	transferSvc := service.NewTransferService(repo, idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-9d22-73bc-8e11-3f5e9c7a6012",
	}))
	txn, err := transferSvc.Submit(service.TransferRequest{
		MerchantNo:    merchantNo,
		OutTradeNo:    "ord_6011_refund_submit",
		BizType:       service.BizTypeRefund,
		RefundOfTxnNo: originTxnNo,
		Amount:        30,
		Status:        service.TxnStatusInit,
	})
	if err != nil {
		t.Fatalf("submit refund failed: %v", err)
	}

	// Flip capabilities after submit; async stages should not re-check these flags.
	repo.UpdateAccountCapabilities(originCreditAccountNo, false, false, false)
	repo.UpdateAccountCapabilities(originDebitAccountNo, false, false, false)

	processor.Enqueue(txn.TxnNo)
	waitTxnStatusRepo(t, repo, txn.TxnNo, service.TxnStatusRecvSuccess, 2*time.Second)

	debit, _ := repo.GetAccount(originDebitAccountNo)
	credit, _ := repo.GetAccount(originCreditAccountNo)
	if debit.Balance != 1030 {
		t.Fatalf("expected debit balance 1030, got %d", debit.Balance)
	}
	if credit.Balance != 170 {
		t.Fatalf("expected credit balance 170, got %d", credit.Balance)
	}
}

func setupRefundAsyncFixture(t *testing.T) (*db.Repository, *pgxpool.Pool, *service.TransferAsyncProcessor, string, string, string, string) {
	t.Helper()

	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)
	merchantNo := "1000000000006000"
	debitAccountNo := "6217701201600000001"
	creditAccountNo := "6217701201600000002"

	if err := repo.CreateAccount(service.Account{
		AccountNo:     debitAccountNo,
		MerchantNo:    merchantNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
	}); err != nil {
		t.Fatalf("create debit account failed: %v", err)
	}
	if err := repo.CreateAccount(service.Account{
		AccountNo:     creditAccountNo,
		MerchantNo:    merchantNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
	}); err != nil {
		t.Fatalf("create credit account failed: %v", err)
	}
	seedAccountBalanceByCredit(t, repo, merchantNo, debitAccountNo, 1000, nil)
	seedAccountBalanceByCredit(t, repo, merchantNo, creditAccountNo, 200, nil)

	originTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a6000"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            originTxnNo,
		MerchantNo:       merchantNo,
		OutTradeNo:       "ord_6000_origin",
		BizType:          service.BizTypeTransfer,
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           100,
		RefundableAmount: 100,
		Status:           service.TxnStatusRecvSuccess,
	}); err != nil {
		t.Fatalf("create origin txn failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	return repo, pool, processor, merchantNo, originTxnNo, debitAccountNo, creditAccountNo
}
