package config

import (
	"os"
	"strconv"
	"strings"
)

const (
	localKMSKeyV1Env = "LOCAL_KMS_KEY_V1"
)

type Config struct {
	HTTPAddr                     string
	PprofEnabled                 bool
	AdminEnabled                 bool
	AdminJWTSecret               string
	AdminAccessTokenTTLSeconds   int
	AdminRefreshTokenTTLSeconds  int
	AdminBootstrapUsername       string
	AdminBootstrapPassword       string
	PostgresDSN                  string
	PostgresMaxConns             int
	RedisAddr                    string
	RedisPassword                string
	RedisDB                      int
	MerchantSecretPassphrase     string
	AuthWindowSeconds            int
	ProcessingKeyTTLSeconds      int
	TxnProcessingGuardTTLMS      int
	TxnAsyncWorkersInit          int
	TxnAsyncWorkersPaySuccess    int
	TxnAsyncQueueSizeInit        int
	TxnAsyncQueueSizePaySuccess  int
	TxnAsyncProfileEnabled       bool
	TxnAsyncProfileLogIntervalMS int
	TxnRecoveryIntervalMS        int
	TxnRecoveryStaleMS           int
	TxnRecoveryBatchSize         int
	WebhookMaxRetries            int
	WebhookWorkerBatchSize       int
	WebhookWorkerIntervalMS      int
	WebhookAsyncWorkers          int
	WebhookAsyncQueueSize        int
	WebhookRetryBackoffMinute    []int
}

func Load() Config {
	redisDB, _ := strconv.Atoi(getenv("REDIS_DB", "0"))
	postgresMaxConns, _ := strconv.Atoi(getenv("POSTGRES_MAX_CONNS", "10"))
	authWindowSeconds, _ := strconv.Atoi(getenv("AUTH_WINDOW_SECONDS", "300"))
	processingKeyTTLSeconds, _ := strconv.Atoi(getenv("PROCESSING_KEY_TTL_SECONDS", "300"))
	txnProcessingGuardTTLMS, _ := strconv.Atoi(getenv("TXN_PROCESSING_GUARD_TTL_MS", "300000"))
	txnAsyncWorkersInit, _ := strconv.Atoi(getenvCompat("TXN_ASYNC_WORKERS_INIT", "TXN_ASYNC_STAGE_WORKERS_INIT", "17"))
	txnAsyncWorkersPaySuccess, _ := strconv.Atoi(getenvCompat("TXN_ASYNC_WORKERS_PAY_SUCCESS", "TXN_ASYNC_STAGE_WORKERS_PAY_SUCCESS", "17"))
	txnAsyncQueueSizeInit, _ := strconv.Atoi(getenv("TXN_ASYNC_QUEUE_SIZE_INIT", "65536"))
	txnAsyncQueueSizePaySuccess, _ := strconv.Atoi(getenv("TXN_ASYNC_QUEUE_SIZE_PAY_SUCCESS", "65536"))
	txnAsyncProfileLogIntervalMS, _ := strconv.Atoi(getenv("TXN_ASYNC_PROFILE_LOG_INTERVAL_MS", "5000"))
	txnRecoveryIntervalMS, _ := strconv.Atoi(getenv("TXN_RECOVERY_INTERVAL_MS", "300000"))
	txnRecoveryStaleMS, _ := strconv.Atoi(getenv("TXN_RECOVERY_STALE_MS", "60000"))
	txnRecoveryBatchSize, _ := strconv.Atoi(getenv("TXN_RECOVERY_BATCH_SIZE", "1000"))
	webhookMaxRetries, _ := strconv.Atoi(getenv("WEBHOOK_MAX_RETRIES", "8"))
	webhookWorkerBatchSize, _ := strconv.Atoi(getenv("WEBHOOK_WORKER_BATCH_SIZE", "100"))
	webhookWorkerIntervalMS, _ := strconv.Atoi(getenv("WEBHOOK_WORKER_INTERVAL_MS", "1000"))
	webhookAsyncWorkers, _ := strconv.Atoi(getenv("WEBHOOK_ASYNC_WORKERS", "16"))
	webhookAsyncQueueSize, _ := strconv.Atoi(getenv("WEBHOOK_ASYNC_QUEUE_SIZE", "65536"))
	adminAccessTokenTTLSeconds, _ := strconv.Atoi(getenv("ADMIN_ACCESS_TOKEN_TTL_SECONDS", "1800"))
	adminRefreshTokenTTLSeconds, _ := strconv.Atoi(getenv("ADMIN_REFRESH_TOKEN_TTL_SECONDS", "604800"))
	pprofEnabled := getenvBool("PPROF_ENABLED", false)
	txnAsyncProfileEnabled := getenvBool("TXN_ASYNC_PROFILE_ENABLED", false)
	adminEnabled := getenvBool("ADMIN_ENABLED", true)
	return Config{
		HTTPAddr:                     getenv("HTTP_ADDR", ":8080"),
		PprofEnabled:                 pprofEnabled,
		AdminEnabled:                 adminEnabled,
		AdminJWTSecret:               getenv("ADMIN_JWT_SECRET", "dev_admin_jwt_secret_change_me"),
		AdminAccessTokenTTLSeconds:   adminAccessTokenTTLSeconds,
		AdminRefreshTokenTTLSeconds:  adminRefreshTokenTTLSeconds,
		AdminBootstrapUsername:       getenv("ADMIN_BOOTSTRAP_USERNAME", "admin"),
		AdminBootstrapPassword:       getenv("ADMIN_BOOTSTRAP_PASSWORD", "admin123456"),
		PostgresDSN:                  getenv("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"),
		PostgresMaxConns:             postgresMaxConns,
		RedisAddr:                    getenv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:                getenv("REDIS_PASSWORD", ""),
		RedisDB:                      redisDB,
		MerchantSecretPassphrase:     resolveMerchantSecretPassphrase(),
		AuthWindowSeconds:            authWindowSeconds,
		ProcessingKeyTTLSeconds:      processingKeyTTLSeconds,
		TxnProcessingGuardTTLMS:      txnProcessingGuardTTLMS,
		TxnAsyncWorkersInit:          txnAsyncWorkersInit,
		TxnAsyncWorkersPaySuccess:    txnAsyncWorkersPaySuccess,
		TxnAsyncQueueSizeInit:        txnAsyncQueueSizeInit,
		TxnAsyncQueueSizePaySuccess:  txnAsyncQueueSizePaySuccess,
		TxnAsyncProfileEnabled:       txnAsyncProfileEnabled,
		TxnAsyncProfileLogIntervalMS: txnAsyncProfileLogIntervalMS,
		TxnRecoveryIntervalMS:        txnRecoveryIntervalMS,
		TxnRecoveryStaleMS:           txnRecoveryStaleMS,
		TxnRecoveryBatchSize:         txnRecoveryBatchSize,
		WebhookMaxRetries:            webhookMaxRetries,
		WebhookWorkerBatchSize:       webhookWorkerBatchSize,
		WebhookWorkerIntervalMS:      webhookWorkerIntervalMS,
		WebhookAsyncWorkers:          webhookAsyncWorkers,
		WebhookAsyncQueueSize:        webhookAsyncQueueSize,
		WebhookRetryBackoffMinute:    []int{1, 5, 15, 60, 360},
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

func getenvCompat(primaryKey, legacyKey, fallback string) string {
	if v := os.Getenv(primaryKey); v != "" {
		return v
	}
	if legacyKey != "" {
		if v := os.Getenv(legacyKey); v != "" {
			return v
		}
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}
