package main

import (
	"context"
	"log"
	"time"

	"github.com/xmz-ai/coin/internal/api"
	"github.com/xmz-ai/coin/internal/config"
	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/platform/security"
	"github.com/xmz-ai/coin/internal/service"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pg, err := db.NewPool(ctx, cfg.PostgresDSN)
	if err != nil {
		log.Fatalf("postgres connect failed: %v", err)
	}
	defer pg.Close()

	secretCipher, err := security.NewAESGCMCipher(cfg.MerchantSecretPassphrase)
	if err != nil {
		log.Fatalf("merchant secret cipher init failed: %v", err)
	}
	secretManager := db.NewMerchantSecretManager(pg, secretCipher)
	repo := db.NewRepository(pg)

	redisClient, err := db.NewClient(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		log.Fatalf("redis connect failed: %v", err)
	}
	defer redisClient.Close()

	ids := idpkg.NewRuntimeUUIDProvider()
	transferService := service.NewTransferService(repo, ids)
	transferRoutingService := service.NewTransferRoutingService(repo)
	processingGuard := service.NewRedisProcessingGuard(redisClient, time.Duration(cfg.ProcessingKeyTTLSeconds)*time.Second)
	asyncTransferProcessor := service.NewTransferAsyncProcessorWithGuard(repo, processingGuard)
	transferWorker := service.NewTransferPollingWorker(repo, asyncTransferProcessor, 200)
	webhookWorker := service.NewWebhookWorker(repo, secretManager, cfg.WebhookMaxRetries, cfg.WebhookWorkerBatchSize, cfg.WebhookRetryBackoffMinute)
	accountResolver := service.NewAccountResolver(repo)
	refundService := service.NewRefundService(repo)
	queryService := service.NewTxnQueryService(repo)
	businessHandler := api.NewBusinessHandler(transferService, transferService, repo, transferRoutingService, asyncTransferProcessor, accountResolver, repo, refundService, queryService, repo, nil)
	// Fallback recovery worker; main path is in-process async Enqueue on API submit.
	go transferWorker.Start(ctx, 500*time.Millisecond)
	go webhookWorker.Start(ctx, time.Duration(cfg.WebhookWorkerIntervalMS)*time.Millisecond)

	authMiddleware := api.NewAuthMiddleware(api.AuthMiddlewareConfig{
		SecretProvider: secretManager,
		TimeWindow:     time.Duration(cfg.AuthWindowSeconds) * time.Second,
	})

	r := api.NewRouter()
	api.RegisterProtectedRoutes(r, api.ProtectedRoutesOptions{
		AuthMiddleware: authMiddleware,
		SecretRotator:  secretManager,
		Business:       businessHandler,
	})
	if err := r.Run(cfg.HTTPAddr); err != nil {
		log.Fatalf("http server failed: %v", err)
	}
}
