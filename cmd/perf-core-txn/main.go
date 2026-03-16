package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"github.com/xmz-ai/coin/internal/api"
	"github.com/xmz-ai/coin/internal/config"
	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/platform/security"
	"github.com/xmz-ai/coin/internal/service"
)

const toOutUserID = "u_perf_to"

type perfConfig struct {
	PostgresDSN          string
	RedisAddr            string
	RedisPassword        string
	RedisDB              int
	Passphrase           string
	ProcessingTTLSeconds int

	Duration            time.Duration
	Concurrency         int
	WarmupRequests      int
	RequestTimeout      time.Duration
	MaxBodyBytes        int64
	WebhookPollInterval time.Duration
	WebhookWaitTimeout  time.Duration
}

type resultRow struct {
	StatusCode        int
	Code              string
	SubmitLatency     time.Duration
	E2ELatency        time.Duration
	SubmitStartedAt   time.Time
	E2ECompletedAt    time.Time
	WebhookDone       <-chan struct{}
	CancelWebhookWait func()
	SubmitOK          bool
	E2EOK             bool
	TransportErr      string
	E2EErr            string
}

type latencyStats struct {
	Count int
	Min   time.Duration
	P50   time.Duration
	P95   time.Duration
	P99   time.Duration
	Max   time.Duration
	Avg   time.Duration
}

type metrics struct {
	Mode               string
	SubmitElapsed      time.Duration
	Elapsed            time.Duration
	Total              int
	SubmitSuccess      int
	E2ESuccess         int
	HTTP5xx            int
	HTTP409            int
	HTTP4xxOther       int
	TransportErr       int
	RequestQPS         float64
	SubmitQPS          float64
	E2EQPS             float64
	SubmitLatency      latencyStats
	E2ELatency         latencyStats
	ErrorCodeHistogram map[string]int
	Errors             map[string]int
}

type webhookSink struct {
	mu      sync.Mutex
	waiters map[string][]chan struct{}
}

func newWebhookSink() *webhookSink {
	return &webhookSink{waiters: map[string][]chan struct{}{}}
}

func (s *webhookSink) expect(outTradeNo string) <-chan struct{} {
	ch := make(chan struct{})
	s.mu.Lock()
	s.waiters[outTradeNo] = append(s.waiters[outTradeNo], ch)
	s.mu.Unlock()
	return ch
}

func (s *webhookSink) signal(outTradeNo string) {
	s.mu.Lock()
	waiters := s.waiters[outTradeNo]
	delete(s.waiters, outTradeNo)
	s.mu.Unlock()
	for _, ch := range waiters {
		close(ch)
	}
}

func (s *webhookSink) cancel(outTradeNo string, target <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiters := s.waiters[outTradeNo]
	if len(waiters) == 0 {
		return
	}
	kept := waiters[:0]
	for _, ch := range waiters {
		if (<-chan struct{})(ch) == target {
			close(ch)
			continue
		}
		kept = append(kept, ch)
	}
	if len(kept) == 0 {
		delete(s.waiters, outTradeNo)
		return
	}
	s.waiters[outTradeNo] = kept
}

func main() {
	cfg, err := loadPerfConfig()
	if err != nil {
		log.Fatalf("load perf config failed: %v", err)
	}
	if err := run(cfg); err != nil {
		log.Fatalf("perf run failed: %v", err)
	}
}

func loadPerfConfig() (perfConfig, error) {
	base := config.Load()

	durationSec := getenvInt("PERF_DURATION_SECONDS", 30)
	concurrency := getenvInt("PERF_CONCURRENCY", 50)
	warmup := getenvInt("PERF_WARMUP", 200)
	requestTimeoutMs := getenvInt("PERF_REQUEST_TIMEOUT_MS", 3000)
	maxBodyBytes := getenvInt("PERF_MAX_BODY_BYTES", 1<<20)
	webhookPollMs := getenvInt("PERF_WEBHOOK_POLL_INTERVAL_MS", 10)
	webhookWaitTimeoutMs := getenvInt("PERF_WEBHOOK_WAIT_TIMEOUT_MS", 60000)

	if durationSec <= 0 {
		return perfConfig{}, fmt.Errorf("PERF_DURATION_SECONDS must be > 0")
	}
	if concurrency <= 0 {
		return perfConfig{}, fmt.Errorf("PERF_CONCURRENCY must be > 0")
	}
	if requestTimeoutMs <= 0 {
		return perfConfig{}, fmt.Errorf("PERF_REQUEST_TIMEOUT_MS must be > 0")
	}
	if webhookPollMs <= 0 {
		return perfConfig{}, fmt.Errorf("PERF_WEBHOOK_POLL_INTERVAL_MS must be > 0")
	}
	if webhookWaitTimeoutMs <= 0 {
		return perfConfig{}, fmt.Errorf("PERF_WEBHOOK_WAIT_TIMEOUT_MS must be > 0")
	}
	if strings.TrimSpace(base.MerchantSecretPassphrase) == "" {
		return perfConfig{}, fmt.Errorf("LOCAL_KMS_KEY_V1 is required")
	}

	return perfConfig{
		PostgresDSN:          base.PostgresDSN,
		RedisAddr:            base.RedisAddr,
		RedisPassword:        base.RedisPassword,
		RedisDB:              base.RedisDB,
		Passphrase:           base.MerchantSecretPassphrase,
		ProcessingTTLSeconds: max(base.ProcessingKeyTTLSeconds, 1),
		Duration:            time.Duration(durationSec) * time.Second,
		Concurrency:         concurrency,
		WarmupRequests:      max(warmup, 0),
		RequestTimeout:      time.Duration(requestTimeoutMs) * time.Millisecond,
		MaxBodyBytes:        int64(maxBodyBytes),
		WebhookPollInterval: time.Duration(webhookPollMs) * time.Millisecond,
		WebhookWaitTimeout:  time.Duration(webhookWaitTimeoutMs) * time.Millisecond,
	}, nil
}

func run(cfg perfConfig) error {
	ctx := context.Background()

	pool, err := db.NewPool(ctx, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	if err := ensureMigrations(pool); err != nil {
		return fmt.Errorf("ensure migrations: %w", err)
	}

	redisClient, err := db.NewClient(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer redisClient.Close()

	cipher, err := security.NewAESGCMCipher(cfg.Passphrase)
	if err != nil {
		return fmt.Errorf("init secret cipher: %w", err)
	}
	secretManager := db.NewMerchantSecretManager(pool, cipher)
	repo := db.NewRepository(pool)

	server, webhookServer, webhookSink, merchantNo, secret, fromAccountNo, err := setupPerfServer(
		ctx,
		repo,
		redisClient,
		secretManager,
		cfg.ProcessingTTLSeconds,
		cfg.WebhookPollInterval,
		cfg.MaxBodyBytes,
	)
	if err != nil {
		return err
	}
	defer server.Close()
	defer webhookServer.Close()

	fmt.Printf("[perf] merchant_no=%s from_account_no=%s\n", merchantNo, fromAccountNo)
	fmt.Printf("[perf] duration=%s concurrency=%d warmup=%d timeout=%s\n",
		cfg.Duration, cfg.Concurrency, cfg.WarmupRequests, cfg.RequestTimeout)
	fmt.Printf("[perf] mode=webhook-e2e webhook_poll=%s webhook_wait_timeout=%s\n", cfg.WebhookPollInterval, cfg.WebhookWaitTimeout)

	httpClient := &http.Client{Timeout: cfg.RequestTimeout}
	if cfg.WarmupRequests > 0 {
		if err := runWarmup(httpClient, server.URL, merchantNo, secret, fromAccountNo, cfg.WarmupRequests, cfg.MaxBodyBytes, webhookSink, cfg.WebhookWaitTimeout); err != nil {
			return fmt.Errorf("warmup failed: %w", err)
		}
	}

	metrics := runLoad(httpClient, server.URL, merchantNo, secret, fromAccountNo, cfg.Duration, cfg.Concurrency, cfg.MaxBodyBytes, webhookSink, cfg.WebhookWaitTimeout, "e2e", true)
	report := buildMetricsMarkdown(metrics)
	fmt.Println(report)
	if err := os.WriteFile("perf.md", []byte(report+"\n"), 0o644); err != nil {
		return fmt.Errorf("write perf.md failed: %w", err)
	}
	return nil
}

func setupPerfServer(
	ctx context.Context,
	repo *db.Repository,
	redisClient *goredis.Client,
	secretManager *db.MerchantSecretManager,
	processingTTLSeconds int,
	webhookPollInterval time.Duration,
	maxBodyBytes int64,
) (*httptest.Server, *httptest.Server, *webhookSink, string, string, string, error) {
	ids := idpkg.NewRuntimeUUIDProvider()
	merchantSvc := service.NewMerchantService(repo, ids)
	customerSvc := service.NewCustomerService(repo, ids)
	transferSvc := service.NewTransferService(repo, ids)
	transferRoutingSvc := service.NewTransferRoutingService(repo)
	accountResolver := service.NewAccountResolver(repo)
	refundSvc := service.NewRefundService(repo)
	querySvc := service.NewTxnQueryService(repo)

	merchant, err := merchantSvc.CreateMerchant("", "perf-core-txn")
	if err != nil {
		return nil, nil, nil, "", "", "", fmt.Errorf("create merchant: %w", err)
	}

	fromCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, "u_perf_from")
	if err != nil {
		return nil, nil, nil, "", "", "", fmt.Errorf("create from customer: %w", err)
	}
	toCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, toOutUserID)
	if err != nil {
		return nil, nil, nil, "", "", "", fmt.Errorf("create to customer: %w", err)
	}

	fromAccountNo, err := repo.NewAccountNo(merchant.MerchantNo, "CUSTOMER")
	if err != nil {
		return nil, nil, nil, "", "", "", fmt.Errorf("new from account no: %w", err)
	}
	toAccountNo, err := repo.NewAccountNo(merchant.MerchantNo, "CUSTOMER")
	if err != nil {
		return nil, nil, nil, "", "", "", fmt.Errorf("new to account no: %w", err)
	}

	if err := repo.CreateAccount(service.Account{
		AccountNo:         fromAccountNo,
		MerchantNo:        merchant.MerchantNo,
		CustomerNo:        fromCustomer.CustomerNo,
		AccountType:       "CUSTOMER",
		AllowOverdraft:    false,
		MaxOverdraftLimit: 0,
		AllowDebitOut:     true,
		AllowCreditIn:     true,
		AllowTransfer:     true,
		Balance:           1_000_000_000_000,
	}); err != nil {
		return nil, nil, nil, "", "", "", fmt.Errorf("create from account: %w", err)
	}
	if err := repo.CreateAccount(service.Account{
		AccountNo:         toAccountNo,
		MerchantNo:        merchant.MerchantNo,
		CustomerNo:        toCustomer.CustomerNo,
		AccountType:       "CUSTOMER",
		AllowOverdraft:    false,
		MaxOverdraftLimit: 0,
		AllowDebitOut:     true,
		AllowCreditIn:     true,
		AllowTransfer:     true,
		Balance:           0,
	}); err != nil {
		return nil, nil, nil, "", "", "", fmt.Errorf("create to account: %w", err)
	}

	secret, _, err := secretManager.RotateSecret(context.Background(), merchant.MerchantNo)
	if err != nil {
		return nil, nil, nil, "", "", "", fmt.Errorf("rotate merchant secret: %w", err)
	}

	processingGuard := service.NewRedisProcessingGuard(redisClient, time.Duration(processingTTLSeconds)*time.Second)
	asyncProcessor := service.NewTransferAsyncProcessorWithGuard(repo, processingGuard)

	sink := newWebhookSink()
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		outTradeNo, _ := payload["out_trade_no"].(string)
		if strings.TrimSpace(outTradeNo) != "" {
			sink.signal(outTradeNo)
		}
		w.WriteHeader(http.StatusOK)
	}))

	webhookURL := strings.Replace(webhookServer.URL, "127.0.0.1", "localhost", 1)
	if err := repo.UpsertWebhookConfig(merchant.MerchantNo, webhookURL, true); err != nil {
		webhookServer.Close()
		return nil, nil, nil, "", "", "", fmt.Errorf("upsert webhook config: %w", err)
	}
	webhookWorker := service.NewWebhookWorker(repo, secretManager, 8, 500, []int{1})
	go webhookWorker.Start(ctx, webhookPollInterval)

	business := api.NewBusinessHandler(
		transferSvc,
		transferSvc,
		repo,
		transferRoutingSvc,
		asyncProcessor,
		webhookWorker,
		accountResolver,
		repo,
		refundSvc,
		querySvc,
		repo,
		nil,
	)

	authMw := api.NewAuthMiddleware(api.AuthMiddlewareConfig{
		SecretProvider: secretManager,
		TimeWindow:     5 * time.Minute,
	})

	r := api.NewRouter()
	api.RegisterProtectedRoutes(r, api.ProtectedRoutesOptions{
		AuthMiddleware: authMw,
		SecretRotator:  secretManager,
		Business:       business,
	})
	return httptest.NewServer(r), webhookServer, sink, merchant.MerchantNo, secret, fromAccountNo, nil
}

func runWarmup(
	client *http.Client,
	baseURL, merchantNo, secret, fromAccountNo string,
	warmup int,
	maxBodyBytes int64,
	sink *webhookSink,
	webhookWaitTimeout time.Duration,
) error {
	for i := 0; i < warmup; i++ {
		nonce := "warmup-" + strconv.Itoa(i)
		row := fireTransferOnce(client, baseURL, merchantNo, secret, fromAccountNo, nonce, maxBodyBytes, sink, webhookWaitTimeout, true)
		awaitWebhookResult(&row, webhookWaitTimeout)
		if !row.SubmitOK || !row.E2EOK || row.TransportErr != "" || row.E2EErr != "" {
			return fmt.Errorf("status=%d code=%s submit_ok=%t e2e_ok=%t transport_err=%s e2e_err=%s",
				row.StatusCode, row.Code, row.SubmitOK, row.E2EOK, row.TransportErr, row.E2EErr)
		}
	}
	return nil
}

func runLoad(
	client *http.Client,
	baseURL, merchantNo, secret, fromAccountNo string,
	duration time.Duration,
	concurrency int,
	maxBodyBytes int64,
	sink *webhookSink,
	webhookWaitTimeout time.Duration,
	mode string,
	requireWebhook bool,
) metrics {
	var idCounter uint64
	start := time.Now()
	deadline := start.Add(duration)

	results := make(chan resultRow, concurrency*8)
	var wg sync.WaitGroup

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				if time.Now().After(deadline) {
					return
				}
				id := atomic.AddUint64(&idCounter, 1)
				nonce := mode + "-" + strconv.Itoa(workerID) + "-" + strconv.FormatUint(id, 10)
				results <- fireTransferOnce(client, baseURL, merchantNo, secret, fromAccountNo, nonce, maxBodyBytes, sink, webhookWaitTimeout, requireWebhook)
			}
		}(w)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	rows := make([]resultRow, 0, 1024)
	pending := make([]int, 0, 1024)
	for r := range results {
		idx := len(rows)
		rows = append(rows, r)
		if requireWebhook && r.SubmitOK && r.WebhookDone != nil {
			pending = append(pending, idx)
		}
	}

	submitElapsed := time.Since(start)
	if requireWebhook {
		for _, idx := range pending {
			awaitWebhookResult(&rows[idx], webhookWaitTimeout)
		}
	}

	return aggregate(rows, submitElapsed, time.Since(start), mode)
}

func fireTransferOnce(
	client *http.Client,
	baseURL, merchantNo, secret, fromAccountNo, nonce string,
	maxBodyBytes int64,
	sink *webhookSink,
	webhookWaitTimeout time.Duration,
	requireWebhook bool,
) resultRow {
	path := "/api/v1/transactions/transfer"
	outTradeNo := "ord_perf_" + nonce

	var done <-chan struct{}
	if requireWebhook {
		if sink == nil {
			return resultRow{TransportErr: "webhook sink not configured"}
		}
		done = sink.expect(outTradeNo)
	}
	cancelWait := func() {
		if requireWebhook && sink != nil && done != nil {
			sink.cancel(outTradeNo, done)
		}
	}

	payload := map[string]any{
		"out_trade_no":    outTradeNo,
		"transfer_scene":  "P2P",
		"from_account_no": fromAccountNo,
		"to_out_user_id":  toOutUserID,
		"amount":          1,
	}

	rawBody, err := json.Marshal(payload)
	if err != nil {
		cancelWait()
		return resultRow{TransportErr: err.Error()}
	}
	if int64(len(rawBody)) > maxBodyBytes {
		cancelWait()
		return resultRow{TransportErr: "request body too large"}
	}

	ts := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
	signature := signRequest(http.MethodPost, path, merchantNo, ts, nonce, rawBody, secret)

	req, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(rawBody))
	if err != nil {
		cancelWait()
		return resultRow{TransportErr: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Merchant-No", merchantNo)
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", signature)

	submitStart := time.Now()
	resp, err := client.Do(req)
	submitLatency := time.Since(submitStart)
	if err != nil {
		cancelWait()
		return resultRow{SubmitLatency: submitLatency, TransportErr: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		cancelWait()
		return resultRow{StatusCode: resp.StatusCode, SubmitLatency: submitLatency, TransportErr: "read response: " + err.Error()}
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		cancelWait()
		return resultRow{StatusCode: resp.StatusCode, SubmitLatency: submitLatency, TransportErr: "decode response: " + err.Error()}
	}
	code, _ := parsed["code"].(string)
	row := resultRow{StatusCode: resp.StatusCode, Code: code, SubmitLatency: submitLatency, SubmitStartedAt: submitStart}
	row.SubmitOK = resp.StatusCode == http.StatusCreated && code == "SUCCESS"
	if !row.SubmitOK {
		cancelWait()
		return row
	}
	if !requireWebhook {
		return row
	}
	row.WebhookDone = done
	row.CancelWebhookWait = cancelWait
	return row
}

func awaitWebhookResult(row *resultRow, webhookWaitTimeout time.Duration) {
	if row == nil || !row.SubmitOK || row.WebhookDone == nil {
		return
	}
	remaining := webhookWaitTimeout - time.Since(row.SubmitStartedAt)
	if remaining <= 0 {
		if row.CancelWebhookWait != nil {
			row.CancelWebhookWait()
		}
		row.E2EErr = "webhook timeout"
		return
	}
	timer := time.NewTimer(remaining)
	select {
	case <-row.WebhookDone:
		row.E2ECompletedAt = time.Now()
		row.E2ELatency = row.E2ECompletedAt.Sub(row.SubmitStartedAt)
		row.E2EOK = true
	case <-timer.C:
		if row.CancelWebhookWait != nil {
			row.CancelWebhookWait()
		}
		row.E2EErr = "webhook timeout"
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func signRequest(method, path, merchantNo, ts, nonce string, body []byte, secret string) string {
	bodyHash := sha256.Sum256(body)
	signing := strings.Join([]string{
		method,
		path,
		merchantNo,
		ts,
		nonce,
		hex.EncodeToString(bodyHash[:]),
	}, "\n")
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signing))
	return hex.EncodeToString(mac.Sum(nil))
}

func aggregate(rows []resultRow, submitElapsed, elapsed time.Duration, mode string) metrics {
	m := metrics{
		Mode:               mode,
		SubmitElapsed:      submitElapsed,
		Elapsed:            elapsed,
		Total:              len(rows),
		ErrorCodeHistogram: map[string]int{},
		Errors:             map[string]int{},
	}
	submitLatencies := make([]time.Duration, 0, len(rows))
	e2eLatencies := make([]time.Duration, 0, len(rows))

	for _, r := range rows {
		submitLatencies = append(submitLatencies, r.SubmitLatency)
		if strings.TrimSpace(r.Code) != "" {
			m.ErrorCodeHistogram[r.Code]++
		}

		if r.TransportErr != "" {
			m.TransportErr++
			m.Errors[r.TransportErr]++
			continue
		}

		switch {
		case r.StatusCode == http.StatusConflict:
			m.HTTP409++
		case r.StatusCode >= 500:
			m.HTTP5xx++
		case r.StatusCode >= 400:
			m.HTTP4xxOther++
		}

		if r.SubmitOK {
			m.SubmitSuccess++
		}

		if mode == "submit" {
			continue
		}
		if r.E2EOK {
			m.E2ESuccess++
			e2eLatencies = append(e2eLatencies, r.E2ELatency)
		} else if r.E2EErr != "" {
			m.Errors[r.E2EErr]++
		}
	}

	if submitElapsed > 0 {
		seconds := submitElapsed.Seconds()
		m.RequestQPS = float64(m.Total) / seconds
		m.SubmitQPS = float64(m.SubmitSuccess) / seconds
	}
	if elapsed > 0 {
		m.E2EQPS = float64(m.E2ESuccess) / elapsed.Seconds()
	}
	m.SubmitLatency = calcLatency(submitLatencies)
	if mode == "submit" {
		m.E2ELatency = latencyStats{}
	} else {
		m.E2ELatency = calcLatency(e2eLatencies)
	}
	return m
}

func calcLatency(vals []time.Duration) latencyStats {
	if len(vals) == 0 {
		return latencyStats{}
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })

	var sum time.Duration
	for _, v := range vals {
		sum += v
	}

	return latencyStats{
		Count: len(vals),
		Min:   vals[0],
		P50:   percentile(vals, 50),
		P95:   percentile(vals, 95),
		P99:   percentile(vals, 99),
		Max:   vals[len(vals)-1],
		Avg:   time.Duration(int64(sum) / int64(len(vals))),
	}
}

func percentile(vals []time.Duration, p int) time.Duration {
	if len(vals) == 0 {
		return 0
	}
	if p <= 0 {
		return vals[0]
	}
	if p >= 100 {
		return vals[len(vals)-1]
	}
	idx := (len(vals)-1)*p/100
	return vals[idx]
}

func buildMetricsMarkdown(m metrics) string {
	ratio := func(n, d int) string {
		if d <= 0 {
			return "0.00%"
		}
		return fmt.Sprintf("%.2f%%", float64(n)*100/float64(d))
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Core Txn Report (%s)\n\n", strings.ToUpper(m.Mode)))
	b.WriteString("| Metric | Value |\n")
	b.WriteString("| --- | --- |\n")
	b.WriteString(fmt.Sprintf("| submit_elapsed | %s |\n", m.SubmitElapsed))
	b.WriteString(fmt.Sprintf("| e2e_elapsed | %s |\n", m.Elapsed))
	b.WriteString(fmt.Sprintf("| total_requests | %d |\n", m.Total))
	b.WriteString(fmt.Sprintf("| request_qps | %.2f |\n", m.RequestQPS))
	b.WriteString(fmt.Sprintf("| submit_success | %d (%s) |\n", m.SubmitSuccess, ratio(m.SubmitSuccess, m.Total)))
	b.WriteString(fmt.Sprintf("| submit_qps | %.2f |\n", m.SubmitQPS))
	b.WriteString(fmt.Sprintf("| e2e_success | %d (%s) |\n", m.E2ESuccess, ratio(m.E2ESuccess, m.Total)))
	b.WriteString(fmt.Sprintf("| e2e_qps | %.2f |\n", m.E2EQPS))
	b.WriteString(fmt.Sprintf("| http_409 | %d |\n", m.HTTP409))
	b.WriteString(fmt.Sprintf("| http_5xx | %d |\n", m.HTTP5xx))
	b.WriteString(fmt.Sprintf("| http_4xx_other | %d |\n", m.HTTP4xxOther))
	b.WriteString(fmt.Sprintf("| transport_err | %d |\n", m.TransportErr))

	b.WriteString("\n| Latency Scope | Count | Min | P50 | P95 | P99 | Max | Avg |\n")
	b.WriteString("| --- | ---: | --- | --- | --- | --- | --- | --- |\n")
	b.WriteString(fmt.Sprintf("| submit | %d | %s | %s | %s | %s | %s | %s |\n",
		m.SubmitLatency.Count,
		m.SubmitLatency.Min,
		m.SubmitLatency.P50,
		m.SubmitLatency.P95,
		m.SubmitLatency.P99,
		m.SubmitLatency.Max,
		m.SubmitLatency.Avg,
	))
	if m.Mode == "submit" {
		b.WriteString("| e2e | N/A | N/A | N/A | N/A | N/A | N/A | N/A |\n")
	} else {
		b.WriteString(fmt.Sprintf("| e2e | %d | %s | %s | %s | %s | %s | %s |\n",
			m.E2ELatency.Count,
			m.E2ELatency.Min,
			m.E2ELatency.P50,
			m.E2ELatency.P95,
			m.E2ELatency.P99,
			m.E2ELatency.Max,
			m.E2ELatency.Avg,
		))
	}

	if len(m.ErrorCodeHistogram) > 0 {
		keys := make([]string, 0, len(m.ErrorCodeHistogram))
		for k := range m.ErrorCodeHistogram {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\n| Response Code | Count |\n")
		b.WriteString("| --- | ---: |\n")
		for _, k := range keys {
			b.WriteString(fmt.Sprintf("| %s | %d |\n", k, m.ErrorCodeHistogram[k]))
		}
	}

	if len(m.Errors) > 0 {
		errKeys := make([]string, 0, len(m.Errors))
		for k := range m.Errors {
			errKeys = append(errKeys, k)
		}
		sort.Strings(errKeys)
		b.WriteString("\n| Error | Count |\n")
		b.WriteString("| --- | ---: |\n")
		for _, k := range errKeys {
			b.WriteString(fmt.Sprintf("| %s | %d |\n", k, m.Errors[k]))
		}
	}

	return b.String()
}

func ensureMigrations(pool *pgxpool.Pool) error {
	sqlText, err := loadAllUpMigrationsSQL()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = pool.Exec(ctx, sqlText)
	return err
}

func loadAllUpMigrationsSQL() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	paths, err := filepath.Glob(filepath.Join(repoRoot, "migrations", "*.up.sql"))
	if err != nil {
		return "", fmt.Errorf("glob migrations failed: %w", err)
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no up migration found")
	}
	sort.Strings(paths)

	var b strings.Builder
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("read migration %s failed: %w", p, err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func getenvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
