package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xmz-ai/coin/internal/db"
	"github.com/xmz-ai/coin/internal/service"
)

type adminAsyncDispatcher interface {
	Enqueue(txnNo string)
}

type adminAsyncStatusDispatcher interface {
	EnqueueByStatus(txnNo, status string) bool
}

type adminAsyncTxnDispatcher interface {
	EnqueueTxn(txn service.TransferTxn) bool
}

type AdminRoutesOptions struct {
	Enabled         bool
	Repo            *db.Repository
	MerchantService *service.MerchantService
	CustomerService *service.CustomerService
	TransferService *service.TransferService
	TransferRouter  *service.TransferRoutingService
	AsyncTransfer   adminAsyncDispatcher
	AccountResolver *service.AccountResolver
	QueryService    *service.TxnQueryService
	SecretRotator   MerchantSecretRotator
	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	NowFn           func() time.Time
}

func RegisterAdminRoutes(r *gin.Engine, opts AdminRoutesOptions) error {
	if r == nil || !opts.Enabled {
		return nil
	}
	if opts.Repo == nil {
		return errors.New("admin repo is required")
	}
	nowFn := opts.NowFn
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}

	tokens, err := newAdminTokenManager(opts.JWTSecret, opts.AccessTokenTTL, opts.RefreshTokenTTL, nowFn)
	if err != nil {
		return err
	}
	authController, err := newAdminAuthController(opts.Repo, tokens)
	if err != nil {
		return err
	}
	setupController, err := newAdminSetupController(opts.Repo, opts.MerchantService, opts.SecretRotator)
	if err != nil {
		return err
	}

	handler := &AdminHandler{
		repo:            opts.Repo,
		merchantService: opts.MerchantService,
		customerService: opts.CustomerService,
		transfer:        opts.TransferService,
		transferRouter:  opts.TransferRouter,
		asyncTransfer:   opts.AsyncTransfer,
		accountResolver: opts.AccountResolver,
		query:           opts.QueryService,
		secretRotator:   opts.SecretRotator,
		nowFn:           nowFn,
	}

	base := r.Group("/admin/api/v1")
	base.GET("/setup/status", setupController.handleStatus)
	base.POST("/setup/initialize", setupController.handleInitialize)
	base.POST("/auth/login", authController.handleLogin)
	base.POST("/auth/refresh", authController.handleRefresh)

	protected := base.Group("")
	protected.Use(authController.Middleware())
	protected.GET("/auth/me", authController.handleMe)
	protected.POST("/auth/logout", authController.handleLogout)

	handler.Register(protected)
	return nil
}

type AdminHandler struct {
	repo            *db.Repository
	merchantService *service.MerchantService
	customerService *service.CustomerService
	transfer        *service.TransferService
	transferRouter  *service.TransferRoutingService
	asyncTransfer   adminAsyncDispatcher
	accountResolver *service.AccountResolver
	query           *service.TxnQueryService
	secretRotator   MerchantSecretRotator
	nowFn           func() time.Time
}

func (h *AdminHandler) Register(group *gin.RouterGroup) {
	if h == nil || group == nil {
		return
	}
	group.GET("/dashboard/overview", h.handleDashboardOverview)

	group.POST("/merchants", h.handleAdminCreateMerchant)
	group.GET("/merchants", h.handleAdminListMerchants)
	group.GET("/merchants/:merchant_no", h.handleAdminGetMerchant)
	group.PATCH("/merchants/:merchant_no/features", h.handleAdminPatchMerchantFeatures)
	group.POST("/merchants/:merchant_no/secret:rotate", h.handleAdminRotateMerchantSecret)
	group.GET("/merchants/:merchant_no/webhooks/config", h.handleAdminGetWebhookConfig)
	group.PUT("/merchants/:merchant_no/webhooks/config", h.handleAdminPutWebhookConfig)

	group.GET("/customers", h.handleAdminListCustomers)
	group.POST("/customers", h.handleAdminCreateCustomer)
	group.GET("/customers/by-out-user-id", h.handleAdminGetCustomerByOutUserID)

	group.GET("/accounts", h.handleAdminListAccounts)
	group.POST("/accounts", h.handleAdminCreateAccount)
	group.PATCH("/accounts/:account_no/capability", h.handleAdminPatchAccountCapability)
	group.GET("/accounts/:account_no/balance", h.handleAdminGetAccountBalance)

	group.POST("/transactions/credit", h.handleAdminCredit)
	group.POST("/transactions/debit", h.handleAdminDebit)
	group.POST("/transactions/transfer", h.handleAdminTransfer)
	group.POST("/transactions/refund", h.handleAdminRefund)
	group.GET("/transactions/:txn_no", h.handleAdminGetTxnByNo)
	group.GET("/transactions/by-out-trade-no", h.handleAdminGetTxnByOutTradeNo)
	group.GET("/transactions", h.handleAdminListTransactions)

	group.GET("/notify/outbox-events", h.handleAdminListOutboxEvents)
	group.GET("/audit/logs", h.handleAdminListAuditLogs)
}

type adminCreateMerchantRequest struct {
	Name                              string `json:"name"`
	AutoCreateAccountOnCustomerCreate *bool  `json:"auto_create_account_on_customer_create"`
	AutoCreateCustomerOnCredit        *bool  `json:"auto_create_customer_on_credit"`
}

type adminPatchMerchantFeatureRequest struct {
	AutoCreateAccountOnCustomerCreate *bool `json:"auto_create_account_on_customer_create"`
	AutoCreateCustomerOnCredit        *bool `json:"auto_create_customer_on_credit"`
}

type adminWebhookConfigRequest struct {
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

type adminCreateCustomerRequest struct {
	MerchantNo string `json:"merchant_no"`
	OutUserID  string `json:"out_user_id"`
}

type adminAccountCapability struct {
	AllowOverdraft    *bool  `json:"allow_overdraft"`
	MaxOverdraftLimit *int64 `json:"max_overdraft_limit"`
	AllowTransfer     *bool  `json:"allow_transfer"`
	AllowCreditIn     *bool  `json:"allow_credit_in"`
	AllowDebitOut     *bool  `json:"allow_debit_out"`
	BookEnabled       *bool  `json:"book_enabled"`
}

type adminCreateAccountRequest struct {
	MerchantNo      string                 `json:"merchant_no"`
	OwnerType       string                 `json:"owner_type"`
	OwnerOutUserID  string                 `json:"owner_out_user_id"`
	OwnerCustomerNo string                 `json:"owner_customer_no"`
	AccountType     string                 `json:"account_type"`
	Capability      adminAccountCapability `json:"capability"`
}

type adminPatchAccountCapabilityRequest struct {
	AllowTransfer *bool `json:"allow_transfer"`
	AllowCreditIn *bool `json:"allow_credit_in"`
	AllowDebitOut *bool `json:"allow_debit_out"`
}

type adminCreditRequest struct {
	MerchantNo      string `json:"merchant_no"`
	OutTradeNo      string `json:"out_trade_no"`
	Title           string `json:"title"`
	Remark          string `json:"remark"`
	DebitAccountNo  string `json:"debit_account_no"`
	CreditAccountNo string `json:"credit_account_no"`
	UserID          string `json:"user_id"`
	ExpireInDays    int64  `json:"expire_in_days"`
	Amount          int64  `json:"amount"`
}

type adminDebitRequest struct {
	MerchantNo      string `json:"merchant_no"`
	OutTradeNo      string `json:"out_trade_no"`
	Title           string `json:"title"`
	Remark          string `json:"remark"`
	BizType         string `json:"biz_type"`
	TransferScene   string `json:"transfer_scene"`
	DebitAccountNo  string `json:"debit_account_no"`
	DebitOutUserID  string `json:"debit_out_user_id"`
	CreditAccountNo string `json:"credit_account_no"`
	CreditOutUserID string `json:"credit_out_user_id"`
	Amount          int64  `json:"amount"`
}

type adminTransferRequest struct {
	MerchantNo     string `json:"merchant_no"`
	OutTradeNo     string `json:"out_trade_no"`
	Title          string `json:"title"`
	Remark         string `json:"remark"`
	BizType        string `json:"biz_type"`
	TransferScene  string `json:"transfer_scene"`
	FromAccountNo  string `json:"from_account_no"`
	FromOutUserID  string `json:"from_out_user_id"`
	ToAccountNo    string `json:"to_account_no"`
	ToOutUserID    string `json:"to_out_user_id"`
	ToExpireInDays int64  `json:"to_expire_in_days"`
	Amount         int64  `json:"amount"`
}

type adminRefundRequest struct {
	MerchantNo    string `json:"merchant_no"`
	OutTradeNo    string `json:"out_trade_no"`
	Title         string `json:"title"`
	Remark        string `json:"remark"`
	BizType       string `json:"biz_type"`
	RefundOfTxnNo string `json:"refund_of_txn_no"`
	Amount        int64  `json:"amount"`
}

func (h *AdminHandler) handleDashboardOverview(c *gin.Context) {
	if h == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	stats, err := h.repo.GetAdminDashboardStats(strings.TrimSpace(c.Query("merchant_no")))
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load dashboard stats failed")
		return
	}
	writeSuccess(c, gin.H{
		"merchant_count": stats.MerchantCount,
		"customer_count": stats.CustomerCount,
		"account_count":  stats.AccountCount,
		"outbox": gin.H{
			"PENDING":    stats.OutboxPendingCount,
			"PROCESSING": stats.OutboxProcessingCount,
			"DEAD":       stats.OutboxDeadCount,
		},
	})
}

func (h *AdminHandler) handleAdminCreateMerchant(c *gin.Context) {
	if h == nil || h.merchantService == nil || h.secretRotator == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin merchant service not configured")
		return
	}
	var req adminCreateMerchantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "name is required")
		return
	}

	autoCreateAccountOnCustomerCreate := true
	if req.AutoCreateAccountOnCustomerCreate != nil {
		autoCreateAccountOnCustomerCreate = *req.AutoCreateAccountOnCustomerCreate
	}
	autoCreateCustomerOnCredit := true
	if req.AutoCreateCustomerOnCredit != nil {
		autoCreateCustomerOnCredit = *req.AutoCreateCustomerOnCredit
	}

	merchant, err := h.merchantService.CreateMerchant("", req.Name)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "create merchant failed")
		return
	}
	if err := h.merchantService.UpsertMerchantFeatureConfig(merchant.MerchantNo, autoCreateAccountOnCustomerCreate, autoCreateCustomerOnCredit); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "save merchant feature config failed")
		return
	}
	secret, version, err := h.secretRotator.RotateSecret(c.Request.Context(), merchant.MerchantNo)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "rotate merchant secret failed")
		return
	}

	h.audit(c, db.AdminAuditLog{
		Action:         "MERCHANT_CREATE",
		TargetType:     "MERCHANT",
		TargetID:       merchant.MerchantNo,
		MerchantNo:     merchant.MerchantNo,
		RequestPayload: req,
		ResultCode:     "SUCCESS",
		ResultMessage:  "ok",
	})

	writeCreated(c, gin.H{
		"merchant_no":                            merchant.MerchantNo,
		"merchant_secret":                        secret,
		"budget_account_no":                      merchant.BudgetAccountNo,
		"receivable_account_no":                  merchant.ReceivableAccountNo,
		"writeoff_account_no":                    merchant.WriteoffAccountNo,
		"secret_version":                         version,
		"auto_create_account_on_customer_create": autoCreateAccountOnCustomerCreate,
		"auto_create_customer_on_credit":         autoCreateCustomerOnCredit,
	})
}

func (h *AdminHandler) handleAdminListMerchants(c *gin.Context) {
	if h == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	pageSize := parsePageSize(c.Query("page_size"), 20, 200)
	if pageSize <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid page_size")
		return
	}
	cursor := strings.TrimSpace(c.Query("page_token"))

	items, next, err := h.repo.ListMerchantsForAdmin(cursor, pageSize)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "list merchants failed")
		return
	}
	resp := make([]gin.H, 0, len(items))
	for _, item := range items {
		resp = append(resp, gin.H{
			"merchant_no":           item.MerchantNo,
			"name":                  item.Name,
			"budget_account_no":     item.BudgetAccountNo,
			"receivable_account_no": item.ReceivableAccountNo,
			"writeoff_account_no":   item.WriteoffAccountNo,
		})
	}
	writeSuccess(c, gin.H{
		"items":           resp,
		"next_page_token": next,
	})
}

func (h *AdminHandler) handleAdminGetMerchant(c *gin.Context) {
	if h == nil || h.merchantService == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	merchantNo := strings.TrimSpace(c.Param("merchant_no"))
	if merchantNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no is required")
		return
	}
	m, ok := h.merchantService.GetMerchantConfigByNo(merchantNo)
	if !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
		return
	}
	featureCfg, found, err := h.merchantService.GetMerchantFeatureConfig(merchantNo)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load merchant feature config failed")
		return
	}
	if !found {
		featureCfg = service.MerchantFeatureConfig{
			AutoCreateAccountOnCustomerCreate: true,
			AutoCreateCustomerOnCredit:        true,
		}
	}
	secretVersion := 0
	if versionReader, ok := h.secretRotator.(MerchantSecretVersionReader); ok {
		version, err := versionReader.GetSecretVersion(c.Request.Context(), merchantNo)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load merchant secret version failed")
			return
		}
		secretVersion = version
	}
	webhookCfg, webhookFound, err := h.repo.GetWebhookConfig(merchantNo)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load webhook config failed")
		return
	}
	if !webhookFound {
		webhookCfg = service.WebhookConfig{URL: "", Enabled: false}
	}
	writeSuccess(c, gin.H{
		"merchant_no":                            m.MerchantNo,
		"name":                                   m.Name,
		"status":                                 "ACTIVE",
		"budget_account_no":                      m.BudgetAccountNo,
		"receivable_account_no":                  m.ReceivableAccountNo,
		"writeoff_account_no":                    m.WriteoffAccountNo,
		"secret_version":                         secretVersion,
		"auto_create_account_on_customer_create": featureCfg.AutoCreateAccountOnCustomerCreate,
		"auto_create_customer_on_credit":         featureCfg.AutoCreateCustomerOnCredit,
		"webhook": gin.H{
			"url":     webhookCfg.URL,
			"enabled": webhookCfg.Enabled,
		},
	})
}

func (h *AdminHandler) handleAdminPatchMerchantFeatures(c *gin.Context) {
	if h == nil || h.merchantService == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	merchantNo := strings.TrimSpace(c.Param("merchant_no"))
	if merchantNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no is required")
		return
	}
	_, ok := h.merchantService.GetMerchantConfigByNo(merchantNo)
	if !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
		return
	}

	var req adminPatchMerchantFeatureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}

	cfg, found, err := h.merchantService.GetMerchantFeatureConfig(merchantNo)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load merchant feature config failed")
		return
	}
	if !found {
		cfg = service.MerchantFeatureConfig{
			AutoCreateAccountOnCustomerCreate: true,
			AutoCreateCustomerOnCredit:        true,
		}
	}
	if req.AutoCreateAccountOnCustomerCreate != nil {
		cfg.AutoCreateAccountOnCustomerCreate = *req.AutoCreateAccountOnCustomerCreate
	}
	if req.AutoCreateCustomerOnCredit != nil {
		cfg.AutoCreateCustomerOnCredit = *req.AutoCreateCustomerOnCredit
	}
	if err := h.merchantService.UpsertMerchantFeatureConfig(merchantNo, cfg.AutoCreateAccountOnCustomerCreate, cfg.AutoCreateCustomerOnCredit); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "save merchant feature config failed")
		return
	}

	h.audit(c, db.AdminAuditLog{
		Action:         "MERCHANT_FEATURE_PATCH",
		TargetType:     "MERCHANT",
		TargetID:       merchantNo,
		MerchantNo:     merchantNo,
		RequestPayload: req,
		ResultCode:     "SUCCESS",
		ResultMessage:  "ok",
	})

	writeSuccess(c, gin.H{
		"merchant_no":                            merchantNo,
		"auto_create_account_on_customer_create": cfg.AutoCreateAccountOnCustomerCreate,
		"auto_create_customer_on_credit":         cfg.AutoCreateCustomerOnCredit,
	})
}

func (h *AdminHandler) handleAdminRotateMerchantSecret(c *gin.Context) {
	if h == nil || h.secretRotator == nil || h.merchantService == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	merchantNo := strings.TrimSpace(c.Param("merchant_no"))
	if merchantNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no is required")
		return
	}
	if _, ok := h.merchantService.GetMerchantConfigByNo(merchantNo); !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
		return
	}
	secret, version, err := h.secretRotator.RotateSecret(c.Request.Context(), merchantNo)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "rotate merchant secret failed")
		return
	}

	h.audit(c, db.AdminAuditLog{
		Action:        "MERCHANT_SECRET_ROTATE",
		TargetType:    "MERCHANT",
		TargetID:      merchantNo,
		MerchantNo:    merchantNo,
		ResultCode:    "SUCCESS",
		ResultMessage: "ok",
	})

	writeSuccess(c, gin.H{
		"merchant_no":     merchantNo,
		"merchant_secret": secret,
		"secret_version":  version,
	})
}

func (h *AdminHandler) handleAdminGetWebhookConfig(c *gin.Context) {
	if h == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	merchantNo := strings.TrimSpace(c.Param("merchant_no"))
	if merchantNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no is required")
		return
	}
	cfg, found, err := h.repo.GetWebhookConfig(merchantNo)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load webhook config failed")
		return
	}
	if !found {
		cfg = service.WebhookConfig{URL: "", Enabled: false}
	}
	writeSuccess(c, gin.H{
		"merchant_no": merchantNo,
		"url":         cfg.URL,
		"enabled":     cfg.Enabled,
	})
}

func (h *AdminHandler) handleAdminPutWebhookConfig(c *gin.Context) {
	if h == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	merchantNo := strings.TrimSpace(c.Param("merchant_no"))
	if merchantNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no is required")
		return
	}
	var req adminWebhookConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if !strings.HasPrefix(strings.ToLower(req.URL), "https://") {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "url must be https")
		return
	}
	if err := h.repo.UpsertWebhookConfig(merchantNo, req.URL, req.Enabled); err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "save webhook config failed")
		return
	}

	h.audit(c, db.AdminAuditLog{
		Action:         "WEBHOOK_UPDATE",
		TargetType:     "MERCHANT",
		TargetID:       merchantNo,
		MerchantNo:     merchantNo,
		RequestPayload: req,
		ResultCode:     "SUCCESS",
		ResultMessage:  "ok",
	})

	writeSuccess(c, gin.H{
		"merchant_no": merchantNo,
		"url":         req.URL,
		"enabled":     req.Enabled,
	})
}

func (h *AdminHandler) handleAdminListCustomers(c *gin.Context) {
	if h == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	merchantNo := strings.TrimSpace(c.Query("merchant_no"))
	pageSize := parsePageSize(c.Query("page_size"), 20, 200)
	if pageSize <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid page_size")
		return
	}

	items, nextToken, err := h.repo.ListCustomersForAdmin(db.AdminCustomerFilter{
		MerchantNo: merchantNo,
		OutUserID:  strings.TrimSpace(c.Query("out_user_id")),
		CustomerNo: strings.TrimSpace(c.Query("customer_no")),
		CursorNo:   strings.TrimSpace(c.Query("page_token")),
		PageSize:   pageSize,
	})
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "list customers failed")
		return
	}

	respItems := make([]gin.H, 0, len(items))
	for _, item := range items {
		respItems = append(respItems, gin.H{
			"customer_no":        item.CustomerNo,
			"merchant_no":        item.MerchantNo,
			"out_user_id":        item.OutUserID,
			"default_account_no": item.DefaultAccountNo,
			"status":             "ACTIVE",
			"created_at":         item.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeSuccess(c, gin.H{
		"items":           respItems,
		"next_page_token": nextToken,
	})
}

func (h *AdminHandler) handleAdminCreateCustomer(c *gin.Context) {
	if h == nil || h.customerService == nil || h.merchantService == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	var req adminCreateCustomerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	req.MerchantNo = strings.TrimSpace(req.MerchantNo)
	req.OutUserID = strings.TrimSpace(req.OutUserID)
	if req.MerchantNo == "" || req.OutUserID == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no and out_user_id are required")
		return
	}
	if _, ok := h.merchantService.GetMerchantConfigByNo(req.MerchantNo); !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
		return
	}
	customer, err := h.customerService.CreateCustomer(req.MerchantNo, req.OutUserID)
	if err != nil {
		if errors.Is(err, service.ErrCustomerExists) {
			writeError(c, http.StatusConflict, "CUSTOMER_EXISTS", "customer already exists")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "create customer failed")
		return
	}

	h.audit(c, db.AdminAuditLog{
		Action:         "CUSTOMER_CREATE",
		TargetType:     "CUSTOMER",
		TargetID:       customer.CustomerNo,
		MerchantNo:     req.MerchantNo,
		RequestPayload: req,
		ResultCode:     "SUCCESS",
		ResultMessage:  "ok",
	})

	writeCreated(c, gin.H{
		"customer_no": customer.CustomerNo,
		"merchant_no": customer.MerchantNo,
		"out_user_id": customer.OutUserID,
	})
}

func (h *AdminHandler) handleAdminGetCustomerByOutUserID(c *gin.Context) {
	if h == nil || h.customerService == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	merchantNo := strings.TrimSpace(c.Query("merchant_no"))
	outUserID := strings.TrimSpace(c.Query("out_user_id"))
	if merchantNo == "" || outUserID == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no and out_user_id are required")
		return
	}
	customer, ok := h.customerService.GetCustomerByOutUserID(merchantNo, outUserID)
	if !ok {
		writeError(c, http.StatusNotFound, "CUSTOMER_NOT_FOUND", "customer not found")
		return
	}
	writeSuccess(c, gin.H{
		"customer_no": customer.CustomerNo,
		"merchant_no": customer.MerchantNo,
		"out_user_id": customer.OutUserID,
		"status":      "ACTIVE",
	})
}

func (h *AdminHandler) handleAdminListAccounts(c *gin.Context) {
	if h == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	merchantNo := strings.TrimSpace(c.Query("merchant_no"))
	pageSize := parsePageSize(c.Query("page_size"), 20, 200)
	if pageSize <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid page_size")
		return
	}

	items, nextToken, err := h.repo.ListAccountsForAdmin(db.AdminAccountFilter{
		MerchantNo: merchantNo,
		AccountNo:  strings.TrimSpace(c.Query("account_no")),
		CustomerNo: strings.TrimSpace(c.Query("customer_no")),
		OutUserID:  strings.TrimSpace(c.Query("out_user_id")),
		CursorNo:   strings.TrimSpace(c.Query("page_token")),
		PageSize:   pageSize,
	})
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "list accounts failed")
		return
	}

	respItems := make([]gin.H, 0, len(items))
	for _, item := range items {
		respItems = append(respItems, gin.H{
			"account_no":          item.AccountNo,
			"merchant_no":         item.MerchantNo,
			"customer_no":         item.CustomerNo,
			"owner_out_user_id":   item.OwnerOutUserID,
			"account_type":        item.AccountType,
			"allow_overdraft":     item.AllowOverdraft,
			"max_overdraft_limit": item.MaxOverdraftLimit,
			"allow_debit_out":     item.AllowDebitOut,
			"allow_credit_in":     item.AllowCreditIn,
			"allow_transfer":      item.AllowTransfer,
			"book_enabled":        item.BookEnabled,
			"balance":             item.Balance,
			"created_at":          item.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeSuccess(c, gin.H{
		"items":           respItems,
		"next_page_token": nextToken,
	})
}

func (h *AdminHandler) handleAdminCreateAccount(c *gin.Context) {
	if h == nil || h.repo == nil || h.merchantService == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	var req adminCreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	req.MerchantNo = strings.TrimSpace(req.MerchantNo)
	req.OwnerType = strings.ToUpper(strings.TrimSpace(req.OwnerType))
	req.OwnerOutUserID = strings.TrimSpace(req.OwnerOutUserID)
	req.OwnerCustomerNo = strings.TrimSpace(req.OwnerCustomerNo)
	req.AccountType = strings.ToUpper(strings.TrimSpace(req.AccountType))
	if req.MerchantNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no is required")
		return
	}
	if _, ok := h.merchantService.GetMerchantConfigByNo(req.MerchantNo); !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
		return
	}
	if req.OwnerType != "CUSTOMER" && req.OwnerType != "MERCHANT" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "owner_type must be CUSTOMER or MERCHANT")
		return
	}

	allowOverdraft := false
	if req.Capability.AllowOverdraft != nil {
		allowOverdraft = *req.Capability.AllowOverdraft
	}
	maxOverdraft := int64(0)
	if req.Capability.MaxOverdraftLimit != nil {
		maxOverdraft = *req.Capability.MaxOverdraftLimit
	}
	if maxOverdraft < 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "max_overdraft_limit must be >= 0")
		return
	}
	allowTransfer := true
	if req.Capability.AllowTransfer != nil {
		allowTransfer = *req.Capability.AllowTransfer
	}
	allowCreditIn := true
	if req.Capability.AllowCreditIn != nil {
		allowCreditIn = *req.Capability.AllowCreditIn
	}
	allowDebitOut := true
	if req.Capability.AllowDebitOut != nil {
		allowDebitOut = *req.Capability.AllowDebitOut
	}
	bookEnabled := false
	if req.Capability.BookEnabled != nil {
		bookEnabled = *req.Capability.BookEnabled
	}

	customerNo := ""
	if req.OwnerType == "CUSTOMER" {
		if req.OwnerOutUserID == "" && req.OwnerCustomerNo == "" {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "owner_out_user_id or owner_customer_no is required")
			return
		}
		if req.OwnerOutUserID != "" {
			customer, ok := h.customerService.GetCustomerByOutUserID(req.MerchantNo, req.OwnerOutUserID)
			if !ok {
				writeError(c, http.StatusNotFound, "CUSTOMER_NOT_FOUND", "customer not found")
				return
			}
			customerNo = customer.CustomerNo
		}
		if req.OwnerCustomerNo != "" {
			customer, ok, err := h.repo.GetCustomerByNo(req.MerchantNo, req.OwnerCustomerNo)
			if err != nil {
				writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load customer failed")
				return
			}
			if !ok {
				writeError(c, http.StatusNotFound, "CUSTOMER_NOT_FOUND", "customer not found")
				return
			}
			if customerNo != "" && customerNo != customer.CustomerNo {
				writeError(c, http.StatusConflict, "ACCOUNT_RESOLVE_CONFLICT", "owner resolve conflict")
				return
			}
			customerNo = customer.CustomerNo
		}
		if req.AccountType == "" {
			req.AccountType = "CUSTOMER"
		}
	} else {
		if req.OwnerOutUserID != "" || req.OwnerCustomerNo != "" {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "owner fields are not allowed for MERCHANT owner")
			return
		}
		if req.AccountType == "" {
			req.AccountType = "MERCHANT"
		}
	}

	accountNo, err := h.repo.NewAccountNo(req.MerchantNo, req.AccountType)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "generate account_no failed")
		return
	}
	if err := h.repo.CreateAccount(service.Account{
		AccountNo:         accountNo,
		MerchantNo:        req.MerchantNo,
		CustomerNo:        customerNo,
		AccountType:       req.AccountType,
		AllowOverdraft:    allowOverdraft,
		MaxOverdraftLimit: maxOverdraft,
		AllowTransfer:     allowTransfer,
		AllowCreditIn:     allowCreditIn,
		AllowDebitOut:     allowDebitOut,
		BookEnabled:       bookEnabled,
		Balance:           0,
	}); err != nil {
		if errors.Is(err, service.ErrAccountNoExists) {
			writeError(c, http.StatusConflict, "ACCOUNT_EXISTS", "account already exists")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "create account failed")
		return
	}

	h.audit(c, db.AdminAuditLog{
		Action:         "ACCOUNT_CREATE",
		TargetType:     "ACCOUNT",
		TargetID:       accountNo,
		MerchantNo:     req.MerchantNo,
		RequestPayload: req,
		ResultCode:     "SUCCESS",
		ResultMessage:  "ok",
	})

	writeCreated(c, gin.H{
		"account_no": accountNo,
	})
}

func (h *AdminHandler) handleAdminPatchAccountCapability(c *gin.Context) {
	if h == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	accountNo := strings.TrimSpace(c.Param("account_no"))
	if accountNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "account_no is required")
		return
	}
	account, ok := h.repo.GetAccount(accountNo)
	if !ok {
		writeError(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
		return
	}

	var req adminPatchAccountCapabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	allowTransfer := account.AllowTransfer
	allowCreditIn := account.AllowCreditIn
	allowDebitOut := account.AllowDebitOut
	if req.AllowTransfer != nil {
		allowTransfer = *req.AllowTransfer
	}
	if req.AllowCreditIn != nil {
		allowCreditIn = *req.AllowCreditIn
	}
	if req.AllowDebitOut != nil {
		allowDebitOut = *req.AllowDebitOut
	}
	h.repo.UpdateAccountCapabilities(accountNo, allowDebitOut, allowCreditIn, allowTransfer)

	h.audit(c, db.AdminAuditLog{
		Action:         "ACCOUNT_CAPABILITY_PATCH",
		TargetType:     "ACCOUNT",
		TargetID:       accountNo,
		MerchantNo:     account.MerchantNo,
		RequestPayload: req,
		ResultCode:     "SUCCESS",
		ResultMessage:  "ok",
	})

	writeSuccess(c, gin.H{
		"account_no":      accountNo,
		"allow_transfer":  allowTransfer,
		"allow_credit_in": allowCreditIn,
		"allow_debit_out": allowDebitOut,
	})
}

func (h *AdminHandler) handleAdminGetAccountBalance(c *gin.Context) {
	if h == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	accountNo := strings.TrimSpace(c.Param("account_no"))
	if accountNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "account_no is required")
		return
	}
	account, ok := h.repo.GetAccount(accountNo)
	if !ok {
		writeError(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
		return
	}
	bookSum := int64(0)
	availableBalance := account.Balance
	if account.BookEnabled {
		sum, err := h.repo.GetAccountBookBalanceSum(accountNo)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load account book sum failed")
			return
		}
		bookSum = sum
		availableBalance, err = h.repo.GetAvailableAccountBookBalanceSum(accountNo)
		if err != nil {
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load available balance failed")
			return
		}
	}
	writeSuccess(c, gin.H{
		"account_no":         account.AccountNo,
		"merchant_no":        account.MerchantNo,
		"balance":            account.Balance,
		"available_balance":  availableBalance,
		"book_enabled":       account.BookEnabled,
		"book_balance_sum":   bookSum,
	})
}

func (h *AdminHandler) handleAdminCredit(c *gin.Context) {
	if h == nil || h.transfer == nil || h.query == nil || h.merchantService == nil || h.transferRouter == nil || h.accountResolver == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	var req adminCreditRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	req.MerchantNo = strings.TrimSpace(req.MerchantNo)
	req.OutTradeNo = strings.TrimSpace(req.OutTradeNo)
	req.Title = strings.TrimSpace(req.Title)
	req.Remark = strings.TrimSpace(req.Remark)
	req.DebitAccountNo = strings.TrimSpace(req.DebitAccountNo)
	req.CreditAccountNo = strings.TrimSpace(req.CreditAccountNo)
	req.UserID = strings.TrimSpace(req.UserID)
	if req.MerchantNo == "" || req.OutTradeNo == "" || req.Amount <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no, out_trade_no and amount are required")
		return
	}
	if !isValidOutTradeNo(req.OutTradeNo) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid out_trade_no")
		return
	}
	if !isValidTxnCopy(req.Title, maxTxnTitleLen) || !isValidTxnCopy(req.Remark, maxTxnRemarkLen) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid title or remark")
		return
	}
	if _, ok := h.merchantService.GetMerchantConfigByNo(req.MerchantNo); !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
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
		resolved, err := h.accountResolver.ResolveCustomerAccount(req.MerchantNo, req.CreditAccountNo, req.UserID)
		if err != nil {
			if errors.Is(err, service.ErrAccountResolveConflict) {
				writeError(c, http.StatusConflict, "ACCOUNT_RESOLVE_CONFLICT", "account resolve conflict")
				return
			}
			writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "resolve credit account failed")
			return
		}
		if resolved == "" {
			if req.CreditAccountNo == "" {
				autoCreated, err := h.accountResolver.EnsureCustomerAccountForCredit(req.MerchantNo, req.UserID)
				if err != nil {
					writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "auto create customer account failed")
					return
				}
				resolved = strings.TrimSpace(autoCreated)
			}
			if resolved == "" {
				writeError(c, http.StatusNotFound, "OUT_USER_ID_NOT_FOUND", "account not found by user_id")
				return
			}
		}
		creditAccountNo = resolved
	}

	resolved, err := h.transferRouter.Resolve(service.TransferRoutingRequest{
		MerchantNo:      req.MerchantNo,
		Scene:           service.SceneIssue,
		DebitAccountNo:  req.DebitAccountNo,
		CreditAccountNo: creditAccountNo,
	})
	if err != nil {
		writeTransferError(c, err)
		return
	}

	creditAccount, ok := h.repo.GetAccount(resolved.CreditAccountNo)
	if !ok {
		writeError(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
		return
	}
	var creditExpireAt *time.Time
	if creditAccount.BookEnabled {
		if req.ExpireInDays > 0 {
			expireAt, err := calcExpireAtByDays(h.nowFn(), req.ExpireInDays)
			if err != nil {
				writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid expire_in_days")
				return
			}
			normalizedExpireAt := normalizeExpiryDayUTC(expireAt)
			creditExpireAt = &normalizedExpireAt
		}
	}

	txn, err := h.transfer.Submit(service.TransferRequest{
		MerchantNo:       req.MerchantNo,
		OutTradeNo:       req.OutTradeNo,
		Title:            req.Title,
		Remark:           req.Remark,
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
	h.enqueueTxn(txn)

	h.audit(c, db.AdminAuditLog{
		Action:         "TXN_SUBMIT_CREDIT",
		TargetType:     "TXN",
		TargetID:       txn.TxnNo,
		MerchantNo:     req.MerchantNo,
		RequestPayload: req,
		ResultCode:     "SUCCESS",
		ResultMessage:  "ok",
	})

	writeCreated(c, gin.H{"txn_no": txn.TxnNo, "status": service.TxnStatusInit})
}

func (h *AdminHandler) handleAdminDebit(c *gin.Context) {
	if h == nil || h.transfer == nil || h.query == nil || h.merchantService == nil || h.transferRouter == nil || h.accountResolver == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	var req adminDebitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	req.MerchantNo = strings.TrimSpace(req.MerchantNo)
	req.OutTradeNo = strings.TrimSpace(req.OutTradeNo)
	req.Title = strings.TrimSpace(req.Title)
	req.Remark = strings.TrimSpace(req.Remark)
	req.BizType = strings.TrimSpace(strings.ToUpper(req.BizType))
	req.TransferScene = strings.TrimSpace(strings.ToUpper(req.TransferScene))
	req.DebitAccountNo = strings.TrimSpace(req.DebitAccountNo)
	req.DebitOutUserID = strings.TrimSpace(req.DebitOutUserID)
	req.CreditAccountNo = strings.TrimSpace(req.CreditAccountNo)
	req.CreditOutUserID = strings.TrimSpace(req.CreditOutUserID)
	if req.MerchantNo == "" || req.OutTradeNo == "" || req.Amount <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no, out_trade_no and amount are required")
		return
	}
	if !isValidOutTradeNo(req.OutTradeNo) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid out_trade_no")
		return
	}
	if !isValidTxnCopy(req.Title, maxTxnTitleLen) || !isValidTxnCopy(req.Remark, maxTxnRemarkLen) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid title or remark")
		return
	}
	if _, ok := h.merchantService.GetMerchantConfigByNo(req.MerchantNo); !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
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

	debitAccountNo, err := h.accountResolver.ResolveCustomerAccount(req.MerchantNo, req.DebitAccountNo, req.DebitOutUserID)
	if err != nil {
		if errors.Is(err, service.ErrAccountResolveConflict) {
			writeError(c, http.StatusConflict, "ACCOUNT_RESOLVE_CONFLICT", "account resolve conflict")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "resolve debit account failed")
		return
	}
	if debitAccountNo == "" {
		writeError(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "debit account not found")
		return
	}

	creditAccountNo, err := h.accountResolver.ResolveCustomerAccount(req.MerchantNo, req.CreditAccountNo, req.CreditOutUserID)
	if err != nil {
		if errors.Is(err, service.ErrAccountResolveConflict) {
			writeError(c, http.StatusConflict, "ACCOUNT_RESOLVE_CONFLICT", "account resolve conflict")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "resolve credit account failed")
		return
	}

	resolved, err := h.transferRouter.Resolve(service.TransferRoutingRequest{
		MerchantNo:      req.MerchantNo,
		Scene:           service.SceneConsume,
		DebitAccountNo:  debitAccountNo,
		CreditAccountNo: creditAccountNo,
	})
	if err != nil {
		writeTransferError(c, err)
		return
	}

	txn, err := h.transfer.Submit(service.TransferRequest{
		MerchantNo:       req.MerchantNo,
		OutTradeNo:       req.OutTradeNo,
		Title:            req.Title,
		Remark:           req.Remark,
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
	h.enqueueTxn(txn)

	h.audit(c, db.AdminAuditLog{Action: "TXN_SUBMIT_DEBIT", TargetType: "TXN", TargetID: txn.TxnNo, MerchantNo: req.MerchantNo, RequestPayload: req, ResultCode: "SUCCESS", ResultMessage: "ok"})
	writeCreated(c, gin.H{"txn_no": txn.TxnNo, "status": service.TxnStatusInit})
}

func (h *AdminHandler) handleAdminTransfer(c *gin.Context) {
	if h == nil || h.transfer == nil || h.query == nil || h.merchantService == nil || h.transferRouter == nil || h.accountResolver == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	var req adminTransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	req.MerchantNo = strings.TrimSpace(req.MerchantNo)
	req.OutTradeNo = strings.TrimSpace(req.OutTradeNo)
	req.Title = strings.TrimSpace(req.Title)
	req.Remark = strings.TrimSpace(req.Remark)
	req.BizType = strings.TrimSpace(strings.ToUpper(req.BizType))
	req.TransferScene = strings.TrimSpace(strings.ToUpper(req.TransferScene))
	req.FromAccountNo = strings.TrimSpace(req.FromAccountNo)
	req.FromOutUserID = strings.TrimSpace(req.FromOutUserID)
	req.ToAccountNo = strings.TrimSpace(req.ToAccountNo)
	req.ToOutUserID = strings.TrimSpace(req.ToOutUserID)
	if req.MerchantNo == "" || req.OutTradeNo == "" || req.Amount <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no, out_trade_no and amount are required")
		return
	}
	if !isValidOutTradeNo(req.OutTradeNo) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid out_trade_no")
		return
	}
	if !isValidTxnCopy(req.Title, maxTxnTitleLen) || !isValidTxnCopy(req.Remark, maxTxnRemarkLen) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid title or remark")
		return
	}
	if _, ok := h.merchantService.GetMerchantConfigByNo(req.MerchantNo); !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
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

	fromAccountNo, err := h.accountResolver.ResolveCustomerAccount(req.MerchantNo, req.FromAccountNo, req.FromOutUserID)
	if err != nil {
		if errors.Is(err, service.ErrAccountResolveConflict) {
			writeError(c, http.StatusConflict, "ACCOUNT_RESOLVE_CONFLICT", "account resolve conflict")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "resolve from account failed")
		return
	}
	toAccountNo, err := h.accountResolver.ResolveCustomerAccount(req.MerchantNo, req.ToAccountNo, req.ToOutUserID)
	if err != nil {
		if errors.Is(err, service.ErrAccountResolveConflict) {
			writeError(c, http.StatusConflict, "ACCOUNT_RESOLVE_CONFLICT", "account resolve conflict")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "resolve to account failed")
		return
	}

	resolved, err := h.transferRouter.Resolve(service.TransferRoutingRequest{
		MerchantNo:      req.MerchantNo,
		Scene:           service.SceneP2P,
		DebitAccountNo:  fromAccountNo,
		CreditAccountNo: toAccountNo,
	})
	if err != nil {
		writeTransferError(c, err)
		return
	}

	fromAccount, ok := h.repo.GetAccount(resolved.DebitAccountNo)
	if !ok {
		writeError(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
		return
	}
	toAccount, ok := h.repo.GetAccount(resolved.CreditAccountNo)
	if !ok {
		writeError(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
		return
	}
	var creditExpireAt *time.Time
	if toAccount.BookEnabled && !fromAccount.BookEnabled {
		if req.ToExpireInDays <= 0 {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "to_expire_in_days is required for expiry account")
			return
		}
		expireAt, err := calcExpireAtByDays(h.nowFn(), req.ToExpireInDays)
		if err != nil {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid to_expire_in_days")
			return
		}
		expireUTC := normalizeExpiryDayUTC(expireAt)
		creditExpireAt = &expireUTC
	}

	txn, err := h.transfer.Submit(service.TransferRequest{
		MerchantNo:       req.MerchantNo,
		OutTradeNo:       req.OutTradeNo,
		Title:            req.Title,
		Remark:           req.Remark,
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
	h.enqueueTxn(txn)

	h.audit(c, db.AdminAuditLog{Action: "TXN_SUBMIT_TRANSFER", TargetType: "TXN", TargetID: txn.TxnNo, MerchantNo: req.MerchantNo, RequestPayload: req, ResultCode: "SUCCESS", ResultMessage: "ok"})
	writeCreated(c, gin.H{"txn_no": txn.TxnNo, "status": service.TxnStatusInit})
}

func (h *AdminHandler) handleAdminRefund(c *gin.Context) {
	if h == nil || h.transfer == nil || h.asyncTransfer == nil || h.merchantService == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	var req adminRefundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	req.MerchantNo = strings.TrimSpace(req.MerchantNo)
	req.OutTradeNo = strings.TrimSpace(req.OutTradeNo)
	req.Title = strings.TrimSpace(req.Title)
	req.Remark = strings.TrimSpace(req.Remark)
	req.BizType = strings.TrimSpace(strings.ToUpper(req.BizType))
	req.RefundOfTxnNo = strings.TrimSpace(req.RefundOfTxnNo)
	if req.MerchantNo == "" || req.OutTradeNo == "" || req.RefundOfTxnNo == "" || req.Amount <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no, out_trade_no, refund_of_txn_no and amount are required")
		return
	}
	if !isValidOutTradeNo(req.OutTradeNo) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid out_trade_no")
		return
	}
	if !isValidTxnCopy(req.Title, maxTxnTitleLen) || !isValidTxnCopy(req.Remark, maxTxnRemarkLen) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid title or remark")
		return
	}
	if !isValidUUID(req.RefundOfTxnNo) {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid refund_of_txn_no")
		return
	}
	if _, ok := h.merchantService.GetMerchantConfigByNo(req.MerchantNo); !ok {
		writeError(c, http.StatusNotFound, "MERCHANT_NOT_FOUND", "merchant not found")
		return
	}
	if req.BizType != "" && req.BizType != service.BizTypeRefund {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "biz_type must be REFUND")
		return
	}

	txn, err := h.transfer.Submit(service.TransferRequest{
		MerchantNo:       req.MerchantNo,
		OutTradeNo:       req.OutTradeNo,
		Title:            req.Title,
		Remark:           req.Remark,
		BizType:          service.BizTypeRefund,
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
		if errors.Is(err, service.ErrTxnNotFound) {
			writeError(c, http.StatusNotFound, "REFUND_ORIGIN_NOT_FOUND", "origin txn not found")
			return
		}
		if errors.Is(err, service.ErrTxnStatusInvalid) || errors.Is(err, service.ErrAccountResolveFailed) {
			writeError(c, http.StatusConflict, "REFUND_ORIGIN_INVALID", "origin txn invalid for refund")
			return
		}
		if errors.Is(err, service.ErrAccountForbidDebit) || errors.Is(err, service.ErrAccountForbidCredit) || errors.Is(err, service.ErrAccountForbidTransfer) {
			writeError(c, http.StatusConflict, "REFUND_ORIGIN_INVALID", "origin txn invalid for refund")
			return
		}
		if errors.Is(err, service.ErrRefundAmountExceeded) {
			writeError(c, http.StatusConflict, "REFUND_AMOUNT_EXCEEDED", "refund amount exceeded")
			return
		}
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "submit refund failed")
		return
	}
	h.enqueueTxn(txn)

	h.audit(c, db.AdminAuditLog{Action: "TXN_SUBMIT_REFUND", TargetType: "TXN", TargetID: txn.TxnNo, MerchantNo: req.MerchantNo, RequestPayload: req, ResultCode: "SUCCESS", ResultMessage: "ok"})
	writeCreated(c, gin.H{"txn_no": txn.TxnNo, "status": service.TxnStatusInit})
}

func (h *AdminHandler) handleAdminGetTxnByNo(c *gin.Context) {
	if h == nil || h.query == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	txnNo := strings.TrimSpace(c.Param("txn_no"))
	if txnNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "txn_no is required")
		return
	}
	item, ok := h.query.GetByTxnNo(txnNo)
	if !ok {
		writeError(c, http.StatusNotFound, "TXN_NOT_FOUND", "txn not found")
		return
	}
	writeSuccess(c, toTxnResponse(item))
}

func (h *AdminHandler) handleAdminGetTxnByOutTradeNo(c *gin.Context) {
	if h == nil || h.query == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	merchantNo := strings.TrimSpace(c.Query("merchant_no"))
	outTradeNo := strings.TrimSpace(c.Query("out_trade_no"))
	if merchantNo == "" || outTradeNo == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "merchant_no and out_trade_no are required")
		return
	}
	item, ok := h.query.GetByOutTradeNo(merchantNo, outTradeNo)
	if !ok {
		writeError(c, http.StatusNotFound, "TXN_NOT_FOUND", "txn not found")
		return
	}
	writeSuccess(c, toTxnResponse(item))
}

func (h *AdminHandler) handleAdminListTransactions(c *gin.Context) {
	if h == nil || h.query == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	merchantNo := strings.TrimSpace(c.Query("merchant_no"))
	pageSize := parsePageSize(c.Query("page_size"), 20, 200)
	if pageSize <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid page_size")
		return
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
	writeSuccess(c, gin.H{"items": respItems, "next_page_token": nextToken})
}

func (h *AdminHandler) handleAdminListOutboxEvents(c *gin.Context) {
	if h == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	pageSize := parsePageSize(c.Query("page_size"), 20, 200)
	if pageSize <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid page_size")
		return
	}
	cursorID := int64(0)
	if raw := strings.TrimSpace(c.Query("page_token")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid page_token")
			return
		}
		cursorID = v
	}
	items, nextToken, err := h.repo.ListOutboxEventsForAdmin(db.AdminOutboxFilter{
		MerchantNo: strings.TrimSpace(c.Query("merchant_no")),
		Status:     strings.TrimSpace(strings.ToUpper(c.Query("status"))),
		TxnNo:      strings.TrimSpace(c.Query("txn_no")),
		CursorID:   cursorID,
		PageSize:   pageSize,
	})
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "list outbox events failed")
		return
	}
	resp := make([]gin.H, 0, len(items))
	for _, item := range items {
		nextRetryAt := ""
		if item.NextRetryAt != nil {
			nextRetryAt = item.NextRetryAt.UTC().Format(time.RFC3339Nano)
		}
		resp = append(resp, gin.H{
			"id":            item.ID,
			"event_id":      item.EventID,
			"txn_no":        item.TxnNo,
			"merchant_no":   item.MerchantNo,
			"out_trade_no":  item.OutTradeNo,
			"status":        item.Status,
			"retry_count":   item.RetryCount,
			"next_retry_at": nextRetryAt,
			"updated_at":    item.UpdatedAt.UTC().Format(time.RFC3339Nano),
			"created_at":    item.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeSuccess(c, gin.H{"items": resp, "next_page_token": nextToken})
}

func (h *AdminHandler) handleAdminListAuditLogs(c *gin.Context) {
	if h == nil || h.repo == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler not configured")
		return
	}
	pageSize := parsePageSize(c.Query("page_size"), 20, 200)
	if pageSize <= 0 {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid page_size")
		return
	}
	cursorID := int64(0)
	if raw := strings.TrimSpace(c.Query("page_token")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid page_token")
			return
		}
		cursorID = v
	}
	items, nextToken, err := h.repo.ListAdminAuditLogs(db.AdminAuditFilter{
		OperatorUsername: strings.TrimSpace(c.Query("operator_username")),
		Action:           strings.TrimSpace(c.Query("action")),
		MerchantNo:       strings.TrimSpace(c.Query("merchant_no")),
		CursorID:         cursorID,
		PageSize:         pageSize,
	})
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "list audit logs failed")
		return
	}
	resp := make([]gin.H, 0, len(items))
	for _, item := range items {
		resp = append(resp, gin.H{
			"audit_id":          item.AuditID,
			"request_id":        item.RequestID,
			"operator_username": item.OperatorUsername,
			"action":            item.Action,
			"target_type":       item.TargetType,
			"target_id":         item.TargetID,
			"merchant_no":       item.MerchantNo,
			"request_payload":   item.RequestPayloadRaw,
			"result_code":       item.ResultCode,
			"result_message":    item.ResultMessage,
			"created_at":        item.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	writeSuccess(c, gin.H{"items": resp, "next_page_token": nextToken})
}

func (h *AdminHandler) enqueueTxn(txn service.TransferTxn) {
	if h == nil || h.asyncTransfer == nil {
		return
	}
	if dispatcher, ok := h.asyncTransfer.(adminAsyncTxnDispatcher); ok {
		_ = dispatcher.EnqueueTxn(txn)
		return
	}
	if dispatcher, ok := h.asyncTransfer.(adminAsyncStatusDispatcher); ok {
		_ = dispatcher.EnqueueByStatus(txn.TxnNo, txn.Status)
		return
	}
	h.asyncTransfer.Enqueue(txn.TxnNo)
}

func (h *AdminHandler) audit(c *gin.Context, entry db.AdminAuditLog) {
	if h == nil || h.repo == nil {
		return
	}
	if entry.RequestID == "" {
		entry.RequestID = getRequestID(c)
	}
	if entry.OperatorUsername == "" {
		if username, ok := adminUsernameFromContext(c); ok {
			entry.OperatorUsername = username
		}
	}
	if entry.OperatorUsername == "" {
		entry.OperatorUsername = "unknown"
	}
	_ = h.repo.InsertAdminAuditLog(entry)
}

func parsePageSize(raw string, fallback, max int) int {
	if fallback <= 0 {
		fallback = 20
	}
	if max <= 0 {
		max = 200
	}
	fixed := strings.TrimSpace(raw)
	if fixed == "" {
		return fallback
	}
	v, err := strconv.Atoi(fixed)
	if err != nil || v <= 0 {
		return -1
	}
	if v > max {
		v = max
	}
	return v
}
