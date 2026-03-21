package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

type failingStageGuard struct {
	err error
}

func (g failingStageGuard) TryBegin(txnNo, stage string) bool {
	return false
}

func (g failingStageGuard) TryBeginWithError(txnNo, stage string) (bool, error) {
	return false, g.err
}

type recordingNotifyDispatcher struct {
	mu     sync.Mutex
	txnNos []string
}

func (d *recordingNotifyDispatcher) Enqueue(txnNo string) {
	d.mu.Lock()
	d.txnNos = append(d.txnNos, txnNo)
	d.mu.Unlock()
}

func (d *recordingNotifyDispatcher) Snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.txnNos))
	copy(out, d.txnNos)
	return out
}

func TestTC8008TransferWorkerProcessesInitTxn(t *testing.T) {
	repo, _, merchant, debitAccountNo, creditAccountNo := setupWorkerTransferFixture(t)

	transferSvc := service.NewTransferService(repo, idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-9d22-73bc-8e11-3f5e9c7a8801",
	}))
	txn, err := transferSvc.Submit(service.TransferRequest{
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_8008",
		BizType:          "TRANSFER",
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           100,
		RefundableAmount: 100,
		Status:           service.TxnStatusInit,
	})
	if err != nil {
		t.Fatalf("submit txn failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	worker := service.NewTransferRecoveryWorker(repo, processor, 100)
	worker.RunOnce()

	waitTxnStatusRepo(t, repo, txn.TxnNo, service.TxnStatusRecvSuccess, 2*time.Second)

	debit, _ := repo.GetAccount(debitAccountNo)
	credit, _ := repo.GetAccount(creditAccountNo)
	if debit.Balance != 900 {
		t.Fatalf("expected debit balance 900, got %d", debit.Balance)
	}
	if credit.Balance != 100 {
		t.Fatalf("expected credit balance 100, got %d", credit.Balance)
	}
}

func TestTC8009TransferWorkerContinuesFromPaySuccess(t *testing.T) {
	repo, pool, merchant, debitAccountNo, creditAccountNo := setupWorkerTransferFixture(t)

	// Simulate debit stage already completed.
	debit, _ := repo.GetAccount(debitAccountNo)
	debit.Balance = 900
	if _, err := pool.Exec(context.Background(), `
		UPDATE account SET balance = $1 WHERE account_no = $2
	`, debit.Balance, debit.AccountNo); err != nil {
		t.Fatalf("seed debit balance failed: %v", err)
	}

	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            "01956f4e-9d22-73bc-8e11-3f5e9c7a8809",
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_8009",
		BizType:          "TRANSFER",
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           100,
		RefundableAmount: 100,
		Status:           service.TxnStatusPaySuccess,
	}); err != nil {
		t.Fatalf("seed txn failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	worker := service.NewTransferRecoveryWorker(repo, processor, 100)
	worker.RunOnce()

	waitTxnStatusRepo(t, repo, "01956f4e-9d22-73bc-8e11-3f5e9c7a8809", service.TxnStatusRecvSuccess, 2*time.Second)

	credit, _ := repo.GetAccount(creditAccountNo)
	if credit.Balance != 100 {
		t.Fatalf("expected credit balance 100, got %d", credit.Balance)
	}
}

func TestTC8010TransferEnqueueFastPathWithoutPollingWorker(t *testing.T) {
	repo, _, merchant, debitAccountNo, creditAccountNo := setupWorkerTransferFixture(t)

	transferSvc := service.NewTransferService(repo, idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-9d22-73bc-8e11-3f5e9c7a8810",
	}))
	txn, err := transferSvc.Submit(service.TransferRequest{
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_8010",
		BizType:          "TRANSFER",
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           100,
		RefundableAmount: 100,
		Status:           service.TxnStatusInit,
	})
	if err != nil {
		t.Fatalf("submit txn failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	processor.Enqueue(txn.TxnNo)

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		got, ok := repo.GetTransferTxn(txn.TxnNo)
		if ok && got.Status == service.TxnStatusRecvSuccess {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, _ := repo.GetTransferTxn(txn.TxnNo)
	t.Fatalf("expected fast-path enqueue to finish without polling worker, got status=%s", got.Status)
}

func TestTC8016TransferSubmitChecksCapabilities(t *testing.T) {
	repo, _, merchant, debitAccountNo, creditAccountNo := setupWorkerTransferFixture(t)
	repo.UpdateAccountCapabilities(creditAccountNo, true, false, true)

	transferSvc := service.NewTransferService(repo, idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-9d22-73bc-8e11-3f5e9c7a8816",
	}))
	_, err := transferSvc.Submit(service.TransferRequest{
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_8016",
		BizType:          "TRANSFER",
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           100,
		RefundableAmount: 100,
		Status:           service.TxnStatusInit,
	})
	if !errors.Is(err, service.ErrAccountForbidCredit) {
		t.Fatalf("expected ErrAccountForbidCredit, got %v", err)
	}
}

func TestTC8017TransferAsyncIgnoresCapabilitiesAfterSubmit(t *testing.T) {
	repo, _, merchant, debitAccountNo, creditAccountNo := setupWorkerTransferFixture(t)

	transferSvc := service.NewTransferService(repo, idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-9d22-73bc-8e11-3f5e9c7a8817",
	}))
	txn, err := transferSvc.Submit(service.TransferRequest{
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_8017",
		BizType:          "TRANSFER",
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           100,
		RefundableAmount: 100,
		Status:           service.TxnStatusInit,
	})
	if err != nil {
		t.Fatalf("submit txn failed: %v", err)
	}

	// Flip capabilities after submit; async stages should not re-check these flags.
	repo.UpdateAccountCapabilities(debitAccountNo, false, false, false)
	repo.UpdateAccountCapabilities(creditAccountNo, false, false, false)

	processor := service.NewTransferAsyncProcessor(repo)
	processor.Enqueue(txn.TxnNo)
	waitTxnStatusRepo(t, repo, txn.TxnNo, service.TxnStatusRecvSuccess, 2*time.Second)

	debit, _ := repo.GetAccount(debitAccountNo)
	credit, _ := repo.GetAccount(creditAccountNo)
	if debit.Balance != 900 {
		t.Fatalf("expected debit balance 900, got %d", debit.Balance)
	}
	if credit.Balance != 100 {
		t.Fatalf("expected credit balance 100, got %d", credit.Balance)
	}
}

func TestTC8030TransferEnqueueTriggersNotifyDispatcherAfterRecvSuccess(t *testing.T) {
	repo, _, merchant, debitAccountNo, creditAccountNo := setupWorkerTransferFixture(t)

	transferSvc := service.NewTransferService(repo, idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-9d22-73bc-8e11-3f5e9c7a8830",
	}))
	txn, err := transferSvc.Submit(service.TransferRequest{
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_8030",
		BizType:          "TRANSFER",
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           100,
		RefundableAmount: 100,
		Status:           service.TxnStatusInit,
	})
	if err != nil {
		t.Fatalf("submit txn failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	dispatcher := &recordingNotifyDispatcher{}
	processor.SetWebhookDispatcher(dispatcher)
	processor.Enqueue(txn.TxnNo)

	waitTxnStatusRepo(t, repo, txn.TxnNo, service.TxnStatusRecvSuccess, 2*time.Second)

	deadline := time.Now().Add(500 * time.Millisecond)
	var calls []string
	for time.Now().Before(deadline) {
		calls = dispatcher.Snapshot()
		if len(calls) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(calls) != 1 {
		t.Fatalf("expected notify dispatcher called once, got=%v", calls)
	}
	if calls[0] != txn.TxnNo {
		t.Fatalf("unexpected notify txn_no: got=%s want=%s", calls[0], txn.TxnNo)
	}
}

func TestTC8010GuardUnavailableDoesNotFailTxn(t *testing.T) {
	repo, _, merchant, debitAccountNo, creditAccountNo := setupWorkerTransferFixture(t)

	transferSvc := service.NewTransferService(repo, idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-9d22-73bc-8e11-3f5e9c7a8811",
	}))
	txn, err := transferSvc.Submit(service.TransferRequest{
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_8010_guard_unavailable",
		BizType:          "TRANSFER",
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           100,
		RefundableAmount: 100,
		Status:           service.TxnStatusInit,
	})
	if err != nil {
		t.Fatalf("submit txn failed: %v", err)
	}

	guardErr := errors.New("processing guard unavailable")
	processor := service.NewTransferAsyncProcessorWithGuard(repo, failingStageGuard{err: guardErr})
	if err := processor.Process(txn.TxnNo); err == nil {
		t.Fatalf("expected process error on guard unavailable")
	}

	got, ok := repo.GetTransferTxn(txn.TxnNo)
	if !ok {
		t.Fatalf("txn not found")
	}
	if got.Status != service.TxnStatusInit {
		t.Fatalf("expected txn status remain INIT on guard error, got %s", got.Status)
	}
	if got.ErrorCode != "" {
		t.Fatalf("expected empty error_code, got %s", got.ErrorCode)
	}
}

func setupWorkerTransferFixture(t *testing.T) (*db.Repository, *pgxpool.Pool, service.Merchant, string, string) {
	t.Helper()

	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c880",
		"01956f4e-8c11-71aa-b2d2-2b079f7e1880",
	})

	merchantSvc := service.NewMerchantService(repo, ids)
	customerSvc := service.NewCustomerService(repo, ids)

	merchant, err := merchantSvc.CreateMerchant("", "worker")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	customer, err := customerSvc.CreateCustomer(merchant.MerchantNo, "u_8008")
	if err != nil {
		t.Fatalf("create customer failed: %v", err)
	}

	debitAccountNo := "6217701201008008001"
	creditAccountNo := "6217701201008008002"
	if err := repo.CreateAccount(service.Account{
		AccountNo:     debitAccountNo,
		MerchantNo:    merchant.MerchantNo,
		CustomerNo:    customer.CustomerNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
		Balance:       1000,
	}); err != nil {
		t.Fatalf("create debit account failed: %v", err)
	}
	if err := repo.CreateAccount(service.Account{
		AccountNo:     creditAccountNo,
		MerchantNo:    merchant.MerchantNo,
		CustomerNo:    customer.CustomerNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
		Balance:       0,
	}); err != nil {
		t.Fatalf("create credit account failed: %v", err)
	}

	return repo, pool, merchant, debitAccountNo, creditAccountNo
}

func waitTxnStatusRepo(t *testing.T, repo *db.Repository, txnNo, wantStatus string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		txn, ok := repo.GetTransferTxn(txnNo)
		if ok && txn.Status == wantStatus {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	txn, ok := repo.GetTransferTxn(txnNo)
	if !ok {
		t.Fatalf("txn not found while waiting status: txn_no=%s want=%s", txnNo, wantStatus)
	}
	t.Fatalf("txn status not reached in time: txn_no=%s got=%s want=%s", txnNo, txn.Status, wantStatus)
}

func waitTxnTerminalRepo(t *testing.T, repo *db.Repository, txnNo string, timeout time.Duration) service.TransferTxn {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		txn, ok := repo.GetTransferTxn(txnNo)
		if ok && (txn.Status == service.TxnStatusRecvSuccess || txn.Status == service.TxnStatusFailed) {
			return txn
		}
		time.Sleep(10 * time.Millisecond)
	}
	txn, ok := repo.GetTransferTxn(txnNo)
	if !ok {
		t.Fatalf("txn not found while waiting terminal: txn_no=%s", txnNo)
	}
	t.Fatalf("txn did not reach terminal status in time: txn_no=%s status=%s", txnNo, txn.Status)
	return service.TransferTxn{}
}
