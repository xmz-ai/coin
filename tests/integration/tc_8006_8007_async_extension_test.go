package integration

import (
	"testing"

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
	svc := service.NewAsyncService()
	txnNo := svc.RecordStuckTxn("m1", "ord_8007")

	// txn compensation
	svc.RunTxnCompensation()
	if got := svc.GetTxnStatus(txnNo); got != service.TxnStatusRecvSuccess {
		t.Fatalf("expected txn compensated to RECV_SUCCESS, got %s", got)
	}

	// notify compensation (retry pending events)
	_ = svc.RecordMainTxnSuccess("m1", "ord_8007_notify")
	svc.RunNotifyCompensation(func(_ service.OutboxEvent) bool { return true })
	pending := svc.ListOutboxPending()
	if len(pending) != 0 {
		t.Fatalf("expected no pending outbox after notify compensation")
	}
}
