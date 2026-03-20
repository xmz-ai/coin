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
	"sync"
	"time"
)

type WebhookSecretProvider interface {
	GetActiveSecret(ctx context.Context, merchantNo string) (secret string, ok bool, err error)
}

const (
	webhookAsyncWorkers   = 4
	webhookAsyncQueueSize = 65536
	webhookCacheTTL       = time.Minute
)

type WebhookWorkerOptions struct {
	AsyncWorkers           int
	AsyncQueueSize         int
	ProfilingEnabled       bool
	ProfilingLogInterval   time.Duration
	MerchantConfigCacheTTL time.Duration
	MerchantSecretCacheTTL time.Duration
}

func (o WebhookWorkerOptions) withDefaults() WebhookWorkerOptions {
	if o.AsyncWorkers <= 0 {
		o.AsyncWorkers = webhookAsyncWorkers
	}
	if o.AsyncQueueSize <= 0 {
		o.AsyncQueueSize = webhookAsyncQueueSize
	}
	if o.ProfilingEnabled && o.ProfilingLogInterval <= 0 {
		o.ProfilingLogInterval = defaultAsyncProfileLogInterval
	}
	if o.MerchantConfigCacheTTL <= 0 {
		o.MerchantConfigCacheTTL = webhookCacheTTL
	}
	if o.MerchantSecretCacheTTL <= 0 {
		o.MerchantSecretCacheTTL = webhookCacheTTL
	}
	return o
}

type webhookQueueItem struct {
	txnNo      string
	enqueuedAt time.Time
}

type WebhookWorker struct {
	repo           Repository
	secrets        WebhookSecretProvider
	client         *http.Client
	maxRetries     int
	batchSize      int
	backoff        []time.Duration
	nowFn          func() time.Time
	queue          chan webhookQueueItem
	profiler       *webhookProfiler
	configCache    sync.Map
	secretCache    sync.Map
	configCacheTTL time.Duration
	secretCacheTTL time.Duration
}

type webhookConfigCacheEntry struct {
	cfg      WebhookConfig
	expireAt time.Time
}

type webhookSecretCacheEntry struct {
	secret   string
	expireAt time.Time
}

func NewWebhookWorker(repo Repository, secrets WebhookSecretProvider, maxRetries, batchSize int, backoffMinutes []int) *WebhookWorker {
	return NewWebhookWorkerWithOptions(repo, secrets, maxRetries, batchSize, backoffMinutes, WebhookWorkerOptions{})
}

func NewWebhookWorkerWithOptions(repo Repository, secrets WebhookSecretProvider, maxRetries, batchSize int, backoffMinutes []int, opts WebhookWorkerOptions) *WebhookWorker {
	if maxRetries <= 0 {
		maxRetries = 8
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	opts = opts.withDefaults()
	w := &WebhookWorker{
		repo:       repo,
		secrets:    secrets,
		client:     &http.Client{Timeout: 3 * time.Second},
		maxRetries: maxRetries,
		batchSize:  batchSize,
		backoff:    normalizeWebhookBackoff(backoffMinutes),
		nowFn: func() time.Time {
			return time.Now().UTC()
		},
		queue:          make(chan webhookQueueItem, opts.AsyncQueueSize),
		configCacheTTL: opts.MerchantConfigCacheTTL,
		secretCacheTTL: opts.MerchantSecretCacheTTL,
	}
	if opts.ProfilingEnabled {
		w.profiler = newWebhookProfiler(opts.ProfilingLogInterval)
		w.profiler.startLogger()
	}
	for i := 0; i < opts.AsyncWorkers; i++ {
		go func() {
			for item := range w.queue {
				if w.profiler != nil {
					w.profiler.observeQueueWait(time.Since(item.enqueuedAt))
					w.profiler.observeQueueDepth(len(w.queue))
				}
				startedAt := time.Now()
				err := w.DeliverTxn(context.Background(), item.txnNo)
				if w.profiler != nil {
					w.profiler.observeDeliver(time.Since(startedAt), err != nil)
				}
			}
		}()
	}
	return w
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

func (w *WebhookWorker) Enqueue(txnNo string) {
	if w == nil {
		return
	}
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" {
		return
	}
	select {
	case w.queue <- webhookQueueItem{txnNo: txnNo, enqueuedAt: time.Now()}:
		if w.profiler != nil {
			w.profiler.observeQueueDepth(len(w.queue))
		}
	default:
		if w.profiler != nil {
			w.profiler.observeDrop()
		}
		// Avoid running delivery on request path when queue is saturated.
		// Periodic polling and compensation will pick up pending events.
	}
}

func (w *WebhookWorker) DeliverTxn(ctx context.Context, txnNo string) error {
	if w == nil || w.repo == nil || w.secrets == nil {
		return nil
	}
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	events, err := w.repo.ClaimDueOutboxEventsByTxnNo(txnNo, w.batchSize, w.nowFn())
	if err != nil {
		return err
	}
	for _, e := range events {
		w.handleEvent(ctx, e)
	}
	return nil
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
	cfg, found, err := w.getWebhookConfig(event.MerchantNo)
	if err != nil {
		w.markRetry(event)
		return
	}
	if !found || !cfg.Enabled || strings.TrimSpace(cfg.URL) == "" {
		_ = w.repo.MarkOutboxEventSuccess(event.EventID)
		_ = w.repo.InsertNotifyLog(event.TxnNo, NotifyStatusSuccess, event.RetryCount)
		return
	}

	secret, ok, err := w.getActiveSecret(ctx, event.MerchantNo)
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

func (w *WebhookWorker) getWebhookConfig(merchantNo string) (WebhookConfig, bool, error) {
	now := w.nowFn().UTC()
	if w.configCacheTTL > 0 {
		if v, ok := w.configCache.Load(merchantNo); ok {
			entry, valid := v.(webhookConfigCacheEntry)
			if valid && !now.After(entry.expireAt) {
				return entry.cfg, true, nil
			}
			w.configCache.Delete(merchantNo)
		}
	}

	cfg, found, err := w.repo.GetWebhookConfig(merchantNo)
	if err != nil || !found {
		return cfg, found, err
	}
	if !cfg.Enabled || strings.TrimSpace(cfg.URL) == "" {
		return cfg, found, nil
	}
	if w.configCacheTTL > 0 {
		w.configCache.Store(merchantNo, webhookConfigCacheEntry{
			cfg:      cfg,
			expireAt: now.Add(w.configCacheTTL),
		})
	}
	return cfg, true, nil
}

func (w *WebhookWorker) getActiveSecret(ctx context.Context, merchantNo string) (string, bool, error) {
	now := w.nowFn().UTC()
	if w.secretCacheTTL > 0 {
		if v, ok := w.secretCache.Load(merchantNo); ok {
			entry, valid := v.(webhookSecretCacheEntry)
			if valid && !now.After(entry.expireAt) {
				return entry.secret, true, nil
			}
			w.secretCache.Delete(merchantNo)
		}
	}

	secret, ok, err := w.secrets.GetActiveSecret(ctx, merchantNo)
	if err != nil || !ok || strings.TrimSpace(secret) == "" {
		return secret, ok, err
	}
	if w.secretCacheTTL > 0 {
		w.secretCache.Store(merchantNo, webhookSecretCacheEntry{
			secret:   secret,
			expireAt: now.Add(w.secretCacheTTL),
		})
	}
	return secret, true, nil
}

func (w *WebhookWorker) deliver(ctx context.Context, url string, event OutboxEventDelivery, secret string) bool {
	payload := map[string]any{
		"event_id":       event.EventID,
		"event_type":     webhookEventType(event),
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
	if w.profiler != nil {
		w.profiler.observeRetry()
	}
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

func webhookEventType(event OutboxEventDelivery) string {
	if event.Status == TxnStatusFailed {
		return "TxnFailed"
	}
	if event.BizType == BizTypeRefund && event.Status == TxnStatusRecvSuccess {
		return "TxnRefunded"
	}
	return "TxnSucceeded"
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
