package service

import (
	"context"
	"testing"
	"time"
)

type countingWebhookConfigRepo struct {
	Repository
	cfg   WebhookConfig
	found bool
	err   error
	calls int
}

func (r *countingWebhookConfigRepo) GetWebhookConfig(string) (WebhookConfig, bool, error) {
	r.calls++
	return r.cfg, r.found, r.err
}

type countingWebhookSecretProvider struct {
	secret string
	ok     bool
	err    error
	calls  int
}

func (s *countingWebhookSecretProvider) GetActiveSecret(context.Context, string) (string, bool, error) {
	s.calls++
	return s.secret, s.ok, s.err
}

func TestWebhookWorkerGetWebhookConfigCachesEnabledConfig(t *testing.T) {
	now := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	repo := &countingWebhookConfigRepo{
		cfg:   WebhookConfig{URL: "https://merchant.example.com/webhook", Enabled: true},
		found: true,
	}
	worker := &WebhookWorker{
		repo:           repo,
		nowFn:          func() time.Time { return now },
		configCacheTTL: time.Minute,
	}

	if _, found, err := worker.getWebhookConfig("m1"); err != nil || !found {
		t.Fatalf("first read failed: found=%v err=%v", found, err)
	}
	if _, found, err := worker.getWebhookConfig("m1"); err != nil || !found {
		t.Fatalf("second read failed: found=%v err=%v", found, err)
	}
	if repo.calls != 1 {
		t.Fatalf("expected one repo call on cache hit, got %d", repo.calls)
	}

	now = now.Add(2 * time.Minute)
	if _, found, err := worker.getWebhookConfig("m1"); err != nil || !found {
		t.Fatalf("third read after expiry failed: found=%v err=%v", found, err)
	}
	if repo.calls != 2 {
		t.Fatalf("expected cache miss after ttl expiry, got %d repo calls", repo.calls)
	}
}

func TestWebhookWorkerGetWebhookConfigDoesNotCacheDisabledConfig(t *testing.T) {
	now := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	repo := &countingWebhookConfigRepo{
		cfg:   WebhookConfig{URL: "https://merchant.example.com/webhook", Enabled: false},
		found: true,
	}
	worker := &WebhookWorker{
		repo:           repo,
		nowFn:          func() time.Time { return now },
		configCacheTTL: time.Minute,
	}

	if _, found, err := worker.getWebhookConfig("m1"); err != nil || !found {
		t.Fatalf("first read failed: found=%v err=%v", found, err)
	}
	if _, found, err := worker.getWebhookConfig("m1"); err != nil || !found {
		t.Fatalf("second read failed: found=%v err=%v", found, err)
	}
	if repo.calls != 2 {
		t.Fatalf("expected disabled config not cached, got %d repo calls", repo.calls)
	}
}

func TestWebhookWorkerGetActiveSecretCachesSecret(t *testing.T) {
	now := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	secrets := &countingWebhookSecretProvider{
		secret: "sec_test",
		ok:     true,
	}
	worker := &WebhookWorker{
		secrets:        secrets,
		nowFn:          func() time.Time { return now },
		secretCacheTTL: time.Minute,
	}

	if _, ok, err := worker.getActiveSecret(context.Background(), "m1"); err != nil || !ok {
		t.Fatalf("first read failed: ok=%v err=%v", ok, err)
	}
	if _, ok, err := worker.getActiveSecret(context.Background(), "m1"); err != nil || !ok {
		t.Fatalf("second read failed: ok=%v err=%v", ok, err)
	}
	if secrets.calls != 1 {
		t.Fatalf("expected one secret provider call on cache hit, got %d", secrets.calls)
	}

	now = now.Add(2 * time.Minute)
	if _, ok, err := worker.getActiveSecret(context.Background(), "m1"); err != nil || !ok {
		t.Fatalf("third read after expiry failed: ok=%v err=%v", ok, err)
	}
	if secrets.calls != 2 {
		t.Fatalf("expected cache miss after ttl expiry, got %d secret provider calls", secrets.calls)
	}
}
