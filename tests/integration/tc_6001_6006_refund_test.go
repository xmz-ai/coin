package integration

import (
	"errors"
	"sync"
	"testing"

	"github.com/xmz-ai/coin/internal/service"
	"github.com/xmz-ai/coin/tests/support/memoryrepo"
)

func TestTC6001OriginTxnNotFoundRejected(t *testing.T) {
	repo := memoryrepo.New()
	svc := service.NewRefundService(repo)

	_, err := svc.SubmitRefund(service.RefundRequest{OriginTxnNo: "missing", Amount: 10})
	if !errors.Is(err, service.ErrTxnNotFound) {
		t.Fatalf("expected ErrTxnNotFound, got %v", err)
	}
}

func TestTC6002RefundAmountExceededRejected(t *testing.T) {
	repo := memoryrepo.New()
	svc := service.NewRefundService(repo)
	svc.RegisterOrigin(service.OriginTxn{
		TxnNo:            "txn_6002",
		RefundableAmount: 50,
		AccountImpacts:   []service.AccountImpact{{AccountNo: "acc_a", Delta: 50}},
	})

	_, err := svc.SubmitRefund(service.RefundRequest{OriginTxnNo: "txn_6002", Amount: 60})
	if !errors.Is(err, service.ErrRefundAmountExceeded) {
		t.Fatalf("expected ErrRefundAmountExceeded, got %v", err)
	}
}

func TestTC6003RefundBreakdownSumMustEqualAmount(t *testing.T) {
	repo := memoryrepo.New()
	svc := service.NewRefundService(repo)
	svc.RegisterOrigin(service.OriginTxn{
		TxnNo:            "txn_6003",
		RefundableAmount: 100,
		AccountImpacts:   []service.AccountImpact{{AccountNo: "acc_a", Delta: 100}},
	})

	_, err := svc.SubmitRefund(service.RefundRequest{
		OriginTxnNo: "txn_6003",
		Amount:      30,
		Breakdown:   []service.RefundPart{{AccountNo: "acc_a", Amount: 20}},
	})
	if !errors.Is(err, service.ErrRefundBreakdownInvalid) {
		t.Fatalf("expected ErrRefundBreakdownInvalid, got %v", err)
	}
}

func TestTC6004RefundBreakdownAccountsMustBelongToOrigin(t *testing.T) {
	repo := memoryrepo.New()
	svc := service.NewRefundService(repo)
	svc.RegisterOrigin(service.OriginTxn{
		TxnNo:            "txn_6004",
		RefundableAmount: 100,
		AccountImpacts:   []service.AccountImpact{{AccountNo: "acc_a", Delta: 100}},
	})

	_, err := svc.SubmitRefund(service.RefundRequest{
		OriginTxnNo: "txn_6004",
		Amount:      30,
		Breakdown:   []service.RefundPart{{AccountNo: "acc_x", Amount: 30}},
	})
	if !errors.Is(err, service.ErrRefundAccountNotInOrigin) {
		t.Fatalf("expected ErrRefundAccountNotInOrigin, got %v", err)
	}
}

func TestTC6005ConcurrentRefundDoesNotExceed(t *testing.T) {
	repo := memoryrepo.New()
	svc := service.NewRefundService(repo)
	svc.RegisterOrigin(service.OriginTxn{
		TxnNo:            "txn_6005",
		RefundableAmount: 100,
		AccountImpacts:   []service.AccountImpact{{AccountNo: "acc_a", Delta: 100}},
	})

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.SubmitRefund(service.RefundRequest{OriginTxnNo: "txn_6005", Amount: 80})
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)

	success := 0
	exceeded := 0
	for err := range errCh {
		if err == nil {
			success++
			continue
		}
		if errors.Is(err, service.ErrRefundAmountExceeded) {
			exceeded++
		}
	}
	if success != 1 || exceeded != 1 {
		t.Fatalf("expected one success and one ErrRefundAmountExceeded, got success=%d exceeded=%d", success, exceeded)
	}
}

func TestTC6006RefundReverseAccounting(t *testing.T) {
	repo := memoryrepo.New()
	repo.CreateAccount(service.Account{AccountNo: "acc_a", Balance: 100, AllowDebitOut: true, AllowCreditIn: true, AllowTransfer: true})
	svc := service.NewRefundService(repo)
	svc.RegisterOrigin(service.OriginTxn{
		TxnNo:            "txn_6006",
		RefundableAmount: 100,
		AccountImpacts:   []service.AccountImpact{{AccountNo: "acc_a", Delta: 100}},
	})

	_, err := svc.SubmitRefund(service.RefundRequest{
		OriginTxnNo: "txn_6006",
		Amount:      30,
		Breakdown:   []service.RefundPart{{AccountNo: "acc_a", Amount: 30}},
	})
	if err != nil {
		t.Fatalf("refund failed: %v", err)
	}

	a, _ := repo.GetAccount("acc_a")
	if a.Balance != 70 {
		t.Fatalf("expected reversed balance 70, got %d", a.Balance)
	}
}
