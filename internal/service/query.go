package service

import (
	"sort"
	"sync"
	"time"
)

type QueryTxn struct {
	TxnNo            string
	OutTradeNo       string
	MerchantNo       string
	OutUserID        string
	Scene            string
	Status           string
	Amount           int64
	RefundableAmount int64
	DebitAccountNo   string
	CreditAccountNo  string
	CreatedAt        time.Time
}

type QueryFilter struct {
	MerchantNo string
	OutUserID  string
	Scene      string
	Status     string
	StartTime  *time.Time
	EndTime    *time.Time
	PageSize   int
	PageToken  string
}

type TxnQueryService struct {
	repo Repository
	mu   sync.RWMutex
	txns []QueryTxn
}

func NewTxnQueryService(repo ...Repository) *TxnQueryService {
	svc := &TxnQueryService{txns: make([]QueryTxn, 0)}
	if len(repo) > 0 {
		svc.repo = repo[0]
	}
	return svc
}

func (s *TxnQueryService) AddTxn(txn QueryTxn) {
	if s.repo != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.txns = append(s.txns, txn)
}

func (s *TxnQueryService) GetByTxnNo(txnNo string) (QueryTxn, bool) {
	if s.repo != nil {
		txn, ok := s.repo.GetTransferTxn(txnNo)
		if !ok {
			return QueryTxn{}, false
		}
		return queryTxnFromTransferTxn(txn), true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, x := range s.txns {
		if x.TxnNo == txnNo {
			return x, true
		}
	}
	return QueryTxn{}, false
}

func (s *TxnQueryService) GetByOutTradeNo(merchantNo, outTradeNo string) (QueryTxn, bool) {
	if s.repo != nil {
		txn, ok := s.repo.GetTransferTxnByOutTradeNo(merchantNo, outTradeNo)
		if !ok {
			return QueryTxn{}, false
		}
		return queryTxnFromTransferTxn(txn), true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, x := range s.txns {
		if x.MerchantNo == merchantNo && x.OutTradeNo == outTradeNo {
			return x, true
		}
	}
	return QueryTxn{}, false
}

func (s *TxnQueryService) List(filter QueryFilter) ([]QueryTxn, string) {
	if s.repo != nil {
		txns, next := s.repo.ListTransferTxns(TxnListFilter{
			MerchantNo: filter.MerchantNo,
			OutUserID:  filter.OutUserID,
			Scene:      filter.Scene,
			Status:     filter.Status,
			StartTime:  filter.StartTime,
			EndTime:    filter.EndTime,
			PageSize:   filter.PageSize,
			PageToken:  filter.PageToken,
		})
		items := make([]QueryTxn, 0, len(txns))
		for _, txn := range txns {
			items = append(items, queryTxnFromTransferTxn(txn))
		}
		return items, next
	}
	s.mu.RLock()
	all := make([]QueryTxn, len(s.txns))
	copy(all, s.txns)
	s.mu.RUnlock()

	filtered := make([]QueryTxn, 0, len(all))
	for _, x := range all {
		if filter.MerchantNo != "" && x.MerchantNo != filter.MerchantNo {
			continue
		}
		if filter.OutUserID != "" && x.OutUserID != filter.OutUserID {
			continue
		}
		if filter.Scene != "" && x.Scene != filter.Scene {
			continue
		}
		if filter.Status != "" && x.Status != filter.Status {
			continue
		}
		if filter.StartTime != nil && x.CreatedAt.Before(filter.StartTime.UTC()) {
			continue
		}
		if filter.EndTime != nil && x.CreatedAt.After(filter.EndTime.UTC()) {
			continue
		}
		filtered = append(filtered, x)
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].TxnNo > filtered[j].TxnNo
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})

	if filter.PageToken != "" {
		cursorAt, cursorTxnNo, ok := DecodePageToken(filter.PageToken)
		if ok {
			next := make([]QueryTxn, 0, len(filtered))
			for _, x := range filtered {
				if x.CreatedAt.Before(cursorAt) || (x.CreatedAt.Equal(cursorAt) && x.TxnNo < cursorTxnNo) {
					next = append(next, x)
				}
			}
			filtered = next
		}
	}

	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if len(filtered) <= pageSize {
		return filtered, ""
	}
	page := filtered[:pageSize]
	last := page[len(page)-1]
	return page, EncodePageToken(last.CreatedAt, last.TxnNo)
}

func queryTxnFromTransferTxn(txn TransferTxn) QueryTxn {
	return QueryTxn{
		TxnNo:            txn.TxnNo,
		OutTradeNo:       txn.OutTradeNo,
		MerchantNo:       txn.MerchantNo,
		Scene:            txn.TransferScene,
		Status:           txn.Status,
		Amount:           txn.Amount,
		RefundableAmount: txn.RefundableAmount,
		DebitAccountNo:   txn.DebitAccountNo,
		CreditAccountNo:  txn.CreditAccountNo,
		CreatedAt:        txn.CreatedAt,
	}
}
