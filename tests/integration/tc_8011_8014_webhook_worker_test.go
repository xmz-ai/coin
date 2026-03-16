package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/platform/security"
	"github.com/xmz-ai/coin/internal/service"
)

func TestTC8011WebhookWorkerSkipsWhenConfigDisabled(t *testing.T) {
	repo, pool, secrets, merchantNo := setupWebhookWorkerFixture(t)
	if err := repo.UpsertWebhookConfig(merchantNo, "https://merchant.example.com/webhook", false); err != nil {
		t.Fatalf("upsert webhook config failed: %v", err)
	}

	worker := service.NewWebhookWorker(repo, secrets, 8, 100, []int{1})
	worker.RunOnce(nil)

	events, err := repo.ClaimDueOutboxEvents(10, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("claim events failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no pending outbox events, got %d", len(events))
	}

	logs := queryNotifyLogsByTxnNo(t, pool, "01956f4e-9d22-73bc-8e11-3f5e9c7a8111")
	if len(logs) != 1 || logs[0].Status != service.NotifyStatusSuccess {
		t.Fatalf("expected one SUCCESS notify log, got %+v", logs)
	}
}

func TestTC8012WebhookWorkerDeliverySuccessWithSignature(t *testing.T) {
	repo, pool, secrets, merchantNo := setupWebhookWorkerFixture(t)
	secret, ok, err := secrets.GetActiveSecret(context.Background(), merchantNo)
	if err != nil || !ok {
		t.Fatalf("get active secret failed: ok=%v err=%v", ok, err)
	}

	hit := int32(0)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hit, 1)
		defer r.Body.Close()

		if got := r.Header.Get("X-Event-Id"); got == "" {
			t.Fatalf("missing X-Event-Id")
		}
		timestamp := r.Header.Get("X-Timestamp")
		signature := r.Header.Get("X-Signature")
		if timestamp == "" || signature == "" {
			t.Fatalf("missing webhook signature headers")
		}

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode body failed: %v", err)
		}
		if payload["event_type"] != "TxnSucceeded" {
			t.Fatalf("unexpected event_type: %v", payload["event_type"])
		}

		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload failed: %v", err)
		}
		bodyHash := sha256.Sum256(bodyBytes)
		eventID := r.Header.Get("X-Event-Id")
		signingString := strings.Join([]string{
			http.MethodPost,
			r.URL.Path,
			merchantNo,
			timestamp,
			eventID,
			hex.EncodeToString(bodyHash[:]),
		}, "\n")
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(signingString))
		expected := hex.EncodeToString(mac.Sum(nil))
		if signature != expected {
			t.Fatalf("signature mismatch: got=%s want=%s", signature, expected)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	if err := repo.UpsertWebhookConfig(merchantNo, ts.URL+"/coin/webhook", true); err != nil {
		t.Fatalf("upsert webhook config failed: %v", err)
	}

	worker := service.NewWebhookWorker(repo, secrets, 8, 100, []int{1, 5})
	worker.RunOnce(nil)

	if atomic.LoadInt32(&hit) != 1 {
		t.Fatalf("expected webhook endpoint hit once")
	}
	events, err := repo.ClaimDueOutboxEvents(10, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("claim events failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no pending outbox events, got %d", len(events))
	}
	logs := queryNotifyLogsByTxnNo(t, pool, "01956f4e-9d22-73bc-8e11-3f5e9c7a8111")
	if len(logs) != 1 || logs[0].Status != service.NotifyStatusSuccess || logs[0].Retries != 0 {
		t.Fatalf("unexpected notify logs: %+v", logs)
	}
}

func TestTC8012WebhookWorkerRefundEventType(t *testing.T) {
	repo, pool, secrets, merchantNo := setupWebhookWorkerFixture(t)

	if _, err := pool.Exec(context.Background(), `DELETE FROM outbox_event`); err != nil {
		t.Fatalf("clear outbox events failed: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `DELETE FROM notify_log`); err != nil {
		t.Fatalf("clear notify logs failed: %v", err)
	}

	refundTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a8112"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO txn (
			txn_no, merchant_no, out_trade_no, biz_type, transfer_scene,
			debit_account_no, credit_account_no, amount, status,
			refund_of_txn_no, refundable_amount, created_at, updated_at
		)
		VALUES ($1::uuid, $2, 'ord_8012_refund', 'REFUND', NULL, NULL, NULL, 30, 'RECV_SUCCESS', NULL, 0, NOW(), NOW())
	`, refundTxnNo, merchantNo); err != nil {
		t.Fatalf("insert refund txn failed: %v", err)
	}
	refundEventID := "01956f4e-9d22-73bc-8e11-3f5e9c7a8f12"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO outbox_event (
			event_id, txn_no, merchant_no, out_trade_no, status, retry_count, next_retry_at, created_at, updated_at
		)
		VALUES ($1::uuid, $2::uuid, $3, 'ord_8012_refund', 'PENDING', 0, NULL, NOW(), NOW())
	`, refundEventID, refundTxnNo, merchantNo); err != nil {
		t.Fatalf("insert refund outbox event failed: %v", err)
	}

	gotEventType := ""
	hit := int32(0)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hit, 1)
		defer r.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode body failed: %v", err)
		}
		if v, _ := payload["event_type"].(string); v != "" {
			gotEventType = v
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	if err := repo.UpsertWebhookConfig(merchantNo, ts.URL+"/coin/webhook", true); err != nil {
		t.Fatalf("upsert webhook config failed: %v", err)
	}

	worker := service.NewWebhookWorker(repo, secrets, 8, 100, []int{1, 5})
	worker.RunOnce(nil)

	if atomic.LoadInt32(&hit) != 1 {
		t.Fatalf("expected webhook endpoint hit once")
	}
	if gotEventType != "TxnRefunded" {
		t.Fatalf("expected event_type TxnRefunded, got %s", gotEventType)
	}
}

func TestTC8013WebhookWorkerRetryAndDead(t *testing.T) {
	repo, pool, secrets, merchantNo := setupWebhookWorkerFixture(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	if err := repo.UpsertWebhookConfig(merchantNo, ts.URL+"/coin/webhook", true); err != nil {
		t.Fatalf("upsert webhook config failed: %v", err)
	}

	worker := service.NewWebhookWorker(repo, secrets, 3, 100, []int{1, 1, 1})
	events := queryOutboxEventsByTxnNo(t, pool, "01956f4e-9d22-73bc-8e11-3f5e9c7a8111")
	if len(events) != 1 {
		t.Fatalf("expected one outbox event before retry rounds, got %+v", events)
	}
	eventID := events[0].EventID
	for i := 0; i < 3; i++ {
		worker.RunOnce(nil)
		if i < 2 {
			if err := repo.MarkOutboxEventRetry(eventID, i+1, time.Now().UTC().Add(-time.Second), false); err != nil {
				t.Fatalf("force retry due failed: %v", err)
			}
		}
	}

	pending, err := repo.ClaimDueOutboxEvents(10, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("claim events failed: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending outbox events after DEAD, got %d", len(pending))
	}

	logs := queryNotifyLogsByTxnNo(t, pool, "01956f4e-9d22-73bc-8e11-3f5e9c7a8111")
	if len(logs) == 0 {
		t.Fatalf("expected notify logs")
	}
	last := logs[len(logs)-1]
	if last.Status != service.NotifyStatusDead {
		t.Fatalf("expected last notify status DEAD, got %s", last.Status)
	}
}

func TestTC8014WebhookWorkerRetryOnSecretUnavailable(t *testing.T) {
	repo, pool, secrets, merchantNo := setupWebhookWorkerFixture(t)
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

	worker := service.NewWebhookWorker(repo, secrets, 8, 100, []int{1})
	worker.RunOnce(nil)

	events, err := repo.ClaimDueOutboxEvents(10, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("claim events failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one pending event after secret failure, got %d", len(events))
	}
	if events[0].RetryCount != 1 {
		t.Fatalf("expected retry_count=1, got %d", events[0].RetryCount)
	}
	logs := queryNotifyLogsByTxnNo(t, pool, "01956f4e-9d22-73bc-8e11-3f5e9c7a8111")
	if len(logs) != 1 || logs[0].Status != service.NotifyStatusFailed {
		t.Fatalf("expected one FAILED notify log, got %+v", logs)
	}
}

func setupWebhookWorkerFixture(t *testing.T) (*db.Repository, *pgxpool.Pool, *db.MerchantSecretManager, string) {
	t.Helper()

	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7d811",
		"01956f4e-8c11-71aa-b2d2-2b079f7e2811",
		"01956f4e-8c11-71aa-b2d2-2b079f7e2812",
	})
	merchantSvc := service.NewMerchantService(repo, ids)
	customerSvc := service.NewCustomerService(repo, ids)

	merchant, err := merchantSvc.CreateMerchant("", "webhook-worker")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	debitCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, "u_8011_debit")
	if err != nil {
		t.Fatalf("create debit customer failed: %v", err)
	}
	creditCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, "u_8011_credit")
	if err != nil {
		t.Fatalf("create credit customer failed: %v", err)
	}
	debitAccountNo := "6217701201801101001"
	creditAccountNo := "6217701201801101002"
	if err := repo.CreateAccount(service.Account{
		AccountNo:     debitAccountNo,
		MerchantNo:    merchant.MerchantNo,
		CustomerNo:    debitCustomer.CustomerNo,
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
		CustomerNo:    creditCustomer.CustomerNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
		Balance:       0,
	}); err != nil {
		t.Fatalf("create credit account failed: %v", err)
	}

	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            "01956f4e-9d22-73bc-8e11-3f5e9c7a8111",
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       "ord_8011",
		BizType:          service.BizTypeTransfer,
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           100,
		RefundableAmount: 100,
		Status:           service.TxnStatusPaySuccess,
	}); err != nil {
		t.Fatalf("create transfer txn failed: %v", err)
	}
	applied, err := repo.ApplyTransferCreditStage("01956f4e-9d22-73bc-8e11-3f5e9c7a8111", creditAccountNo, 100)
	if err != nil || !applied {
		t.Fatalf("apply transfer credit stage failed: applied=%v err=%v", applied, err)
	}

	cipher, err := security.NewAESGCMCipher("tc8011_local_secret_cipher_key")
	if err != nil {
		t.Fatalf("init cipher failed: %v", err)
	}
	secrets := db.NewMerchantSecretManager(pool, cipher)
	if _, _, err := secrets.RotateSecret(context.Background(), merchant.MerchantNo); err != nil {
		t.Fatalf("rotate merchant secret failed: %v", err)
	}

	return repo, pool, secrets, merchant.MerchantNo
}
