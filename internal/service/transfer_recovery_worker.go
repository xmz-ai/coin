package service

import (
	"context"
	"log"
	"time"
)

type TransferRecoveryWorker struct {
	repo           Repository
	processor      *TransferAsyncProcessor
	batchSize      int
	staleThreshold time.Duration
}

func NewTransferRecoveryWorker(repo Repository, processor *TransferAsyncProcessor, batchSize int) *TransferRecoveryWorker {
	return NewTransferRecoveryWorkerWithStaleThreshold(repo, processor, batchSize, 0)
}

func NewTransferRecoveryWorkerWithStaleThreshold(repo Repository, processor *TransferAsyncProcessor, batchSize int, staleThreshold time.Duration) *TransferRecoveryWorker {
	if batchSize <= 0 {
		batchSize = 100
	}
	return &TransferRecoveryWorker{
		repo:           repo,
		processor:      processor,
		batchSize:      batchSize,
		staleThreshold: staleThreshold,
	}
}

func (w *TransferRecoveryWorker) RunOnce() {
	if w == nil || w.repo == nil || w.processor == nil {
		return
	}

	for _, status := range []string{TxnStatusInit, TxnStatusProcessing, TxnStatusPaySuccess} {
		txnNos, err := w.listTxnNosByStatus(status)
		if err != nil {
			continue
		}
		for _, txnNo := range txnNos {
			_ = w.processor.EnqueueByStatus(txnNo, status)
		}
	}
}

func (w *TransferRecoveryWorker) Start(ctx context.Context, interval time.Duration) {
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

func (w *TransferRecoveryWorker) StartWithReport(ctx context.Context, interval time.Duration, report func(processed int, runErr error)) {
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

func (w *TransferRecoveryWorker) runOnceWithResult() (int, error) {
	dispatched := 0
	for _, status := range []string{TxnStatusInit, TxnStatusProcessing, TxnStatusPaySuccess} {
		txnNos, err := w.listTxnNosByStatus(status)
		if err != nil {
			return dispatched, err
		}
		for _, txnNo := range txnNos {
			if ok := w.processor.EnqueueByStatus(txnNo, status); ok {
				dispatched++
			} else {
				log.Printf("txn recovery enqueue dropped: txn_no=%s status=%s", txnNo, status)
			}
		}
	}
	return dispatched, nil
}

func (w *TransferRecoveryWorker) listTxnNosByStatus(status string) ([]string, error) {
	if w == nil || w.repo == nil {
		return nil, nil
	}
	if w.staleThreshold > 0 {
		staleBefore := time.Now().UTC().Add(-w.staleThreshold)
		return w.repo.ListStaleTransferTxnNosByStatus(status, staleBefore, w.batchSize)
	}
	txns, err := w.repo.ListTransferTxnsByStatus(status, w.batchSize)
	if err != nil {
		return nil, err
	}
	items := make([]string, 0, len(txns))
	for _, txn := range txns {
		items = append(items, txn.TxnNo)
	}
	return items, nil
}
