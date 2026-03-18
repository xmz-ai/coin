package integration

import (
	"context"
	"testing"
	"time"

	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

func TestTC8006WebhookSignatureDeterministic(t *testing.T) {
	svc := service.NewAsyncService()

	sig1 := svc.SignWebhook("sec_8006", []byte(`{"ok":true}`), "1710000000000", "nonce-1")
	sig2 := svc.SignWebhook("sec_8006", []byte(`{"ok":true}`), "1710000000000", "nonce-1")
	if sig1 == "" || sig1 != sig2 {
		t.Fatalf("expected deterministic non-empty signature")
	}
}

func TestTC8007CompensationTasksForTxnAndNotify(t *testing.T) {
	repo, pool, secrets, merchantNo := setupWebhookWorkerFixture(t)

	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-c111-71aa-b2d2-2b079f7e2801",
	})
	transferSvc := service.NewTransferService(repo, ids)
	txn, err := transferSvc.Submit(service.TransferRequest{
		MerchantNo:       merchantNo,
		OutTradeNo:       "ord_8007_comp",
		BizType:          service.BizTypeTransfer,
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   "6217701201801101001",
		CreditAccountNo:  "6217701201801101002",
		Amount:           60,
		RefundableAmount: 60,
		Status:           service.TxnStatusPaySuccess,
	})
	if err != nil {
		t.Fatalf("submit txn failed: %v", err)
	}

	if _, err := pool.Exec(context.Background(), `
		UPDATE merchant_api_credential
		SET active = false
		WHERE merchant_no = $1
	`, merchantNo); err != nil {
		t.Fatalf("deactivate merchant secret failed: %v", err)
	}
	if err := repo.UpsertWebhookConfig(merchantNo, "https://merchant.example.com/webhook", true); err != nil {
		t.Fatalf("upsert webhook config failed: %v", err)
	}

	processor := service.NewTransferAsyncProcessor(repo)
	txnWorker := service.NewTransferRecoveryWorker(repo, processor, 100)
	notifyWorker := service.NewWebhookWorker(repo, secrets, 8, 100, []int{1})
	compWorker := service.NewCompensationWorker(txnWorker, notifyWorker, repo)

	compWorker.RunOnce(context.Background())
	waitTxnStatusRepo(t, repo, txn.TxnNo, service.TxnStatusRecvSuccess, 2*time.Second)
	notifyWorker.RunOnce(context.Background())

	events, err := repo.ClaimDueOutboxEvents(10, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("claim events failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 pending outbox events, got %+v", events)
	}
	retryByTxnNo := map[string]int{}
	for _, item := range events {
		retryByTxnNo[item.TxnNo] = item.RetryCount
	}
	if retryByTxnNo[txn.TxnNo] != 1 {
		t.Fatalf("expected compensated txn retry_count=1, got map=%+v", retryByTxnNo)
	}
}
