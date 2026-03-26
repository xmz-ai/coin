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
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

const opTimeout = 3 * time.Second

var noExpireBookDate = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

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

	return r.createMerchant(ctx, r.queries, m)
}

func (r *Repository) CreateMerchantWithAccounts(m service.Merchant, accounts ...service.Account) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.queries.WithTx(tx)
	if err := r.createMerchant(ctx, qtx, m); err != nil {
		return err
	}
	for _, account := range accounts {
		if err := r.createAccount(ctx, qtx, account); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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
		WriteoffAccountNo:   row.WriteoffAccountNo,
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

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := r.queries.WithTx(tx)

	if err := r.createAccount(ctx, qtx, a); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) createMerchant(ctx context.Context, q *dbsqlc.Queries, m service.Merchant) error {
	merchantID, err := parseUUID(m.MerchantID)
	if err != nil {
		return err
	}

	err = q.CreateMerchant(ctx, dbsqlc.CreateMerchantParams{
		MerchantID:          merchantID,
		MerchantNo:          m.MerchantNo,
		Name:                m.Name,
		BudgetAccountNo:     m.BudgetAccountNo,
		ReceivableAccountNo: m.ReceivableAccountNo,
		WriteoffAccountNo:   nullableText(strings.TrimSpace(m.WriteoffAccountNo)),
	})
	if isUniqueViolation(err) {
		return service.ErrMerchantNoExists
	}
	return err
}

func (r *Repository) createAccount(ctx context.Context, q *dbsqlc.Queries, a service.Account) error {
	customerNo := strings.TrimSpace(a.CustomerNo)
	err := q.CreateAccount(ctx, dbsqlc.CreateAccountParams{
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
	})
	if isUniqueViolation(err) {
		return service.ErrAccountNoExists
	}
	if err != nil {
		return err
	}
	if a.BookEnabled {
		noExpireBookNo, err := idpkg.NewRuntimeUUIDProvider().NewUUIDv7()
		if err != nil {
			return err
		}
		if _, err := q.UpsertAccountBookBalance(ctx, dbsqlc.UpsertAccountBookBalanceParams{
			BookNo:    noExpireBookNo,
			AccountNo: a.AccountNo,
			ExpireAt:  toPGDate(noExpireBookDate),
			Delta:     0,
		}); err != nil {
			return err
		}
	}
	if customerNo == "" {
		return nil
	}
	_, err = q.SetCustomerDefaultAccountIfEmpty(ctx, dbsqlc.SetCustomerDefaultAccountIfEmptyParams{
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

func (r *Repository) GetAvailableBalance(accountNo string) (int64, bool, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	row, err := r.queries.GetAccountByNo(ctx, accountNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !row.BookEnabled {
		return row.Balance, true, nil
	}
	sum, err := r.queries.GetAvailableAccountBookBalanceSum(ctx, dbsqlc.GetAvailableAccountBookBalanceSumParams{
		AccountNo:  row.AccountNo,
		NowUtc:     toPGDate(time.Now().UTC()),
		NoExpireAt: toPGDate(noExpireBookDate),
	})
	if err != nil {
		return 0, false, err
	}
	return sum, true, nil
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

func (r *Repository) GetAccountByOutUserID(merchantNo, outUserID string) (service.Account, bool) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	row, err := r.queries.GetAccountByMerchantOutUserID(ctx, dbsqlc.GetAccountByMerchantOutUserIDParams{
		MerchantNo: merchantNo,
		OutUserID:  outUserID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Account{}, false
	}
	if err != nil {
		return service.Account{}, false
	}
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
	}, true
}

func (r *Repository) CreateTransferTxn(txn service.TransferTxn) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	err := r.queries.CreateTransferTxn(ctx, dbsqlc.CreateTransferTxnParams{
		TxnNo:            txn.TxnNo,
		MerchantNo:       txn.MerchantNo,
		OutTradeNo:       txn.OutTradeNo,
		Title:            nullIfEmpty(txn.Title),
		Remark:           nullIfEmpty(txn.Remark),
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

	row, err := r.queries.GetTransferTxnByNo(ctx, txnNo)
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

func (r *Repository) ListAccountChangeLogs(filter service.AccountChangeLogListFilter) ([]service.AccountChangeLog, string) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	cursorAt, cursorChangeID, hasCursor := service.DecodeChangeLogPageToken(filter.PageToken)
	rows, err := r.queries.ListAccountChangeLogs(ctx, dbsqlc.ListAccountChangeLogsParams{
		MerchantNo:      filter.MerchantNo,
		AccountNo:       filter.AccountNo,
		HasCursor:       hasCursor,
		CursorCreatedAt: nullableTimestamp(&cursorAt, hasCursor),
		CursorChangeID:  cursorChangeID,
		PageLimit:       int32(pageSize + 1),
	})
	if err != nil {
		return nil, ""
	}

	items := make([]service.AccountChangeLog, 0, len(rows))
	for _, row := range rows {
		items = append(items, service.AccountChangeLog{
			ChangeID:      row.ChangeID,
			TxnNo:         row.TxnNo,
			AccountNo:     row.AccountNo,
			Delta:         row.Delta,
			BalanceBefore: row.BalanceBefore,
			BalanceAfter:  row.BalanceAfter,
			Title:         row.Title,
			Remark:        row.Remark,
			CreatedAt:     pgTimestampToTime(row.CreatedAt),
		})
	}

	if len(items) <= pageSize {
		return items, ""
	}
	page := items[:pageSize]
	last := page[len(page)-1]
	return page, service.EncodeChangeLogPageToken(last.CreatedAt, last.ChangeID)
}

func (r *Repository) ListActiveAccountBooks(accountNo string, now time.Time) ([]service.AccountBook, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	rows, err := r.queries.ListActiveAccountBooks(ctx, dbsqlc.ListActiveAccountBooksParams{
		AccountNo: accountNo,
		NowUtc:    toPGDate(now),
	})
	if err != nil {
		return nil, err
	}

	items := make([]service.AccountBook, 0, len(rows))
	for _, row := range rows {
		expireAt := time.Time{}
		if row.ExpireAt.Valid {
			expireAt = normalizeUTCDate(row.ExpireAt.Time)
		}
		items = append(items, service.AccountBook{
			BookNo:    row.BookNo,
			AccountNo: row.AccountNo,
			ExpireAt:  expireAt,
			Balance:   row.Balance,
		})
	}
	return items, nil
}

func (r *Repository) ListBookCreditChangeLogs(bookNo string) ([]service.BookCreditChangeLog, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	rows, err := r.queries.ListBookCreditChangeLogs(ctx, bookNo)
	if err != nil {
		return nil, err
	}

	items := make([]service.BookCreditChangeLog, 0, len(rows))
	for _, row := range rows {
		items = append(items, service.BookCreditChangeLog{
			ChangeID:  row.ChangeID,
			TxnNo:     row.TxnNo,
			Delta:     row.Delta,
			CreatedAt: pgTimestampToTime(row.CreatedAt),
			Title:     row.Title,
		})
	}
	return items, nil
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

	return r.queries.UpdateTransferTxnStatus(ctx, dbsqlc.UpdateTransferTxnStatusParams{
		Status:    status,
		ErrorCode: nullIfEmpty(errorCode),
		ErrorMsg:  nullIfEmpty(errorMsg),
		TxnNo:     txnNo,
	})
}

func (r *Repository) TransitionTransferTxnStatus(txnNo, fromStatus, toStatus, errorCode, errorMsg string) (bool, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	affected, err := r.queries.UpdateTransferTxnStatusFrom(ctx, dbsqlc.UpdateTransferTxnStatusFromParams{
		NextStatus: toStatus,
		ErrorCode:  nullIfEmpty(errorCode),
		ErrorMsg:   nullIfEmpty(errorMsg),
		TxnNo:      txnNo,
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

	return r.queries.UpdateTransferTxnParties(ctx, dbsqlc.UpdateTransferTxnPartiesParams{
		DebitAccountNo:  textValue(debitAccountNo),
		CreditAccountNo: textValue(creditAccountNo),
		TxnNo:           txnNo,
	})
}

func (r *Repository) TryDecreaseTxnRefundable(txnNo string, amount int64) (left int64, ok bool, err error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	left, err = r.queries.TryDecreaseTxnRefundable(ctx, dbsqlc.TryDecreaseTxnRefundableParams{
		Amount: amount,
		TxnNo:  txnNo,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return left, true, nil
}

func (r *Repository) ApplyTxnStage(txnNo, expectedStatus string) (service.TxnStageApplyResult, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	var result service.TxnStageApplyResult

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.queries.WithTx(tx)
	stage, err := qtx.GetTransferTxnStageForUpdate(ctx, txnNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, service.ErrTxnNotFound
	}
	if err != nil {
		return result, err
	}
	txnUUID := stage.TxnNo

	result.BizType = strings.TrimSpace(stage.BizType)
	result.CurrentStatus = strings.TrimSpace(stage.Status)
	result.DebitAccountNo = strings.TrimSpace(stage.DebitAccountNo)
	result.CreditAccountNo = strings.TrimSpace(stage.CreditAccountNo)
	if result.BizType != service.BizTypeTransfer && result.BizType != service.BizTypeRefund {
		if err := tx.Commit(ctx); err != nil {
			return result, err
		}
		return result, nil
	}
	if result.CurrentStatus != expectedStatus {
		if err := tx.Commit(ctx); err != nil {
			return result, err
		}
		return result, nil
	}

	switch expectedStatus {
	case service.TxnStatusInit:
		switch result.BizType {
		case service.BizTypeTransfer:
			if result.DebitAccountNo == "" {
				return result, service.ErrAccountResolveFailed
			}
			if err := applyAccountDebitTx(ctx, r, qtx, txnUUID, result.DebitAccountNo, stage.Amount); err != nil {
				return result, err
			}
		case service.BizTypeRefund:
			if !stage.RefundOfTxnNo.Valid {
				return result, service.ErrTxnStatusInvalid
			}
			originTxnUUID := stage.RefundOfTxnNo
			_, err = qtx.DecreaseOriginTxnRefundableIfValid(ctx, dbsqlc.DecreaseOriginTxnRefundableIfValidParams{
				Amount:      stage.Amount,
				OriginTxnNo: originTxnUUID,
				MerchantNo:  stage.MerchantNo,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				classifiedErr := classifyOriginRefundDecreaseMiss(ctx, qtx, originTxnUUID, stage.MerchantNo, stage.Amount)
				if classifiedErr != nil {
					return result, classifiedErr
				}
				return result, service.ErrRefundAmountExceeded
			}
			if err != nil {
				return result, err
			}

			refundDebit := strings.TrimSpace(result.DebitAccountNo)
			refundCredit := strings.TrimSpace(result.CreditAccountNo)
			if refundDebit == "" || refundCredit == "" {
				return result, service.ErrAccountResolveFailed
			}
			if err := applyAccountDebitTx(ctx, r, qtx, txnUUID, refundDebit, stage.Amount); err != nil {
				return result, err
			}
		default:
			return result, service.ErrTxnStatusInvalid
		}

		if err := qtx.UpdateTransferTxnStatus(ctx, dbsqlc.UpdateTransferTxnStatusParams{
			Status:    service.TxnStatusPaySuccess,
			ErrorCode: nil,
			ErrorMsg:  nil,
			TxnNo:     txnUUID,
		}); err != nil {
			return result, err
		}
		result.Applied = true
		result.CurrentStatus = service.TxnStatusPaySuccess

	case service.TxnStatusPaySuccess:
		switch result.BizType {
		case service.BizTypeTransfer:
			if result.CreditAccountNo == "" {
				return result, service.ErrAccountResolveFailed
			}
			if err := applyTransferCreditTx(
				ctx,
				qtx,
				txnUUID,
				result.DebitAccountNo,
				result.CreditAccountNo,
				stage.TransferScene,
				stage.Amount,
				pgDatePtr(stage.CreditExpireAt),
			); err != nil {
				return result, err
			}
		case service.BizTypeRefund:
			if result.CreditAccountNo == "" {
				return result, service.ErrAccountResolveFailed
			}
			if !stage.RefundOfTxnNo.Valid {
				return result, service.ErrTxnStatusInvalid
			}
			if err := applyRefundCreditTx(
				ctx,
				qtx,
				txnUUID,
				result.CreditAccountNo,
				stage.Amount,
				stage.RefundOfTxnNo,
				stage.MerchantNo,
			); err != nil {
				return result, err
			}
		default:
			return result, service.ErrTxnStatusInvalid
		}

		if err := qtx.UpdateTransferTxnStatus(ctx, dbsqlc.UpdateTransferTxnStatusParams{
			Status:    service.TxnStatusRecvSuccess,
			ErrorCode: nil,
			ErrorMsg:  nil,
			TxnNo:     txnUUID,
		}); err != nil {
			return result, err
		}
		if err := insertOutboxEventTx(ctx, tx, txnUUID, stage.MerchantNo, stage.OutTradeNo); err != nil {
			return result, err
		}
		result.Applied = true
		result.CurrentStatus = service.TxnStatusRecvSuccess

	default:
		return result, service.ErrTxnStatusInvalid
	}

	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func (r *Repository) ApplyTransferDebitStage(txnNo, debitAccountNo string, amount int64) (bool, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.queries.WithTx(tx)
	stage, err := qtx.GetTransferTxnStageForUpdate(ctx, txnNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, service.ErrTxnNotFound
	}
	if err != nil {
		return false, err
	}
	txnUUID := stage.TxnNo
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
	if err := applyAccountDebitTx(ctx, r, qtx, txnUUID, stageDebitAccountNo, amount); err != nil {
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

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.queries.WithTx(tx)
	stage, err := qtx.GetTransferTxnStageForUpdate(ctx, txnNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, service.ErrTxnNotFound
	}
	if err != nil {
		return false, err
	}
	txnUUID := stage.TxnNo
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
	if err := applyTransferCreditTx(
		ctx,
		qtx,
		txnUUID,
		stage.DebitAccountNo,
		stageCreditAccountNo,
		stage.TransferScene,
		amount,
		pgDatePtr(stage.CreditExpireAt),
	); err != nil {
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

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.queries.WithTx(tx)
	refund, err := qtx.GetTransferTxnStageForUpdate(ctx, refundTxnNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, service.ErrTxnNotFound
	}
	if err != nil {
		return false, err
	}
	refundTxnUUID := refund.TxnNo
	if refund.Status != service.TxnStatusInit {
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	if refund.BizType != service.BizTypeRefund || !refund.RefundOfTxnNo.Valid {
		return false, service.ErrTxnStatusInvalid
	}

	originTxnUUID := refund.RefundOfTxnNo
	_, err = qtx.DecreaseOriginTxnRefundableIfValid(ctx, dbsqlc.DecreaseOriginTxnRefundableIfValidParams{
		Amount:      amount,
		OriginTxnNo: originTxnUUID,
		MerchantNo:  refund.MerchantNo,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		classifiedErr := classifyOriginRefundDecreaseMiss(ctx, qtx, originTxnUUID, refund.MerchantNo, amount)
		if classifiedErr != nil {
			return false, classifiedErr
		}
		return false, service.ErrRefundAmountExceeded
	}
	if err != nil {
		return false, err
	}

	refundDebit := strings.TrimSpace(refund.DebitAccountNo)
	refundCredit := strings.TrimSpace(refund.CreditAccountNo)
	if refundDebit == "" || refundCredit == "" {
		return false, service.ErrAccountResolveFailed
	}
	if err := applyAccountDebitTx(ctx, r, qtx, refundTxnUUID, refundDebit, amount); err != nil {
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

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := r.queries.WithTx(tx)
	refund, err := qtx.GetTransferTxnStageForUpdate(ctx, refundTxnNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, service.ErrTxnNotFound
	}
	if err != nil {
		return false, err
	}
	refundTxnUUID := refund.TxnNo
	if refund.Status != service.TxnStatusPaySuccess {
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	if !refund.RefundOfTxnNo.Valid {
		return false, service.ErrTxnStatusInvalid
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

	if err := applyRefundCreditTx(
		ctx,
		qtx,
		refundTxnUUID,
		stageCreditAccountNo,
		amount,
		refund.RefundOfTxnNo,
		refund.MerchantNo,
	); err != nil {
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

func classifyOriginRefundDecreaseMiss(ctx context.Context, q *dbsqlc.Queries, originTxnUUID pgtype.UUID, expectedMerchantNo string, amount int64) error {
	origin, err := q.GetTransferTxnByNo(ctx, originTxnUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.ErrTxnNotFound
	}
	if err != nil {
		return err
	}
	if expectedMerchantNo != "" && origin.MerchantNo != expectedMerchantNo {
		return service.ErrTxnNotFound
	}
	if origin.BizType != service.BizTypeTransfer || origin.Status != service.TxnStatusRecvSuccess {
		return service.ErrTxnStatusInvalid
	}
	if origin.RefundableAmount < amount {
		return service.ErrRefundAmountExceeded
	}
	// Concurrent refunds can consume the quota between condition check and update.
	return service.ErrRefundAmountExceeded
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

	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := r.queries.ClaimDueOutboxEventsByTxnNo(ctx, dbsqlc.ClaimDueOutboxEventsByTxnNoParams{
		TxnNo:     txnNo,
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

	return r.queries.MarkOutboxEventSuccess(ctx, eventID)
}

func (r *Repository) MarkOutboxEventRetry(eventID string, retryCount int, nextRetryAt time.Time, dead bool) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	return r.queries.MarkOutboxEventRetry(ctx, dbsqlc.MarkOutboxEventRetryParams{
		RetryCount:  int32(retryCount),
		NextRetryAt: pgtype.Timestamptz{Time: nextRetryAt.UTC(), Valid: true},
		MarkDead:    dead,
		EventID:     eventID,
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

func buildTransferBookCreditParts(
	ctx context.Context,
	q *dbsqlc.Queries,
	txnUUID pgtype.UUID,
	debitAccountNo string,
	amount int64,
) ([]bookCreditPart, bool, error) {
	if !txnUUID.Valid || strings.TrimSpace(debitAccountNo) == "" || amount <= 0 {
		return nil, false, service.ErrAccountResolveFailed
	}

	debitBookChanges, err := q.ListOriginDebitBookChanges(ctx, dbsqlc.ListOriginDebitBookChangesParams{
		TxnNo:     txnUUID,
		AccountNo: debitAccountNo,
	})
	if err != nil {
		return nil, false, err
	}
	if len(debitBookChanges) == 0 {
		return nil, false, nil
	}

	parts := make([]bookCreditPart, 0, len(debitBookChanges))
	sum := int64(0)
	for _, change := range debitBookChanges {
		partAmount := -change.Delta
		if partAmount <= 0 {
			continue
		}
		expireAt := pgDateToTime(change.ExpireAt)
		if expireAt.IsZero() {
			return nil, true, service.ErrAccountResolveFailed
		}
		parts = append(parts, bookCreditPart{
			ExpireAt: expireAt,
			Amount:   partAmount,
		})
		sum += partAmount
	}
	if len(parts) == 0 || sum != amount {
		return nil, true, service.ErrAccountResolveFailed
	}
	return parts, true, nil
}

func buildRefundBookCreditParts(
	ctx context.Context,
	q *dbsqlc.Queries,
	refundTxnUUID, originTxnUUID pgtype.UUID,
	merchantNo string,
	refundAmount int64,
) ([]bookCreditPart, error) {
	if !refundTxnUUID.Valid || !originTxnUUID.Valid || refundAmount <= 0 {
		return nil, service.ErrAccountResolveFailed
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
	traceErr error,
) error {
	if traceErr == nil {
		traceErr = service.ErrAccountResolveFailed
	}
	if amount <= 0 || strings.TrimSpace(credit.AccountNo) == "" {
		return service.ErrAccountResolveFailed
	}
	if !credit.BookEnabled || len(parts) == 0 {
		return traceErr
	}

	type plannedBookPart struct {
		BookNo        pgtype.UUID
		ExpireAt      pgtype.Date
		Delta         int64
		BalanceBefore int64
		BalanceAfter  int64
	}

	planned := make([]plannedBookPart, 0, len(parts))
	sum := int64(0)

	for _, part := range parts {
		if part.Amount <= 0 || part.ExpireAt.IsZero() {
			return traceErr
		}
		sum += part.Amount

		expireAt := toPGDate(part.ExpireAt)
		bookNo := pgtype.UUID{}
		balanceBefore := int64(0)

		book, err := q.GetAccountBookForUpdateByAccountExpire(ctx, dbsqlc.GetAccountBookForUpdateByAccountExpireParams{
			AccountNo: credit.AccountNo,
			ExpireAt:  expireAt,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			bookNoString, idErr := idpkg.NewRuntimeUUIDProvider().NewUUIDv7()
			if idErr != nil {
				return idErr
			}
			parsedBookNo, parseErr := parseUUID(bookNoString)
			if parseErr != nil {
				return parseErr
			}
			bookNo = parsedBookNo
		case err != nil:
			return err
		default:
			bookNo = book.BookNo
			balanceBefore = book.Balance
		}

		planned = append(planned, plannedBookPart{
			BookNo:        bookNo,
			ExpireAt:      expireAt,
			Delta:         part.Amount,
			BalanceBefore: balanceBefore,
			BalanceAfter:  balanceBefore + part.Amount,
		})
	}

	if sum != amount {
		return traceErr
	}

	creditBefore := credit.Balance
	creditAfter := creditBefore + amount
	for _, item := range planned {
		if err := q.InsertAccountBookChange(ctx, dbsqlc.InsertAccountBookChangeParams{
			TxnNo:         txnUUID,
			AccountNo:     credit.AccountNo,
			BookNo:        item.BookNo,
			Delta:         item.Delta,
			BalanceBefore: item.BalanceBefore,
			BalanceAfter:  item.BalanceAfter,
			ExpireAt:      item.ExpireAt,
		}); err != nil {
			return err
		}
	}
	if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
		TxnNo:         txnUUID,
		AccountNo:     credit.AccountNo,
		Delta:         amount,
		BalanceBefore: creditBefore,
		BalanceAfter:  creditAfter,
	}); err != nil {
		return err
	}

	for _, item := range planned {
		book, err := q.UpsertAccountBookBalance(ctx, dbsqlc.UpsertAccountBookBalanceParams{
			BookNo:    pgUUIDToString(item.BookNo),
			AccountNo: credit.AccountNo,
			ExpireAt:  item.ExpireAt,
			Delta:     item.Delta,
		})
		if err != nil {
			return err
		}
		if book.Balance != item.BalanceAfter {
			return traceErr
		}
	}

	if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
		Balance:   creditAfter,
		AccountNo: credit.AccountNo,
	}); err != nil {
		return err
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

func validateDebitBalanceOnly(a service.Account, amount int64) error {
	if amount <= 0 {
		return service.ErrInsufficientBalance
	}
	if !a.AllowOverdraft {
		if a.Balance < amount {
			return service.ErrInsufficientBalance
		}
		return nil
	}
	if a.MaxOverdraftLimit == 0 {
		return nil
	}
	if a.Balance+a.MaxOverdraftLimit < amount {
		return service.ErrInsufficientBalance
	}
	return nil
}

func applyAccountDebitTx(ctx context.Context, repo *Repository, q *dbsqlc.Queries, txnUUID pgtype.UUID, debitAccountNo string, amount int64) error {
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

	if debit.BookEnabled {
		if err := repo.sweepExpiredBooksTx(ctx, q, debit, time.Now().UTC()); err != nil {
			return err
		}
		debitRow, err = q.GetAccountForUpdateByNo(ctx, debitAccountNo)
		if errors.Is(err, pgx.ErrNoRows) {
			return service.ErrAccountResolveFailed
		}
		if err != nil {
			return err
		}
		debit = accountFromGetAccountForUpdateRow(debitRow)
		if err := validateDebitBalanceOnly(debit, amount); err != nil {
			return err
		}
		books, err := q.ListAvailableAccountBooksForUpdate(ctx, dbsqlc.ListAvailableAccountBooksForUpdateParams{
			AccountNo:  debit.AccountNo,
			NowUtc:     toPGDate(time.Now().UTC()),
			NoExpireAt: toPGDate(noExpireBookDate),
		})
		if err != nil {
			return err
		}

		type bookChange struct {
			BookNo        pgtype.UUID
			ExpireAt      pgtype.Date
			Delta         int64
			BalanceBefore int64
			BalanceAfter  int64
		}

		left := amount
		changes := make([]bookChange, 0, len(books))
		for _, book := range books {
			if left == 0 {
				break
			}
			isNoExpire := book.ExpireAt.Valid && normalizeUTCDate(book.ExpireAt.Time).Equal(noExpireBookDate)
			if !isNoExpire && book.Balance <= 0 {
				continue
			}

			delta := int64(0)
			after := book.Balance
			if isNoExpire {
				delta = -left
				after = book.Balance - left
				left = 0
			} else {
				use := book.Balance
				if use > left {
					use = left
				}
				if use <= 0 {
					continue
				}
				delta = -use
				after = book.Balance - use
				left -= use
			}
			changes = append(changes, bookChange{
				BookNo:        book.BookNo,
				ExpireAt:      book.ExpireAt,
				Delta:         delta,
				BalanceBefore: book.Balance,
				BalanceAfter:  after,
			})
		}
		if left > 0 {
			return service.ErrInsufficientBalance
		}

		debitBefore := debit.Balance
		debitAfter := debitBefore - amount
		for _, change := range changes {
			if err := q.InsertAccountBookChange(ctx, dbsqlc.InsertAccountBookChangeParams{
				TxnNo:         txnUUID,
				AccountNo:     debit.AccountNo,
				BookNo:        change.BookNo,
				Delta:         change.Delta,
				BalanceBefore: change.BalanceBefore,
				BalanceAfter:  change.BalanceAfter,
				ExpireAt:      change.ExpireAt,
			}); err != nil {
				return err
			}
		}
		if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
			TxnNo:         txnUUID,
			AccountNo:     debit.AccountNo,
			Delta:         -amount,
			BalanceBefore: debitBefore,
			BalanceAfter:  debitAfter,
		}); err != nil {
			return err
		}

		bookNos := make([]string, 0, len(changes))
		deltas := make([]int64, 0, len(changes))
		for _, change := range changes {
			bookNos = append(bookNos, pgUUIDToString(change.BookNo))
			deltas = append(deltas, change.Delta)
		}
		updatedBooks, err := q.BatchUpdateAccountBookBalances(ctx, dbsqlc.BatchUpdateAccountBookBalancesParams{
			BookNos: bookNos,
			Deltas:  deltas,
		})
		if err != nil {
			return err
		}
		if len(updatedBooks) != len(changes) {
			return service.ErrInsufficientBalance
		}
		updatedBalancesByBookNo := make(map[string]int64, len(updatedBooks))
		for _, row := range updatedBooks {
			updatedBalancesByBookNo[pgUUIDToString(row.BookNo)] = row.Balance
		}
		for i := range changes {
			after, ok := updatedBalancesByBookNo[pgUUIDToString(changes[i].BookNo)]
			if !ok {
				return service.ErrInsufficientBalance
			}
			if after != changes[i].BalanceAfter {
				return service.ErrInsufficientBalance
			}
		}
		if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
			Balance:   debitAfter,
			AccountNo: debit.AccountNo,
		}); err != nil {
			return err
		}
		return nil
	}

	if err := validateDebitBalanceOnly(debit, amount); err != nil {
		return err
	}

	debitBefore := debit.Balance
	debitAfter := debitBefore - amount
	if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
		TxnNo:         txnUUID,
		AccountNo:     debit.AccountNo,
		Delta:         -amount,
		BalanceBefore: debitBefore,
		BalanceAfter:  debitAfter,
	}); err != nil {
		return err
	}
	if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
		Balance:   debitAfter,
		AccountNo: debit.AccountNo,
	}); err != nil {
		return err
	}
	return nil
}

func (r *Repository) sweepExpiredBooksTx(ctx context.Context, q *dbsqlc.Queries, debit service.Account, now time.Time) error {
	if r == nil || !debit.BookEnabled {
		return nil
	}

	expiredBooks, err := q.ListExpiredPositiveAccountBooksForUpdate(ctx, dbsqlc.ListExpiredPositiveAccountBooksForUpdateParams{
		AccountNo:  debit.AccountNo,
		NowUtc:     toPGDate(now),
		NoExpireAt: toPGDate(noExpireBookDate),
	})
	if err != nil {
		return err
	}
	if len(expiredBooks) == 0 {
		return nil
	}

	writeoffAccount, err := r.ensureMerchantWriteoffAccountTx(ctx, q, debit.MerchantNo)
	if err != nil {
		return err
	}

	writeoffTxnNo, err := idpkg.NewRuntimeUUIDProvider().NewUUIDv7()
	if err != nil {
		return err
	}
	writeoffTxnUUID, err := parseUUID(writeoffTxnNo)
	if err != nil {
		return err
	}
	writeoffOutTradeNo := buildExpireWriteoffOutTradeNo(debit.AccountNo, writeoffTxnNo)

	totalWrittenOff := int64(0)
	bookNos := make([]string, 0, len(expiredBooks))
	deltas := make([]int64, 0, len(expiredBooks))
	for _, book := range expiredBooks {
		if book.Balance <= 0 {
			continue
		}
		totalWrittenOff += book.Balance
		if err := q.InsertAccountBookChange(ctx, dbsqlc.InsertAccountBookChangeParams{
			TxnNo:         writeoffTxnUUID,
			AccountNo:     debit.AccountNo,
			BookNo:        book.BookNo,
			Delta:         -book.Balance,
			BalanceBefore: book.Balance,
			BalanceAfter:  0,
			ExpireAt:      book.ExpireAt,
		}); err != nil {
			return err
		}
		bookNos = append(bookNos, pgUUIDToString(book.BookNo))
		deltas = append(deltas, -book.Balance)
	}
	if totalWrittenOff <= 0 {
		return nil
	}

	if err := q.CreateTransferTxn(ctx, dbsqlc.CreateTransferTxnParams{
		TxnNo:            writeoffTxnNo,
		MerchantNo:       debit.MerchantNo,
		OutTradeNo:       writeoffOutTradeNo,
		BizType:          service.BizTypeTransfer,
		TransferScene:    nullableText(service.SceneExpireWriteoff),
		Title:            nullIfEmpty("expire writeoff"),
		Remark:           nullIfEmpty("system expiry writeoff"),
		DebitAccountNo:   nullIfEmpty(debit.AccountNo),
		CreditAccountNo:  nullIfEmpty(writeoffAccount.AccountNo),
		CreditExpireAt:   pgtype.Date{},
		Amount:           totalWrittenOff,
		Status:           service.TxnStatusRecvSuccess,
		RefundOfTxnNo:    nullIfEmpty(""),
		RefundableAmount: 0,
		ErrorCode:        nullIfEmpty(""),
		ErrorMsg:         nullIfEmpty(""),
	}); err != nil {
		return err
	}

	debitBefore := debit.Balance
	debitAfter := debitBefore - totalWrittenOff
	if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
		TxnNo:         writeoffTxnUUID,
		AccountNo:     debit.AccountNo,
		Delta:         -totalWrittenOff,
		BalanceBefore: debitBefore,
		BalanceAfter:  debitAfter,
	}); err != nil {
		return err
	}

	updatedBooks, err := q.BatchUpdateAccountBookBalances(ctx, dbsqlc.BatchUpdateAccountBookBalancesParams{
		BookNos: bookNos,
		Deltas:  deltas,
	})
	if err != nil {
		return err
	}
	if len(updatedBooks) != len(bookNos) {
		return service.ErrAccountResolveFailed
	}
	for _, row := range updatedBooks {
		if row.Balance != 0 {
			return service.ErrAccountResolveFailed
		}
	}
	if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
		Balance:   debitAfter,
		AccountNo: debit.AccountNo,
	}); err != nil {
		return err
	}

	return applyAccountCreditWithAccountTx(ctx, q, writeoffTxnUUID, writeoffAccount, totalWrittenOff, nil)
}

func (r *Repository) ensureMerchantWriteoffAccountTx(ctx context.Context, q *dbsqlc.Queries, merchantNo string) (service.Account, error) {
	merchant, err := q.GetMerchantForUpdateByNo(ctx, merchantNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Account{}, service.ErrInvalidMerchantNo
	}
	if err != nil {
		return service.Account{}, err
	}

	writeoffAccountNo := strings.TrimSpace(merchant.WriteoffAccountNo)
	if writeoffAccountNo == "" {
		writeoffAccountNo, err = r.codeProvider.NewAccountNo(merchantNo, service.AccountTypeWriteoff)
		if err != nil {
			if errors.Is(err, idpkg.ErrCodeAllocatorUnavailable) {
				return service.Account{}, service.ErrCodeAllocatorUnavailable
			}
			return service.Account{}, fmt.Errorf("new writeoff account no: %w", err)
		}
		if err := r.createAccount(ctx, q, service.Account{
			AccountNo:     writeoffAccountNo,
			MerchantNo:    merchantNo,
			AccountType:   service.AccountTypeWriteoff,
			AllowDebitOut: false,
			AllowCreditIn: true,
			AllowTransfer: false,
		}); err != nil {
			return service.Account{}, err
		}
		if err := q.UpdateMerchantWriteoffAccountNo(ctx, dbsqlc.UpdateMerchantWriteoffAccountNoParams{
			MerchantNo:        merchantNo,
			WriteoffAccountNo: nullableText(writeoffAccountNo),
		}); err != nil {
			return service.Account{}, err
		}
	}

	accountRow, err := q.GetAccountForUpdateByNo(ctx, writeoffAccountNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Account{}, service.ErrAccountResolveFailed
	}
	if err != nil {
		return service.Account{}, err
	}
	return accountFromGetAccountForUpdateRow(accountRow), nil
}

func buildExpireWriteoffOutTradeNo(accountNo, txnNo string) string {
	sanitizedTxnNo := strings.ReplaceAll(strings.TrimSpace(txnNo), "-", "")
	if len(sanitizedTxnNo) > 8 {
		sanitizedTxnNo = sanitizedTxnNo[:8]
	}
	return fmt.Sprintf("sys_expire_%s_%s", strings.TrimSpace(accountNo), sanitizedTxnNo)
}

func applyTransferCreditTx(
	ctx context.Context,
	q *dbsqlc.Queries,
	txnUUID pgtype.UUID,
	debitAccountNo, creditAccountNo, transferScene string,
	amount int64,
	creditExpireAt *time.Time,
) error {
	if strings.TrimSpace(creditAccountNo) == "" || amount <= 0 {
		return service.ErrAccountResolveFailed
	}

	creditRow, err := q.GetAccountForUpdateByNo(ctx, creditAccountNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.ErrAccountResolveFailed
	}
	if err != nil {
		return err
	}
	creditAccount := accountFromGetAccountForUpdateRow(creditRow)

	if strings.TrimSpace(transferScene) == service.SceneP2P && creditAccount.BookEnabled {
		parts, hasBookDebitTrace, err := buildTransferBookCreditParts(ctx, q, txnUUID, debitAccountNo, amount)
		if err != nil {
			return err
		}
		if hasBookDebitTrace {
			return applyBookCreditPartsTx(ctx, q, txnUUID, creditAccount, amount, parts, service.ErrAccountResolveFailed)
		}
	}

	return applyAccountCreditWithAccountTx(ctx, q, txnUUID, creditAccount, amount, creditExpireAt)
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
	return applyAccountCreditWithAccountTx(ctx, q, txnUUID, credit, amount, creditExpireAt)
}

func applyAccountCreditWithAccountTx(
	ctx context.Context,
	q *dbsqlc.Queries,
	txnUUID pgtype.UUID,
	credit service.Account,
	amount int64,
	creditExpireAt *time.Time,
) error {
	if strings.TrimSpace(credit.AccountNo) == "" || amount <= 0 {
		return service.ErrAccountResolveFailed
	}

	if credit.BookEnabled {
		expireAt := noExpireBookDate
		if creditExpireAt != nil && !creditExpireAt.IsZero() {
			expireAt = creditExpireAt.UTC()
		}
		expireDate := toPGDate(expireAt)
		bookNo := pgtype.UUID{}
		bookBefore := int64(0)
		bookRow, err := q.GetAccountBookForUpdateByAccountExpire(ctx, dbsqlc.GetAccountBookForUpdateByAccountExpireParams{
			AccountNo: credit.AccountNo,
			ExpireAt:  expireDate,
		})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			bookNoString, idErr := idpkg.NewRuntimeUUIDProvider().NewUUIDv7()
			if idErr != nil {
				return idErr
			}
			parsedBookNo, parseErr := parseUUID(bookNoString)
			if parseErr != nil {
				return parseErr
			}
			bookNo = parsedBookNo
		case err != nil:
			return err
		default:
			bookNo = bookRow.BookNo
			bookBefore = bookRow.Balance
		}
		bookAfter := bookBefore + amount

		creditBefore := credit.Balance
		creditAfter := creditBefore + amount
		if err := q.InsertAccountBookChange(ctx, dbsqlc.InsertAccountBookChangeParams{
			TxnNo:         txnUUID,
			AccountNo:     credit.AccountNo,
			BookNo:        bookNo,
			Delta:         amount,
			BalanceBefore: bookBefore,
			BalanceAfter:  bookAfter,
			ExpireAt:      expireDate,
		}); err != nil {
			return err
		}
		if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
			TxnNo:         txnUUID,
			AccountNo:     credit.AccountNo,
			Delta:         amount,
			BalanceBefore: creditBefore,
			BalanceAfter:  creditAfter,
		}); err != nil {
			return err
		}

		book, err := q.UpsertAccountBookBalance(ctx, dbsqlc.UpsertAccountBookBalanceParams{
			BookNo:    pgUUIDToString(bookNo),
			AccountNo: credit.AccountNo,
			ExpireAt:  expireDate,
			Delta:     amount,
		})
		if err != nil {
			return err
		}
		if book.Balance != bookAfter {
			return service.ErrAccountResolveFailed
		}
		if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
			Balance:   creditAfter,
			AccountNo: credit.AccountNo,
		}); err != nil {
			return err
		}
		return nil
	}

	creditBefore := credit.Balance
	creditAfter := creditBefore + amount
	if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
		TxnNo:         txnUUID,
		AccountNo:     credit.AccountNo,
		Delta:         amount,
		BalanceBefore: creditBefore,
		BalanceAfter:  creditAfter,
	}); err != nil {
		return err
	}
	if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
		Balance:   creditAfter,
		AccountNo: credit.AccountNo,
	}); err != nil {
		return err
	}
	return nil
}

func applyRefundCreditTx(
	ctx context.Context,
	q *dbsqlc.Queries,
	txnUUID pgtype.UUID,
	creditAccountNo string,
	amount int64,
	originTxnUUID pgtype.UUID,
	merchantNo string,
) error {
	if strings.TrimSpace(creditAccountNo) == "" || amount <= 0 {
		return service.ErrAccountResolveFailed
	}
	if !originTxnUUID.Valid {
		return service.ErrTxnStatusInvalid
	}

	creditRes, err := q.TryCreditAccountBalanceNonBookRefund(ctx, dbsqlc.TryCreditAccountBalanceNonBookRefundParams{
		AccountNo: creditAccountNo,
		Amount:    amount,
	})
	if err == nil {
		creditAfter := creditRes.Balance
		creditBefore := creditAfter - amount
		if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
			TxnNo:         txnUUID,
			AccountNo:     creditAccountNo,
			Delta:         amount,
			BalanceBefore: creditBefore,
			BalanceAfter:  creditAfter,
		}); err != nil {
			return err
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	creditRow, err := q.GetAccountForUpdateByNo(ctx, creditAccountNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.ErrAccountResolveFailed
	}
	if err != nil {
		return err
	}
	creditAccount := accountFromGetAccountForUpdateRow(creditRow)
	if !creditAccount.BookEnabled {
		creditBefore := creditAccount.Balance
		creditAfter := creditBefore + amount
		if err := q.InsertAccountChange(ctx, dbsqlc.InsertAccountChangeParams{
			TxnNo:         txnUUID,
			AccountNo:     creditAccount.AccountNo,
			Delta:         amount,
			BalanceBefore: creditBefore,
			BalanceAfter:  creditAfter,
		}); err != nil {
			return err
		}
		if err := q.UpdateAccountBalance(ctx, dbsqlc.UpdateAccountBalanceParams{
			Balance:   creditAfter,
			AccountNo: creditAccount.AccountNo,
		}); err != nil {
			return err
		}
		return nil
	}

	parts, err := buildRefundBookCreditParts(ctx, q, txnUUID, originTxnUUID, merchantNo, amount)
	if err != nil {
		return err
	}
	if err := applyBookCreditPartsTx(ctx, q, txnUUID, creditAccount, amount, parts, service.ErrRefundOriginBookTraceMissing); err != nil {
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
		Title:            row.Title,
		Remark:           row.Remark,
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
		Title:            row.Title,
		Remark:           row.Remark,
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
		Title:            row.Title,
		Remark:           row.Remark,
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
		Title:            row.Title,
		Remark:           row.Remark,
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

func pgUUIDToString(v pgtype.UUID) string {
	if !v.Valid {
		return ""
	}
	return uuid.UUID(v.Bytes).String()
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
		EventID:           deterministicOutboxEventID(txnUUID, "TxnSucceeded"),
		TxnNo:             txnUUID,
		MerchantNo:        merchantNo,
		OutTradeNo:        nullIfEmpty(outTradeNo),
		Status:            "PENDING",
		WebhookMerchantNo: merchantNo,
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
