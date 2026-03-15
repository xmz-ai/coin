package service

import (
	"context"
	"time"
)

type TransferPollingWorker struct {
	repo      Repository
	processor *TransferAsyncProcessor
	batchSize int
}

func NewTransferPollingWorker(repo Repository, processor *TransferAsyncProcessor, batchSize int) *TransferPollingWorker {
	if batchSize <= 0 {
		batchSize = 100
	}
	return &TransferPollingWorker{
		repo:      repo,
		processor: processor,
		batchSize: batchSize,
	}
}

func (w *TransferPollingWorker) RunOnce() {
	if w == nil || w.repo == nil || w.processor == nil {
		return
	}

	// Recovery path: scan unfinished txns and drive them to terminal status.
	for _, status := range []string{TxnStatusInit, TxnStatusProcessing, TxnStatusPaySuccess} {
		txns, err := w.repo.ListTransferTxnsByStatus(status, w.batchSize)
		if err != nil {
			continue
		}
		for _, txn := range txns {
			_ = w.processor.Process(txn.TxnNo)
		}
	}
}

func (w *TransferPollingWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	w.RunOnce()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.RunOnce()
		}
	}
}
