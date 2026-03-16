package service

import (
	"context"
	"log"
	"time"
)

type CompensationReporter interface {
	IncTxnCompensationRun()
	IncNotifyCompensationRun()
}

type CompensationWorker struct {
	txnWorker *TransferRecoveryWorker
	notify    *WebhookWorker
	reporter  CompensationReporter
}

func NewCompensationWorker(txnWorker *TransferRecoveryWorker, notify *WebhookWorker, reporter CompensationReporter) *CompensationWorker {
	return &CompensationWorker{
		txnWorker: txnWorker,
		notify:    notify,
		reporter:  reporter,
	}
}

func (w *CompensationWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Second
	}
	if w == nil {
		return
	}

	w.RunOnce(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.RunOnce(ctx)
		}
	}
}

func (w *CompensationWorker) RunOnce(ctx context.Context) {
	if w == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if w.txnWorker != nil {
		w.txnWorker.RunOnce()
		if w.reporter != nil {
			w.reporter.IncTxnCompensationRun()
		}
	}

	if w.notify != nil {
		w.notify.RunOnce(ctx)
		if w.reporter != nil {
			w.reporter.IncNotifyCompensationRun()
		}
	}
}

func NewCompensationReportHook() func(processed int, runErr error) {
	return func(processed int, runErr error) {
		if runErr != nil {
			log.Printf("txn compensation run failed: err=%v processed=%d", runErr, processed)
			return
		}
		log.Printf("txn compensation run done: processed=%d", processed)
	}
}
