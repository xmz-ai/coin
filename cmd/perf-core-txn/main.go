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

const (
	toBookTransferOutUser    = "u_perf_to_book"
	toNonBookTransferOutUser = "u_perf_to_non_book"
	progressBarWidth         = 24
	progressTickInterval     = 500 * time.Millisecond
)

type perfConfig struct {
	PostgresDSN          string
	RedisAddr            string
	RedisPassword        string
	RedisDB              int
	Passphrase           string
	ProcessingTTLSeconds int
	TxnAsyncOpts         service.TransferAsyncProcessorOptions

	Duration            time.Duration
	Concurrency         int
	WarmupRequests      int
	RequestTimeout      time.Duration
	MaxBodyBytes        int64
	WebhookPollInterval time.Duration
	WebhookWaitTimeout  time.Duration
	TxnRecoveryInterval time.Duration
	TxnRecoveryStale    time.Duration
	TxnRecoveryBatch    int
}

type resultRow struct {
	StatusCode        int
	Code              string
	TxnNo             string
	SubmitLatency     time.Duration
	E2ELatency        time.Duration
	SubmitStartedAt   time.Time
	E2ECompletedAt    time.Time
	WebhookDone       <-chan time.Time
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

type scenarioFireFn func(nonce string) resultRow

type perfRuntime struct {
	Server                *httptest.Server
	WebhookServer         *httptest.Server
	WebhookSink           *webhookSink
	MerchantNo            string
	Secret                string
	TransferFromAccountNo string
	TransferBookToAccount string
	TransferToAccountNo   string
}

type webhookSink struct {
	mu      sync.Mutex
	waiters map[string][]chan time.Time
}

func newWebhookSink() *webhookSink {
	return &webhookSink{waiters: map[string][]chan time.Time{}}
}

func (s *webhookSink) expect(outTradeNo string) <-chan time.Time {
	ch := make(chan time.Time, 1)
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
	doneAt := time.Now()
	for _, ch := range waiters {
		select {
		case ch <- doneAt:
		default:
		}
	}
}

func (s *webhookSink) cancel(outTradeNo string, target <-chan time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	waiters := s.waiters[outTradeNo]
	if len(waiters) == 0 {
		return
	}
	kept := waiters[:0]
	for _, ch := range waiters {
		if (<-chan time.Time)(ch) == target {
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
	txnRecoveryIntervalMs := getenvInt("PERF_TXN_RECOVERY_INTERVAL_MS", max(base.TxnRecoveryIntervalMS, 1))
	txnRecoveryStaleMs := getenvInt("PERF_TXN_RECOVERY_STALE_MS", max(base.TxnRecoveryStaleMS, 1))
	txnRecoveryBatch := getenvInt("PERF_TXN_RECOVERY_BATCH", max(base.TxnRecoveryBatchSize, 1))

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
	if txnRecoveryIntervalMs <= 0 {
		return perfConfig{}, fmt.Errorf("PERF_TXN_RECOVERY_INTERVAL_MS must be > 0")
	}
	if txnRecoveryStaleMs <= 0 {
		return perfConfig{}, fmt.Errorf("PERF_TXN_RECOVERY_STALE_MS must be > 0")
	}
	if txnRecoveryBatch <= 0 {
		return perfConfig{}, fmt.Errorf("PERF_TXN_RECOVERY_BATCH must be > 0")
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
		TxnAsyncOpts: service.TransferAsyncProcessorOptions{
			InitWorkers:       max(base.TxnAsyncWorkersInit, 1),
			PaySuccessWorkers: max(base.TxnAsyncWorkersPaySuccess, 1),
			InitQueueSize:     max(base.TxnAsyncQueueSizeInit, 1),
			PaySuccessQueue:   max(base.TxnAsyncQueueSizePaySuccess, 1),
		},
		Duration:            time.Duration(durationSec) * time.Second,
		Concurrency:         concurrency,
		WarmupRequests:      max(warmup, 0),
		RequestTimeout:      time.Duration(requestTimeoutMs) * time.Millisecond,
		MaxBodyBytes:        int64(maxBodyBytes),
		WebhookPollInterval: time.Duration(webhookPollMs) * time.Millisecond,
		WebhookWaitTimeout:  time.Duration(webhookWaitTimeoutMs) * time.Millisecond,
		TxnRecoveryInterval: time.Duration(txnRecoveryIntervalMs) * time.Millisecond,
		TxnRecoveryStale:    time.Duration(txnRecoveryStaleMs) * time.Millisecond,
		TxnRecoveryBatch:    txnRecoveryBatch,
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

	runtimeEnv, err := setupPerfServer(
		ctx,
		repo,
		redisClient,
		secretManager,
		cfg.ProcessingTTLSeconds,
		cfg.TxnAsyncOpts,
		cfg.TxnRecoveryBatch,
		cfg.TxnRecoveryInterval,
		cfg.TxnRecoveryStale,
		cfg.WebhookPollInterval,
		cfg.MaxBodyBytes,
	)
	if err != nil {
		return err
	}
	defer runtimeEnv.Server.Close()
	defer runtimeEnv.WebhookServer.Close()

	initialFromBalance, initialBookBalance, initialToBalance, err := snapshotBalances(
		repo,
		runtimeEnv.TransferFromAccountNo,
		runtimeEnv.TransferBookToAccount,
		runtimeEnv.TransferToAccountNo,
	)
	if err != nil {
		return err
	}

	fmt.Printf("[perf] merchant_no=%s from_account_no=%s book_to_account_no=%s to_account_no=%s\n",
		runtimeEnv.MerchantNo, runtimeEnv.TransferFromAccountNo, runtimeEnv.TransferBookToAccount, runtimeEnv.TransferToAccountNo)
	fmt.Printf("[perf] duration=%s concurrency=%d warmup=%d timeout=%s\n",
		cfg.Duration, cfg.Concurrency, cfg.WarmupRequests, cfg.RequestTimeout)
	fmt.Printf("[perf] mode=webhook-e2e webhook_poll=%s webhook_wait_timeout=%s\n", cfg.WebhookPollInterval, cfg.WebhookWaitTimeout)
	fmt.Printf("[perf] txn_async init_workers=%d pay_success_workers=%d init_queue=%d pay_success_queue=%d\n",
		cfg.TxnAsyncOpts.InitWorkers, cfg.TxnAsyncOpts.PaySuccessWorkers, cfg.TxnAsyncOpts.InitQueueSize, cfg.TxnAsyncOpts.PaySuccessQueue)
	fmt.Printf("[perf] txn_recovery interval=%s stale=%s batch=%d\n", cfg.TxnRecoveryInterval, cfg.TxnRecoveryStale, cfg.TxnRecoveryBatch)

	httpClient := &http.Client{Timeout: cfg.RequestTimeout}
	const bookTransferExpireInDays = int64(30)
	fireBookTransfer := func(nonce string) resultRow {
		outTradeNo := "ord_perf_book_transfer_" + nonce
		payload := map[string]any{
			"out_trade_no":      outTradeNo,
			"transfer_scene":    "P2P",
			"from_account_no":   runtimeEnv.TransferFromAccountNo,
			"to_account_no":     runtimeEnv.TransferBookToAccount,
			"to_expire_in_days": bookTransferExpireInDays,
			"amount":            1,
		}
		return fireAPIPostOnce(httpClient, runtimeEnv.Server.URL, runtimeEnv.MerchantNo, runtimeEnv.Secret, nonce, "/api/v1/transactions/transfer", outTradeNo, payload, cfg.MaxBodyBytes, runtimeEnv.WebhookSink, true)
	}
	fireBookRefund := func(nonce, originTxnNo string) resultRow {
		outTradeNo := "ord_perf_book_refund_" + nonce
		payload := map[string]any{
			"out_trade_no":     outTradeNo,
			"refund_of_txn_no": originTxnNo,
			"amount":           1,
		}
		return fireAPIPostOnce(httpClient, runtimeEnv.Server.URL, runtimeEnv.MerchantNo, runtimeEnv.Secret, nonce, "/api/v1/transactions/refund", outTradeNo, payload, cfg.MaxBodyBytes, runtimeEnv.WebhookSink, true)
	}
	fireTransfer := func(nonce string) resultRow {
		outTradeNo := "ord_perf_transfer_" + nonce
		payload := map[string]any{
			"out_trade_no":    outTradeNo,
			"transfer_scene":  "P2P",
			"from_account_no": runtimeEnv.TransferFromAccountNo,
			"to_account_no":   runtimeEnv.TransferToAccountNo,
			"amount":          1,
		}
		return fireAPIPostOnce(httpClient, runtimeEnv.Server.URL, runtimeEnv.MerchantNo, runtimeEnv.Secret, nonce, "/api/v1/transactions/transfer", outTradeNo, payload, cfg.MaxBodyBytes, runtimeEnv.WebhookSink, true)
	}
	fireRefund := func(nonce, originTxnNo string) resultRow {
		outTradeNo := "ord_perf_refund_" + nonce
		payload := map[string]any{
			"out_trade_no":     outTradeNo,
			"refund_of_txn_no": originTxnNo,
			"amount":           1,
		}
		return fireAPIPostOnce(httpClient, runtimeEnv.Server.URL, runtimeEnv.MerchantNo, runtimeEnv.Secret, nonce, "/api/v1/transactions/refund", outTradeNo, payload, cfg.MaxBodyBytes, runtimeEnv.WebhookSink, true)
	}

	if cfg.WarmupRequests > 0 {
		for i := 0; i < cfg.WarmupRequests; i++ {
			nonce := "warmup-book-transfer-" + strconv.Itoa(i)
			row := fireBookTransfer(nonce)
			awaitWebhookResult(&row, cfg.WebhookWaitTimeout)
			if !row.SubmitOK || !row.E2EOK || row.TransportErr != "" || row.E2EErr != "" || strings.TrimSpace(row.TxnNo) == "" {
				return fmt.Errorf("book transfer warmup failed: status=%d code=%s submit_ok=%t e2e_ok=%t txn_no=%s transport_err=%s e2e_err=%s",
					row.StatusCode, row.Code, row.SubmitOK, row.E2EOK, row.TxnNo, row.TransportErr, row.E2EErr)
			}
			bookOriginTxnNo := row.TxnNo
			nonce = "warmup-book-refund-" + strconv.Itoa(i)
			row = fireBookRefund(nonce, bookOriginTxnNo)
			awaitWebhookResult(&row, cfg.WebhookWaitTimeout)
			if !row.SubmitOK || !row.E2EOK || row.TransportErr != "" || row.E2EErr != "" {
				return fmt.Errorf("book refund warmup failed: status=%d code=%s submit_ok=%t e2e_ok=%t origin_txn_no=%s transport_err=%s e2e_err=%s",
					row.StatusCode, row.Code, row.SubmitOK, row.E2EOK, bookOriginTxnNo, row.TransportErr, row.E2EErr)
			}

			nonce = "warmup-transfer-" + strconv.Itoa(i)
			row = fireTransfer(nonce)
			awaitWebhookResult(&row, cfg.WebhookWaitTimeout)
			if !row.SubmitOK || !row.E2EOK || row.TransportErr != "" || row.E2EErr != "" || strings.TrimSpace(row.TxnNo) == "" {
				return fmt.Errorf("transfer warmup failed: status=%d code=%s submit_ok=%t e2e_ok=%t txn_no=%s transport_err=%s e2e_err=%s",
					row.StatusCode, row.Code, row.SubmitOK, row.E2EOK, row.TxnNo, row.TransportErr, row.E2EErr)
			}

			originTxnNo := row.TxnNo
			nonce = "warmup-refund-" + strconv.Itoa(i)
			row = fireRefund(nonce, originTxnNo)
			awaitWebhookResult(&row, cfg.WebhookWaitTimeout)
			if !row.SubmitOK || !row.E2EOK || row.TransportErr != "" || row.E2EErr != "" {
				return fmt.Errorf("refund warmup failed: status=%d code=%s submit_ok=%t e2e_ok=%t origin_txn_no=%s transport_err=%s e2e_err=%s",
					row.StatusCode, row.Code, row.SubmitOK, row.E2EOK, originTxnNo, row.TransportErr, row.E2EErr)
			}
			renderCountProgress("warmup", i+1, cfg.WarmupRequests)
		}
		fmt.Print("\n")
		if err := assertBalances(repo,
			runtimeEnv.TransferFromAccountNo,
			runtimeEnv.TransferBookToAccount,
			runtimeEnv.TransferToAccountNo,
			initialFromBalance,
			initialBookBalance,
			initialToBalance,
			"warmup",
		); err != nil {
			return err
		}
	}

	fmt.Printf("[perf] scenario=book_transfer start\n")
	bookTransferRows, bookTransferMetrics := runLoadDuration(
		cfg.Duration,
		cfg.Concurrency,
		cfg.WebhookWaitTimeout,
		"book_transfer",
		true,
		fireBookTransfer,
	)
	bookOriginTxnNos := collectSucceededTxnNos(bookTransferRows)
	if err := assertScenarioMetrics("book_transfer", bookTransferMetrics, true); err != nil {
		return err
	}
	if len(bookOriginTxnNos) == 0 {
		return fmt.Errorf("book_transfer completed but no successful txn_no produced")
	}
	if len(bookOriginTxnNos) != bookTransferMetrics.Total {
		return fmt.Errorf("book_transfer successful txn_no count mismatch: got=%d total=%d", len(bookOriginTxnNos), bookTransferMetrics.Total)
	}
	if err := assertUniqueTxnNos("book_transfer", bookOriginTxnNos); err != nil {
		return err
	}
	if err := assertTxnStatusByOutTradePrefix(pool, "ord_perf_book_transfer_book_transfer-", service.TxnStatusRecvSuccess, bookTransferMetrics.Total, "book_transfer"); err != nil {
		return err
	}
	if err := assertNoInFlightTxnByOutTradePrefix(pool, "ord_perf_book_transfer_book_transfer-", "book_transfer"); err != nil {
		return err
	}
	if err := assertChangeLogCountsByOutTradePrefix(pool, "ord_perf_book_transfer_book_transfer-", bookTransferMetrics.Total*2, bookTransferMetrics.Total, "book_transfer"); err != nil {
		return err
	}
	if err := assertBalances(repo,
		runtimeEnv.TransferFromAccountNo,
		runtimeEnv.TransferBookToAccount,
		runtimeEnv.TransferToAccountNo,
		initialFromBalance-int64(len(bookOriginTxnNos)),
		initialBookBalance+int64(len(bookOriginTxnNos)),
		initialToBalance,
		"book_transfer",
	); err != nil {
		return err
	}
	fmt.Printf("[perf] scenario=book_transfer done successful_origins=%d\n", len(bookOriginTxnNos))

	fmt.Printf("[perf] scenario=book_refund start input_origins=%d\n", len(bookOriginTxnNos))
	bookRefundMetrics := runLoadByOriginTxnNos(
		bookOriginTxnNos,
		cfg.Concurrency,
		cfg.WebhookWaitTimeout,
		"book_refund",
		true,
		fireBookRefund,
	)
	if err := assertScenarioMetrics("book_refund", bookRefundMetrics, true); err != nil {
		return err
	}
	if err := assertScenarioInputCoverage("book_refund", len(bookOriginTxnNos), bookRefundMetrics); err != nil {
		return err
	}
	if err := assertTxnStatusByOutTradePrefix(pool, "ord_perf_book_refund_book_refund-", service.TxnStatusRecvSuccess, bookRefundMetrics.Total, "book_refund"); err != nil {
		return err
	}
	if err := assertNoInFlightTxnByOutTradePrefix(pool, "ord_perf_book_refund_book_refund-", "book_refund"); err != nil {
		return err
	}
	if err := assertChangeLogCountsByOutTradePrefix(pool, "ord_perf_book_refund_book_refund-", bookRefundMetrics.Total*2, bookRefundMetrics.Total, "book_refund"); err != nil {
		return err
	}
	if err := assertBalances(repo,
		runtimeEnv.TransferFromAccountNo,
		runtimeEnv.TransferBookToAccount,
		runtimeEnv.TransferToAccountNo,
		initialFromBalance,
		initialBookBalance,
		initialToBalance,
		"book_refund",
	); err != nil {
		return err
	}
	fmt.Printf("[perf] scenario=book_refund done\n")

	fmt.Printf("[perf] scenario=transfer start\n")
	transferRows, transferMetrics := runLoadDuration(
		cfg.Duration,
		cfg.Concurrency,
		cfg.WebhookWaitTimeout,
		"transfer",
		true,
		fireTransfer,
	)
	originTxnNos := collectSucceededTxnNos(transferRows)
	if err := assertScenarioMetrics("transfer", transferMetrics, true); err != nil {
		return err
	}
	if len(originTxnNos) == 0 {
		return fmt.Errorf("transfer completed but no successful txn_no produced")
	}
	if len(originTxnNos) != transferMetrics.Total {
		return fmt.Errorf("transfer successful txn_no count mismatch: got=%d total=%d", len(originTxnNos), transferMetrics.Total)
	}
	if err := assertUniqueTxnNos("transfer", originTxnNos); err != nil {
		return err
	}
	if err := assertTxnStatusByOutTradePrefix(pool, "ord_perf_transfer_transfer-", service.TxnStatusRecvSuccess, transferMetrics.Total, "transfer"); err != nil {
		return err
	}
	if err := assertNoInFlightTxnByOutTradePrefix(pool, "ord_perf_transfer_transfer-", "transfer"); err != nil {
		return err
	}
	if err := assertChangeLogCountsByOutTradePrefix(pool, "ord_perf_transfer_transfer-", transferMetrics.Total*2, 0, "transfer"); err != nil {
		return err
	}
	if err := assertBalances(repo,
		runtimeEnv.TransferFromAccountNo,
		runtimeEnv.TransferBookToAccount,
		runtimeEnv.TransferToAccountNo,
		initialFromBalance-int64(len(originTxnNos)),
		initialBookBalance,
		initialToBalance+int64(len(originTxnNos)),
		"transfer",
	); err != nil {
		return err
	}
	fmt.Printf("[perf] scenario=transfer done successful_origins=%d\n", len(originTxnNos))

	fmt.Printf("[perf] scenario=refund start input_origins=%d\n", len(originTxnNos))
	refundMetrics := runLoadByOriginTxnNos(
		originTxnNos,
		cfg.Concurrency,
		cfg.WebhookWaitTimeout,
		"refund",
		true,
		fireRefund,
	)
	if err := assertScenarioMetrics("refund", refundMetrics, true); err != nil {
		return err
	}
	if err := assertScenarioInputCoverage("refund", len(originTxnNos), refundMetrics); err != nil {
		return err
	}
	if err := assertTxnStatusByOutTradePrefix(pool, "ord_perf_refund_refund-", service.TxnStatusRecvSuccess, refundMetrics.Total, "refund"); err != nil {
		return err
	}
	if err := assertNoInFlightTxnByOutTradePrefix(pool, "ord_perf_refund_refund-", "refund"); err != nil {
		return err
	}
	if err := assertChangeLogCountsByOutTradePrefix(pool, "ord_perf_refund_refund-", refundMetrics.Total*2, 0, "refund"); err != nil {
		return err
	}
	if err := assertBalances(repo,
		runtimeEnv.TransferFromAccountNo,
		runtimeEnv.TransferBookToAccount,
		runtimeEnv.TransferToAccountNo,
		initialFromBalance,
		initialBookBalance,
		initialToBalance,
		"refund",
	); err != nil {
		return err
	}
	fmt.Printf("[perf] scenario=refund done\n")

	finalFromBalance, finalBookBalance, finalToBalance, err := snapshotBalances(
		repo,
		runtimeEnv.TransferFromAccountNo,
		runtimeEnv.TransferBookToAccount,
		runtimeEnv.TransferToAccountNo,
	)
	if err != nil {
		return err
	}
	if finalFromBalance != initialFromBalance || finalBookBalance != initialBookBalance || finalToBalance != initialToBalance {
		return fmt.Errorf("balance not restored after perf run: from_balance=%d want=%d book_balance=%d want=%d to_balance=%d want=%d",
			finalFromBalance, initialFromBalance, finalBookBalance, initialBookBalance, finalToBalance, initialToBalance)
	}

	reports := []string{
		fmt.Sprintf("# Core Txn Perf Report\n\n- generated_at_utc: %s\n- duration_per_transfer_scenario: %s\n- concurrency: %d\n- warmup_pair_count_per_mode: %d\n- transfer_origins_for_refund: %d\n- book_transfer_origins_for_refund: %d\n- final_balance_check: PASS (from=%d, to=%d, book=%d)",
			time.Now().UTC().Format(time.RFC3339),
			cfg.Duration,
			cfg.Concurrency,
			cfg.WarmupRequests,
			len(originTxnNos),
			len(bookOriginTxnNos),
			finalFromBalance,
			finalToBalance,
			finalBookBalance,
		),
		buildMetricsMarkdown(transferMetrics),
		buildMetricsMarkdown(refundMetrics),
		buildMetricsMarkdown(bookTransferMetrics),
		buildMetricsMarkdown(bookRefundMetrics),
	}
	fullReport := strings.Join(reports, "\n\n")
	fmt.Println(buildMetricsMarkdown(transferMetrics))
	fmt.Println(buildMetricsMarkdown(refundMetrics))
	fmt.Println(buildMetricsMarkdown(bookTransferMetrics))
	fmt.Println(buildMetricsMarkdown(bookRefundMetrics))
	if err := os.WriteFile("perf.md", []byte(fullReport+"\n"), 0o644); err != nil {
		return fmt.Errorf("write perf.md failed: %w", err)
	}
	return nil
}

func snapshotBalances(repo *db.Repository, fromAccountNo, bookAccountNo, toAccountNo string) (int64, int64, int64, error) {
	from, ok := repo.GetAccount(fromAccountNo)
	if !ok {
		return 0, 0, 0, fmt.Errorf("from account not found: %s", fromAccountNo)
	}
	book, ok := repo.GetAccount(bookAccountNo)
	if !ok {
		return 0, 0, 0, fmt.Errorf("book account not found: %s", bookAccountNo)
	}
	to, ok := repo.GetAccount(toAccountNo)
	if !ok {
		return 0, 0, 0, fmt.Errorf("to account not found: %s", toAccountNo)
	}
	return from.Balance, book.Balance, to.Balance, nil
}

func assertBalances(
	repo *db.Repository,
	fromAccountNo, bookAccountNo, toAccountNo string,
	wantFrom, wantBook, wantTo int64,
	stage string,
) error {
	gotFrom, gotBook, gotTo, err := snapshotBalances(repo, fromAccountNo, bookAccountNo, toAccountNo)
	if err != nil {
		return fmt.Errorf("%s balance check failed: %w", stage, err)
	}
	if gotFrom != wantFrom || gotBook != wantBook || gotTo != wantTo {
		return fmt.Errorf("%s balance mismatch: from_balance=%d want=%d book_balance=%d want=%d to_balance=%d want=%d",
			stage, gotFrom, wantFrom, gotBook, wantBook, gotTo, wantTo)
	}
	return nil
}

func assertScenarioMetrics(stage string, m metrics, requireWebhook bool) error {
	if m.Total <= 0 {
		return fmt.Errorf("%s no requests executed", stage)
	}
	if m.TransportErr != 0 || m.HTTP5xx != 0 || m.HTTP4xxOther != 0 || m.HTTP409 != 0 {
		return fmt.Errorf("%s request errors found: transport_err=%d http_5xx=%d http_4xx_other=%d http_409=%d",
			stage, m.TransportErr, m.HTTP5xx, m.HTTP4xxOther, m.HTTP409)
	}
	if m.SubmitSuccess != m.Total {
		return fmt.Errorf("%s submit success mismatch: submit_success=%d total=%d", stage, m.SubmitSuccess, m.Total)
	}
	if requireWebhook && m.E2ESuccess != m.Total {
		return fmt.Errorf("%s e2e success mismatch: e2e_success=%d total=%d", stage, m.E2ESuccess, m.Total)
	}
	if successCount, ok := m.ErrorCodeHistogram["SUCCESS"]; !ok || successCount != m.Total {
		return fmt.Errorf("%s response code mismatch: SUCCESS=%d total=%d", stage, successCount, m.Total)
	}
	return nil
}

func assertScenarioInputCoverage(stage string, wantInputs int, m metrics) error {
	if m.Total != wantInputs {
		return fmt.Errorf("%s total mismatch: total=%d want=%d", stage, m.Total, wantInputs)
	}
	if m.SubmitSuccess != wantInputs {
		return fmt.Errorf("%s submit success mismatch: submit_success=%d want=%d", stage, m.SubmitSuccess, wantInputs)
	}
	if m.E2ESuccess != wantInputs {
		return fmt.Errorf("%s e2e success mismatch: e2e_success=%d want=%d", stage, m.E2ESuccess, wantInputs)
	}
	return nil
}

func assertUniqueTxnNos(stage string, txnNos []string) error {
	seen := make(map[string]struct{}, len(txnNos))
	for _, txnNo := range txnNos {
		key := strings.TrimSpace(txnNo)
		if key == "" {
			return fmt.Errorf("%s contains empty txn_no", stage)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%s contains duplicate txn_no: %s", stage, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func assertTxnStatusByOutTradePrefix(pool *pgxpool.Pool, outTradePrefix, wantStatus string, wantTotal int, stage string) error {
	if pool == nil {
		return fmt.Errorf("%s status check failed: db pool is nil", stage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := pool.Query(ctx, `
SELECT status, COUNT(*)
FROM txn
WHERE out_trade_no LIKE $1
GROUP BY status
`, outTradePrefix+"%")
	if err != nil {
		return fmt.Errorf("%s status check query failed: %w", stage, err)
	}
	defer rows.Close()

	total := 0
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return fmt.Errorf("%s status check scan failed: %w", stage, err)
		}
		total += count
		if strings.TrimSpace(status) != wantStatus {
			return fmt.Errorf("%s unexpected txn status for prefix=%s: status=%s count=%d want_status=%s",
				stage, outTradePrefix, status, count, wantStatus)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%s status check rows error: %w", stage, err)
	}
	if total != wantTotal {
		return fmt.Errorf("%s txn count mismatch for prefix=%s: got=%d want=%d",
			stage, outTradePrefix, total, wantTotal)
	}
	return nil
}

func assertNoInFlightTxnByOutTradePrefix(pool *pgxpool.Pool, outTradePrefix, stage string) error {
	if pool == nil {
		return fmt.Errorf("%s in-flight check failed: db pool is nil", stage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var inFlight int
	err := pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM txn
WHERE out_trade_no LIKE $1
  AND status IN ('INIT', 'PAY_SUCCESS')
`, outTradePrefix+"%").Scan(&inFlight)
	if err != nil {
		return fmt.Errorf("%s in-flight check query failed: %w", stage, err)
	}
	if inFlight != 0 {
		return fmt.Errorf("%s in-flight txn remains for prefix=%s: count=%d", stage, outTradePrefix, inFlight)
	}
	return nil
}

func assertChangeLogCountsByOutTradePrefix(pool *pgxpool.Pool, outTradePrefix string, wantAccountChange, wantBookChange int, stage string) error {
	if pool == nil {
		return fmt.Errorf("%s change-log check failed: db pool is nil", stage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var gotAccountChange int
	if err := pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM account_change_log l
JOIN txn t ON t.txn_no = l.txn_no
WHERE t.out_trade_no LIKE $1
`, outTradePrefix+"%").Scan(&gotAccountChange); err != nil {
		return fmt.Errorf("%s account_change_log count query failed: %w", stage, err)
	}
	if gotAccountChange != wantAccountChange {
		return fmt.Errorf("%s account_change_log count mismatch for prefix=%s: got=%d want=%d",
			stage, outTradePrefix, gotAccountChange, wantAccountChange)
	}

	var gotBookChange int
	if err := pool.QueryRow(ctx, `
SELECT COUNT(*)
FROM account_book_change_log l
JOIN txn t ON t.txn_no = l.txn_no
WHERE t.out_trade_no LIKE $1
`, outTradePrefix+"%").Scan(&gotBookChange); err != nil {
		return fmt.Errorf("%s account_book_change_log count query failed: %w", stage, err)
	}
	if gotBookChange != wantBookChange {
		return fmt.Errorf("%s account_book_change_log count mismatch for prefix=%s: got=%d want=%d",
			stage, outTradePrefix, gotBookChange, wantBookChange)
	}
	return nil
}

func collectSucceededTxnNos(rows []resultRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.SubmitOK && row.E2EOK && strings.TrimSpace(row.TxnNo) != "" {
			out = append(out, row.TxnNo)
		}
	}
	return out
}

func setupPerfServer(
	ctx context.Context,
	repo *db.Repository,
	redisClient *goredis.Client,
	secretManager *db.MerchantSecretManager,
	processingTTLSeconds int,
	txnAsyncOpts service.TransferAsyncProcessorOptions,
	txnRecoveryBatch int,
	txnRecoveryInterval time.Duration,
	txnRecoveryStale time.Duration,
	webhookPollInterval time.Duration,
	maxBodyBytes int64,
) (*perfRuntime, error) {
	ids := idpkg.NewRuntimeUUIDProvider()
	merchantSvc := service.NewMerchantService(repo, ids)
	customerSvc := service.NewCustomerService(repo, ids)
	transferSvc := service.NewTransferService(repo, ids)
	transferRoutingSvc := service.NewTransferRoutingService(repo)
	accountResolver := service.NewAccountResolver(repo, customerSvc)
	querySvc := service.NewTxnQueryService(repo)

	merchant, err := merchantSvc.CreateMerchant("", "perf-core-txn")
	if err != nil {
		return nil, fmt.Errorf("create merchant: %w", err)
	}

	fromCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, "u_perf_from")
	if err != nil {
		return nil, fmt.Errorf("create from customer: %w", err)
	}
	transferCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, toNonBookTransferOutUser)
	if err != nil {
		return nil, fmt.Errorf("create transfer customer: %w", err)
	}
	bookTransferCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, toBookTransferOutUser)
	if err != nil {
		return nil, fmt.Errorf("create book transfer customer: %w", err)
	}

	fromAccountNo, err := repo.NewAccountNo(merchant.MerchantNo, "CUSTOMER")
	if err != nil {
		return nil, fmt.Errorf("new from account no: %w", err)
	}
	toAccountNo, err := repo.NewAccountNo(merchant.MerchantNo, "CUSTOMER")
	if err != nil {
		return nil, fmt.Errorf("new transfer account no: %w", err)
	}
	toBookTransferAccountNo, err := repo.NewAccountNo(merchant.MerchantNo, "CUSTOMER")
	if err != nil {
		return nil, fmt.Errorf("new book transfer account no: %w", err)
	}

	if err := repo.CreateAccount(service.Account{
		AccountNo:         fromAccountNo,
		MerchantNo:        merchant.MerchantNo,
		CustomerNo:        fromCustomer.CustomerNo,
		AccountType:       "CUSTOMER",
		AllowOverdraft:    true,
		MaxOverdraftLimit: 0,
		AllowDebitOut:     true,
		AllowCreditIn:     true,
		AllowTransfer:     true,
		Balance:           0,
	}); err != nil {
		return nil, fmt.Errorf("create from account: %w", err)
	}
	if err := repo.CreateAccount(service.Account{
		AccountNo:         toBookTransferAccountNo,
		MerchantNo:        merchant.MerchantNo,
		CustomerNo:        bookTransferCustomer.CustomerNo,
		AccountType:       "CUSTOMER",
		AllowOverdraft:    false,
		MaxOverdraftLimit: 0,
		AllowDebitOut:     true,
		AllowCreditIn:     true,
		AllowTransfer:     true,
		BookEnabled:       true,
		Balance:           0,
	}); err != nil {
		return nil, fmt.Errorf("create book transfer account: %w", err)
	}
	if err := repo.CreateAccount(service.Account{
		AccountNo:         toAccountNo,
		MerchantNo:        merchant.MerchantNo,
		CustomerNo:        transferCustomer.CustomerNo,
		AccountType:       "CUSTOMER",
		AllowOverdraft:    false,
		MaxOverdraftLimit: 0,
		AllowDebitOut:     true,
		AllowCreditIn:     true,
		AllowTransfer:     true,
		BookEnabled:       false,
		Balance:           0,
	}); err != nil {
		return nil, fmt.Errorf("create transfer account: %w", err)
	}
	secret, _, err := secretManager.RotateSecret(context.Background(), merchant.MerchantNo)
	if err != nil {
		return nil, fmt.Errorf("rotate merchant secret: %w", err)
	}

	processingGuard := service.NewRedisProcessingGuard(redisClient, time.Duration(processingTTLSeconds)*time.Second)
	asyncProcessor := service.NewTransferAsyncProcessorWithGuardAndOptions(repo, processingGuard, txnAsyncOpts)
	txnRecoveryWorker := service.NewTransferRecoveryWorkerWithStaleThreshold(repo, asyncProcessor, txnRecoveryBatch, txnRecoveryStale)

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
		return nil, fmt.Errorf("upsert webhook config: %w", err)
	}
	webhookWorker := service.NewWebhookWorker(repo, secretManager, 8, 500, []int{1})
	asyncProcessor.SetWebhookDispatcher(webhookWorker)
	go txnRecoveryWorker.Start(ctx, txnRecoveryInterval)
	go webhookWorker.Start(ctx, webhookPollInterval)

	business := api.NewBusinessHandler(
		transferSvc,
		repo,
		transferRoutingSvc,
		asyncProcessor,
		accountResolver,
		repo,
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
		AuthMiddleware:  authMw,
		SecretRotator:   secretManager,
		Business:        business,
		MerchantCreator: merchantSvc,
	})
	return &perfRuntime{
		Server:                httptest.NewServer(r),
		WebhookServer:         webhookServer,
		WebhookSink:           sink,
		MerchantNo:            merchant.MerchantNo,
		Secret:                secret,
		TransferFromAccountNo: fromAccountNo,
		TransferBookToAccount: toBookTransferAccountNo,
		TransferToAccountNo:   toAccountNo,
	}, nil
}

func runLoadDuration(
	duration time.Duration,
	concurrency int,
	webhookWaitTimeout time.Duration,
	mode string,
	requireWebhook bool,
	fire scenarioFireFn,
) ([]resultRow, metrics) {
	var idCounter uint64
	start := time.Now()
	deadline := start.Add(duration)
	progressDone := make(chan struct{})
	progressExited := make(chan struct{})
	go func() {
		ticker := time.NewTicker(progressTickInterval)
		defer ticker.Stop()
		defer close(progressExited)
		for {
			select {
			case <-ticker.C:
				renderDurationProgress(mode+" submit", time.Since(start), duration, int64(atomic.LoadUint64(&idCounter)))
			case <-progressDone:
				renderDurationProgress(mode+" submit", duration, duration, int64(atomic.LoadUint64(&idCounter)))
				fmt.Print("\n")
				return
			}
		}
	}()

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
				results <- fire(nonce)
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
	close(progressDone)
	<-progressExited

	submitElapsed := time.Since(start)
	if requireWebhook {
		step := progressUpdateStep(len(pending))
		for i, idx := range pending {
			awaitWebhookResult(&rows[idx], webhookWaitTimeout)
			done := i + 1
			if done%step == 0 || done == len(pending) {
				renderCountProgress(mode+" webhook", done, len(pending))
			}
		}
		if len(pending) > 0 {
			renderCountProgress(mode+" webhook", len(pending), len(pending))
			fmt.Print("\n")
		}
	}

	return rows, aggregate(rows, submitElapsed, time.Since(start), mode)
}

type refundFireFn func(nonce, originTxnNo string) resultRow

func runLoadByOriginTxnNos(
	originTxnNos []string,
	concurrency int,
	webhookWaitTimeout time.Duration,
	mode string,
	requireWebhook bool,
	fire refundFireFn,
) metrics {
	if len(originTxnNos) == 0 {
		return aggregate(nil, 0, 0, mode)
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(originTxnNos) {
		concurrency = len(originTxnNos)
	}

	type refundJob struct {
		Idx      int
		OriginNo string
	}
	jobs := make(chan refundJob, len(originTxnNos))
	results := make(chan resultRow, len(originTxnNos))
	start := time.Now()

	for i, originTxnNo := range originTxnNos {
		jobs <- refundJob{Idx: i + 1, OriginNo: originTxnNo}
	}
	close(jobs)

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobs {
				nonce := mode + "-" + strconv.Itoa(workerID) + "-" + strconv.Itoa(job.Idx)
				results <- fire(nonce, job.OriginNo)
			}
		}(w)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	rows := make([]resultRow, 0, len(originTxnNos))
	pending := make([]int, 0, len(originTxnNos))
	step := progressUpdateStep(len(originTxnNos))
	for row := range results {
		idx := len(rows)
		rows = append(rows, row)
		if requireWebhook && row.SubmitOK && row.WebhookDone != nil {
			pending = append(pending, idx)
		}
		done := len(rows)
		if done%step == 0 || done == len(originTxnNos) {
			renderCountProgress(mode+" submit", done, len(originTxnNos))
		}
	}
	if len(originTxnNos) > 0 {
		renderCountProgress(mode+" submit", len(originTxnNos), len(originTxnNos))
		fmt.Print("\n")
	}
	submitElapsed := time.Since(start)
	if requireWebhook {
		step = progressUpdateStep(len(pending))
		for i, idx := range pending {
			awaitWebhookResult(&rows[idx], webhookWaitTimeout)
			done := i + 1
			if done%step == 0 || done == len(pending) {
				renderCountProgress(mode+" webhook", done, len(pending))
			}
		}
		if len(pending) > 0 {
			renderCountProgress(mode+" webhook", len(pending), len(pending))
			fmt.Print("\n")
		}
	}
	return aggregate(rows, submitElapsed, time.Since(start), mode)
}

func fireAPIPostOnce(
	client *http.Client,
	baseURL, merchantNo, secret, nonce, path, outTradeNo string,
	payload map[string]any,
	maxBodyBytes int64,
	sink *webhookSink,
	requireWebhook bool,
) resultRow {
	var done <-chan time.Time
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
	txnNo := ""
	if dataMap, ok := parsed["data"].(map[string]any); ok {
		if v, ok := dataMap["txn_no"].(string); ok {
			txnNo = strings.TrimSpace(v)
		}
	}
	row := resultRow{StatusCode: resp.StatusCode, Code: code, TxnNo: txnNo, SubmitLatency: submitLatency, SubmitStartedAt: submitStart}
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
	case doneAt := <-row.WebhookDone:
		row.E2ECompletedAt = doneAt
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

func progressUpdateStep(total int) int {
	if total <= 0 {
		return 1
	}
	step := total / 20
	if step <= 0 {
		return 1
	}
	return step
}

func renderCountProgress(label string, done, total int) {
	if total <= 0 {
		return
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	ratio := float64(done) / float64(total)
	bar := buildProgressBar(ratio, progressBarWidth)
	percent := int(ratio*100 + 0.5)
	fmt.Printf("\r[perf] progress=%s %s %3d%% (%d/%d)", label, bar, percent, done, total)
}

func renderDurationProgress(label string, elapsed, total time.Duration, processed int64) {
	if total <= 0 {
		return
	}
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > total {
		elapsed = total
	}
	ratio := float64(elapsed) / float64(total)
	bar := buildProgressBar(ratio, progressBarWidth)
	percent := int(ratio*100 + 0.5)
	fmt.Printf("\r[perf] progress=%s %s %3d%% elapsed=%s/%s processed=%d", label, bar, percent, elapsed.Round(time.Second), total.Round(time.Second), processed)
}

func buildProgressBar(ratio float64, width int) string {
	if width <= 0 {
		width = progressBarWidth
	}
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]"
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
	idx := (len(vals) - 1) * p / 100
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
