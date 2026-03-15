package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type WebhookSecretProvider interface {
	GetActiveSecret(ctx context.Context, merchantNo string) (secret string, ok bool, err error)
}

type WebhookWorker struct {
	repo       Repository
	secrets    WebhookSecretProvider
	client     *http.Client
	maxRetries int
	batchSize  int
	backoff    []time.Duration
	nowFn      func() time.Time
}

func NewWebhookWorker(repo Repository, secrets WebhookSecretProvider, maxRetries, batchSize int, backoffMinutes []int) *WebhookWorker {
	if maxRetries <= 0 {
		maxRetries = 8
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	return &WebhookWorker{
		repo:       repo,
		secrets:    secrets,
		client:     &http.Client{Timeout: 3 * time.Second},
		maxRetries: maxRetries,
		batchSize:  batchSize,
		backoff:    normalizeWebhookBackoff(backoffMinutes),
		nowFn: func() time.Time {
			return time.Now().UTC()
		},
	}
}

func (w *WebhookWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	w.RunOnce(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.RunOnce(ctx)
		}
	}
}

func (w *WebhookWorker) StartWithReport(ctx context.Context, interval time.Duration, report func(claimed int, runErr error)) {
	if interval <= 0 {
		interval = time.Second
	}
	if report == nil {
		report = func(int, error) {}
	}
	if w == nil || w.repo == nil || w.secrets == nil {
		report(0, nil)
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	run := func() {
		claimed, err := w.runOnceWithReport(ctx)
		report(claimed, err)
	}

	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func (w *WebhookWorker) runOnceWithReport(ctx context.Context) (int, error) {
	events, err := w.repo.ClaimDueOutboxEvents(w.batchSize, w.nowFn())
	if err != nil {
		return 0, err
	}
	for _, e := range events {
		w.handleEvent(ctx, e)
	}
	return len(events), nil
}

func (w *WebhookWorker) RunOnce(ctx context.Context) {
	if w == nil || w.repo == nil || w.secrets == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	events, err := w.repo.ClaimDueOutboxEvents(w.batchSize, w.nowFn())
	if err != nil {
		return
	}
	for _, e := range events {
		w.handleEvent(ctx, e)
	}
}

func (w *WebhookWorker) handleEvent(ctx context.Context, event OutboxEventDelivery) {
	cfg, found, err := w.repo.GetWebhookConfig(event.MerchantNo)
	if err != nil {
		w.markRetry(event)
		return
	}
	if !found || !cfg.Enabled || strings.TrimSpace(cfg.URL) == "" {
		_ = w.repo.MarkOutboxEventSuccess(event.EventID)
		_ = w.repo.InsertNotifyLog(event.TxnNo, NotifyStatusSuccess, event.RetryCount)
		return
	}

	secret, ok, err := w.secrets.GetActiveSecret(ctx, event.MerchantNo)
	if err != nil || !ok || strings.TrimSpace(secret) == "" {
		w.markRetry(event)
		return
	}

	if w.deliver(ctx, cfg.URL, event, secret) {
		_ = w.repo.MarkOutboxEventSuccess(event.EventID)
		_ = w.repo.InsertNotifyLog(event.TxnNo, NotifyStatusSuccess, event.RetryCount)
		return
	}
	w.markRetry(event)
}

func (w *WebhookWorker) deliver(ctx context.Context, url string, event OutboxEventDelivery, secret string) bool {
	payload := map[string]any{
		"event_id":       event.EventID,
		"event_type":     "TxnSucceeded",
		"occurred_at":    w.nowFn().UTC().Format(time.RFC3339Nano),
		"merchant_no":    event.MerchantNo,
		"txn_no":         event.TxnNo,
		"out_trade_no":   event.OutTradeNo,
		"biz_type":       event.BizType,
		"transfer_scene": event.TransferScene,
		"amount":         event.Amount,
		"status":         event.Status,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false
	}
	timestamp := strconv.FormatInt(w.nowFn().UTC().UnixMilli(), 10)
	signature := signWebhook(secret, req.URL.Path, event.MerchantNo, timestamp, event.EventID, body)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Event-Id", event.EventID)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Signature", signature)

	resp, err := w.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
}

func (w *WebhookWorker) markRetry(event OutboxEventDelivery) {
	nextRetry := event.RetryCount + 1
	dead := nextRetry >= w.maxRetries
	nextAt := w.nowFn().Add(w.retryDelay(nextRetry))
	_ = w.repo.MarkOutboxEventRetry(event.EventID, nextRetry, nextAt, dead)

	status := NotifyStatusFailed
	if dead {
		status = NotifyStatusDead
	}
	_ = w.repo.InsertNotifyLog(event.TxnNo, status, nextRetry)
}

func (w *WebhookWorker) retryDelay(retryCount int) time.Duration {
	if len(w.backoff) == 0 {
		return time.Minute
	}
	idx := retryCount - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(w.backoff) {
		idx = len(w.backoff) - 1
	}
	return w.backoff[idx]
}

func signWebhook(secret, path, merchantNo, timestamp, nonce string, body []byte) string {
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	bodyHash := sha256.Sum256(body)
	signingString := strings.Join([]string{
		http.MethodPost,
		path,
		merchantNo,
		timestamp,
		nonce,
		hex.EncodeToString(bodyHash[:]),
	}, "\n")

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingString))
	return hex.EncodeToString(mac.Sum(nil))
}

func normalizeWebhookBackoff(minutes []int) []time.Duration {
	if len(minutes) == 0 {
		minutes = []int{1, 5, 15, 60, 360}
	}
	out := make([]time.Duration, 0, len(minutes))
	for _, m := range minutes {
		if m <= 0 {
			continue
		}
		out = append(out, time.Duration(m)*time.Minute)
	}
	if len(out) == 0 {
		out = []time.Duration{time.Minute}
	}
	return out
}

func NewWebhookReportHook() func(claimed int, runErr error) {
	return func(claimed int, runErr error) {
		if runErr != nil {
			log.Printf("notify compensation run failed: err=%v claimed=%d", runErr, claimed)
			return
		}
		log.Printf("notify compensation run done: claimed=%d", claimed)
	}
}
