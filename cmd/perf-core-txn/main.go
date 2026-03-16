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

	Duration       time.Duration
	Concurrency    int
	WarmupRequests int
	RequestTimeout time.Duration
	MaxBodyBytes   int64
}

type resultRow struct {
	StatusCode int
	Code       string
	Latency    time.Duration
	Err        string
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
	Elapsed            time.Duration
	Total              int
	Success            int
	HTTP5xx            int
	HTTP409            int
	HTTP4xxOther       int
	TransportErr       int
	QPS                float64
	Latency            latencyStats
	ErrorCodeHistogram map[string]int
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

	if durationSec <= 0 {
		return perfConfig{}, fmt.Errorf("PERF_DURATION_SECONDS must be > 0")
	}
	if concurrency <= 0 {
		return perfConfig{}, fmt.Errorf("PERF_CONCURRENCY must be > 0")
	}
	if requestTimeoutMs <= 0 {
		return perfConfig{}, fmt.Errorf("PERF_REQUEST_TIMEOUT_MS must be > 0")
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
		Duration:             time.Duration(durationSec) * time.Second,
		Concurrency:          concurrency,
		WarmupRequests:       max(warmup, 0),
		RequestTimeout:       time.Duration(requestTimeoutMs) * time.Millisecond,
		MaxBodyBytes:         int64(maxBodyBytes),
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

	server, merchantNo, secret, fromAccountNo, err := setupPerfServer(repo, redisClient, secretManager, cfg.ProcessingTTLSeconds)
	if err != nil {
		return err
	}
	defer server.Close()

	fmt.Printf("[perf] merchant_no=%s from_account_no=%s\n", merchantNo, fromAccountNo)
	fmt.Printf("[perf] duration=%s concurrency=%d warmup=%d timeout=%s\n",
		cfg.Duration, cfg.Concurrency, cfg.WarmupRequests, cfg.RequestTimeout)

	httpClient := &http.Client{Timeout: cfg.RequestTimeout}
	if cfg.WarmupRequests > 0 {
		if err := runWarmup(httpClient, server.URL, merchantNo, secret, fromAccountNo, cfg.WarmupRequests, cfg.MaxBodyBytes); err != nil {
			return fmt.Errorf("warmup failed: %w", err)
		}
	}

	m := runLoad(httpClient, server.URL, merchantNo, secret, fromAccountNo, cfg.Duration, cfg.Concurrency, cfg.MaxBodyBytes)
	printMetrics(m)
	return nil
}

func setupPerfServer(
	repo *db.Repository,
	redisClient *goredis.Client,
	secretManager *db.MerchantSecretManager,
	processingTTLSeconds int,
) (*httptest.Server, string, string, string, error) {
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
		return nil, "", "", "", fmt.Errorf("create merchant: %w", err)
	}

	fromCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, "u_perf_from")
	if err != nil {
		return nil, "", "", "", fmt.Errorf("create from customer: %w", err)
	}
	toCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, toOutUserID)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("create to customer: %w", err)
	}

	fromAccountNo, err := repo.NewAccountNo(merchant.MerchantNo, "CUSTOMER")
	if err != nil {
		return nil, "", "", "", fmt.Errorf("new from account no: %w", err)
	}
	toAccountNo, err := repo.NewAccountNo(merchant.MerchantNo, "CUSTOMER")
	if err != nil {
		return nil, "", "", "", fmt.Errorf("new to account no: %w", err)
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
		return nil, "", "", "", fmt.Errorf("create from account: %w", err)
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
		return nil, "", "", "", fmt.Errorf("create to account: %w", err)
	}

	secret, _, err := secretManager.RotateSecret(context.Background(), merchant.MerchantNo)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("rotate merchant secret: %w", err)
	}

	processingGuard := service.NewRedisProcessingGuard(redisClient, time.Duration(processingTTLSeconds)*time.Second)
	asyncProcessor := service.NewTransferAsyncProcessorWithGuard(repo, processingGuard)

	business := api.NewBusinessHandler(
		transferSvc,
		transferSvc,
		repo,
		transferRoutingSvc,
		asyncProcessor,
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
	return httptest.NewServer(r), merchant.MerchantNo, secret, fromAccountNo, nil
}

func runWarmup(client *http.Client, baseURL, merchantNo, secret, fromAccountNo string, warmup int, maxBodyBytes int64) error {
	for i := 0; i < warmup; i++ {
		row := fireTransferOnce(client, baseURL, merchantNo, secret, fromAccountNo, "warmup-"+strconv.Itoa(i), maxBodyBytes)
		if row.StatusCode != http.StatusCreated || row.Code != "SUCCESS" {
			return fmt.Errorf("status=%d code=%s err=%s", row.StatusCode, row.Code, row.Err)
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
				nonce := "load-" + strconv.Itoa(workerID) + "-" + strconv.FormatUint(id, 10)
				results <- fireTransferOnce(client, baseURL, merchantNo, secret, fromAccountNo, nonce, maxBodyBytes)
			}
		}(w)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	rows := make([]resultRow, 0, 1024)
	for r := range results {
		rows = append(rows, r)
	}
	return aggregate(rows, time.Since(start))
}

func fireTransferOnce(
	client *http.Client,
	baseURL, merchantNo, secret, fromAccountNo, nonce string,
	maxBodyBytes int64,
) resultRow {
	path := "/api/v1/transactions/transfer"
	payload := map[string]any{
		"out_trade_no":    "ord_perf_" + nonce,
		"transfer_scene":  "P2P",
		"from_account_no": fromAccountNo,
		"to_out_user_id":  toOutUserID,
		"amount":          1,
	}

	rawBody, err := json.Marshal(payload)
	if err != nil {
		return resultRow{Err: err.Error()}
	}
	if int64(len(rawBody)) > maxBodyBytes {
		return resultRow{Err: "request body too large"}
	}

	ts := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
	signature := signRequest(http.MethodPost, path, merchantNo, ts, nonce, rawBody, secret)

	req, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(rawBody))
	if err != nil {
		return resultRow{Err: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Merchant-No", merchantNo)
	req.Header.Set("X-Timestamp", ts)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", signature)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return resultRow{Latency: time.Since(start), Err: err.Error()}
	}
	defer resp.Body.Close()
	latency := time.Since(start)

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return resultRow{StatusCode: resp.StatusCode, Latency: latency, Err: "read response: " + err.Error()}
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return resultRow{StatusCode: resp.StatusCode, Latency: latency, Err: "decode response: " + err.Error()}
	}
	code, _ := parsed["code"].(string)
	return resultRow{StatusCode: resp.StatusCode, Code: code, Latency: latency}
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

func aggregate(rows []resultRow, elapsed time.Duration) metrics {
	m := metrics{
		Elapsed:            elapsed,
		Total:              len(rows),
		ErrorCodeHistogram: map[string]int{},
	}
	latencies := make([]time.Duration, 0, len(rows))

	for _, r := range rows {
		if r.Err != "" {
			m.TransportErr++
			continue
		}
		latencies = append(latencies, r.Latency)
		m.ErrorCodeHistogram[r.Code]++

		switch {
		case r.StatusCode >= 200 && r.StatusCode < 300:
			m.Success++
		case r.StatusCode == http.StatusConflict:
			m.HTTP409++
		case r.StatusCode >= 500:
			m.HTTP5xx++
		case r.StatusCode >= 400:
			m.HTTP4xxOther++
		}
	}

	if elapsed > 0 {
		m.QPS = float64(m.Total) / elapsed.Seconds()
	}
	m.Latency = calcLatency(latencies)
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

func printMetrics(m metrics) {
	fmt.Println("[perf] ===== core txn real load report =====")
	fmt.Printf("[perf] elapsed=%s\n", m.Elapsed)
	fmt.Printf("[perf] total=%d success=%d 409=%d 5xx=%d 4xx_other=%d transport_err=%d\n",
		m.Total, m.Success, m.HTTP409, m.HTTP5xx, m.HTTP4xxOther, m.TransportErr)
	fmt.Printf("[perf] qps=%.2f\n", m.QPS)
	fmt.Printf("[perf] latency count=%d min=%s p50=%s p95=%s p99=%s max=%s avg=%s\n",
		m.Latency.Count, m.Latency.Min, m.Latency.P50, m.Latency.P95, m.Latency.P99, m.Latency.Max, m.Latency.Avg)

	if len(m.ErrorCodeHistogram) == 0 {
		return
	}

	keys := make([]string, 0, len(m.ErrorCodeHistogram))
	for k := range m.ErrorCodeHistogram {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Println("[perf] code_histogram:")
	for _, k := range keys {
		fmt.Printf("[perf]   %s=%d\n", k, m.ErrorCodeHistogram[k])
	}
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
