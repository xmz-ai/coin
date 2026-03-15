package service

import (
	"errors"
	"fmt"
	"strings"
	"sync"
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

type AccountResolver struct {
	repo Repository
}

func NewAccountResolver(repo Repository) *AccountResolver {
	return &AccountResolver{repo: repo}
}

func (r *AccountResolver) ResolveCustomerAccount(merchantNo, accountNo, outUserID string) (string, error) {
	m, ok := r.repo.GetMerchantByNo(merchantNo)
	if !ok {
		return "", nil
	}

	var fromAccount string
	if accountNo != "" {
		a, ok := r.repo.GetAccount(accountNo)
		if ok && a.MerchantNo == m.MerchantNo {
			fromAccount = a.AccountNo
		}
	}

	var fromOutUser string
	if outUserID != "" {
		c, ok := r.repo.GetCustomerByOutUserID(merchantNo, outUserID)
		if ok {
			a, ok := r.repo.GetAccountByCustomerNo(merchantNo, c.CustomerNo)
			if ok {
				fromOutUser = a.AccountNo
			}
		}
	}

	if fromAccount != "" && fromOutUser != "" && fromAccount != fromOutUser {
		return "", ErrAccountResolveConflict
	}
	if fromAccount != "" {
		return fromAccount, nil
	}
	if fromOutUser != "" {
		return fromOutUser, nil
	}
	return "", nil
}

func (r *AccountResolver) ResolveMerchantSystemAccount(merchantNo, accountNo, outUserID, accountType string) (string, error) {
	if outUserID != "" {
		return "", ErrOutUserIDNotAllowedForSystemAccount
	}

	m, ok := r.repo.GetMerchantByNo(merchantNo)
	if !ok {
		return "", nil
	}

	if accountNo != "" {
		a, ok := r.repo.GetAccount(accountNo)
		if ok && a.MerchantNo == m.MerchantNo {
			return a.AccountNo, nil
		}
		return "", nil
	}
	if accountType == AccountTypeBudget {
		return m.BudgetAccountNo, nil
	}
	if accountType == AccountTypeReceivable {
		return m.ReceivableAccountNo, nil
	}
	return "", nil
}

var ErrProcessingGuardUnavailable = errors.New("processing guard unavailable")

type StageProcessingGuard interface {
	TryBegin(txnNo, stage string) bool
}

func ProcessingKey(txnNo, stage string) string {
	return strings.TrimSpace(txnNo) + "+" + strings.TrimSpace(stage)
}

type ProcessingGuard struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func NewProcessingGuard() *ProcessingGuard {
	return &ProcessingGuard{seen: map[string]struct{}{}}
}

func (g *ProcessingGuard) TryBegin(txnNo, stage string) bool {
	key := ProcessingKey(txnNo, stage)
	if key == "+" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.seen[key]; ok {
		return false
	}
	g.seen[key] = struct{}{}
	return true
}
