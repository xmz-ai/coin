package service

import (
	"errors"
	"strings"
	"sync"
)

const (
	defaultAsyncInitWorkers       = 4
	defaultAsyncPaySuccessWorkers = 4
	defaultAsyncInitQueueSize     = 65536
	defaultAsyncPaySuccessQueue   = 65536
)

// TransferAsyncProcessor processes transfer/refund transactions asynchronously
// by stage queues: INIT -> PAY_SUCCESS.
type StageProcessingGuardWithError interface {
	TryBeginWithError(txnNo, stage string) (bool, error)
}

type TransferAsyncProcessorOptions struct {
	InitWorkers       int
	PaySuccessWorkers int
	InitQueueSize     int
	PaySuccessQueue   int
}

func (o TransferAsyncProcessorOptions) withDefaults() TransferAsyncProcessorOptions {
	if o.InitWorkers <= 0 {
		o.InitWorkers = defaultAsyncInitWorkers
	}
	if o.PaySuccessWorkers <= 0 {
		o.PaySuccessWorkers = defaultAsyncPaySuccessWorkers
	}
	if o.InitQueueSize <= 0 {
		o.InitQueueSize = defaultAsyncInitQueueSize
	}
	if o.PaySuccessQueue <= 0 {
		o.PaySuccessQueue = defaultAsyncPaySuccessQueue
	}
	return o
}

type TransferAsyncProcessor struct {
	repo            Repository
	guard           StageProcessingGuard
	initQueue       *stageQueue
	paySuccessQueue *stageQueue
}

type stageQueue struct {
	ch      chan string
	mu      sync.Mutex
	pending map[string]struct{}
}

func newStageQueue(size int) *stageQueue {
	if size <= 0 {
		size = 1
	}
	return &stageQueue{
		ch:      make(chan string, size),
		pending: map[string]struct{}{},
	}
}

func (q *stageQueue) Enqueue(txnNo string) bool {
	if q == nil {
		return false
	}
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" {
		return false
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.pending[txnNo]; exists {
		return true
	}
	select {
	case q.ch <- txnNo:
		q.pending[txnNo] = struct{}{}
		return true
	default:
		return false
	}
}

func (q *stageQueue) Done(txnNo string) {
	if q == nil {
		return
	}
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" {
		return
	}
	q.mu.Lock()
	delete(q.pending, txnNo)
	q.mu.Unlock()
}

func NewTransferAsyncProcessor(repo Repository) *TransferAsyncProcessor {
	return NewTransferAsyncProcessorWithGuardAndOptions(repo, NewProcessingGuard(), TransferAsyncProcessorOptions{})
}

func NewTransferAsyncProcessorWithGuard(repo Repository, guard StageProcessingGuard) *TransferAsyncProcessor {
	return NewTransferAsyncProcessorWithGuardAndOptions(repo, guard, TransferAsyncProcessorOptions{})
}

func NewTransferAsyncProcessorWithGuardAndOptions(repo Repository, guard StageProcessingGuard, opts TransferAsyncProcessorOptions) *TransferAsyncProcessor {
	if guard == nil {
		guard = NewProcessingGuard()
	}
	opts = opts.withDefaults()
	p := &TransferAsyncProcessor{
		repo:            repo,
		guard:           guard,
		initQueue:       newStageQueue(opts.InitQueueSize),
		paySuccessQueue: newStageQueue(opts.PaySuccessQueue),
	}
	p.startWorkers(TxnStatusInit, opts.InitWorkers, p.initQueue)
	p.startWorkers(TxnStatusPaySuccess, opts.PaySuccessWorkers, p.paySuccessQueue)
	return p
}

func (p *TransferAsyncProcessor) startWorkers(stage string, workerCount int, queue *stageQueue) {
	if queue == nil {
		return
	}
	for i := 0; i < workerCount; i++ {
		go func(expectedStatus string, q *stageQueue) {
			for txnNo := range q.ch {
				func() {
					defer q.Done(txnNo)
					_ = p.processStage(txnNo, expectedStatus)
				}()
			}
		}(stage, queue)
	}
}

func (p *TransferAsyncProcessor) Enqueue(txnNo string) {
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" || p == nil || p.repo == nil {
		return
	}
	txn, ok := p.repo.GetTransferTxn(txnNo)
	if !ok {
		return
	}
	_ = p.EnqueueByStatus(txnNo, txn.Status)
}

func (p *TransferAsyncProcessor) EnqueueByStatus(txnNo, status string) bool {
	txnNo = strings.TrimSpace(txnNo)
	status = strings.TrimSpace(status)
	if txnNo == "" {
		return false
	}

	switch status {
	case TxnStatusInit:
		return p.initQueue.Enqueue(txnNo)
	case TxnStatusPaySuccess:
		return p.paySuccessQueue.Enqueue(txnNo)
	case TxnStatusRecvSuccess, TxnStatusFailed:
		return true
	default:
		return false
	}
}

// Process processes exactly one stage based on current txn status.
func (p *TransferAsyncProcessor) Process(txnNo string) error {
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" || p == nil || p.repo == nil {
		return ErrTxnNotFound
	}

	txn, ok := p.repo.GetTransferTxn(txnNo)
	if !ok {
		return ErrTxnNotFound
	}
	return p.processStage(txnNo, txn.Status)
}

func (p *TransferAsyncProcessor) processStage(txnNo, expectedStatus string) error {
	txnNo = strings.TrimSpace(txnNo)
	expectedStatus = strings.TrimSpace(expectedStatus)
	if txnNo == "" {
		return ErrTxnNotFound
	}
	if expectedStatus == "" {
		txn, ok := p.repo.GetTransferTxn(txnNo)
		if !ok {
			return ErrTxnNotFound
		}
		expectedStatus = txn.Status
	}
	if expectedStatus == TxnStatusRecvSuccess || expectedStatus == TxnStatusFailed {
		return nil
	}

	ok, err := p.tryStage(txnNo, expectedStatus)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	defer p.endStage(txnNo, expectedStatus)

	txn, exists := p.repo.GetTransferTxn(txnNo)
	if !exists {
		return ErrTxnNotFound
	}
	if txn.BizType != BizTypeTransfer && txn.BizType != BizTypeRefund {
		return nil
	}
	if txn.Status != expectedStatus {
		_ = p.EnqueueByStatus(txnNo, txn.Status)
		return nil
	}

	switch expectedStatus {
	case TxnStatusInit:
		if txn.BizType == BizTypeRefund {
			applied, err := p.repo.ApplyRefundDebitStage(txn.TxnNo, txn.Amount)
			if err != nil {
				return p.handleStageError(txnNo, TxnStatusInit, p.refundDebitErrorCode(err), err)
			}
			if !applied {
				p.enqueueByCurrentStatus(txnNo)
				return nil
			}
			_ = p.EnqueueByStatus(txnNo, TxnStatusPaySuccess)
			return nil
		}
		applied, err := p.repo.ApplyTransferDebitStage(txn.TxnNo, txn.DebitAccountNo, txn.Amount)
		if err != nil {
			return p.handleStageError(txnNo, TxnStatusInit, "DEBIT_FAILED", err)
		}
		if !applied {
			p.enqueueByCurrentStatus(txnNo)
			return nil
		}
		_ = p.EnqueueByStatus(txnNo, TxnStatusPaySuccess)
		return nil
	case TxnStatusPaySuccess:
		if txn.BizType == BizTypeRefund {
			applied, err := p.repo.ApplyRefundCreditStage(txn.TxnNo, txn.CreditAccountNo, txn.Amount)
			if err != nil {
				return p.handleStageError(txnNo, TxnStatusPaySuccess, p.refundCreditErrorCode(err), err)
			}
			if !applied {
				p.enqueueByCurrentStatus(txnNo)
				return nil
			}
			return nil
		}
		applied, err := p.repo.ApplyTransferCreditStage(txn.TxnNo, txn.CreditAccountNo, txn.Amount)
		if err != nil {
			return p.handleStageError(txnNo, TxnStatusPaySuccess, "CREDIT_FAILED", err)
		}
		if !applied {
			p.enqueueByCurrentStatus(txnNo)
			return nil
		}
		return nil
	default:
		_ = p.fail(txnNo, expectedStatus, "TXN_STATUS_INVALID", "unknown txn status")
		return ErrTxnStatusInvalid
	}
}

func (p *TransferAsyncProcessor) enqueueByCurrentStatus(txnNo string) {
	txn, ok := p.repo.GetTransferTxn(txnNo)
	if !ok {
		return
	}
	_ = p.EnqueueByStatus(txnNo, txn.Status)
}

func (p *TransferAsyncProcessor) fail(txnNo, fromStatus, errorCode, errorMsg string) error {
	_, err := p.repo.TransitionTransferTxnStatus(txnNo, fromStatus, TxnStatusFailed, errorCode, errorMsg)
	return err
}

func (p *TransferAsyncProcessor) tryStage(txnNo, stage string) (bool, error) {
	if p.guard == nil {
		return false, ErrProcessingGuardUnavailable
	}
	if g, ok := p.guard.(StageProcessingGuardWithError); ok {
		return g.TryBeginWithError(txnNo, stage)
	}
	if p.guard.TryBegin(txnNo, stage) {
		return true, nil
	}
	return false, nil
}

func (p *TransferAsyncProcessor) endStage(txnNo, stage string) {
	if p.guard == nil {
		return
	}
	g, ok := p.guard.(StageProcessingGuardEnder)
	if !ok {
		return
	}
	g.End(txnNo, stage)
}

func (p *TransferAsyncProcessor) handleStageError(txnNo, fromStatus, errorCode string, err error) error {
	if err == nil {
		return nil
	}
	if p.shouldFailOnError(err) {
		_ = p.fail(txnNo, fromStatus, errorCode, err.Error())
	}
	return err
}

func (p *TransferAsyncProcessor) shouldFailOnError(err error) bool {
	switch {
	case errors.Is(err, ErrTxnNotFound):
		return true
	case errors.Is(err, ErrTxnStatusInvalid):
		return true
	case errors.Is(err, ErrAccountResolveFailed):
		return true
	case errors.Is(err, ErrAccountResolveConflict):
		return true
	case errors.Is(err, ErrOutUserIDNotAllowedForSystemAccount):
		return true
	case errors.Is(err, ErrAccountForbidDebit):
		return true
	case errors.Is(err, ErrAccountForbidCredit):
		return true
	case errors.Is(err, ErrAccountForbidTransfer):
		return true
	case errors.Is(err, ErrInsufficientBalance):
		return true
	case errors.Is(err, ErrRefundAmountExceeded):
		return true
	case errors.Is(err, ErrRefundOriginBookTraceMissing):
		return true
	case errors.Is(err, ErrBookDisabled):
		return true
	case errors.Is(err, ErrExpireAtRequired):
		return true
	default:
		return false
	}
}

func (p *TransferAsyncProcessor) refundDebitErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrTxnNotFound):
		return "REFUND_ORIGIN_NOT_FOUND"
	case errors.Is(err, ErrRefundAmountExceeded):
		return "REFUND_AMOUNT_EXCEEDED"
	default:
		return "REFUND_DEBIT_FAILED"
	}
}

func (p *TransferAsyncProcessor) refundCreditErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrTxnNotFound):
		return "REFUND_ORIGIN_NOT_FOUND"
	case errors.Is(err, ErrRefundOriginBookTraceMissing):
		return "REFUND_ORIGIN_BOOK_TRACE_MISSING"
	default:
		return "REFUND_CREDIT_FAILED"
	}
}
