package integration

import (
	"context"
	"errors"
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

	got, ok := repo.GetTransferTxn(txn.TxnNo)
	if !ok {
		t.Fatalf("txn not found")
	}
	if got.Status != service.TxnStatusRecvSuccess {
		t.Fatalf("expected txn status RECV_SUCCESS, got %s", got.Status)
	}

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

	got, ok := repo.GetTransferTxn("01956f4e-9d22-73bc-8e11-3f5e9c7a8809")
	if !ok {
		t.Fatalf("txn not found")
	}
	if got.Status != service.TxnStatusRecvSuccess {
		t.Fatalf("expected txn status RECV_SUCCESS, got %s", got.Status)
	}

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
