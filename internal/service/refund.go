package service

import "sync"

type RefundRequest struct {
	MerchantNo  string
	RefundTxnNo string
	OriginTxnNo string
	Amount      int64
	Breakdown   []RefundPart
}

type RefundResult struct {
	RefundNo             string
	OriginRefundableLeft int64
}

type RefundService struct {
	repo Repository
	mu   sync.Mutex

	origins map[string]OriginTxn
	seq     int
}

func NewRefundService(repo Repository) *RefundService {
	return &RefundService{repo: repo, origins: map[string]OriginTxn{}}
}

func (s *RefundService) RegisterOrigin(o OriginTxn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.origins[o.TxnNo] = o
}

func (s *RefundService) SubmitRefund(req RefundRequest) (RefundResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.RefundTxnNo != "" {
		return s.submitByRepository(req)
	}
	return s.submitByOriginCache(req)
}

func (s *RefundService) submitByRepository(req RefundRequest) (RefundResult, error) {
	origin, ok := s.repo.GetTransferTxn(req.OriginTxnNo)
	if !ok {
		return RefundResult{}, ErrTxnNotFound
	}
	if req.MerchantNo != "" && origin.MerchantNo != req.MerchantNo {
		return RefundResult{}, ErrTxnNotFound
	}

	if len(req.Breakdown) > 0 {
		sum := int64(0)
		allowed := map[string]struct{}{}
		if origin.DebitAccountNo != "" {
			allowed[origin.DebitAccountNo] = struct{}{}
		}
		if origin.CreditAccountNo != "" {
			allowed[origin.CreditAccountNo] = struct{}{}
		}
		for _, p := range req.Breakdown {
			sum += p.Amount
			if _, ok := allowed[p.AccountNo]; !ok {
				return RefundResult{}, ErrRefundAccountNotInOrigin
			}
		}
		if sum != req.Amount {
			return RefundResult{}, ErrRefundBreakdownInvalid
		}
	}

	left, ok, err := s.repo.ApplyRefund(req.RefundTxnNo, req.OriginTxnNo, req.Amount)
	if err != nil {
		return RefundResult{}, err
	}
	if !ok {
		return RefundResult{}, ErrRefundAmountExceeded
	}
	return RefundResult{
		RefundNo:             req.RefundTxnNo,
		OriginRefundableLeft: left,
	}, nil
}

func (s *RefundService) submitByOriginCache(req RefundRequest) (RefundResult, error) {
	origin, ok := s.origins[req.OriginTxnNo]
	if !ok {
		return RefundResult{}, ErrTxnNotFound
	}

	if len(req.Breakdown) > 0 {
		sum := int64(0)
		allowed := map[string]struct{}{}
		for _, p := range origin.AccountImpacts {
			allowed[p.AccountNo] = struct{}{}
		}
		for _, p := range req.Breakdown {
			sum += p.Amount
			if _, ok := allowed[p.AccountNo]; !ok {
				return RefundResult{}, ErrRefundAccountNotInOrigin
			}
		}
		if sum != req.Amount {
			return RefundResult{}, ErrRefundBreakdownInvalid
		}
	}

	if origin.RefundableAmount < req.Amount {
		return RefundResult{}, ErrRefundAmountExceeded
	}

	origin.RefundableAmount -= req.Amount
	s.origins[req.OriginTxnNo] = origin

	for _, p := range req.Breakdown {
		a, ok := s.repo.GetAccount(p.AccountNo)
		if !ok {
			continue
		}
		a.Balance -= p.Amount
		_ = s.repo.CreateAccount(a)
	}

	s.seq++
	return RefundResult{
		RefundNo:             req.OriginTxnNo + "-r-" + string(rune('0'+s.seq)),
		OriginRefundableLeft: origin.RefundableAmount,
	}, nil
}
