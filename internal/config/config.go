package config

import (
	"os"
	"strconv"
)

const (
	localKMSKeyV1Env = "LOCAL_KMS_KEY_V1"
)

type Config struct {
	HTTPAddr                  string
	PostgresDSN               string
	RedisAddr                 string
	RedisPassword             string
	RedisDB                   int
	MerchantSecretPassphrase  string
	AuthWindowSeconds         int
	ProcessingKeyTTLSeconds   int
	WebhookMaxRetries         int
	WebhookWorkerBatchSize    int
	WebhookWorkerIntervalMS   int
	WebhookRetryBackoffMinute []int
}

func Load() Config {
	redisDB, _ := strconv.Atoi(getenv("REDIS_DB", "0"))
	authWindowSeconds, _ := strconv.Atoi(getenv("AUTH_WINDOW_SECONDS", "300"))
	processingKeyTTLSeconds, _ := strconv.Atoi(getenv("PROCESSING_KEY_TTL_SECONDS", "300"))
	webhookMaxRetries, _ := strconv.Atoi(getenv("WEBHOOK_MAX_RETRIES", "8"))
	webhookWorkerBatchSize, _ := strconv.Atoi(getenv("WEBHOOK_WORKER_BATCH_SIZE", "100"))
	webhookWorkerIntervalMS, _ := strconv.Atoi(getenv("WEBHOOK_WORKER_INTERVAL_MS", "1000"))
	return Config{
		HTTPAddr:                  getenv("HTTP_ADDR", ":8080"),
		PostgresDSN:               getenv("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"),
		RedisAddr:                 getenv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:             getenv("REDIS_PASSWORD", ""),
		RedisDB:                   redisDB,
		MerchantSecretPassphrase:  resolveMerchantSecretPassphrase(),
		AuthWindowSeconds:         authWindowSeconds,
		ProcessingKeyTTLSeconds:   processingKeyTTLSeconds,
		WebhookMaxRetries:         webhookMaxRetries,
		WebhookWorkerBatchSize:    webhookWorkerBatchSize,
		WebhookWorkerIntervalMS:   webhookWorkerIntervalMS,
		WebhookRetryBackoffMinute: []int{1, 5, 15, 60, 360},
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
