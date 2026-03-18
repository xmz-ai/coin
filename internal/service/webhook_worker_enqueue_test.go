package service

import (
	"context"
	"testing"
	"time"
)

type staticWebhookSecretProvider struct{}

func (staticWebhookSecretProvider) GetActiveSecret(context.Context, string) (string, bool, error) {
	return "sec_test", true, nil
}

type blockingClaimRepo struct {
	Repository
	claimCalled chan struct{}
	release     chan struct{}
}

func (r *blockingClaimRepo) ClaimDueOutboxEventsByTxnNo(txnNo string, limit int, now time.Time) ([]OutboxEventDelivery, error) {
	select {
	case r.claimCalled <- struct{}{}:
	default:
	}
	<-r.release
	return nil, nil
}

func TestWebhookWorkerEnqueueQueueFullDoesNotFallbackToSyncDeliver(t *testing.T) {
	release := make(chan struct{})
	repo := &blockingClaimRepo{
		claimCalled: make(chan struct{}, 1),
		release:     release,
	}
	defer close(release)

	worker := &WebhookWorker{
		repo:    repo,
		secrets: staticWebhookSecretProvider{},
		queue:   make(chan string, 1),
	}
	worker.queue <- "txn_existing"

	done := make(chan struct{})
	go func() {
		worker.Enqueue("txn_new")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("enqueue blocked when queue is full")
	}

	select {
	case <-repo.claimCalled:
		t.Fatalf("queue-full enqueue should not fallback to synchronous delivery")
	default:
	}
}
