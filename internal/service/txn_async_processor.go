package service

import (
	"errors"
	"strings"
)

const (
	asyncProcessorWorkers   = 4
	asyncProcessorQueueSize = 256
)

// TransferAsyncProcessor processes transfer transactions asynchronously.
// It advances txn status in ordered stages:
// INIT -> PROCESSING -> PAY_SUCCESS -> RECV_SUCCESS
type StageProcessingGuardWithError interface {
	TryBeginWithError(txnNo, stage string) (bool, error)
}

type TransferAsyncProcessor struct {
	repo   Repository
	guard  StageProcessingGuard
	queue  chan string
}

func NewTransferAsyncProcessor(repo Repository) *TransferAsyncProcessor {
	return NewTransferAsyncProcessorWithGuard(repo, NewProcessingGuard())
}

func NewTransferAsyncProcessorWithGuard(repo Repository, guard StageProcessingGuard) *TransferAsyncProcessor {
	if guard == nil {
		guard = NewProcessingGuard()
	}
	p := &TransferAsyncProcessor{
		repo:  repo,
		guard: guard,
		queue: make(chan string, asyncProcessorQueueSize),
	}
	for i := 0; i < asyncProcessorWorkers; i++ {
		go func() {
			for txnNo := range p.queue {
				_ = p.Process(txnNo)
			}
		}()
	}
	return p
}

func (p *TransferAsyncProcessor) Enqueue(txnNo string) {
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" {
		return
	}
	// Fast path: enqueue for in-process workers.
	// Polling worker only acts as recovery fallback.
	select {
	case p.queue <- txnNo:
	default:
		_ = p.Process(txnNo)
	}
}

func (p *TransferAsyncProcessor) Process(txnNo string) error {
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" {
		return ErrTxnNotFound
	}

	for i := 0; i < 8; i++ {
		txn, ok := p.repo.GetTransferTxn(txnNo)
		if !ok {
			return ErrTxnNotFound
		}
		if txn.BizType != BizTypeTransfer && txn.BizType != BizTypeRefund {
			return nil
		}

		switch txn.Status {
		case TxnStatusInit:
			ok, err := p.tryStage(txn.TxnNo, TxnStatusInit)
			if err != nil {
				_ = p.fail(txnNo, TxnStatusInit, "PROCESSING_KEY_UNAVAILABLE", err.Error())
				return err
			}
			if !ok {
				continue
			}
			moved, err := p.transit(txn, TxnStatusProcessing, "", "")
			if err != nil {
				_ = p.fail(txnNo, TxnStatusInit, "STATUS_TRANSITION_FAILED", err.Error())
				return err
			}
			if !moved {
				continue
			}
		case TxnStatusProcessing:
			ok, err := p.tryStage(txn.TxnNo, TxnStatusProcessing)
			if err != nil {
				_ = p.fail(txnNo, TxnStatusProcessing, "PROCESSING_KEY_UNAVAILABLE", err.Error())
				return err
			}
			if !ok {
				continue
			}
			if txn.BizType == BizTypeRefund {
				applied, err := p.repo.ApplyRefundDebitStage(txn.TxnNo, txn.Amount)
				if err != nil {
					_ = p.fail(txnNo, TxnStatusProcessing, p.refundDebitErrorCode(err), err.Error())
					return err
				}
				if !applied {
					continue
				}
				continue
			}
			applied, err := p.repo.ApplyTransferDebitStage(txn.TxnNo, txn.DebitAccountNo, txn.Amount)
			if err != nil {
				_ = p.fail(txnNo, TxnStatusProcessing, "DEBIT_FAILED", err.Error())
				return err
			}
			if !applied {
				continue
			}
		case TxnStatusPaySuccess:
			ok, err := p.tryStage(txn.TxnNo, TxnStatusPaySuccess)
			if err != nil {
				_ = p.fail(txnNo, TxnStatusPaySuccess, "PROCESSING_KEY_UNAVAILABLE", err.Error())
				return err
			}
			if !ok {
				continue
			}
			if txn.BizType == BizTypeRefund {
				applied, err := p.repo.ApplyRefundCreditStage(txn.TxnNo, txn.CreditAccountNo, txn.Amount)
				if err != nil {
					_ = p.fail(txnNo, TxnStatusPaySuccess, p.refundCreditErrorCode(err), err.Error())
					return err
				}
				if !applied {
					continue
				}
				continue
			}
			applied, err := p.repo.ApplyTransferCreditStage(txn.TxnNo, txn.CreditAccountNo, txn.Amount)
			if err != nil {
				_ = p.fail(txnNo, TxnStatusPaySuccess, "CREDIT_FAILED", err.Error())
				return err
			}
			if !applied {
				continue
			}
		case TxnStatusRecvSuccess, TxnStatusFailed:
			return nil
		default:
			_ = p.fail(txnNo, txn.Status, "TXN_STATUS_INVALID", "unknown txn status")
			return ErrTxnStatusInvalid
		}
	}

	return errors.New("async processing max loop reached")
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
	default:
		return "REFUND_CREDIT_FAILED"
	}
}
