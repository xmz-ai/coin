package service

import (
	"fmt"
	"strings"
	"time"
)

type TransferRequest struct {
	MerchantNo       string
	OutTradeNo       string
	Title            string
	Remark           string
	BizType          string
	TransferScene    string
	DebitAccountNo   string
	CreditAccountNo  string
	CreditExpireAt   *time.Time
	RefundOfTxnNo    string
	Amount           int64
	RefundableAmount int64
	Status           string
}

type TransferService struct {
	repo Repository
	ids  interface{ NewUUIDv7() (string, error) }
}

func NewTransferService(repo Repository, ids interface{ NewUUIDv7() (string, error) }) *TransferService {
	return &TransferService{repo: repo, ids: ids}
}

func (s *TransferService) Submit(req TransferRequest) (TransferTxn, error) {
	txnNo, err := s.ids.NewUUIDv7()
	if err != nil {
		return TransferTxn{}, fmt.Errorf("new txn no: %w", err)
	}

	bizType := req.BizType
	if bizType == "" {
		bizType = BizTypeTransfer
	}
	transferScene := req.TransferScene
	if bizType == BizTypeTransfer && transferScene == "" {
		transferScene = SceneAdjust
	}

	refundable := req.RefundableAmount
	if refundable < 0 {
		refundable = 0
	}
	if bizType == BizTypeTransfer && refundable == 0 {
		refundable = req.Amount
	}
	status := req.Status
	if status == "" {
		status = TxnStatusInit
	}
	txn := TransferTxn{
		TxnNo:            txnNo,
		MerchantNo:       req.MerchantNo,
		OutTradeNo:       req.OutTradeNo,
		Title:            req.Title,
		Remark:           req.Remark,
		BizType:          bizType,
		TransferScene:    transferScene,
		DebitAccountNo:   req.DebitAccountNo,
		CreditAccountNo:  req.CreditAccountNo,
		Amount:           req.Amount,
		RefundOfTxnNo:    req.RefundOfTxnNo,
		RefundableAmount: refundable,
		Status:           status,
	}
	if req.CreditExpireAt != nil {
		txn.CreditExpireAt = req.CreditExpireAt.UTC()
	}
	if bizType == BizTypeRefund {
		originTxnNo := strings.TrimSpace(req.RefundOfTxnNo)
		origin, ok := s.repo.GetTransferTxn(originTxnNo)
		if !ok {
			return TransferTxn{}, ErrTxnNotFound
		}
		if strings.TrimSpace(origin.MerchantNo) != strings.TrimSpace(req.MerchantNo) {
			return TransferTxn{}, ErrTxnNotFound
		}
		if strings.TrimSpace(origin.BizType) != BizTypeTransfer || strings.TrimSpace(origin.Status) != TxnStatusRecvSuccess {
			return TransferTxn{}, ErrTxnStatusInvalid
		}
		if origin.RefundableAmount < req.Amount {
			return TransferTxn{}, ErrRefundAmountExceeded
		}
		refundDebit := strings.TrimSpace(origin.CreditAccountNo)
		refundCredit := strings.TrimSpace(origin.DebitAccountNo)
		if refundDebit == "" || refundCredit == "" {
			return TransferTxn{}, ErrAccountResolveFailed
		}
		txn.DebitAccountNo = refundDebit
		txn.CreditAccountNo = refundCredit
		// Refund keeps a looser credit rule by design:
		// only validate debit-out capability at submit time.
		if err := s.validateSubmitRefundAccounts(req.MerchantNo, txn.DebitAccountNo, txn.CreditAccountNo); err != nil {
			return TransferTxn{}, err
		}
	} else if strings.TrimSpace(txn.DebitAccountNo) != "" || strings.TrimSpace(txn.CreditAccountNo) != "" {
		// Transfer capability checks are enforced at submit time when parties are known.
		if err := s.validateSubmitTransferAccounts(req.MerchantNo, transferScene, txn.DebitAccountNo, txn.CreditAccountNo); err != nil {
			return TransferTxn{}, err
		}
	}
	if err := s.repo.CreateTransferTxn(txn); err != nil {
		return TransferTxn{}, err
	}
	return txn, nil
}

func (s *TransferService) validateSubmitTransferAccounts(merchantNo, scene, debitAccountNo, creditAccountNo string) error {
	debitNo := strings.TrimSpace(debitAccountNo)
	creditNo := strings.TrimSpace(creditAccountNo)
	if debitNo == "" || creditNo == "" {
		return ErrAccountResolveFailed
	}

	debit, ok := s.repo.GetAccount(debitNo)
	if !ok {
		return ErrAccountResolveFailed
	}
	credit, ok := s.repo.GetAccount(creditNo)
	if !ok {
		return ErrAccountResolveFailed
	}
	if strings.TrimSpace(debit.MerchantNo) != strings.TrimSpace(merchantNo) || strings.TrimSpace(credit.MerchantNo) != strings.TrimSpace(merchantNo) {
		return ErrAccountResolveFailed
	}

	if err := debit.CanDebitOut(); err != nil {
		return err
	}
	if err := credit.CanCredit(); err != nil {
		return err
	}
	if scene == SceneP2P {
		if err := debit.CanTransfer(); err != nil {
			return err
		}
	}
	return nil
}

func (s *TransferService) validateSubmitRefundAccounts(merchantNo, debitAccountNo, creditAccountNo string) error {
	debitNo := strings.TrimSpace(debitAccountNo)
	creditNo := strings.TrimSpace(creditAccountNo)
	if debitNo == "" || creditNo == "" {
		return ErrAccountResolveFailed
	}

	debit, ok := s.repo.GetAccount(debitNo)
	if !ok {
		return ErrAccountResolveFailed
	}
	credit, ok := s.repo.GetAccount(creditNo)
	if !ok {
		return ErrAccountResolveFailed
	}
	if strings.TrimSpace(debit.MerchantNo) != strings.TrimSpace(merchantNo) || strings.TrimSpace(credit.MerchantNo) != strings.TrimSpace(merchantNo) {
		return ErrAccountResolveFailed
	}

	if err := debit.CanDebitOut(); err != nil {
		return err
	}
	return nil
}

func (s *TransferService) UpdateTxnStatus(txnNo, status, errorCode, errorMsg string) error {
	return s.repo.UpdateTransferTxnStatus(txnNo, status, errorCode, errorMsg)
}
