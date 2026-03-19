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

	pg, err := db.NewPoolWithMaxConns(ctx, cfg.PostgresDSN, cfg.PostgresMaxConns)
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
	merchantService := service.NewMerchantService(repo, ids)
	transferService := service.NewTransferService(repo, ids)
	customerService := service.NewCustomerService(repo, ids)
	transferRoutingService := service.NewTransferRoutingService(repo)
	guardTTL := time.Duration(cfg.TxnProcessingGuardTTLMS) * time.Millisecond
	if guardTTL <= 0 {
		guardTTL = time.Duration(cfg.ProcessingKeyTTLSeconds) * time.Second
	}
	processingGuard := service.NewRedisProcessingGuard(redisClient, guardTTL)
	asyncProfileLogInterval := time.Duration(cfg.TxnAsyncProfileLogIntervalMS) * time.Millisecond
	asyncTransferProcessor := service.NewTransferAsyncProcessorWithGuardAndOptions(repo, processingGuard, service.TransferAsyncProcessorOptions{
		InitWorkers:          cfg.TxnAsyncWorkersInit,
		PaySuccessWorkers:    cfg.TxnAsyncWorkersPaySuccess,
		InitQueueSize:        cfg.TxnAsyncQueueSizeInit,
		PaySuccessQueue:      cfg.TxnAsyncQueueSizePaySuccess,
		ProfilingEnabled:     cfg.TxnAsyncProfileEnabled,
		ProfilingLogInterval: asyncProfileLogInterval,
	})
	transferWorker := service.NewTransferRecoveryWorkerWithStaleThreshold(
		repo,
		asyncTransferProcessor,
		cfg.TxnRecoveryBatchSize,
		time.Duration(cfg.TxnRecoveryStaleMS)*time.Millisecond,
	)
	webhookWorker := service.NewWebhookWorkerWithOptions(repo, secretManager, cfg.WebhookMaxRetries, cfg.WebhookWorkerBatchSize, cfg.WebhookRetryBackoffMinute, service.WebhookWorkerOptions{
		AsyncWorkers:         cfg.WebhookAsyncWorkers,
		AsyncQueueSize:       cfg.WebhookAsyncQueueSize,
		ProfilingEnabled:     cfg.TxnAsyncProfileEnabled,
		ProfilingLogInterval: asyncProfileLogInterval,
	})
	asyncTransferProcessor.SetWebhookDispatcher(webhookWorker)
	accountResolver := service.NewAccountResolver(repo, customerService)
	queryService := service.NewTxnQueryService(repo)
	businessHandler := api.NewBusinessHandler(transferService, repo, transferRoutingService, asyncTransferProcessor, accountResolver, repo, queryService, repo, nil)
	// Fallback recovery worker; main path is in-process async Enqueue on API submit.
	go transferWorker.Start(ctx, time.Duration(cfg.TxnRecoveryIntervalMS)*time.Millisecond)
	go webhookWorker.Start(ctx, time.Duration(cfg.WebhookWorkerIntervalMS)*time.Millisecond)

	authMiddleware := api.NewAuthMiddleware(api.AuthMiddlewareConfig{
		SecretProvider: secretManager,
		TimeWindow:     time.Duration(cfg.AuthWindowSeconds) * time.Second,
	})

	r := api.NewRouter(api.RouterOptions{
		EnablePprof: cfg.PprofEnabled,
	})
	api.RegisterProtectedRoutes(r, api.ProtectedRoutesOptions{
		AuthMiddleware:  authMiddleware,
		SecretRotator:   secretManager,
		Business:        businessHandler,
		MerchantCreator: merchantService,
	})
	if err := r.Run(cfg.HTTPAddr); err != nil {
		log.Fatalf("http server failed: %v", err)
	}
}
