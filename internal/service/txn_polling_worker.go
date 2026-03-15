package service

import (
	"context"
	"log"
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

func (w *TransferPollingWorker) StartWithReport(ctx context.Context, interval time.Duration, report func(processed int, runErr error)) {
	if interval <= 0 {
		interval = time.Second
	}
	if report == nil {
		report = func(int, error) {}
	}
	if w == nil || w.repo == nil || w.processor == nil {
		report(0, nil)
		return
	}

	run := func() {
		processed, err := w.runOnceWithResult()
		report(processed, err)
	}

	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func (w *TransferPollingWorker) runOnceWithResult() (int, error) {
	processed := 0
	for _, status := range []string{TxnStatusInit, TxnStatusProcessing, TxnStatusPaySuccess} {
		txns, err := w.repo.ListTransferTxnsByStatus(status, w.batchSize)
		if err != nil {
			return processed, err
		}
		for _, txn := range txns {
			if err := w.processor.Process(txn.TxnNo); err == nil {
				processed++
			} else {
				log.Printf("txn compensation process failed: txn_no=%s status=%s err=%v", txn.TxnNo, status, err)
			}
		}
	}
	return processed, nil
}
