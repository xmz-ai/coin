package integration

import (
	"testing"

	"github.com/xmz-ai/coin/internal/service"
)

func TestTC8001OutboxWrittenWithMainTxn(t *testing.T) {
	svc := service.NewAsyncService()

	txnNo := svc.RecordMainTxnSuccess("m1", "ord_8001")
	events := svc.ListOutboxPending()
	if len(events) != 1 || events[0].TxnNo != txnNo {
		t.Fatalf("expected one outbox event bound to txn")
	}
}

func TestTC8002WebhookSuccessDelivery(t *testing.T) {
	svc := service.NewAsyncService()
	txnNo := svc.RecordMainTxnSuccess("m1", "ord_8002")

	svc.ProcessOutbox(func(_ service.OutboxEvent) bool { return true })
	logs := svc.ListNotifyLogs(txnNo)
	if len(logs) != 1 || logs[0].Status != service.NotifyStatusSuccess {
		t.Fatalf("expected notify success")
	}
}

func TestTC8003WebhookRetryOnFailure(t *testing.T) {
	svc := service.NewAsyncService()
	_ = svc.RecordMainTxnSuccess("m1", "ord_8003")

	svc.ProcessOutbox(func(_ service.OutboxEvent) bool { return false })
	events := svc.ListOutboxPending()
	if len(events) != 1 || events[0].RetryCount != 1 {
		t.Fatalf("expected pending event with retry_count=1")
	}
}

func TestTC8004WebhookDeadAfterMaxRetry(t *testing.T) {
	svc := service.NewAsyncService()
	_ = svc.RecordMainTxnSuccess("m1", "ord_8004")

	for i := 0; i < 4; i++ {
		svc.ProcessOutbox(func(_ service.OutboxEvent) bool { return false })
	}
	events := svc.ListOutboxDead()
	if len(events) != 1 {
		t.Fatalf("expected one dead event")
	}
}

func TestTC8005CompensationAdvancesStuckTxn(t *testing.T) {
	svc := service.NewAsyncService()
	txnNo := svc.RecordStuckTxn("m1", "ord_8005")

	svc.RunCompensation()
	status := svc.GetTxnStatus(txnNo)
	if status != service.TxnStatusRecvSuccess {
		t.Fatalf("expected compensated txn to be RECV_SUCCESS, got %s", status)
	}
}
