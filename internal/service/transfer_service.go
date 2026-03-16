package service

import (
	"fmt"
	"time"
)

type TransferRequest struct {
	MerchantNo       string
	OutTradeNo       string
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
	if err := s.repo.CreateTransferTxn(txn); err != nil {
		return TransferTxn{}, err
	}
	s.repo.IncAppliedChange()
	return txn, nil
}

func (s *TransferService) UpdateTxnStatus(txnNo, status, errorCode, errorMsg string) error {
	return s.repo.UpdateTransferTxnStatus(txnNo, status, errorCode, errorMsg)
}
