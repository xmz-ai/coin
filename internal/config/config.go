package config

import (
	"os"
	"strconv"
)

const (
	localKMSKeyV1Env = "LOCAL_KMS_KEY_V1"
)

type Config struct {
	HTTPAddr                       string
	PostgresDSN                    string
	RedisAddr                      string
	RedisPassword                  string
	RedisDB                        int
	MerchantSecretPassphrase       string
	AuthWindowSeconds              int
	ProcessingKeyTTLSeconds        int
	TxnProcessingGuardTTLMS        int
	TxnAsyncStageWorkersInit       int
	TxnAsyncStageWorkersPaySuccess int
	TxnAsyncQueueSizeInit          int
	TxnAsyncQueueSizePaySuccess    int
	TxnRecoveryIntervalMS          int
	TxnRecoveryStaleMS             int
	TxnRecoveryBatchSize           int
	WebhookMaxRetries              int
	WebhookWorkerBatchSize         int
	WebhookWorkerIntervalMS        int
	WebhookRetryBackoffMinute      []int
	NotifyCompensationIntervalMS   int
}

func Load() Config {
	redisDB, _ := strconv.Atoi(getenv("REDIS_DB", "0"))
	authWindowSeconds, _ := strconv.Atoi(getenv("AUTH_WINDOW_SECONDS", "300"))
	processingKeyTTLSeconds, _ := strconv.Atoi(getenv("PROCESSING_KEY_TTL_SECONDS", "300"))
	txnProcessingGuardTTLMS, _ := strconv.Atoi(getenv("TXN_PROCESSING_GUARD_TTL_MS", "300000"))
	txnAsyncStageWorkersInit, _ := strconv.Atoi(getenv("TXN_ASYNC_STAGE_WORKERS_INIT", "4"))
	txnAsyncStageWorkersPaySuccess, _ := strconv.Atoi(getenv("TXN_ASYNC_STAGE_WORKERS_PAY_SUCCESS", "4"))
	txnAsyncQueueSizeInit, _ := strconv.Atoi(getenv("TXN_ASYNC_QUEUE_SIZE_INIT", "10240"))
	txnAsyncQueueSizePaySuccess, _ := strconv.Atoi(getenv("TXN_ASYNC_QUEUE_SIZE_PAY_SUCCESS", "10240"))
	txnRecoveryIntervalMS, _ := strconv.Atoi(getenv("TXN_RECOVERY_INTERVAL_MS", "300000"))
	txnRecoveryStaleMS, _ := strconv.Atoi(getenv("TXN_RECOVERY_STALE_MS", "60000"))
	txnRecoveryBatchSize, _ := strconv.Atoi(getenv("TXN_RECOVERY_BATCH_SIZE", "1000"))
	webhookMaxRetries, _ := strconv.Atoi(getenv("WEBHOOK_MAX_RETRIES", "8"))
	webhookWorkerBatchSize, _ := strconv.Atoi(getenv("WEBHOOK_WORKER_BATCH_SIZE", "100"))
	webhookWorkerIntervalMS, _ := strconv.Atoi(getenv("WEBHOOK_WORKER_INTERVAL_MS", "1000"))
	notifyCompensationIntervalMS, _ := strconv.Atoi(getenv("NOTIFY_COMPENSATION_INTERVAL_MS", "1000"))
	return Config{
		HTTPAddr:                       getenv("HTTP_ADDR", ":8080"),
		PostgresDSN:                    getenv("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"),
		RedisAddr:                      getenv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:                  getenv("REDIS_PASSWORD", ""),
		RedisDB:                        redisDB,
		MerchantSecretPassphrase:       resolveMerchantSecretPassphrase(),
		AuthWindowSeconds:              authWindowSeconds,
		ProcessingKeyTTLSeconds:        processingKeyTTLSeconds,
		TxnProcessingGuardTTLMS:        txnProcessingGuardTTLMS,
		TxnAsyncStageWorkersInit:       txnAsyncStageWorkersInit,
		TxnAsyncStageWorkersPaySuccess: txnAsyncStageWorkersPaySuccess,
		TxnAsyncQueueSizeInit:          txnAsyncQueueSizeInit,
		TxnAsyncQueueSizePaySuccess:    txnAsyncQueueSizePaySuccess,
		TxnRecoveryIntervalMS:          txnRecoveryIntervalMS,
		TxnRecoveryStaleMS:             txnRecoveryStaleMS,
		TxnRecoveryBatchSize:           txnRecoveryBatchSize,
		WebhookMaxRetries:              webhookMaxRetries,
		WebhookWorkerBatchSize:         webhookWorkerBatchSize,
		WebhookWorkerIntervalMS:        webhookWorkerIntervalMS,
		WebhookRetryBackoffMinute:      []int{1, 5, 15, 60, 360},
		NotifyCompensationIntervalMS:   notifyCompensationIntervalMS,
	}
}

func resolveMerchantSecretPassphrase() string {
	return os.Getenv(localKMSKeyV1Env)
}

func getenv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}
