package memoryrepo

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/xmz-ai/coin/internal/domain"
	"github.com/xmz-ai/coin/internal/service"
)

const outboxClaimProcessLease = 30 * time.Minute

// Repo is an in-memory repository used by tests.
type Repo struct {
	mu sync.RWMutex

	merchantsByNo    map[string]service.Merchant
	merchantsByID    map[string]service.Merchant
	accountsByNo     map[string]service.Account
	customersByK     map[string]service.Customer
	customersByNo    map[string]service.Customer
	transferTxnByK   map[string]service.TransferTxn
	transferTxnByNo  map[string]service.TransferTxn
	accountChanges   []AccountChange
	webhookConfigs   map[string]service.WebhookConfig
	featureConfigs   map[string]service.MerchantFeatureConfig
	outboxByEventID  map[string]outboxRecord
	outboxEventOrder []string
	notifyLogsByTxn  map[string][]service.NotifyLog

	secretsByMerchantNo map[string]string
	txnCompRuns         int
	notifyCompRuns      int
}

type AccountChange struct {
	TxnNo        string
	AccountNo    string
	Delta        int64
	BalanceAfter int64
}

type outboxRecord struct {
	event     service.OutboxEventDelivery
	status    string
	retries   int
	nextAt    time.Time
	updatedAt time.Time
}

func New() *Repo {
	return &Repo{
		merchantsByNo:       map[string]service.Merchant{},
		merchantsByID:       map[string]service.Merchant{},
		accountsByNo:        map[string]service.Account{},
		customersByK:        map[string]service.Customer{},
		customersByNo:       map[string]service.Customer{},
		transferTxnByK:      map[string]service.TransferTxn{},
		transferTxnByNo:     map[string]service.TransferTxn{},
		accountChanges:      make([]AccountChange, 0),
		webhookConfigs:      map[string]service.WebhookConfig{},
		featureConfigs:      map[string]service.MerchantFeatureConfig{},
		outboxByEventID:     map[string]outboxRecord{},
		outboxEventOrder:    make([]string, 0),
		notifyLogsByTxn:     map[string][]service.NotifyLog{},
		secretsByMerchantNo: map[string]string{},
	}
}

func (r *Repo) CreateMerchant(m service.Merchant) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.merchantsByNo[m.MerchantNo]; exists {
		return service.ErrMerchantNoExists
	}
	r.merchantsByNo[m.MerchantNo] = m
	r.merchantsByID[m.MerchantID] = m
	if _, ok := r.secretsByMerchantNo[m.MerchantNo]; !ok {
		r.secretsByMerchantNo[m.MerchantNo] = "sec_" + m.MerchantNo
	}
	return nil
}

func (r *Repo) GetMerchantByNo(merchantNo string) (service.Merchant, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.merchantsByNo[merchantNo]
	return m, ok
}

func (r *Repo) UpsertMerchantFeatureConfig(merchantNo string, autoCreateAccountOnCustomerCreate, autoCreateCustomerOnCredit bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.featureConfigs[merchantNo] = service.MerchantFeatureConfig{
		AutoCreateAccountOnCustomerCreate: autoCreateAccountOnCustomerCreate,
		AutoCreateCustomerOnCredit:        autoCreateCustomerOnCredit,
	}
	return nil
}

func (r *Repo) GetMerchantFeatureConfig(merchantNo string) (service.MerchantFeatureConfig, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.featureConfigs[merchantNo]
	return cfg, ok, nil
}

func (r *Repo) CreateAccount(a service.Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accountsByNo[a.AccountNo] = a
	if a.CustomerNo != "" {
		if c, ok := r.customersByNo[a.CustomerNo]; ok && c.MerchantNo == a.MerchantNo && c.DefaultAccountNo == "" {
			c.DefaultAccountNo = a.AccountNo
			r.customersByNo[a.CustomerNo] = c
			r.customersByK[a.MerchantNo+":"+c.OutUserID] = c
		}
	}
	return nil
}

func (r *Repo) GetAccount(accountNo string) (service.Account, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.accountsByNo[accountNo]
	return a, ok
}

func (r *Repo) UpdateAccountCapabilities(accountNo string, allowDebitOut, allowCreditIn, allowTransfer bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.accountsByNo[accountNo]
	if !ok {
		return
	}
	a.AllowDebitOut = allowDebitOut
	a.AllowCreditIn = allowCreditIn
	a.AllowTransfer = allowTransfer
	r.accountsByNo[accountNo] = a
}

func (r *Repo) CreateCustomer(c service.Customer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.merchantsByNo[c.MerchantNo]
	if !ok {
		return service.ErrInvalidMerchantNo
	}
	key := c.MerchantNo + ":" + c.OutUserID
	if _, exists := r.customersByK[key]; exists {
		return service.ErrCustomerExists
	}
	if c.CustomerNo != "" {
		if _, exists := r.customersByNo[c.CustomerNo]; exists {
			return service.ErrCustomerExists
		}
		r.customersByNo[c.CustomerNo] = c
	}
	r.customersByK[key] = c
	return nil
}

func (r *Repo) GetCustomerByOutUserID(merchantNo, outUserID string) (service.Customer, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.customersByK[merchantNo+":"+outUserID]
	return c, ok
}

func (r *Repo) GetMerchantByID(merchantID string) (service.Merchant, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.merchantsByID[merchantID]
	return m, ok
}

func (r *Repo) GetAccountByCustomerNo(merchantNo, customerNo string) (service.Account, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	customer, ok := r.customersByNo[customerNo]
	if !ok {
		return service.Account{}, false
	}
	if customer.MerchantNo != merchantNo || customer.DefaultAccountNo == "" {
		return service.Account{}, false
	}
	account, ok := r.accountsByNo[customer.DefaultAccountNo]
	if !ok {
		return service.Account{}, false
	}
	if account.MerchantNo != merchantNo || account.CustomerNo != customerNo {
		return service.Account{}, false
	}
	return account, true
}

func (r *Repo) CreateTransferTxn(txn service.TransferTxn) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := txn.MerchantNo + ":" + txn.OutTradeNo
	if _, exists := r.transferTxnByK[k]; exists {
		return service.ErrDuplicateOutTradeNo
	}
	if txn.CreatedAt.IsZero() {
		txn.CreatedAt = time.Now().UTC()
	}
	r.transferTxnByK[k] = txn
	r.transferTxnByNo[txn.TxnNo] = txn
	return nil
}

func (r *Repo) GetTransferTxn(txnNo string) (service.TransferTxn, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	txn, ok := r.transferTxnByNo[txnNo]
	return txn, ok
}

func (r *Repo) GetTransferTxnByOutTradeNo(merchantNo, outTradeNo string) (service.TransferTxn, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	txn, ok := r.transferTxnByK[merchantNo+":"+outTradeNo]
	return txn, ok
}

func (r *Repo) ListTransferTxnsByStatus(status string, limit int) ([]service.TransferTxn, error) {
	r.mu.RLock()
	items := make([]service.TransferTxn, 0, len(r.transferTxnByNo))
	for _, txn := range r.transferTxnByNo {
		if txn.Status == status {
			items = append(items, txn)
		}
	}
	r.mu.RUnlock()

	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].TxnNo < items[j].TxnNo
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})

	if limit <= 0 || len(items) <= limit {
		return items, nil
	}
	return items[:limit], nil
}

func (r *Repo) ListStaleTransferTxnNosByStatus(status string, staleBefore time.Time, limit int) ([]string, error) {
	txns, err := r.ListTransferTxnsByStatus(status, 0)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	staleBefore = staleBefore.UTC()
	items := make([]string, 0, limit)
	for _, txn := range txns {
		if !staleBefore.IsZero() && txn.CreatedAt.After(staleBefore) {
			continue
		}
		items = append(items, txn.TxnNo)
		if len(items) >= limit {
			break
		}
	}
	return items, nil
}

func (r *Repo) ListTransferTxns(filter service.TxnListFilter) ([]service.TransferTxn, string) {
	r.mu.RLock()
	all := make([]service.TransferTxn, 0, len(r.transferTxnByNo))
	for _, x := range r.transferTxnByNo {
		all = append(all, x)
	}
	merchantsByNo := make(map[string]service.Merchant, len(r.merchantsByNo))
	for k, v := range r.merchantsByNo {
		merchantsByNo[k] = v
	}
	accountsByNo := make(map[string]service.Account, len(r.accountsByNo))
	for k, v := range r.accountsByNo {
		accountsByNo[k] = v
	}
	customersByK := make(map[string]service.Customer, len(r.customersByK))
	for k, v := range r.customersByK {
		customersByK[k] = v
	}
	r.mu.RUnlock()

	matchesOutUser := func(txn service.TransferTxn, outUserID string) bool {
		merchant, ok := merchantsByNo[txn.MerchantNo]
		if !ok {
			return false
		}
		customer, ok := customersByK[merchant.MerchantNo+":"+outUserID]
		if !ok {
			return false
		}
		if a, ok := accountsByNo[txn.DebitAccountNo]; ok && a.CustomerNo == customer.CustomerNo {
			return true
		}
		if a, ok := accountsByNo[txn.CreditAccountNo]; ok && a.CustomerNo == customer.CustomerNo {
			return true
		}
		return false
	}

	filtered := make([]service.TransferTxn, 0, len(all))
	for _, x := range all {
		if filter.MerchantNo != "" && x.MerchantNo != filter.MerchantNo {
			continue
		}
		if filter.OutUserID != "" && !matchesOutUser(x, filter.OutUserID) {
			continue
		}
		if filter.Scene != "" && x.TransferScene != filter.Scene {
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
		cursorAt, cursorTxnNo, ok := service.DecodePageToken(filter.PageToken)
		if ok {
			next := make([]service.TransferTxn, 0, len(filtered))
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
	return page, service.EncodePageToken(last.CreatedAt, last.TxnNo)
}

func (r *Repo) UpdateTransferTxnStatus(txnNo, status, errorCode, errorMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	txn, ok := r.transferTxnByNo[txnNo]
	if !ok {
		return nil
	}
	txn.Status = status
	txn.ErrorCode = errorCode
	txn.ErrorMsg = errorMsg
	r.transferTxnByNo[txnNo] = txn
	r.transferTxnByK[txn.MerchantNo+":"+txn.OutTradeNo] = txn
	return nil
}

func (r *Repo) TransitionTransferTxnStatus(txnNo, fromStatus, toStatus, errorCode, errorMsg string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	txn, ok := r.transferTxnByNo[txnNo]
	if !ok {
		return false, nil
	}
	if txn.Status != fromStatus {
		return false, nil
	}
	txn.Status = toStatus
	txn.ErrorCode = errorCode
	txn.ErrorMsg = errorMsg
	r.transferTxnByNo[txnNo] = txn
	r.transferTxnByK[txn.MerchantNo+":"+txn.OutTradeNo] = txn
	return true, nil
}

func (r *Repo) UpdateTransferTxnParties(txnNo, debitAccountNo, creditAccountNo string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	txn, ok := r.transferTxnByNo[txnNo]
	if !ok {
		return nil
	}
	txn.DebitAccountNo = debitAccountNo
	txn.CreditAccountNo = creditAccountNo
	r.transferTxnByNo[txnNo] = txn
	r.transferTxnByK[txn.MerchantNo+":"+txn.OutTradeNo] = txn
	return nil
}

func (r *Repo) TryDecreaseTxnRefundable(txnNo string, amount int64) (left int64, ok bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	txn, exists := r.transferTxnByNo[txnNo]
	if !exists {
		return 0, false, service.ErrTxnNotFound
	}
	if txn.RefundableAmount < amount {
		return txn.RefundableAmount, false, nil
	}
	txn.RefundableAmount -= amount
	r.transferTxnByNo[txnNo] = txn
	r.transferTxnByK[txn.MerchantNo+":"+txn.OutTradeNo] = txn
	return txn.RefundableAmount, true, nil
}

func (r *Repo) ApplyTransferDebitStage(txnNo, debitAccountNo string, amount int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	txn, ok := r.transferTxnByNo[txnNo]
	if !ok {
		return false, service.ErrTxnNotFound
	}
	if txn.Status != service.TxnStatusInit {
		return false, nil
	}
	if txn.DebitAccountNo != "" && debitAccountNo != "" && txn.DebitAccountNo != debitAccountNo {
		return false, service.ErrAccountResolveFailed
	}
	if err := r.applyAccountDebitLocked(txnNo, txn.DebitAccountNo, amount); err != nil {
		return false, err
	}
	txn.Status = service.TxnStatusPaySuccess
	r.transferTxnByNo[txnNo] = txn
	r.transferTxnByK[txn.MerchantNo+":"+txn.OutTradeNo] = txn
	return true, nil
}

func (r *Repo) ApplyTransferCreditStage(txnNo, creditAccountNo string, amount int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	txn, ok := r.transferTxnByNo[txnNo]
	if !ok {
		return false, service.ErrTxnNotFound
	}
	if txn.Status != service.TxnStatusPaySuccess {
		return false, nil
	}
	if txn.CreditAccountNo != "" && creditAccountNo != "" && txn.CreditAccountNo != creditAccountNo {
		return false, service.ErrAccountResolveFailed
	}
	if err := r.applyAccountCreditLocked(txnNo, txn.CreditAccountNo, amount); err != nil {
		return false, err
	}
	txn.Status = service.TxnStatusRecvSuccess
	r.transferTxnByNo[txnNo] = txn
	r.transferTxnByK[txn.MerchantNo+":"+txn.OutTradeNo] = txn

	eventID := txn.TxnNo + ":TxnSucceeded"
	if _, exists := r.outboxByEventID[eventID]; !exists {
		r.outboxByEventID[eventID] = outboxRecord{
			event: service.OutboxEventDelivery{
				EventID:       eventID,
				TxnNo:         txn.TxnNo,
				MerchantNo:    txn.MerchantNo,
				OutTradeNo:    txn.OutTradeNo,
				BizType:       txn.BizType,
				TransferScene: txn.TransferScene,
				Amount:        txn.Amount,
				Status:        txn.Status,
				RetryCount:    0,
			},
			status:    "PENDING",
			retries:   0,
			updatedAt: time.Now().UTC(),
		}
		r.outboxEventOrder = append(r.outboxEventOrder, eventID)
	}
	return true, nil
}

func (r *Repo) ApplyRefundDebitStage(refundTxnNo string, amount int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	refund, exists := r.transferTxnByNo[refundTxnNo]
	if !exists {
		return false, service.ErrTxnNotFound
	}
	if refund.Status != service.TxnStatusInit {
		return false, nil
	}
	if refund.BizType != service.BizTypeRefund || refund.RefundOfTxnNo == "" {
		return false, service.ErrTxnStatusInvalid
	}

	origin, exists := r.transferTxnByNo[refund.RefundOfTxnNo]
	if !exists {
		return false, service.ErrTxnNotFound
	}
	if origin.MerchantNo != refund.MerchantNo {
		return false, service.ErrTxnNotFound
	}
	if origin.BizType != service.BizTypeTransfer || origin.Status != service.TxnStatusRecvSuccess {
		return false, service.ErrTxnStatusInvalid
	}
	if origin.RefundableAmount < amount {
		return false, service.ErrRefundAmountExceeded
	}
	if err := r.applyAccountDebitLocked(refundTxnNo, origin.CreditAccountNo, amount); err != nil {
		return false, err
	}

	origin.RefundableAmount -= amount
	r.transferTxnByNo[origin.TxnNo] = origin
	r.transferTxnByK[origin.MerchantNo+":"+origin.OutTradeNo] = origin

	refund.DebitAccountNo = origin.CreditAccountNo
	refund.CreditAccountNo = origin.DebitAccountNo
	refund.Status = service.TxnStatusPaySuccess
	r.transferTxnByNo[refundTxnNo] = refund
	r.transferTxnByK[refund.MerchantNo+":"+refund.OutTradeNo] = refund
	return true, nil
}

func (r *Repo) ApplyRefundCreditStage(refundTxnNo, creditAccountNo string, amount int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	refund, exists := r.transferTxnByNo[refundTxnNo]
	if !exists {
		return false, service.ErrTxnNotFound
	}
	if refund.Status != service.TxnStatusPaySuccess {
		return false, nil
	}
	stageCredit := refund.CreditAccountNo
	if stageCredit == "" {
		stageCredit = creditAccountNo
	}
	if stageCredit == "" {
		return false, service.ErrAccountResolveFailed
	}
	if refund.CreditAccountNo != "" && creditAccountNo != "" && refund.CreditAccountNo != creditAccountNo {
		return false, service.ErrAccountResolveFailed
	}
	if err := r.applyAccountCreditLocked(refundTxnNo, stageCredit, amount); err != nil {
		return false, err
	}

	refund.CreditAccountNo = stageCredit
	refund.Status = service.TxnStatusRecvSuccess
	r.transferTxnByNo[refundTxnNo] = refund
	r.transferTxnByK[refund.MerchantNo+":"+refund.OutTradeNo] = refund

	eventID := refund.TxnNo + ":TxnSucceeded"
	if _, exists := r.outboxByEventID[eventID]; !exists {
		r.outboxByEventID[eventID] = outboxRecord{
			event: service.OutboxEventDelivery{
				EventID:       eventID,
				TxnNo:         refund.TxnNo,
				MerchantNo:    refund.MerchantNo,
				OutTradeNo:    refund.OutTradeNo,
				BizType:       refund.BizType,
				TransferScene: refund.TransferScene,
				Amount:        refund.Amount,
				Status:        refund.Status,
				RetryCount:    0,
			},
			status:    "PENDING",
			retries:   0,
			updatedAt: time.Now().UTC(),
		}
		r.outboxEventOrder = append(r.outboxEventOrder, eventID)
	}
	return true, nil
}

func (r *Repo) ApplyRefund(refundTxnNo, originTxnNo string, amount int64) (left int64, ok bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	origin, exists := r.transferTxnByNo[originTxnNo]
	if !exists {
		return 0, false, service.ErrTxnNotFound
	}
	if origin.RefundableAmount < amount {
		return origin.RefundableAmount, false, nil
	}
	if err := r.applyAccountTransferLocked(refundTxnNo, origin.CreditAccountNo, origin.DebitAccountNo, amount); err != nil {
		return 0, false, err
	}
	origin.RefundableAmount -= amount
	r.transferTxnByNo[originTxnNo] = origin
	r.transferTxnByK[origin.MerchantNo+":"+origin.OutTradeNo] = origin

	refund, exists := r.transferTxnByNo[refundTxnNo]
	if exists {
		refund.DebitAccountNo = origin.CreditAccountNo
		refund.CreditAccountNo = origin.DebitAccountNo
		refund.Status = service.TxnStatusRecvSuccess
		r.transferTxnByNo[refundTxnNo] = refund
		r.transferTxnByK[refund.MerchantNo+":"+refund.OutTradeNo] = refund

		eventID := refund.TxnNo + ":TxnSucceeded"
		if _, exists := r.outboxByEventID[eventID]; !exists {
			r.outboxByEventID[eventID] = outboxRecord{
				event: service.OutboxEventDelivery{
					EventID:       eventID,
					TxnNo:         refund.TxnNo,
					MerchantNo:    refund.MerchantNo,
					OutTradeNo:    refund.OutTradeNo,
					BizType:       refund.BizType,
					TransferScene: refund.TransferScene,
					Amount:        refund.Amount,
					Status:        refund.Status,
					RetryCount:    0,
				},
				status:    "PENDING",
				retries:   0,
				updatedAt: time.Now().UTC(),
			}
			r.outboxEventOrder = append(r.outboxEventOrder, eventID)
		}
	}
	return origin.RefundableAmount, true, nil
}

func (r *Repo) TxnCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.transferTxnByK)
}

func (r *Repo) UpsertWebhookConfig(merchantNo, url string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.webhookConfigs[merchantNo] = service.WebhookConfig{URL: url, Enabled: enabled}
	return nil
}

func (r *Repo) GetWebhookConfig(merchantNo string) (service.WebhookConfig, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.webhookConfigs[merchantNo]
	return cfg, ok, nil
}

func (r *Repo) ClaimDueOutboxEvents(limit int, now time.Time) ([]service.OutboxEventDelivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now = now.UTC()
	if limit <= 0 {
		limit = 100
	}
	items := make([]service.OutboxEventDelivery, 0, limit)
	staleBefore := time.Now().UTC().Add(-outboxClaimProcessLease)
	for _, id := range r.outboxEventOrder {
		rec, ok := r.outboxByEventID[id]
		if !ok {
			continue
		}
		claimable := false
		switch rec.status {
		case "PENDING":
			claimable = rec.nextAt.IsZero() || !rec.nextAt.After(now)
		case "PROCESSING":
			claimable = !rec.updatedAt.IsZero() && !rec.updatedAt.After(staleBefore)
		}
		if !claimable {
			continue
		}
		rec.status = "PROCESSING"
		rec.updatedAt = now
		r.outboxByEventID[id] = rec
		items = append(items, rec.event)
		if len(items) >= limit {
			break
		}
	}
	return items, nil
}

func (r *Repo) ClaimDueOutboxEventsByTxnNo(txnNo string, limit int, now time.Time) ([]service.OutboxEventDelivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now = now.UTC()
	if limit <= 0 {
		limit = 100
	}
	items := make([]service.OutboxEventDelivery, 0, limit)
	staleBefore := time.Now().UTC().Add(-outboxClaimProcessLease)
	for _, id := range r.outboxEventOrder {
		rec, ok := r.outboxByEventID[id]
		if !ok {
			continue
		}
		if rec.event.TxnNo != txnNo {
			continue
		}
		claimable := false
		switch rec.status {
		case "PENDING":
			claimable = rec.nextAt.IsZero() || !rec.nextAt.After(now)
		case "PROCESSING":
			claimable = !rec.updatedAt.IsZero() && !rec.updatedAt.After(staleBefore)
		}
		if !claimable {
			continue
		}
		rec.status = "PROCESSING"
		rec.updatedAt = now
		r.outboxByEventID[id] = rec
		items = append(items, rec.event)
		if len(items) >= limit {
			break
		}
	}
	return items, nil
}

func (r *Repo) MarkOutboxEventSuccess(eventID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	rec, ok := r.outboxByEventID[eventID]
	if !ok {
		return nil
	}
	rec.status = service.NotifyStatusSuccess
	rec.updatedAt = time.Now().UTC()
	r.outboxByEventID[eventID] = rec
	return nil
}

func (r *Repo) MarkOutboxEventRetry(eventID string, retryCount int, nextRetryAt time.Time, dead bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	rec, ok := r.outboxByEventID[eventID]
	if !ok {
		return nil
	}
	rec.retries = retryCount
	rec.event.RetryCount = retryCount
	rec.nextAt = nextRetryAt.UTC()
	if dead {
		rec.status = service.NotifyStatusDead
	} else {
		rec.status = "PENDING"
	}
	rec.updatedAt = time.Now().UTC()
	r.outboxByEventID[eventID] = rec
	return nil
}

func (r *Repo) InsertNotifyLog(txnNo, status string, retries int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.notifyLogsByTxn[txnNo] = append(r.notifyLogsByTxn[txnNo], service.NotifyLog{
		TxnNo:   txnNo,
		Status:  status,
		Retries: retries,
	})
	return nil
}

func (r *Repo) GetActiveSecret(_ context.Context, merchantNo string) (string, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.secretsByMerchantNo[merchantNo]
	if !ok {
		return "", false, nil
	}
	return v, true, nil
}

func (r *Repo) SetMerchantSecret(merchantNo, secret string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.secretsByMerchantNo[merchantNo] = secret
}

func (r *Repo) ListNotifyLogs(txnNo string) []service.NotifyLog {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := r.notifyLogsByTxn[txnNo]
	out := make([]service.NotifyLog, len(items))
	copy(out, items)
	return out
}

func (r *Repo) IncTxnCompensationRun() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.txnCompRuns++
}

func (r *Repo) IncNotifyCompensationRun() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notifyCompRuns++
}

func (r *Repo) TxnCompensationRunCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.txnCompRuns
}

func (r *Repo) NotifyCompensationRunCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.notifyCompRuns
}

func (r *Repo) ListAccountChangesByTxnNo(txnNo string) []AccountChange {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]AccountChange, 0)
	for _, item := range r.accountChanges {
		if item.TxnNo == txnNo {
			out = append(out, item)
		}
	}
	return out
}

func (r *Repo) applyAccountTransferLocked(txnNo, debitAccountNo, creditAccountNo string, amount int64) error {
	debit, ok := r.accountsByNo[debitAccountNo]
	if !ok {
		return service.ErrAccountResolveFailed
	}
	credit, ok := r.accountsByNo[creditAccountNo]
	if !ok {
		return service.ErrAccountResolveFailed
	}
	if !debit.AllowDebitOut {
		return service.ErrAccountForbidDebit
	}
	if !credit.AllowCreditIn {
		return service.ErrAccountForbidCredit
	}
	if err := domain.Account(debit).CanDebit(amount); err != nil {
		return err
	}
	debit.Balance -= amount
	credit.Balance += amount
	r.accountsByNo[debitAccountNo] = debit
	r.accountsByNo[creditAccountNo] = credit
	r.appendAccountChangeLocked(txnNo, debit.AccountNo, -amount, debit.Balance)
	r.appendAccountChangeLocked(txnNo, credit.AccountNo, amount, credit.Balance)
	return nil
}

func (r *Repo) applyAccountDebitLocked(txnNo, debitAccountNo string, amount int64) error {
	debit, ok := r.accountsByNo[debitAccountNo]
	if !ok {
		return service.ErrAccountResolveFailed
	}
	if !debit.AllowDebitOut {
		return service.ErrAccountForbidDebit
	}
	if err := domain.Account(debit).CanDebit(amount); err != nil {
		return err
	}
	debit.Balance -= amount
	r.accountsByNo[debitAccountNo] = debit
	r.appendAccountChangeLocked(txnNo, debit.AccountNo, -amount, debit.Balance)
	return nil
}

func (r *Repo) applyAccountCreditLocked(txnNo, creditAccountNo string, amount int64) error {
	credit, ok := r.accountsByNo[creditAccountNo]
	if !ok {
		return service.ErrAccountResolveFailed
	}
	if !credit.AllowCreditIn {
		return service.ErrAccountForbidCredit
	}
	credit.Balance += amount
	r.accountsByNo[creditAccountNo] = credit
	r.appendAccountChangeLocked(txnNo, credit.AccountNo, amount, credit.Balance)
	return nil
}

func (r *Repo) appendAccountChangeLocked(txnNo, accountNo string, delta, balanceAfter int64) {
	if txnNo == "" || accountNo == "" {
		return
	}
	r.accountChanges = append(r.accountChanges, AccountChange{
		TxnNo:        txnNo,
		AccountNo:    accountNo,
		Delta:        delta,
		BalanceAfter: balanceAfter,
	})
}
