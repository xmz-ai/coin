package service

type TransferRoutingRequest struct {
	MerchantNo      string
	Scene           string
	DebitAccountNo  string
	CreditAccountNo string
}

type TransferRoutingResult struct {
	DebitAccountNo  string
	CreditAccountNo string
}

type TransferRoutingService struct {
	repo Repository
}

func NewTransferRoutingService(repo Repository) *TransferRoutingService {
	return &TransferRoutingService{repo: repo}
}

func (s *TransferRoutingService) Resolve(req TransferRoutingRequest) (TransferRoutingResult, error) {
	debit := req.DebitAccountNo
	credit := req.CreditAccountNo

	m, ok := s.repo.GetMerchantByNo(req.MerchantNo)
	if !ok {
		return TransferRoutingResult{}, ErrAccountResolveFailed
	}

	switch req.Scene {
	case SceneIssue:
		if debit == "" {
			debit = m.BudgetAccountNo
		}
	case SceneConsume:
		if credit == "" {
			credit = m.ReceivableAccountNo
		}
	case SceneP2P:
		if debit == "" || credit == "" {
			return TransferRoutingResult{}, ErrAccountResolveFailed
		}
	}

	if debit == "" || credit == "" {
		return TransferRoutingResult{}, ErrAccountResolveFailed
	}

	da, ok := s.repo.GetAccount(debit)
	if !ok {
		return TransferRoutingResult{}, ErrAccountResolveFailed
	}
	ca, ok := s.repo.GetAccount(credit)
	if !ok {
		return TransferRoutingResult{}, ErrAccountResolveFailed
	}
	if da.MerchantNo != m.MerchantNo || ca.MerchantNo != m.MerchantNo {
		return TransferRoutingResult{}, ErrAccountResolveFailed
	}

	if err := da.CanDebitOut(); err != nil {
		return TransferRoutingResult{}, err
	}
	if err := ca.CanCredit(); err != nil {
		return TransferRoutingResult{}, err
	}
	if req.Scene == SceneP2P {
		if err := da.CanTransfer(); err != nil {
			return TransferRoutingResult{}, err
		}
	}

	return TransferRoutingResult{DebitAccountNo: debit, CreditAccountNo: credit}, nil
}
