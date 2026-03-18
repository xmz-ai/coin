package service

import (
	"errors"
	"strings"
)

const (
	defaultAsyncInitWorkers       = 4
	defaultAsyncProcessingWorkers = 4
	defaultAsyncPaySuccessWorkers = 4
	defaultAsyncInitQueueSize     = 256
	defaultAsyncProcessingQueue   = 256
	defaultAsyncPaySuccessQueue   = 256
)

// TransferAsyncProcessor processes transfer/refund transactions asynchronously
// by stage queues: INIT -> PROCESSING -> PAY_SUCCESS.
type StageProcessingGuardWithError interface {
	TryBeginWithError(txnNo, stage string) (bool, error)
}

type TransferAsyncProcessorOptions struct {
	InitWorkers       int
	ProcessingWorkers int
	PaySuccessWorkers int
	InitQueueSize     int
	ProcessingQueue   int
	PaySuccessQueue   int
}

func (o TransferAsyncProcessorOptions) withDefaults() TransferAsyncProcessorOptions {
	if o.InitWorkers <= 0 {
		o.InitWorkers = defaultAsyncInitWorkers
	}
	if o.ProcessingWorkers <= 0 {
		o.ProcessingWorkers = defaultAsyncProcessingWorkers
	}
	if o.PaySuccessWorkers <= 0 {
		o.PaySuccessWorkers = defaultAsyncPaySuccessWorkers
	}
	if o.InitQueueSize <= 0 {
		o.InitQueueSize = defaultAsyncInitQueueSize
	}
	if o.ProcessingQueue <= 0 {
		o.ProcessingQueue = defaultAsyncProcessingQueue
	}
	if o.PaySuccessQueue <= 0 {
		o.PaySuccessQueue = defaultAsyncPaySuccessQueue
	}
	return o
}

type TransferAsyncProcessor struct {
	repo            Repository
	guard           StageProcessingGuard
	initQueue       chan string
	processingQueue chan string
	paySuccessQueue chan string
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
		initQueue:       make(chan string, opts.InitQueueSize),
		processingQueue: make(chan string, opts.ProcessingQueue),
		paySuccessQueue: make(chan string, opts.PaySuccessQueue),
	}
	p.startWorkers(TxnStatusInit, opts.InitWorkers, p.initQueue)
	p.startWorkers(TxnStatusProcessing, opts.ProcessingWorkers, p.processingQueue)
	p.startWorkers(TxnStatusPaySuccess, opts.PaySuccessWorkers, p.paySuccessQueue)
	return p
}

func (p *TransferAsyncProcessor) startWorkers(stage string, workerCount int, queue <-chan string) {
	for i := 0; i < workerCount; i++ {
		go func(expectedStatus string, jobs <-chan string) {
			for txnNo := range jobs {
				_ = p.processStage(txnNo, expectedStatus)
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
		return enqueueTxn(p.initQueue, txnNo)
	case TxnStatusProcessing:
		return enqueueTxn(p.processingQueue, txnNo)
	case TxnStatusPaySuccess:
		return enqueueTxn(p.paySuccessQueue, txnNo)
	case TxnStatusRecvSuccess, TxnStatusFailed:
		return true
	default:
		return false
	}
}

func enqueueTxn(queue chan string, txnNo string) bool {
	if queue == nil {
		return false
	}
	select {
	case queue <- txnNo:
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
		moved, err := p.transit(txn, TxnStatusProcessing, "", "")
		if err != nil {
			_ = p.fail(txnNo, TxnStatusInit, "STATUS_TRANSITION_FAILED", err.Error())
			return err
		}
		if !moved {
			p.enqueueByCurrentStatus(txnNo)
			return nil
		}
		_ = p.EnqueueByStatus(txnNo, TxnStatusProcessing)
		return nil
	case TxnStatusProcessing:
		if txn.BizType == BizTypeRefund {
			applied, err := p.repo.ApplyRefundDebitStage(txn.TxnNo, txn.Amount)
			if err != nil {
				return p.handleStageError(txnNo, TxnStatusProcessing, p.refundDebitErrorCode(err), err)
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
			return p.handleStageError(txnNo, TxnStatusProcessing, "DEBIT_FAILED", err)
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

func (p *TransferAsyncProcessor) transit(txn TransferTxn, nextStatus, errorCode, errorMsg string) (bool, error) {
	if txn.Status == nextStatus {
		return false, nil
	}
	sm := NewTxnStateMachine(txn.Status)
	if err := sm.Transit(nextStatus); err != nil {
		return false, err
	}
	return p.repo.TransitionTransferTxnStatus(txn.TxnNo, txn.Status, nextStatus, errorCode, errorMsg)
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
