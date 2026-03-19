package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	dbsqlc "github.com/xmz-ai/coin/internal/db/sqlc"
	"github.com/xmz-ai/coin/internal/domain"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

const opTimeout = 3 * time.Second

type Repository struct {
	pool                 *pgxpool.Pool
	queries              *dbsqlc.Queries
	codeProvider         idpkg.CodeProvider
	codeSequenceInitOnce sync.Map
	txnCompRuns          int64
	notifyCompRuns       int64
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	r := &Repository{
		pool:    pool,
		queries: dbsqlc.New(pool),
	}
	r.codeProvider = idpkg.NewDBCodeProvider(r)
	return r
}

func (r *Repository) withTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), opTimeout)
}

func (r *Repository) CreateMerchant(m service.Merchant) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	merchantID, err := parseUUID(m.MerchantID)
	if err != nil {
		return err
	}

	err = r.queries.CreateMerchant(ctx, dbsqlc.CreateMerchantParams{
		MerchantID:          merchantID,
		MerchantNo:          m.MerchantNo,
		Name:                m.Name,
		BudgetAccountNo:     m.BudgetAccountNo,
		ReceivableAccountNo: m.ReceivableAccountNo,
	})
	if isUniqueViolation(err) {
		return service.ErrMerchantNoExists
	}
	return err
}

func (r *Repository) GetMerchantByNo(merchantNo string) (service.Merchant, bool) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	row, err := r.queries.GetMerchantByNo(ctx, merchantNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Merchant{}, false
	}
	if err != nil {
		return service.Merchant{}, false
	}
	return service.Merchant{
		MerchantID:          row.MerchantID,
		MerchantNo:          row.MerchantNo,
		Name:                row.Name,
		BudgetAccountNo:     row.BudgetAccountNo,
		ReceivableAccountNo: row.ReceivableAccountNo,
	}, true
}

func (r *Repository) UpsertMerchantFeatureConfig(merchantNo string, autoCreateAccountOnCustomerCreate, autoCreateCustomerOnCredit bool) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	tag, err := r.pool.Exec(ctx, `
		UPDATE merchant
		SET auto_create_account_on_customer_create = $2,
			auto_create_customer_on_credit = $3,
			updated_at = NOW()
		WHERE merchant_no = $1
	`, merchantNo, autoCreateAccountOnCustomerCreate, autoCreateCustomerOnCredit)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return service.ErrInvalidMerchantNo
	}
	return err
}

func (r *Repository) GetMerchantFeatureConfig(merchantNo string) (service.MerchantFeatureConfig, bool, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	var cfg service.MerchantFeatureConfig
	err := r.pool.QueryRow(ctx, `
		SELECT auto_create_account_on_customer_create, auto_create_customer_on_credit
		FROM merchant
		WHERE merchant_no = $1
		LIMIT 1
	`, merchantNo).Scan(&cfg.AutoCreateAccountOnCustomerCreate, &cfg.AutoCreateCustomerOnCredit)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.MerchantFeatureConfig{}, false, nil
	}
	if err != nil {
		return service.MerchantFeatureConfig{}, false, err
	}
	return cfg, true, nil
}

func (r *Repository) CreateAccount(a service.Account) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	customerNo := strings.TrimSpace(a.CustomerNo)

	err := r.queries.CreateAccount(ctx, dbsqlc.CreateAccountParams{
		AccountNo:         a.AccountNo,
		MerchantNo:        a.MerchantNo,
		CustomerNo:        nullableText(customerNo),
		AccountType:       a.AccountType,
		AllowOverdraft:    a.AllowOverdraft,
		MaxOverdraftLimit: a.MaxOverdraftLimit,
		AllowDebitOut:     a.AllowDebitOut,
		AllowCreditIn:     a.AllowCreditIn,
		AllowTransfer:     a.AllowTransfer,
		BookEnabled:       a.BookEnabled,
		Balance:           a.Balance,
	})
	if isUniqueViolation(err) {
		return service.ErrAccountNoExists
	}
	if err != nil {
		return err
	}

	if customerNo == "" {
		return nil
	}
	_, err = r.queries.SetCustomerDefaultAccountIfEmpty(ctx, dbsqlc.SetCustomerDefaultAccountIfEmptyParams{
		DefaultAccountNo: nullableText(a.AccountNo),
		MerchantNo:       a.MerchantNo,
		CustomerNo:       customerNo,
	})
	return err
}

func (r *Repository) NewMerchantNo() (string, error) {
	return r.codeProvider.NewMerchantNo()
}

func (r *Repository) NewCustomerNo() (string, error) {
	return r.codeProvider.NewCustomerNo()
}

func (r *Repository) NewAccountNo(merchantNo, accountType string) (string, error) {
	return r.codeProvider.NewAccountNo(merchantNo, accountType)
}

func (r *Repository) LeaseRange(codeType, scopeKey string, batchSize int64) (int64, int64, error) {
	if strings.TrimSpace(codeType) == "" || strings.TrimSpace(scopeKey) == "" || batchSize <= 0 {
		return 0, 0, fmt.Errorf("invalid lease args")
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	seqKey := codeType + ":" + scopeKey
	if _, ok := r.codeSequenceInitOnce.Load(seqKey); !ok {
		if err := r.queries.InitCodeSequence(ctx, dbsqlc.InitCodeSequenceParams{
			CodeType: codeType,
			ScopeKey: scopeKey,
		}); err != nil {
			return 0, 0, err
		}
		r.codeSequenceInitOnce.Store(seqKey, struct{}{})
	}

	row, err := r.queries.LeaseCodeRange(ctx, dbsqlc.LeaseCodeRangeParams{
		BatchSize: batchSize,
		CodeType:  codeType,
		ScopeKey:  scopeKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		r.codeSequenceInitOnce.Delete(seqKey)
		return 0, 0, idpkg.ErrCodeAllocatorUnavailable
	}
	if err != nil {
		return 0, 0, err
	}
	return row.StartValue, row.EndValue, nil
}

func (r *Repository) GetAccount(accountNo string) (service.Account, bool) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	row, err := r.queries.GetAccountByNo(ctx, accountNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Account{}, false
	}
	if err != nil {
		return service.Account{}, false
	}
	return accountFromGetAccountRow(row), true
}

func (r *Repository) UpdateAccountCapabilities(accountNo string, allowDebitOut, allowCreditIn, allowTransfer bool) {
	ctx, cancel := r.withTimeout()
	defer cancel()
	_ = r.queries.UpdateAccountCapabilities(ctx, dbsqlc.UpdateAccountCapabilitiesParams{
		AllowDebitOut: allowDebitOut,
		AllowCreditIn: allowCreditIn,
		AllowTransfer: allowTransfer,
		AccountNo:     accountNo,
	})
}

func (r *Repository) CreateCustomer(c service.Customer) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	customerID, err := parseUUID(c.CustomerID)
	if err != nil {
		return err
	}

	err = r.queries.CreateCustomer(ctx, dbsqlc.CreateCustomerParams{
		CustomerID:       customerID,
		CustomerNo:       c.CustomerNo,
		MerchantNo:       c.MerchantNo,
		OutUserID:        c.OutUserID,
		DefaultAccountNo: nullableText(strings.TrimSpace(c.DefaultAccountNo)),
	})
	if isUniqueViolation(err) {
		return service.ErrCustomerExists
	}
	return err
}

func (r *Repository) GetCustomerByOutUserID(merchantNo, outUserID string) (service.Customer, bool) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	row, err := r.queries.GetCustomerByOutUserID(ctx, dbsqlc.GetCustomerByOutUserIDParams{
		MerchantNo: merchantNo,
		OutUserID:  outUserID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Customer{}, false
	}
	if err != nil {
		return service.Customer{}, false
	}
	return service.Customer{
		CustomerID:       row.CustomerID,
		CustomerNo:       row.CustomerNo,
		MerchantNo:       row.MerchantNo,
		OutUserID:        row.OutUserID,
		DefaultAccountNo: strings.TrimSpace(row.DefaultAccountNo),
	}, true
}

func (r *Repository) GetMerchantByID(merchantID string) (service.Merchant, bool) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	merchantUUID, err := parseUUID(merchantID)
	if err != nil {
		return service.Merchant{}, false
	}

	row, err := r.queries.GetMerchantByID(ctx, merchantUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Merchant{}, false
	}
	if err != nil {
		return service.Merchant{}, false
	}
	return service.Merchant{
		MerchantID:          row.MerchantID,
		MerchantNo:          row.MerchantNo,
		Name:                row.Name,
		BudgetAccountNo:     row.BudgetAccountNo,
		ReceivableAccountNo: row.ReceivableAccountNo,
	}, true
}

func (r *Repository) GetAccountByCustomerNo(merchantNo, customerNo string) (service.Account, bool) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	accountNo, err := r.queries.GetAccountNoByCustomerNo(ctx, dbsqlc.GetAccountNoByCustomerNoParams{
		MerchantNo: merchantNo,
		CustomerNo: customerNo,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Account{}, false
	}
	if err != nil {
		return service.Account{}, false
	}
	if !accountNo.Valid || strings.TrimSpace(accountNo.String) == "" {
		return service.Account{}, false
	}

	row, err := r.queries.GetAccountByNo(ctx, strings.TrimSpace(accountNo.String))
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Account{}, false
	}
	if err != nil {
		return service.Account{}, false
	}
	return accountFromGetAccountRow(row), true
}

func (r *Repository) CreateTransferTxn(txn service.TransferTxn) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	txnUUID, err := parseUUID(txn.TxnNo)
	if err != nil {
		return err
	}

	err = r.queries.CreateTransferTxn(ctx, dbsqlc.CreateTransferTxnParams{
		TxnNo:            txnUUID,
		MerchantNo:       txn.MerchantNo,
		OutTradeNo:       txn.OutTradeNo,
		BizType:          txn.BizType,
		TransferScene:    nullIfEmpty(txn.TransferScene),
		DebitAccountNo:   nullIfEmpty(txn.DebitAccountNo),
		CreditAccountNo:  nullIfEmpty(txn.CreditAccountNo),
		CreditExpireAt:   nullableDate(&txn.CreditExpireAt, !txn.CreditExpireAt.IsZero()),
		Amount:           txn.Amount,
		Status:           txn.Status,
		RefundOfTxnNo:    nullIfEmpty(txn.RefundOfTxnNo),
		RefundableAmount: txn.RefundableAmount,
		ErrorCode:        nullIfEmpty(txn.ErrorCode),
		ErrorMsg:         nullIfEmpty(txn.ErrorMsg),
	})
	if isUniqueViolation(err) {
		return service.ErrDuplicateOutTradeNo
	}
	return err
}

func (r *Repository) GetTransferTxn(txnNo string) (service.TransferTxn, bool) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	txnUUID, err := parseUUID(txnNo)
	if err != nil {
		return service.TransferTxn{}, false
	}

	row, err := r.queries.GetTransferTxnByNo(ctx, txnUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.TransferTxn{}, false
	}
	if err != nil {
		return service.TransferTxn{}, false
	}
	return transferTxnFromByNoRow(row), true
}

func (r *Repository) GetTransferTxnByOutTradeNo(merchantNo, outTradeNo string) (service.TransferTxn, bool) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	row, err := r.queries.GetTransferTxnByOutTradeNo(ctx, dbsqlc.GetTransferTxnByOutTradeNoParams{
		MerchantNo: merchantNo,
		OutTradeNo: outTradeNo,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return service.TransferTxn{}, false
	}
	if err != nil {
		return service.TransferTxn{}, false
	}
	return transferTxnFromByOutTradeRow(row), true
}

func (r *Repository) ListTransferTxns(filter service.TxnListFilter) ([]service.TransferTxn, string) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	cursorAt, cursorTxnNo, hasCursor := service.DecodePageToken(filter.PageToken)
	listParams := dbsqlc.ListTransferTxnsParams{
		MerchantNo:      filter.MerchantNo,
		HasOutUserID:    filter.OutUserID != "",
		OutUserID:       filter.OutUserID,
		HasScene:        filter.Scene != "",
		Scene:           nullableText(filter.Scene),
		HasStatus:       filter.Status != "",
		Status:          filter.Status,
		HasStartTime:    filter.StartTime != nil,
		StartTime:       nullableTimestamp(filter.StartTime, filter.StartTime != nil),
		HasEndTime:      filter.EndTime != nil,
		EndTime:         nullableTimestamp(filter.EndTime, filter.EndTime != nil),
		HasCursor:       hasCursor,
		CursorCreatedAt: nullableTimestamp(&cursorAt, hasCursor),
		PageLimit:       int32(pageSize + 1),
	}
	if hasCursor {
		cursorUUID, err := parseUUID(cursorTxnNo)
		if err != nil {
			return nil, ""
		}
		listParams.CursorTxnNo = cursorUUID
	}

	rows, err := r.queries.ListTransferTxns(ctx, listParams)
	if err != nil {
		return nil, ""
	}

	items := make([]service.TransferTxn, 0, len(rows))
	for _, row := range rows {
		items = append(items, transferTxnFromListRow(row))
	}

	if len(items) <= pageSize {
		return items, ""
	}
	page := items[:pageSize]
	last := page[len(page)-1]
	return page, service.EncodePageToken(last.CreatedAt, last.TxnNo)
}

func (r *Repository) ListTransferTxnsByStatus(status string, limit int) ([]service.TransferTxn, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := r.queries.ListTransferTxnsByStatus(ctx, dbsqlc.ListTransferTxnsByStatusParams{
		Status:    status,
		PageLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}

	items := make([]service.TransferTxn, 0, len(rows))
	for _, row := range rows {
		items = append(items, transferTxnFromListByStatusRow(row))
	}
	return items, nil
}

func (r *Repository) ListStaleTransferTxnNosByStatus(status string, staleBefore time.Time, limit int) ([]string, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := r.pool.Query(ctx, `
SELECT t.txn_no::text
FROM txn t
WHERE t.status = $1
  AND t.updated_at <= $2
ORDER BY t.updated_at ASC, t.txn_no ASC
LIMIT $3
`, status, staleBefore.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]string, 0, limit)
	for rows.Next() {
		var txnNo string
		if err := rows.Scan(&txnNo); err != nil {
			return nil, err
		}
		items = append(items, txnNo)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Repository) UpdateTransferTxnStatus(txnNo, status, errorCode, errorMsg string) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	txnUUID, err := parseUUID(txnNo)
	if err != nil {
		return err
	}
	return r.queries.UpdateTransferTxnStatus(ctx, dbsqlc.UpdateTransferTxnStatusParams{
		Status:    status,
		ErrorCode: nullIfEmpty(errorCode),
		ErrorMsg:  nullIfEmpty(errorMsg),
		TxnNo:     txnUUID,
	})
}

func (r *Repository) TransitionTransferTxnStatus(txnNo, fromStatus, toStatus, errorCode, errorMsg string) (bool, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	txnUUID, err := parseUUID(txnNo)
	if err != nil {
		return false, err
	}

	affected, err := r.queries.UpdateTransferTxnStatusFrom(ctx, dbsqlc.UpdateTransferTxnStatusFromParams{
		NextStatus: toStatus,
		ErrorCode:  nullIfEmpty(errorCode),
		ErrorMsg:   nullIfEmpty(errorMsg),
		TxnNo:      txnUUID,
		FromStatus: fromStatus,
	})
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (r *Repository) UpdateTransferTxnParties(txnNo, debitAccountNo, creditAccountNo string) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	txnUUID, err := parseUUID(txnNo)
	if err != nil {
		return err
	}
	return r.queries.UpdateTransferTxnParties(ctx, dbsqlc.UpdateTransferTxnPartiesParams{
		DebitAccountNo:  textValue(debitAccountNo),
		CreditAccountNo: textValue(creditAccountNo),
		TxnNo:           txnUUID,
	})
}

func (r *Repository) TryDecreaseTxnRefundable(txnNo string, amount int64) (left int64, ok bool, err error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	txnUUID, err := parseUUID(txnNo)
	if err != nil {
		return 0, false, err
	}

	left, err = r.queries.TryDecreaseTxnRefundable(ctx, dbsqlc.TryDecreaseTxnRefundableParams{
		Amount: amount,
		TxnNo:  txnUUID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return left, true, nil
}

func (r *Repository) ApplyTransferDebitStage(txnNo, debitAccountNo string, amount int64) (bool, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	txnUUID, err := parseUUID(txnNo)
	if err != nil {
		return false, service.ErrAccountResolveFailed
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.queries.WithTx(tx)
	stage, err := qtx.GetTransferTxnStageForUpdate(ctx, txnUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, service.ErrTxnNotFound
	}
	if err != nil {
		return false, err
	}
	if stage.Status != service.TxnStatusInit {
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}

	stageDebitAccountNo := stage.DebitAccountNo
	if stageDebitAccountNo == "" {
		stageDebitAccountNo = debitAccountNo
	}
	if stageDebitAccountNo == "" {
		return false, service.ErrAccountResolveFailed
	}
	if debitAccountNo != "" && stage.DebitAccountNo != "" && debitAccountNo != stage.DebitAccountNo {
		return false, service.ErrAccountResolveFailed
	}
	if err := applyAccountDebitTx(ctx, qtx, txnUUID, stageDebitAccountNo, amount); err != nil {
		return false, err
	}
	if err := qtx.UpdateTransferTxnStatus(ctx, dbsqlc.UpdateTransferTxnStatusParams{
		Status:    service.TxnStatusPaySuccess,
		ErrorCode: nil,
		ErrorMsg:  nil,
		TxnNo:     txnUUID,
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) ApplyTransferCreditStage(txnNo, creditAccountNo string, amount int64) (bool, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	txnUUID, err := parseUUID(txnNo)
	if err != nil {
		return false, service.ErrAccountResolveFailed
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.queries.WithTx(tx)
	stage, err := qtx.GetTransferTxnStageForUpdate(ctx, txnUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, service.ErrTxnNotFound
	}
	if err != nil {
		return false, err
	}
	if stage.Status != service.TxnStatusPaySuccess {
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}

	stageCreditAccountNo := stage.CreditAccountNo
	if stageCreditAccountNo == "" {
		stageCreditAccountNo = creditAccountNo
	}
	if stageCreditAccountNo == "" {
		return false, service.ErrAccountResolveFailed
	}
	if creditAccountNo != "" && stage.CreditAccountNo != "" && creditAccountNo != stage.CreditAccountNo {
		return false, service.ErrAccountResolveFailed
	}
	if err := applyAccountCreditTx(ctx, qtx, txnUUID, stageCreditAccountNo, amount, pgDatePtr(stage.CreditExpireAt)); err != nil {
		return false, err
	}
	if err := qtx.UpdateTransferTxnStatus(ctx, dbsqlc.UpdateTransferTxnStatusParams{
		Status:    service.TxnStatusRecvSuccess,
		ErrorCode: nil,
		ErrorMsg:  nil,
		TxnNo:     txnUUID,
	}); err != nil {
		return false, err
	}
	if err := insertOutboxEventTx(ctx, tx, txnUUID, stage.MerchantNo, stage.OutTradeNo); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) ApplyRefundDebitStage(refundTxnNo string, amount int64) (bool, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	refundTxnUUID, err := parseUUID(refundTxnNo)
	if err != nil {
		return false, service.ErrTxnNotFound
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.queries.WithTx(tx)
	refund, err := qtx.GetTransferTxnStageForUpdate(ctx, refundTxnUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, service.ErrTxnNotFound
	}
	if err != nil {
		return false, err
	}
	if refund.Status != service.TxnStatusInit {
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	if refund.BizType != service.BizTypeRefund || strings.TrimSpace(refund.RefundOfTxnNo) == "" {
		return false, service.ErrTxnStatusInvalid
	}

	originTxnUUID, err := parseUUID(refund.RefundOfTxnNo)
	if err != nil {
		return false, service.ErrTxnNotFound
	}
	origin, err := qtx.GetOriginTxnForUpdate(ctx, originTxnUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, service.ErrTxnNotFound
	}
	if err != nil {
		return false, err
	}
	if origin.MerchantNo != refund.MerchantNo {
		return false, service.ErrTxnNotFound
	}
	originTxn, err := qtx.GetTransferTxnByNo(ctx, originTxnUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, service.ErrTxnNotFound
	}
	if err != nil {
		return false, err
	}
	if originTxn.BizType != service.BizTypeTransfer || originTxn.Status != service.TxnStatusRecvSuccess {
		return false, service.ErrTxnStatusInvalid
	}
	if origin.RefundableAmount < amount {
		return false, service.ErrRefundAmountExceeded
	}
	if err := qtx.DecreaseOriginTxnRefundable(ctx, dbsqlc.DecreaseOriginTxnRefundableParams{
		Amount:      amount,
		OriginTxnNo: originTxnUUID,
	}); err != nil {
		return false, err
	}

	refundDebit := origin.CreditAccountNo
	refundCredit := origin.DebitAccountNo
	if err := qtx.UpdateTransferTxnParties(ctx, dbsqlc.UpdateTransferTxnPartiesParams{
		DebitAccountNo:  textValue(refundDebit),
		CreditAccountNo: textValue(refundCredit),
		TxnNo:           refundTxnUUID,
	}); err != nil {
		return false, err
	}
	if err := applyAccountDebitTx(ctx, qtx, refundTxnUUID, refundDebit, amount); err != nil {
		return false, err
	}
	if err := qtx.UpdateTransferTxnStatus(ctx, dbsqlc.UpdateTransferTxnStatusParams{
		Status:    service.TxnStatusPaySuccess,
		ErrorCode: nil,
		ErrorMsg:  nil,
		TxnNo:     refundTxnUUID,
	}); err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) ApplyRefundCreditStage(refundTxnNo, creditAccountNo string, amount int64) (bool, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	refundTxnUUID, err := parseUUID(refundTxnNo)
	if err != nil {
		return false, service.ErrTxnNotFound
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.queries.WithTx(tx)
	refund, err := qtx.GetTransferTxnStageForUpdate(ctx, refundTxnUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, service.ErrTxnNotFound
	}
	if err != nil {
		return false, err
	}
	if refund.Status != service.TxnStatusPaySuccess {
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}

	stageCreditAccountNo := strings.TrimSpace(refund.CreditAccountNo)
	if stageCreditAccountNo == "" {
		stageCreditAccountNo = strings.TrimSpace(creditAccountNo)
	}
	if stageCreditAccountNo == "" {
		return false, service.ErrAccountResolveFailed
	}
	if strings.TrimSpace(creditAccountNo) != "" && strings.TrimSpace(refund.CreditAccountNo) != "" && strings.TrimSpace(creditAccountNo) != strings.TrimSpace(refund.CreditAccountNo) {
		return false, service.ErrAccountResolveFailed
	}

	creditRow, err := qtx.GetAccountForUpdateByNo(ctx, stageCreditAccountNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, service.ErrAccountResolveFailed
	}
	if err != nil {
		return false, err
	}
	creditAccount := accountFromGetAccountForUpdateRow(creditRow)
	if !creditAccount.AllowCreditIn {
		return false, service.ErrAccountForbidCredit
	}

	if creditAccount.BookEnabled {
		parts, err := buildRefundBookCreditParts(ctx, qtx, refundTxnNo, refund.RefundOfTxnNo, refund.MerchantNo, amount)
		if err != nil {
			return false, err
		}
		if err := applyBookCreditPartsTx(ctx, qtx, refundTxnUUID, creditAccount, amount, parts); err != nil {
			return false, err
		}
	} else if err := applyAccountCreditTx(ctx, qtx, refundTxnUUID, stageCreditAccountNo, amount, nil); err != nil {
		return false, err
	}
	if err := qtx.UpdateTransferTxnStatus(ctx, dbsqlc.UpdateTransferTxnStatusParams{
		Status:    service.TxnStatusRecvSuccess,
		ErrorCode: nil,
		ErrorMsg:  nil,
		TxnNo:     refundTxnUUID,
	}); err != nil {
		return false, err
	}
	if err := insertOutboxEventTx(ctx, tx, refundTxnUUID, refund.MerchantNo, refund.OutTradeNo); err != nil {
		return false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) ApplyRefund(refundTxnNo, originTxnNo string, amount int64) (left int64, ok bool, err error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	refundTxnUUID, err := parseUUID(refundTxnNo)
	if err != nil {
		return 0, false, err
	}
	originTxnUUID, err := parseUUID(originTxnNo)
	if err != nil {
		return 0, false, err
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.queries.WithTx(tx)

	origin, err := qtx.GetOriginTxnForUpdate(ctx, originTxnUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, service.ErrTxnNotFound
	}
	if err != nil {
		return 0, false, err
	}
	if origin.RefundableAmount < amount {
		return origin.RefundableAmount, false, nil
	}
	left = origin.RefundableAmount - amount

	if err := qtx.DecreaseOriginTxnRefundable(ctx, dbsqlc.DecreaseOriginTxnRefundableParams{
		Amount:      amount,
		OriginTxnNo: originTxnUUID,
	}); err != nil {
		return 0, false, err
	}

	refundDebit := origin.CreditAccountNo
	refundCredit := origin.DebitAccountNo
	if err := applyAccountTransferTx(ctx, qtx, refundTxnUUID, refundDebit, refundCredit, amount); err != nil {
		return 0, false, err
	}

	if err := qtx.UpdateTransferTxnParties(ctx, dbsqlc.UpdateTransferTxnPartiesParams{
		DebitAccountNo:  textValue(refundDebit),
		CreditAccountNo: textValue(refundCredit),
		TxnNo:           refundTxnUUID,
	}); err != nil {
		return 0, false, err
	}
	if err := qtx.UpdateTransferTxnStatus(ctx, dbsqlc.UpdateTransferTxnStatusParams{
		Status:    service.TxnStatusRecvSuccess,
		ErrorCode: nil,
		ErrorMsg:  nil,
		TxnNo:     refundTxnUUID,
	}); err != nil {
		return 0, false, err
	}
	refundTxnRow, err := qtx.GetTransferTxnByNo(ctx, refundTxnUUID)
	if err != nil {
		return 0, false, err
	}
	if err := insertOutboxEventTx(ctx, tx, refundTxnUUID, refundTxnRow.MerchantNo, refundTxnRow.OutTradeNo); err != nil {
		return 0, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	return left, true, nil
}

func (r *Repository) UpsertWebhookConfig(merchantNo, url string, enabled bool) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	return r.queries.UpsertWebhookConfig(ctx, dbsqlc.UpsertWebhookConfigParams{
		MerchantNo: merchantNo,
		Url:        url,
		Enabled:    enabled,
	})
}

func (r *Repository) GetWebhookConfig(merchantNo string) (service.WebhookConfig, bool, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	row, err := r.queries.GetWebhookConfig(ctx, merchantNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.WebhookConfig{}, false, nil
	}
	if err != nil {
		return service.WebhookConfig{}, false, err
	}
	return service.WebhookConfig{
		URL:     row.Url,
		Enabled: row.Enabled,
	}, true, nil
}

func (r *Repository) ClaimDueOutboxEvents(limit int, now time.Time) ([]service.OutboxEventDelivery, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := r.queries.ClaimDueOutboxEvents(ctx, dbsqlc.ClaimDueOutboxEventsParams{
		NowAt:     pgtype.Timestamptz{Time: now.UTC(), Valid: true},
		PageLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]service.OutboxEventDelivery, 0, len(rows))
	for _, row := range rows {
		items = append(items, service.OutboxEventDelivery{
			EventID:       row.EventID,
			TxnNo:         row.TxnNo,
			MerchantNo:    row.MerchantNo,
			OutTradeNo:    row.OutTradeNo,
			BizType:       row.BizType,
			TransferScene: row.TransferScene,
			Amount:        row.Amount,
			Status:        row.Status,
			RetryCount:    int(row.RetryCount),
		})
	}
	return items, nil
}

func (r *Repository) ClaimDueOutboxEventsByTxnNo(txnNo string, limit int, now time.Time) ([]service.OutboxEventDelivery, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	txnUUID, err := parseUUID(txnNo)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := r.queries.ClaimDueOutboxEventsByTxnNo(ctx, dbsqlc.ClaimDueOutboxEventsByTxnNoParams{
		TxnNo:     txnUUID,
		NowAt:     pgtype.Timestamptz{Time: now.UTC(), Valid: true},
		PageLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]service.OutboxEventDelivery, 0, len(rows))
	for _, row := range rows {
		items = append(items, service.OutboxEventDelivery{
			EventID:       row.EventID,
			TxnNo:         row.TxnNo,
			MerchantNo:    row.MerchantNo,
			OutTradeNo:    row.OutTradeNo,
			BizType:       row.BizType,
			TransferScene: row.TransferScene,
			Amount:        row.Amount,
			Status:        row.Status,
			RetryCount:    int(row.RetryCount),
		})
	}
	return items, nil
}

func (r *Repository) MarkOutboxEventSuccess(eventID string) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	eventUUID, err := parseUUID(eventID)
	if err != nil {
		return err
	}
	return r.queries.MarkOutboxEventSuccess(ctx, eventUUID)
}

func (r *Repository) MarkOutboxEventRetry(eventID string, retryCount int, nextRetryAt time.Time, dead bool) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	eventUUID, err := parseUUID(eventID)
	if err != nil {
		return err
	}
	return r.queries.MarkOutboxEventRetry(ctx, dbsqlc.MarkOutboxEventRetryParams{
		RetryCount:  int32(retryCount),
		NextRetryAt: pgtype.Timestamptz{Time: nextRetryAt.UTC(), Valid: true},
		MarkDead:    dead,
		EventID:     eventUUID,
	})
}

func (r *Repository) InsertNotifyLog(txnNo, status string, retries int) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	txnUUID, err := parseUUID(txnNo)
	if err != nil {
		return err
	}
	return r.queries.InsertNotifyLog(ctx, dbsqlc.InsertNotifyLogParams{
		TxnNo:   txnUUID,
		Status:  status,
		Retries: int32(retries),
	})
}

func (r *Repository) TxnCount() int {
	ctx, cancel := r.withTimeout()
	defer cancel()

	n, err := r.queries.TxnCount(ctx)
	if err != nil {
		return 0
	}
	return int(n)
}

func (r *Repository) IncTxnCompensationRun() {
	atomic.AddInt64(&r.txnCompRuns, 1)
}

func (r *Repository) IncNotifyCompensationRun() {
	atomic.AddInt64(&r.notifyCompRuns, 1)
}

type bookCreditPart struct {
	ExpireAt time.Time
	Amount   int64
}

func buildRefundBookCreditParts(
	ctx context.Context,
	q *dbsqlc.Queries,
	refundTxnNo, originTxnNo, merchantNo string,
	refundAmount int64,
) ([]bookCreditPart, error) {
	if strings.TrimSpace(refundTxnNo) == "" || strings.TrimSpace(originTxnNo) == "" || refundAmount <= 0 {
		return nil, service.ErrAccountResolveFailed
	}

	refundTxnUUID, err := parseUUID(refundTxnNo)
	if err != nil {
		return nil, service.ErrTxnNotFound
	}
	originTxnUUID, err := parseUUID(originTxnNo)
	if err != nil {
		return nil, service.ErrTxnNotFound
	}
	originTxn, err := q.GetTransferTxnByNo(ctx, originTxnUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, service.ErrTxnNotFound
	}
	if err != nil {
		return nil, err
	}
	if originTxn.MerchantNo != merchantNo {
		return nil, service.ErrTxnNotFound
	}

	originDebitAccountNo := strings.TrimSpace(originTxn.DebitAccountNo)
	originCreditAccountNo := strings.TrimSpace(originTxn.CreditAccountNo)
	if originDebitAccountNo == "" || originCreditAccountNo == "" {
		return nil, service.ErrAccountResolveFailed
	}

	originBookDebits, err := q.ListOriginDebitBookChanges(ctx, dbsqlc.ListOriginDebitBookChangesParams{
		TxnNo:     originTxnUUID,
		AccountNo: originDebitAccountNo,
	})
	if err != nil {
		return nil, err
	}
	if len(originBookDebits) == 0 {
		return nil, service.ErrRefundOriginBookTraceMissing
	}

	refundStats, err := q.GetRefundDebitStatsByOrigin(ctx, dbsqlc.GetRefundDebitStatsByOriginParams{
		RefundTxnNo:           refundTxnUUID,
		MerchantNo:            merchantNo,
		OriginTxnNo:           originTxnUUID,
		OriginCreditAccountNo: originCreditAccountNo,
	})
	if err != nil {
		return nil, err
	}
	if refundStats.CurrentDebited <= 0 {
		return nil, service.ErrRefundOriginBookTraceMissing
	}
	priorDebited := refundStats.TotalDebited - refundStats.CurrentDebited
	if priorDebited < 0 {
		return nil, service.ErrRefundOriginBookTraceMissing
	}

	refundBaseDate := normalizeUTCDate(time.Now().UTC())
	leftOffset := priorDebited
	leftRefund := refundAmount
	parts := make([]bookCreditPart, 0, len(originBookDebits))

	for _, debit := range originBookDebits {
		consumed := -debit.Delta
		if consumed <= 0 {
			continue
		}

		alreadyRefunded := minInt64(leftOffset, consumed)
		leftOffset -= alreadyRefunded

		available := consumed - alreadyRefunded
		if available <= 0 {
			continue
		}

		use := minInt64(available, leftRefund)
		if use <= 0 {
			continue
		}

		remainingDays := diffDaysUTCDate(pgDateToTime(debit.ExpireAt), pgTimestampToTime(debit.CreatedAt))
		if remainingDays <= 0 {
			remainingDays = 1
		}
		expireAt := refundBaseDate.Add(time.Duration(remainingDays) * 24 * time.Hour)

		if n := len(parts); n > 0 && parts[n-1].ExpireAt.Equal(expireAt) {
			parts[n-1].Amount += use
		} else {
			parts = append(parts, bookCreditPart{
				ExpireAt: expireAt,
				Amount:   use,
			})
		}

		leftRefund -= use
		if leftRefund == 0 {
			break
		}
	}

	if leftRefund != 0 {
		return nil, service.ErrRefundOriginBookTraceMissing
	}
	return parts, nil
}

func applyBookCreditPartsTx(
	ctx context.Context,
	q *dbsqlc.Queries,
	txnUUID pgtype.UUID,
	credit service.Account,
	amount int64,
	parts []bookCreditPart,
) error {
	if amount <= 0 || strings.TrimSpace(credit.AccountNo) == "" {
		return service.ErrAccountResolveFailed
	}
	if !credit.BookEnabled || len(parts) == 0 {
		return service.ErrRefundOriginBookTraceMissing
	}

	type appliedBookPart struct {
		Book      dbsqlc.UpsertAccountBookBalanceRow
		Delta     int64
		BalanceAt pgtype.Date
	}

	applied := make([]appliedBookPart, 0, len(parts))
	sum := int64(0)

	for _, part := range parts {
		if part.Amount <= 0 || part.ExpireAt.IsZero() {
			return service.ErrRefundOriginBookTraceMissing
		}
		sum += part.Amount

		bookNo, err := idpkg.NewRuntimeUUIDProvider().NewUUIDv7()
		if err != nil {
			return err
		}
		bookUUID, err := parseUUID(bookNo)
		if err != nil {
			return err
		}

		book, err := q.UpsertAccountBookBalance(ctx, dbsqlc.UpsertAccountBookBalanceParams{
			BookNo:    bookUUID,
			AccountNo: credit.AccountNo,
			ExpireAt:  toPGDate(part.ExpireAt),
			Delta:     part.Amount,
		})
		if err != nil {
			return err
		}
		applied = append(applied, appliedBookPart{
			Book:      book,
			Delta:     part.Amount,
			BalanceAt: book.ExpireAt,
		})
	}

	if sum != amount {
		return service.ErrRefundOriginBookTraceMissing
	}

	creditAfter := credit.Balance + amount
	if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
		Balance:   creditAfter,
		AccountNo: credit.AccountNo,
	}); err != nil {
		return err
	}
	if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
		TxnNo:        txnUUID,
		AccountNo:    credit.AccountNo,
		Delta:        amount,
		BalanceAfter: creditAfter,
	}); err != nil {
		return err
	}

	for _, item := range applied {
		if err := q.InsertAccountBookChange(ctx, dbsqlc.InsertAccountBookChangeParams{
			TxnNo:        txnUUID,
			AccountNo:    credit.AccountNo,
			BookNo:       item.Book.BookNo,
			Delta:        item.Delta,
			BalanceAfter: item.Book.Balance,
			ExpireAt:     item.BalanceAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func normalizeUTCDate(ts time.Time) time.Time {
	u := ts.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

func diffDaysUTCDate(later, earlier time.Time) int64 {
	return int64(normalizeUTCDate(later).Sub(normalizeUTCDate(earlier)) / (24 * time.Hour))
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func applyAccountTransferTx(ctx context.Context, q *dbsqlc.Queries, txnUUID pgtype.UUID, debitAccountNo, creditAccountNo string, amount int64) error {
	if debitAccountNo == "" || creditAccountNo == "" || amount <= 0 {
		return service.ErrAccountResolveFailed
	}

	if err := applyAccountDebitTx(ctx, q, txnUUID, debitAccountNo, amount); err != nil {
		return err
	}
	if err := applyAccountCreditTx(ctx, q, txnUUID, creditAccountNo, amount, nil); err != nil {
		return err
	}
	return nil
}

func applyAccountDebitTx(ctx context.Context, q *dbsqlc.Queries, txnUUID pgtype.UUID, debitAccountNo string, amount int64) error {
	if debitAccountNo == "" || amount <= 0 {
		return service.ErrAccountResolveFailed
	}

	debitRow, err := q.GetAccountForUpdateByNo(ctx, debitAccountNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.ErrAccountResolveFailed
	}
	if err != nil {
		return err
	}

	debit := accountFromGetAccountForUpdateRow(debitRow)
	if !debit.AllowDebitOut {
		return service.ErrAccountForbidDebit
	}

	if debit.BookEnabled {
		books, err := q.ListAvailableAccountBooksForUpdate(ctx, dbsqlc.ListAvailableAccountBooksForUpdateParams{
			AccountNo: debit.AccountNo,
			NowUtc:    toPGDate(time.Now().UTC()),
		})
		if err != nil {
			return err
		}

		type bookChange struct {
			BookNo       pgtype.UUID
			ExpireAt     pgtype.Date
			Delta        int64
			BalanceAfter int64
		}

		left := amount
		changes := make([]bookChange, 0, len(books))
		for _, book := range books {
			if left == 0 {
				break
			}
			if book.Balance <= 0 {
				continue
			}
			use := book.Balance
			if use > left {
				use = left
			}
			after := book.Balance - use

			if err := q.UpdateAccountBookBalance(ctx, dbsqlc.UpdateAccountBookBalanceParams{
				BookNo:  book.BookNo,
				Balance: after,
			}); err != nil {
				return err
			}
			changes = append(changes, bookChange{
				BookNo:       book.BookNo,
				ExpireAt:     book.ExpireAt,
				Delta:        -use,
				BalanceAfter: after,
			})
			left -= use
		}
		if left > 0 {
			return service.ErrInsufficientBalance
		}

		debitAfter := debit.Balance - amount
		if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
			Balance:   debitAfter,
			AccountNo: debit.AccountNo,
		}); err != nil {
			return err
		}
		if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
			TxnNo:        txnUUID,
			AccountNo:    debit.AccountNo,
			Delta:        -amount,
			BalanceAfter: debitAfter,
		}); err != nil {
			return err
		}
		for _, change := range changes {
			if err := q.InsertAccountBookChange(ctx, dbsqlc.InsertAccountBookChangeParams{
				TxnNo:        txnUUID,
				AccountNo:    debit.AccountNo,
				BookNo:       change.BookNo,
				Delta:        change.Delta,
				BalanceAfter: change.BalanceAfter,
				ExpireAt:     change.ExpireAt,
			}); err != nil {
				return err
			}
		}
		return nil
	}

	if err := domain.Account(debit).CanDebit(amount); err != nil {
		return err
	}

	debitAfter := debit.Balance - amount
	if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
		Balance:   debitAfter,
		AccountNo: debit.AccountNo,
	}); err != nil {
		return err
	}
	if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
		TxnNo:        txnUUID,
		AccountNo:    debit.AccountNo,
		Delta:        -amount,
		BalanceAfter: debitAfter,
	}); err != nil {
		return err
	}
	return nil
}

func applyAccountCreditTx(ctx context.Context, q *dbsqlc.Queries, txnUUID pgtype.UUID, creditAccountNo string, amount int64, creditExpireAt *time.Time) error {
	if creditAccountNo == "" || amount <= 0 {
		return service.ErrAccountResolveFailed
	}

	creditRow, err := q.GetAccountForUpdateByNo(ctx, creditAccountNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.ErrAccountResolveFailed
	}
	if err != nil {
		return err
	}

	credit := accountFromGetAccountForUpdateRow(creditRow)
	if !credit.AllowCreditIn {
		return service.ErrAccountForbidCredit
	}

	if credit.BookEnabled {
		if creditExpireAt == nil || creditExpireAt.IsZero() {
			return service.ErrExpireAtRequired
		}
		expireAt := creditExpireAt.UTC()
		bookNo, err := idpkg.NewRuntimeUUIDProvider().NewUUIDv7()
		if err != nil {
			return err
		}
		bookUUID, err := parseUUID(bookNo)
		if err != nil {
			return err
		}

		book, err := q.UpsertAccountBookBalance(ctx, dbsqlc.UpsertAccountBookBalanceParams{
			BookNo:    bookUUID,
			AccountNo: credit.AccountNo,
			ExpireAt:  toPGDate(expireAt),
			Delta:     amount,
		})
		if err != nil {
			return err
		}

		creditAfter := credit.Balance + amount
		if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
			Balance:   creditAfter,
			AccountNo: credit.AccountNo,
		}); err != nil {
			return err
		}
		if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
			TxnNo:        txnUUID,
			AccountNo:    credit.AccountNo,
			Delta:        amount,
			BalanceAfter: creditAfter,
		}); err != nil {
			return err
		}
		if err := q.InsertAccountBookChange(ctx, dbsqlc.InsertAccountBookChangeParams{
			TxnNo:        txnUUID,
			AccountNo:    credit.AccountNo,
			BookNo:       book.BookNo,
			Delta:        amount,
			BalanceAfter: book.Balance,
			ExpireAt:     book.ExpireAt,
		}); err != nil {
			return err
		}
		return nil
	}

	creditAfter := credit.Balance + amount
	if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
		Balance:   creditAfter,
		AccountNo: credit.AccountNo,
	}); err != nil {
		return err
	}
	if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
		TxnNo:        txnUUID,
		AccountNo:    credit.AccountNo,
		Delta:        amount,
		BalanceAfter: creditAfter,
	}); err != nil {
		return err
	}
	return nil
}

func accountFromGetAccountRow(row dbsqlc.GetAccountByNoRow) service.Account {
	return service.Account{
		AccountNo:         row.AccountNo,
		MerchantNo:        row.MerchantNo,
		CustomerNo:        row.CustomerNo,
		AccountType:       row.AccountType,
		AllowOverdraft:    row.AllowOverdraft,
		MaxOverdraftLimit: row.MaxOverdraftLimit,
		AllowDebitOut:     row.AllowDebitOut,
		AllowCreditIn:     row.AllowCreditIn,
		AllowTransfer:     row.AllowTransfer,
		BookEnabled:       row.BookEnabled,
		Balance:           row.Balance,
	}
}

func accountFromGetAccountForUpdateRow(row dbsqlc.GetAccountForUpdateByNoRow) service.Account {
	return service.Account{
		AccountNo:         row.AccountNo,
		MerchantNo:        row.MerchantNo,
		CustomerNo:        row.CustomerNo,
		AccountType:       row.AccountType,
		AllowOverdraft:    row.AllowOverdraft,
		MaxOverdraftLimit: row.MaxOverdraftLimit,
		AllowDebitOut:     row.AllowDebitOut,
		AllowCreditIn:     row.AllowCreditIn,
		AllowTransfer:     row.AllowTransfer,
		BookEnabled:       row.BookEnabled,
		Balance:           row.Balance,
	}
}

func transferTxnFromByNoRow(row dbsqlc.GetTransferTxnByNoRow) service.TransferTxn {
	return service.TransferTxn{
		TxnNo:            row.TxnNo,
		MerchantNo:       row.MerchantNo,
		OutTradeNo:       row.OutTradeNo,
		BizType:          row.BizType,
		TransferScene:    row.TransferScene,
		DebitAccountNo:   row.DebitAccountNo,
		CreditAccountNo:  row.CreditAccountNo,
		CreditExpireAt:   pgDateToTime(row.CreditExpireAt),
		Amount:           row.Amount,
		RefundOfTxnNo:    row.RefundOfTxnNo,
		RefundableAmount: row.RefundableAmount,
		Status:           row.Status,
		ErrorCode:        row.ErrorCode,
		ErrorMsg:         row.ErrorMsg,
		CreatedAt:        pgTimestampToTime(row.CreatedAt),
	}
}
func transferTxnFromByOutTradeRow(row dbsqlc.GetTransferTxnByOutTradeNoRow) service.TransferTxn {
	return service.TransferTxn{
		TxnNo:            row.TxnNo,
		MerchantNo:       row.MerchantNo,
		OutTradeNo:       row.OutTradeNo,
		BizType:          row.BizType,
		TransferScene:    row.TransferScene,
		DebitAccountNo:   row.DebitAccountNo,
		CreditAccountNo:  row.CreditAccountNo,
		CreditExpireAt:   pgDateToTime(row.CreditExpireAt),
		Amount:           row.Amount,
		RefundOfTxnNo:    row.RefundOfTxnNo,
		RefundableAmount: row.RefundableAmount,
		Status:           row.Status,
		ErrorCode:        row.ErrorCode,
		ErrorMsg:         row.ErrorMsg,
		CreatedAt:        pgTimestampToTime(row.CreatedAt),
	}
}
func transferTxnFromListRow(row dbsqlc.ListTransferTxnsRow) service.TransferTxn {
	return service.TransferTxn{
		TxnNo:            row.TxnNo,
		MerchantNo:       row.MerchantNo,
		OutTradeNo:       row.OutTradeNo,
		BizType:          row.BizType,
		TransferScene:    row.TransferScene,
		DebitAccountNo:   row.DebitAccountNo,
		CreditAccountNo:  row.CreditAccountNo,
		CreditExpireAt:   pgDateToTime(row.CreditExpireAt),
		Amount:           row.Amount,
		RefundOfTxnNo:    row.RefundOfTxnNo,
		RefundableAmount: row.RefundableAmount,
		Status:           row.Status,
		ErrorCode:        row.ErrorCode,
		ErrorMsg:         row.ErrorMsg,
		CreatedAt:        pgTimestampToTime(row.CreatedAt),
	}
}
func transferTxnFromListByStatusRow(row dbsqlc.ListTransferTxnsByStatusRow) service.TransferTxn {
	return service.TransferTxn{
		TxnNo:            row.TxnNo,
		MerchantNo:       row.MerchantNo,
		OutTradeNo:       row.OutTradeNo,
		BizType:          row.BizType,
		TransferScene:    row.TransferScene,
		DebitAccountNo:   row.DebitAccountNo,
		CreditAccountNo:  row.CreditAccountNo,
		CreditExpireAt:   pgDateToTime(row.CreditExpireAt),
		Amount:           row.Amount,
		RefundOfTxnNo:    row.RefundOfTxnNo,
		RefundableAmount: row.RefundableAmount,
		Status:           row.Status,
		ErrorCode:        row.ErrorCode,
		ErrorMsg:         row.ErrorMsg,
		CreatedAt:        pgTimestampToTime(row.CreatedAt),
	}
}

func parseUUID(v string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(strings.TrimSpace(v)); err != nil {
		return pgtype.UUID{}, err
	}
	return u, nil
}

func mustPGUUID(v uuid.UUID) pgtype.UUID {
	var out pgtype.UUID
	copy(out.Bytes[:], v[:])
	out.Valid = true
	return out
}

func parseOptionalUUID(v string) (pgtype.UUID, error) {
	if strings.TrimSpace(v) == "" {
		return pgtype.UUID{}, nil
	}
	return parseUUID(v)
}

func nullableText(v string) pgtype.Text {
	if v == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: v, Valid: true}
}

func textValue(v string) pgtype.Text {
	return pgtype.Text{String: v, Valid: true}
}

func nullableTimestamp(t *time.Time, valid bool) pgtype.Timestamptz {
	if !valid || t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func nullableDate(t *time.Time, valid bool) pgtype.Date {
	if !valid || t == nil {
		return pgtype.Date{}
	}
	return toPGDate(t.UTC())
}

func toPGDate(t time.Time) pgtype.Date {
	u := t.UTC()
	d := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
	return pgtype.Date{Time: d, Valid: true}
}

func pgDatePtr(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	v := d.Time.UTC()
	return &v
}

func pgTimestampPtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	v := ts.Time.UTC()
	return &v
}

func pgDateToTime(d pgtype.Date) time.Time {
	if !d.Valid {
		return time.Time{}
	}
	return d.Time.UTC()
}

func pgTimestampToTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

func insertOutboxEventTx(ctx context.Context, tx pgx.Tx, txnUUID pgtype.UUID, merchantNo, outTradeNo string) error {
	qtx := dbsqlc.New(tx)
	return qtx.InsertOutboxEvent(ctx, dbsqlc.InsertOutboxEventParams{
		EventID:    deterministicOutboxEventID(txnUUID, "TxnSucceeded"),
		TxnNo:      txnUUID,
		MerchantNo: merchantNo,
		OutTradeNo: nullIfEmpty(outTradeNo),
		Status:     "PENDING",
	})
}

func deterministicOutboxEventID(txnUUID pgtype.UUID, eventType string) pgtype.UUID {
	seed := make([]byte, 0, len(txnUUID.Bytes)+1+len(eventType))
	seed = append(seed, txnUUID.Bytes[:]...)
	seed = append(seed, ':')
	seed = append(seed, eventType...)
	return mustPGUUID(uuid.NewSHA1(uuid.NameSpaceOID, seed))
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

func nullIfEmpty(v string) any {
	if v == "" {
		return nil
	}
	return v
}
