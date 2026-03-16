package integration

import (
	"sync"
	"testing"
	"time"

	"github.com/xmz-ai/coin/internal/service"
)

func TestTC9010PostgresDebitStageWritesBalanceAndLog(t *testing.T) {
	repo, pool, _, debitAccountNo, _, txnNo := setupPostgresTransferFixture(t, service.TxnStatusProcessing, 120)

	applied, err := repo.ApplyTransferDebitStage(txnNo, debitAccountNo, 120)
	if err != nil {
		t.Fatalf("apply debit stage failed: %v", err)
	}
	if !applied {
		t.Fatalf("expected debit stage applied")
	}

	txn, ok := repo.GetTransferTxn(txnNo)
	if !ok || txn.Status != service.TxnStatusPaySuccess {
		t.Fatalf("unexpected txn status: %+v ok=%v", txn, ok)
	}
	debit, _ := repo.GetAccount(debitAccountNo)
	if debit.Balance != 880 {
		t.Fatalf("unexpected debit balance: %d", debit.Balance)
	}

	logs := queryAccountChangesByTxnNo(t, pool, txnNo)
	if len(logs) != 1 {
		t.Fatalf("expected 1 change log, got %d", len(logs))
	}
	if logs[0].AccountNo != debitAccountNo || logs[0].Delta != -120 || logs[0].BalanceAfter != 880 {
		t.Fatalf("unexpected debit log: %+v", logs[0])
	}
}

func TestTC9011PostgresCreditStageWritesBalanceAndLog(t *testing.T) {
	repo, pool, _, _, creditAccountNo, txnNo := setupPostgresTransferFixture(t, service.TxnStatusPaySuccess, 120)

	applied, err := repo.ApplyTransferCreditStage(txnNo, creditAccountNo, 120)
	if err != nil {
		t.Fatalf("apply credit stage failed: %v", err)
	}
	if !applied {
		t.Fatalf("expected credit stage applied")
	}

	txn, ok := repo.GetTransferTxn(txnNo)
	if !ok || txn.Status != service.TxnStatusRecvSuccess {
		t.Fatalf("unexpected txn status: %+v ok=%v", txn, ok)
	}
	credit, _ := repo.GetAccount(creditAccountNo)
	if credit.Balance != 320 {
		t.Fatalf("unexpected credit balance: %d", credit.Balance)
	}

	logs := queryAccountChangesByTxnNo(t, pool, txnNo)
	if len(logs) != 1 {
		t.Fatalf("expected 1 change log, got %d", len(logs))
	}
	if logs[0].AccountNo != creditAccountNo || logs[0].Delta != 120 || logs[0].BalanceAfter != 320 {
		t.Fatalf("unexpected credit log: %+v", logs[0])
	}
}

func TestTC9012PostgresAsyncProcessorWritesTwoLogsAndBalances(t *testing.T) {
	repo, pool, _, debitAccountNo, creditAccountNo, txnNo := setupPostgresTransferFixture(t, service.TxnStatusInit, 150)

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
	if debit.Balance != 850 {
		t.Fatalf("unexpected debit balance: %d", debit.Balance)
	}
	if credit.Balance != 350 {
		t.Fatalf("unexpected credit balance: %d", credit.Balance)
	}

	logs := queryAccountChangesByTxnNo(t, pool, txnNo)
	if len(logs) != 2 {
		t.Fatalf("expected 2 change logs, got %d", len(logs))
	}
	check := map[string]int64{}
	for _, item := range logs {
		check[item.AccountNo] += item.Delta
	}
	if check[debitAccountNo] != -150 || check[creditAccountNo] != 150 {
		t.Fatalf("unexpected change deltas: %+v", check)
	}
	events, err := repo.ClaimDueOutboxEventsByTxnNo(txnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim outbox events failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 outbox event, got %+v", events)
	}
}

func TestTC9013PostgresRefundWritesReverseLogsAndBalances(t *testing.T) {
	repo, pool, merchant, debitAccountNo, creditAccountNo, _ := setupPostgresTransferFixture(t, service.TxnStatusInit, 200)

	originTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a9130"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            originTxnNo,
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_9013_origin",
		BizType:          "TRANSFER",
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           200,
		RefundableAmount: 200,
		Status:           service.TxnStatusProcessing,
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

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a9131"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            refundTxnNo,
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_9013_refund",
		BizType:          "REFUND",
		TransferScene:    "",
		Amount:           50,
		RefundOfTxnNo:    originTxnNo,
		RefundableAmount: 0,
		Status:           service.TxnStatusInit,
	}); err != nil {
		t.Fatalf("create refund txn failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	if err := processor.Process(refundTxnNo); err != nil {
		t.Fatalf("process refund failed: %v", err)
	}
	refund, ok := repo.GetTransferTxn(refundTxnNo)
	if !ok || refund.Status != service.TxnStatusRecvSuccess {
		t.Fatalf("unexpected refund txn status: %+v ok=%v", refund, ok)
	}
	origin, ok := repo.GetTransferTxn(originTxnNo)
	if !ok {
		t.Fatalf("origin txn not found")
	}
	if origin.RefundableAmount != 150 {
		t.Fatalf("expected origin refundable_amount=150, got %d", origin.RefundableAmount)
	}

	debit, _ := repo.GetAccount(debitAccountNo)
	credit, _ := repo.GetAccount(creditAccountNo)
	if debit.Balance != 850 {
		t.Fatalf("unexpected debit balance after refund: %d", debit.Balance)
	}
	if credit.Balance != 350 {
		t.Fatalf("unexpected credit balance after refund: %d", credit.Balance)
	}

	logs := queryAccountChangesByTxnNo(t, pool, refundTxnNo)
	if len(logs) != 2 {
		t.Fatalf("expected 2 refund change logs, got %d", len(logs))
	}
	check := map[string]int64{}
	for _, item := range logs {
		check[item.AccountNo] += item.Delta
	}
	if check[creditAccountNo] != -50 || check[debitAccountNo] != 50 {
		t.Fatalf("unexpected refund deltas: %+v", check)
	}
	events, err := repo.ClaimDueOutboxEventsByTxnNo(refundTxnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim refund outbox events failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 refund outbox event, got %+v", events)
	}
}

func TestTC9020PostgresConcurrentRefundCASNoOverRefund(t *testing.T) {
	repo, _, merchant, debitAccountNo, creditAccountNo, _ := setupPostgresTransferFixture(t, service.TxnStatusInit, 200)

	originTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a9140"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            originTxnNo,
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_9020_origin",
		BizType:          "TRANSFER",
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           100,
		RefundableAmount: 100,
		Status:           service.TxnStatusProcessing,
	}); err != nil {
		t.Fatalf("create origin txn failed: %v", err)
	}
	applied, err := repo.ApplyTransferDebitStage(originTxnNo, debitAccountNo, 100)
	if err != nil || !applied {
		t.Fatalf("apply origin debit stage failed: applied=%v err=%v", applied, err)
	}
	applied, err = repo.ApplyTransferCreditStage(originTxnNo, creditAccountNo, 100)
	if err != nil || !applied {
		t.Fatalf("apply origin credit stage failed: applied=%v err=%v", applied, err)
	}

	refundTxnA := "01956f4e-9d22-73bc-8e11-3f5e9c7a9141"
	refundTxnB := "01956f4e-9d22-73bc-8e11-3f5e9c7a9142"
	for _, item := range []service.TransferTxn{
		{
			TxnNo:            refundTxnA,
			MerchantNo:       merchant.MerchantNo,
			OutTradeNo:       "ord_9020_refund_a",
			BizType:          "REFUND",
			Amount:           80,
			RefundOfTxnNo:    originTxnNo,
			RefundableAmount: 0,
			Status:           service.TxnStatusInit,
		},
		{
			TxnNo:            refundTxnB,
			MerchantNo:       merchant.MerchantNo,
			OutTradeNo:       "ord_9020_refund_b",
			BizType:          "REFUND",
			Amount:           80,
			RefundOfTxnNo:    originTxnNo,
			RefundableAmount: 0,
			Status:           service.TxnStatusInit,
		},
	} {
		if err := repo.CreateTransferTxn(item); err != nil {
			t.Fatalf("create refund txn failed: %v", err)
		}
	}

	type result struct{ err error }
	ch := make(chan result, 2)
	var wg sync.WaitGroup
	for _, refundTxnNo := range []string{refundTxnA, refundTxnB} {
		wg.Add(1)
		go func(refundTxnNo string) {
			defer wg.Done()
			processor := service.NewTransferAsyncProcessor(repo)
			err := processor.Process(refundTxnNo)
			ch <- result{err: err}
		}(refundTxnNo)
	}
	wg.Wait()
	close(ch)

	success := 0
	exceeded := 0
	for item := range ch {
		if item.err == nil {
			success++
			continue
		}
		if item.err == service.ErrRefundAmountExceeded {
			exceeded++
		}
	}
	if success != 1 || exceeded != 1 {
		t.Fatalf("expected one success and one ErrRefundAmountExceeded, got success=%d exceeded=%d", success, exceeded)
	}

	origin, ok := repo.GetTransferTxn(originTxnNo)
	if !ok {
		t.Fatalf("origin txn not found")
	}
	if origin.RefundableAmount != 20 {
		t.Fatalf("expected origin refundable_amount=20, got %d", origin.RefundableAmount)
	}

	successByStatus := 0
	exceededByStatus := 0
	for _, refundTxnNo := range []string{refundTxnA, refundTxnB} {
		refundTxn, ok := repo.GetTransferTxn(refundTxnNo)
		if !ok {
			t.Fatalf("refund txn not found: %s", refundTxnNo)
		}
		if refundTxn.Status == service.TxnStatusRecvSuccess {
			successByStatus++
		}
		if refundTxn.Status == service.TxnStatusFailed && refundTxn.ErrorCode == "REFUND_AMOUNT_EXCEEDED" {
			exceededByStatus++
		}
	}
	if successByStatus != 1 || exceededByStatus != 1 {
		t.Fatalf("expected refund status success=1 exceeded=1, got success=%d exceeded=%d", successByStatus, exceededByStatus)
	}
}

