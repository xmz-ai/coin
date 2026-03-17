package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xmz-ai/coin/internal/service"
)

type TransferSubmitter interface {
	Submit(req service.TransferRequest) (service.TransferTxn, error)
}

type MerchantFinder interface {
	GetMerchantByNo(merchantNo string) (service.Merchant, bool)
}

type TransferRouter interface {
	Resolve(req service.TransferRoutingRequest) (service.TransferRoutingResult, error)
}

type TransferAsyncDispatcher interface {
	Enqueue(txnNo string)
}

type WebhookAsyncDispatcher interface {
	Enqueue(txnNo string)
}

type CustomerAccountResolver interface {
	ResolveCustomerAccount(merchantNo, accountNo, outUserID string) (string, error)
}

type AccountFinder interface {
	GetAccount(accountNo string) (service.Account, bool)
}

type TxnQueryStore interface {
	AddTxn(txn service.QueryTxn)
	GetByTxnNo(txnNo string) (service.QueryTxn, bool)
	GetByOutTradeNo(merchantNo, outTradeNo string) (service.QueryTxn, bool)
	List(filter service.QueryFilter) ([]service.QueryTxn, string)
}

type WebhookConfigStore interface {
	UpsertWebhookConfig(merchantNo, url string, enabled bool) error
	GetWebhookConfig(merchantNo string) (service.WebhookConfig, bool, error)
}

type BusinessHandler struct {
	transfer        TransferSubmitter
	merchants       MerchantFinder
	transferRouter  TransferRouter
	asyncTransfer   TransferAsyncDispatcher
	asyncWebhook    WebhookAsyncDispatcher
	accountResolver CustomerAccountResolver
	accounts        AccountFinder
	query           TxnQueryStore
	webhooks        WebhookConfigStore
	nowFn           func() time.Time
}

func NewBusinessHandler(
	transfer TransferSubmitter,
	merchants MerchantFinder,
	transferRouter TransferRouter,
	asyncTransfer TransferAsyncDispatcher,
	asyncWebhook WebhookAsyncDispatcher,
	accountResolver CustomerAccountResolver,
	accounts AccountFinder,
	query TxnQueryStore,
	webhooks WebhookConfigStore,
	nowFn func() time.Time,
) *BusinessHandler {
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	return &BusinessHandler{
		transfer:        transfer,
		merchants:       merchants,
		transferRouter:  transferRouter,
		asyncTransfer:   asyncTransfer,
		asyncWebhook:    asyncWebhook,
		accountResolver: accountResolver,
		accounts:        accounts,
		query:           query,
		webhooks:        webhooks,
		nowFn:           nowFn,
	}
}

const refundAccountBookUnsupportedCode = "REFUND_ACCOUNT_BOOK_UNSUPPORTED"

func (h *BusinessHandler) Register(v1 *gin.RouterGroup) {
	if v1 == nil {
		return
	}

	v1.POST("/transactions/credit", h.handleCredit)
	v1.POST("/transactions/debit", h.handleDebit)
	v1.POST("/transactions/transfer", h.handleTransfer)
	v1.POST("/transactions/refund", h.handleRefund)
	v1.GET("/transactions/:txn_no", h.handleGetByTxnNo)
	v1.GET("/transactions/by-out-trade-no/:out_trade_no", h.handleGetByOutTradeNo)
	v1.GET("/transactions", h.handleListTransactions)
	v1.GET("/webhooks/config", h.handleGetWebhookConfig)
	v1.PUT("/webhooks/config", h.handlePutWebhookConfig)
}

type creditRequest struct {
	OutTradeNo      string `json:"out_trade_no"`
	DebitAccountNo  string `json:"debit_account_no"`
	CreditAccountNo string `json:"credit_account_no"`
	UserID          string `json:"user_id"`
	ExpireInDays    int64  `json:"expire_in_days"`
	Amount          int64  `json:"amount"`
}

type debitRequest struct {
	OutTradeNo      string `json:"out_trade_no"`
	BizType         string `json:"biz_type"`
	TransferScene   string `json:"transfer_scene"`
	DebitAccountNo  string `json:"debit_account_no"`
	DebitOutUserID  string `json:"debit_out_user_id"`
	CreditAccountNo string `json:"credit_account_no"`
	CreditOutUserID string `json:"credit_out_user_id"`
	Amount          int64  `json:"amount"`
}

type transferRequest struct {
	OutTradeNo    string `json:"out_trade_no"`
	BizType       string `json:"biz_type"`
	TransferScene string `json:"transfer_scene"`
	FromAccountNo string `json:"from_account_no"`
	FromOutUserID string `json:"from_out_user_id"`
	ToAccountNo   string `json:"to_account_no"`
	ToOutUserID   string `json:"to_out_user_id"`
	ToExpireAt    string `json:"to_expire_at"`
	Amount        int64  `json:"amount"`
}

type refundRequest struct {
	OutTradeNo    string `json:"out_trade_no"`
	BizType       string `json:"biz_type"`
	RefundOfTxnNo string `json:"refund_of_txn_no"`
	Amount        int64  `json:"amount"`
}

type webhookConfigRequest struct {
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

func (h *BusinessHandler) handleCredit(c *gin.Context) {
	if h == nil || h.transfer == nil || h.query == nil || h.merchants == nil || h.transferRouter == nil || h.asyncTransfer == nil || h.accountResolver == nil || h.accounts == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "business handler not configured")
		return
	}

	merchantNo, ok := MerchantNoFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "merchant context missing")
		return
	}
	if _, ok := h.merchants.GetMerchantByNo(merchantNo); !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
		return
	}

	var req creditRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}

	req.OutTradeNo = strings.TrimSpace(req.OutTradeNo)
	req.DebitAccountNo = strings.TrimSpace(req.DebitAccountNo)
	req.CreditAccountNo = strings.TrimSpace(req.CreditAccountNo)
	req.UserID = strings.TrimSpace(req.UserID)

	if req.OutTradeNo == "" || req.Amount <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "out_trade_no and amount are required")
		return
	}
	if req.CreditAccountNo == "" && req.UserID == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "credit_account_no or user_id is required")
		return
	}
	if req.ExpireInDays < 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "expire_in_days must be >= 0")
		return
	}

	creditAccountNo := req.CreditAccountNo
	if req.UserID != "" {
		resolved, err := h.accountResolver.ResolveCustomerAccount(merchantNo, req.CreditAccountNo, req.UserID)
		if err != nil {
			if errors.Is(err, service.ErrAccountResolveConflict) {
				writeError(c, http.StatusConflict, "ACCOUNT_RESOLVE_CONFLICT", "account resolve conflict")
				return
			}
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "resolve credit account failed")
			return
		}
		if resolved == "" {
			writeError(c, http.StatusNotFound, "OUT_USER_ID_NOT_FOUND", "account not found by user_id")
			return
		}
		creditAccountNo = resolved
	}

	resolved, err := h.transferRouter.Resolve(service.TransferRoutingRequest{
		MerchantNo:      merchantNo,
		Scene:           service.SceneIssue,
		DebitAccountNo:  req.DebitAccountNo,
		CreditAccountNo: creditAccountNo,
	})
	if err != nil {
		writeTransferError(c, err)
		return
	}

	creditAccount, ok := h.accounts.GetAccount(resolved.CreditAccountNo)
	if !ok {
		writeError(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
		return
	}
	var creditExpireAt *time.Time
	if creditAccount.BookEnabled {
		if req.ExpireInDays <= 0 {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "expire_in_days is required for expiry account")
			return
		}
		expireAt, err := calcExpireAtByDays(h.nowFn(), req.ExpireInDays)
		if err != nil {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid expire_in_days")
			return
		}
		normalizedExpireAt := normalizeExpiryDayUTC(expireAt)
		creditExpireAt = &normalizedExpireAt
	}

	txn, err := h.transfer.Submit(service.TransferRequest{
		MerchantNo:       merchantNo,
		OutTradeNo:       req.OutTradeNo,
		BizType:          service.BizTypeTransfer,
		TransferScene:    service.SceneIssue,
		DebitAccountNo:   resolved.DebitAccountNo,
		CreditAccountNo:  resolved.CreditAccountNo,
		CreditExpireAt:   creditExpireAt,
		Amount:           req.Amount,
		RefundableAmount: req.Amount,
		Status:           service.TxnStatusInit,
	})
	if err != nil {
		if errors.Is(err, service.ErrDuplicateOutTradeNo) {
			writeError(c, http.StatusConflict, "DUPLICATE_OUT_TRADE_NO", "duplicate out_trade_no")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "submit transfer failed")
		return
	}
	h.asyncTransfer.Enqueue(txn.TxnNo)
	if h.asyncWebhook != nil {
		h.asyncWebhook.Enqueue(txn.TxnNo)
	}

	writeCreated(c, gin.H{
		"txn_no": txn.TxnNo,
		"status": service.TxnStatusInit,
	})
}

func (h *BusinessHandler) handleDebit(c *gin.Context) {
	if h == nil || h.transfer == nil || h.query == nil || h.merchants == nil || h.transferRouter == nil || h.asyncTransfer == nil || h.accountResolver == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "business handler not configured")
		return
	}

	merchantNo, ok := MerchantNoFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "merchant context missing")
		return
	}
	if _, ok := h.merchants.GetMerchantByNo(merchantNo); !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
		return
	}

	var req debitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}

	req.OutTradeNo = strings.TrimSpace(req.OutTradeNo)
	req.BizType = strings.TrimSpace(strings.ToUpper(req.BizType))
	req.TransferScene = strings.TrimSpace(strings.ToUpper(req.TransferScene))
	req.DebitAccountNo = strings.TrimSpace(req.DebitAccountNo)
	req.DebitOutUserID = strings.TrimSpace(req.DebitOutUserID)
	req.CreditAccountNo = strings.TrimSpace(req.CreditAccountNo)
	req.CreditOutUserID = strings.TrimSpace(req.CreditOutUserID)

	if req.OutTradeNo == "" || req.Amount <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "out_trade_no and amount are required")
		return
	}
	if req.DebitAccountNo == "" && req.DebitOutUserID == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "debit_account_no or debit_out_user_id is required")
		return
	}
	if req.BizType != "" && req.BizType != service.BizTypeTransfer {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "biz_type must be TRANSFER")
		return
	}
	if req.TransferScene != "" && req.TransferScene != service.SceneConsume {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "transfer_scene must be CONSUME")
		return
	}

	debitAccountNo := req.DebitAccountNo
	if req.DebitOutUserID != "" {
		resolvedDebit, err := h.accountResolver.ResolveCustomerAccount(merchantNo, req.DebitAccountNo, req.DebitOutUserID)
		if err != nil {
			if errors.Is(err, service.ErrAccountResolveConflict) {
				writeError(c, http.StatusConflict, "ACCOUNT_RESOLVE_CONFLICT", "account resolve conflict")
				return
			}
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "resolve debit account failed")
			return
		}
		if resolvedDebit == "" {
			writeError(c, http.StatusNotFound, "OUT_USER_ID_NOT_FOUND", "account not found by debit_out_user_id")
			return
		}
		debitAccountNo = resolvedDebit
	}

	creditAccountNo := req.CreditAccountNo
	if req.CreditOutUserID != "" {
		resolvedCredit, err := h.accountResolver.ResolveCustomerAccount(merchantNo, req.CreditAccountNo, req.CreditOutUserID)
		if err != nil {
			if errors.Is(err, service.ErrAccountResolveConflict) {
				writeError(c, http.StatusConflict, "ACCOUNT_RESOLVE_CONFLICT", "account resolve conflict")
				return
			}
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "resolve credit account failed")
			return
		}
		if resolvedCredit == "" {
			writeError(c, http.StatusNotFound, "OUT_USER_ID_NOT_FOUND", "account not found by credit_out_user_id")
			return
		}
		creditAccountNo = resolvedCredit
	}

	resolved, err := h.transferRouter.Resolve(service.TransferRoutingRequest{
		MerchantNo:      merchantNo,
		Scene:           service.SceneConsume,
		DebitAccountNo:  debitAccountNo,
		CreditAccountNo: creditAccountNo,
	})
	if err != nil {
		writeTransferError(c, err)
		return
	}

	txn, err := h.transfer.Submit(service.TransferRequest{
		MerchantNo:       merchantNo,
		OutTradeNo:       req.OutTradeNo,
		BizType:          service.BizTypeTransfer,
		TransferScene:    service.SceneConsume,
		DebitAccountNo:   resolved.DebitAccountNo,
		CreditAccountNo:  resolved.CreditAccountNo,
		Amount:           req.Amount,
		RefundableAmount: req.Amount,
		Status:           service.TxnStatusInit,
	})
	if err != nil {
		if errors.Is(err, service.ErrDuplicateOutTradeNo) {
			writeError(c, http.StatusConflict, "DUPLICATE_OUT_TRADE_NO", "duplicate out_trade_no")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "submit transfer failed")
		return
	}
	h.asyncTransfer.Enqueue(txn.TxnNo)
	if h.asyncWebhook != nil {
		h.asyncWebhook.Enqueue(txn.TxnNo)
	}

	writeCreated(c, gin.H{
		"txn_no": txn.TxnNo,
		"status": service.TxnStatusInit,
	})
}

func (h *BusinessHandler) handleTransfer(c *gin.Context) {
	if h == nil || h.transfer == nil || h.query == nil || h.merchants == nil || h.transferRouter == nil || h.asyncTransfer == nil || h.accountResolver == nil || h.accounts == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "business handler not configured")
		return
	}

	merchantNo, ok := MerchantNoFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "merchant context missing")
		return
	}
	if _, ok := h.merchants.GetMerchantByNo(merchantNo); !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
		return
	}

	var req transferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}

	req.OutTradeNo = strings.TrimSpace(req.OutTradeNo)
	req.BizType = strings.TrimSpace(strings.ToUpper(req.BizType))
	req.TransferScene = strings.TrimSpace(strings.ToUpper(req.TransferScene))
	req.FromAccountNo = strings.TrimSpace(req.FromAccountNo)
	req.FromOutUserID = strings.TrimSpace(req.FromOutUserID)
	req.ToAccountNo = strings.TrimSpace(req.ToAccountNo)
	req.ToOutUserID = strings.TrimSpace(req.ToOutUserID)
	req.ToExpireAt = strings.TrimSpace(req.ToExpireAt)

	if req.OutTradeNo == "" || req.Amount <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "out_trade_no and amount are required")
		return
	}
	if req.FromAccountNo == "" && req.FromOutUserID == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "from_account_no or from_out_user_id is required")
		return
	}
	if req.ToAccountNo == "" && req.ToOutUserID == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "to_account_no or to_out_user_id is required")
		return
	}
	if req.BizType != "" && req.BizType != service.BizTypeTransfer {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "biz_type must be TRANSFER")
		return
	}
	if req.TransferScene != "" && req.TransferScene != service.SceneP2P {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "transfer_scene must be P2P")
		return
	}

	fromAccountNo := req.FromAccountNo
	if req.FromOutUserID != "" {
		resolvedFrom, err := h.accountResolver.ResolveCustomerAccount(merchantNo, req.FromAccountNo, req.FromOutUserID)
		if err != nil {
			if errors.Is(err, service.ErrAccountResolveConflict) {
				writeError(c, http.StatusConflict, "ACCOUNT_RESOLVE_CONFLICT", "account resolve conflict")
				return
			}
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "resolve from account failed")
			return
		}
		if resolvedFrom == "" {
			writeError(c, http.StatusNotFound, "OUT_USER_ID_NOT_FOUND", "account not found by from_out_user_id")
			return
		}
		fromAccountNo = resolvedFrom
	}

	toAccountNo := req.ToAccountNo
	if req.ToOutUserID != "" {
		resolvedTo, err := h.accountResolver.ResolveCustomerAccount(merchantNo, req.ToAccountNo, req.ToOutUserID)
		if err != nil {
			if errors.Is(err, service.ErrAccountResolveConflict) {
				writeError(c, http.StatusConflict, "ACCOUNT_RESOLVE_CONFLICT", "account resolve conflict")
				return
			}
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "resolve to account failed")
			return
		}
		if resolvedTo == "" {
			writeError(c, http.StatusNotFound, "OUT_USER_ID_NOT_FOUND", "account not found by to_out_user_id")
			return
		}
		toAccountNo = resolvedTo
	}

	resolved, err := h.transferRouter.Resolve(service.TransferRoutingRequest{
		MerchantNo:      merchantNo,
		Scene:           service.SceneP2P,
		DebitAccountNo:  fromAccountNo,
		CreditAccountNo: toAccountNo,
	})
	if err != nil {
		writeTransferError(c, err)
		return
	}

	toAccount, ok := h.accounts.GetAccount(resolved.CreditAccountNo)
	if !ok {
		writeError(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
		return
	}
	var creditExpireAt *time.Time
	if toAccount.BookEnabled {
		if req.ToExpireAt == "" {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "to_expire_at is required for expiry account")
			return
		}
		expireAt, err := time.Parse(time.RFC3339, req.ToExpireAt)
		if err != nil {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid to_expire_at")
			return
		}
		expireUTC := normalizeExpiryDayUTC(expireAt)
		creditExpireAt = &expireUTC
	} else if req.ToExpireAt != "" {
		if _, err := time.Parse(time.RFC3339, req.ToExpireAt); err != nil {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid to_expire_at")
			return
		}
	}

	txn, err := h.transfer.Submit(service.TransferRequest{
		MerchantNo:       merchantNo,
		OutTradeNo:       req.OutTradeNo,
		BizType:          service.BizTypeTransfer,
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   resolved.DebitAccountNo,
		CreditAccountNo:  resolved.CreditAccountNo,
		CreditExpireAt:   creditExpireAt,
		Amount:           req.Amount,
		RefundableAmount: req.Amount,
		Status:           service.TxnStatusInit,
	})
	if err != nil {
		if errors.Is(err, service.ErrDuplicateOutTradeNo) {
			writeError(c, http.StatusConflict, "DUPLICATE_OUT_TRADE_NO", "duplicate out_trade_no")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "submit transfer failed")
		return
	}
	h.asyncTransfer.Enqueue(txn.TxnNo)
	if h.asyncWebhook != nil {
		h.asyncWebhook.Enqueue(txn.TxnNo)
	}

	writeCreated(c, gin.H{
		"txn_no": txn.TxnNo,
		"status": service.TxnStatusInit,
	})
}

func (h *BusinessHandler) handleRefund(c *gin.Context) {
	if h == nil || h.transfer == nil || h.asyncTransfer == nil || h.merchants == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "business handler not configured")
		return
	}

	merchantNo, ok := MerchantNoFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "merchant context missing")
		return
	}
	if _, ok := h.merchants.GetMerchantByNo(merchantNo); !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
		return
	}

	var req refundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}

	req.OutTradeNo = strings.TrimSpace(req.OutTradeNo)
	req.BizType = strings.TrimSpace(strings.ToUpper(req.BizType))
	req.RefundOfTxnNo = strings.TrimSpace(req.RefundOfTxnNo)
	if req.OutTradeNo == "" || req.RefundOfTxnNo == "" || req.Amount <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "out_trade_no, refund_of_txn_no and amount are required")
		return
	}
	if req.BizType != "" && req.BizType != service.BizTypeRefund {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "biz_type must be REFUND")
		return
	}

	if h.query != nil {
		if origin, found := h.query.GetByTxnNo(req.RefundOfTxnNo); found && origin.MerchantNo == merchantNo {
			if strings.TrimSpace(origin.DebitAccountNo) == "" {
				writeError(c, http.StatusConflict, refundAccountBookUnsupportedCode, "refund target account missing")
				return
			}
			if h.accounts != nil {
				if targetAccount, ok := h.accounts.GetAccount(strings.TrimSpace(origin.DebitAccountNo)); ok && targetAccount.BookEnabled {
					writeError(c, http.StatusConflict, refundAccountBookUnsupportedCode, "refund to book-enabled target account is not supported")
					return
				}
			}
		}
	}

	txn, err := h.transfer.Submit(service.TransferRequest{
		MerchantNo:       merchantNo,
		OutTradeNo:       req.OutTradeNo,
		BizType:          service.BizTypeRefund,
		TransferScene:    "",
		Amount:           req.Amount,
		RefundOfTxnNo:    req.RefundOfTxnNo,
		RefundableAmount: 0,
		Status:           service.TxnStatusInit,
	})
	if err != nil {
		if errors.Is(err, service.ErrDuplicateOutTradeNo) {
			writeError(c, http.StatusConflict, "DUPLICATE_OUT_TRADE_NO", "duplicate out_trade_no")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "submit refund failed")
		return
	}
	if h.asyncWebhook != nil {
		h.asyncWebhook.Enqueue(txn.TxnNo)
	}
	h.asyncTransfer.Enqueue(txn.TxnNo)

	writeCreated(c, gin.H{
		"txn_no": txn.TxnNo,
		"status": service.TxnStatusInit,
	})
}

func (h *BusinessHandler) handleGetByTxnNo(c *gin.Context) {
	if h == nil || h.query == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "business handler not configured")
		return
	}

	merchantNo, ok := MerchantNoFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "merchant context missing")
		return
	}

	txnNo := strings.TrimSpace(c.Param("txn_no"))
	if txnNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "txn_no is required")
		return
	}

	item, ok := h.query.GetByTxnNo(txnNo)
	if !ok || item.MerchantNo != merchantNo {
		writeError(c, http.StatusNotFound, "TXN_NOT_FOUND", "txn not found")
		return
	}

	writeSuccess(c, toTxnResponse(item))
}

func (h *BusinessHandler) handleGetByOutTradeNo(c *gin.Context) {
	if h == nil || h.query == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "business handler not configured")
		return
	}

	merchantNo, ok := MerchantNoFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "merchant context missing")
		return
	}

	outTradeNo := strings.TrimSpace(c.Param("out_trade_no"))
	if outTradeNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "out_trade_no is required")
		return
	}

	item, ok := h.query.GetByOutTradeNo(merchantNo, outTradeNo)
	if !ok {
		writeError(c, http.StatusNotFound, "TXN_NOT_FOUND", "txn not found")
		return
	}

	writeSuccess(c, toTxnResponse(item))
}

func (h *BusinessHandler) handleListTransactions(c *gin.Context) {
	if h == nil || h.query == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "business handler not configured")
		return
	}

	merchantNo, ok := MerchantNoFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "merchant context missing")
		return
	}

	pageSize := 20
	if raw := strings.TrimSpace(c.Query("page_size")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid page_size")
			return
		}
		if v > 200 {
			v = 200
		}
		pageSize = v
	}

	var startTime *time.Time
	if raw := strings.TrimSpace(c.Query("start_time")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid start_time")
			return
		}
		v := t.UTC()
		startTime = &v
	}

	var endTime *time.Time
	if raw := strings.TrimSpace(c.Query("end_time")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid end_time")
			return
		}
		v := t.UTC()
		endTime = &v
	}
	if startTime != nil && endTime != nil && startTime.After(*endTime) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "start_time must be <= end_time")
		return
	}

	items, nextToken := h.query.List(service.QueryFilter{
		MerchantNo: merchantNo,
		OutUserID:  strings.TrimSpace(c.Query("out_user_id")),
		Scene:      strings.TrimSpace(strings.ToUpper(c.Query("transfer_scene"))),
		Status:     strings.TrimSpace(strings.ToUpper(c.Query("status"))),
		StartTime:  startTime,
		EndTime:    endTime,
		PageSize:   pageSize,
		PageToken:  strings.TrimSpace(c.Query("page_token")),
	})

	respItems := make([]gin.H, 0, len(items))
	for _, item := range items {
		respItems = append(respItems, toTxnResponse(item))
	}

	writeSuccess(c, gin.H{
		"items":           respItems,
		"next_page_token": nextToken,
	})
}

func (h *BusinessHandler) handleGetWebhookConfig(c *gin.Context) {
	if h == nil || h.webhooks == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "business handler not configured")
		return
	}
	merchantNo, ok := MerchantNoFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "merchant context missing")
		return
	}

	cfg, found, err := h.webhooks.GetWebhookConfig(merchantNo)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load webhook config failed")
		return
	}
	if !found {
		writeSuccess(c, gin.H{
			"url":     "",
			"enabled": false,
			"retry_policy": gin.H{
				"max_retries": 8,
				"backoff":     []string{"1m", "5m", "15m", "1h", "6h"},
			},
		})
		return
	}

	writeSuccess(c, gin.H{
		"url":     cfg.URL,
		"enabled": cfg.Enabled,
		"retry_policy": gin.H{
			"max_retries": 8,
			"backoff":     []string{"1m", "5m", "15m", "1h", "6h"},
		},
	})
}

func (h *BusinessHandler) handlePutWebhookConfig(c *gin.Context) {
	if h == nil || h.webhooks == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "business handler not configured")
		return
	}
	merchantNo, ok := MerchantNoFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "merchant context missing")
		return
	}

	var req webhookConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if !strings.HasPrefix(strings.ToLower(req.URL), "https://") {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "url must be https")
		return
	}
	if err := h.webhooks.UpsertWebhookConfig(merchantNo, req.URL, req.Enabled); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "save webhook config failed")
		return
	}

	writeSuccess(c, gin.H{
		"url":     req.URL,
		"enabled": req.Enabled,
		"retry_policy": gin.H{
			"max_retries": 8,
			"backoff":     []string{"1m", "5m", "15m", "1h", "6h"},
		},
	})
}

func toTxnResponse(item service.QueryTxn) gin.H {
	return gin.H{
		"txn_no":            item.TxnNo,
		"out_trade_no":      item.OutTradeNo,
		"transfer_scene":    item.Scene,
		"status":            item.Status,
		"amount":            item.Amount,
		"refundable_amount": item.RefundableAmount,
		"debit_account_no":  item.DebitAccountNo,
		"credit_account_no": item.CreditAccountNo,
		"error_code":        item.ErrorCode,
		"error_msg":         item.ErrorMsg,
		"created_at":        item.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func calcExpireAtByDays(now time.Time, expireInDays int64) (time.Time, error) {
	const maxExpireInDays = int64((1<<63 - 1) / int64(24*time.Hour))
	if expireInDays <= 0 || expireInDays > maxExpireInDays {
		return time.Time{}, errors.New("invalid expire_in_days")
	}
	return now.UTC().Add(time.Duration(expireInDays) * 24 * time.Hour), nil
}

func normalizeExpiryDayUTC(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func writeSuccess(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{
		"code":       "SUCCESS",
		"message":    "ok",
		"request_id": getRequestID(c),
		"data":       data,
	})
}

func writeCreated(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, gin.H{
		"code":       "SUCCESS",
		"message":    "ok",
		"request_id": getRequestID(c),
		"data":       data,
	})
}

func writeError(c *gin.Context, httpCode int, code, message string) {
	c.JSON(httpCode, gin.H{
		"code":       code,
		"message":    message,
		"request_id": getRequestID(c),
	})
}

func writeTransferError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAccountResolveFailed):
		writeError(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
	case errors.Is(err, service.ErrAccountForbidDebit):
		writeError(c, http.StatusForbidden, "ACCOUNT_FORBID_DEBIT", "account forbid debit")
	case errors.Is(err, service.ErrAccountForbidCredit):
		writeError(c, http.StatusForbidden, "ACCOUNT_FORBID_CREDIT", "account forbid credit")
	case errors.Is(err, service.ErrAccountForbidTransfer):
		writeError(c, http.StatusForbidden, "ACCOUNT_FORBID_TRANSFER", "account forbid transfer")
	case errors.Is(err, service.ErrInsufficientBalance):
		writeError(c, http.StatusConflict, "INSUFFICIENT_BALANCE", "insufficient balance")
	default:
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "transfer validate failed")
	}
}
