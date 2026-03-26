package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	dbsqlc "github.com/xmz-ai/coin/internal/db/sqlc"
	"github.com/xmz-ai/coin/internal/service"
)

type AdminUser struct {
	UserID       int64
	Username     string
	PasswordHash string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AdminAuditLog struct {
	RequestID        string
	OperatorUsername string
	Action           string
	TargetType       string
	TargetID         string
	MerchantNo       string
	RequestPayload   any
	ResultCode       string
	ResultMessage    string
}

type AdminDashboardStats struct {
	MerchantCount         int64
	CustomerCount         int64
	AccountCount          int64
	TxnCount              int64
	TxnInitCount          int64
	TxnPayCount           int64
	TxnRecvCount          int64
	TxnFailedCount        int64
	OutboxPendingCount    int64
	OutboxProcessingCount int64
	OutboxDeadCount       int64
}

type AdminOutboxEvent struct {
	ID          int64
	EventID     string
	TxnNo       string
	MerchantNo  string
	OutTradeNo  string
	Status      string
	RetryCount  int
	NextRetryAt *time.Time
	UpdatedAt   time.Time
	CreatedAt   time.Time
}

type AdminOutboxFilter struct {
	MerchantNo string
	Status     string
	TxnNo      string
	CursorID   int64
	PageSize   int
}

type AdminAuditLogRow struct {
	AuditID           int64
	RequestID         string
	OperatorUsername  string
	Action            string
	TargetType        string
	TargetID          string
	MerchantNo        string
	RequestPayloadRaw string
	ResultCode        string
	ResultMessage     string
	CreatedAt         time.Time
}

type AdminAuditFilter struct {
	OperatorUsername string
	Action           string
	MerchantNo       string
	CursorID         int64
	PageSize         int
}

type AdminCustomerRow struct {
	CustomerNo       string
	MerchantNo       string
	OutUserID        string
	DefaultAccountNo string
	CreatedAt        time.Time
}

type AdminCustomerFilter struct {
	MerchantNo string
	OutUserID  string
	CustomerNo string
	CursorNo   string
	PageSize   int
}

type AdminAccountRow struct {
	AccountNo         string
	MerchantNo        string
	CustomerNo        string
	OwnerOutUserID    string
	AccountType       string
	AllowOverdraft    bool
	MaxOverdraftLimit int64
	AllowDebitOut     bool
	AllowCreditIn     bool
	AllowTransfer     bool
	BookEnabled       bool
	Balance           int64
	CreatedAt         time.Time
}

type AdminAccountFilter struct {
	MerchantNo string
	AccountNo  string
	CustomerNo string
	OutUserID  string
	CursorNo   string
	PageSize   int
}

type AdminSetupState struct {
	Initialized              bool
	InitializedAdminUsername string
	DefaultMerchantNo        string
	InitializedAt            *time.Time
	UpdatedAt                time.Time
}

var ErrAdminUserExists = errors.New("admin user exists")

const adminSetupAdvisoryLockKey int64 = 22060323

func (r *Repository) EnsureAdminUser(username, passwordHash string) error {
	if r == nil {
		return errors.New("repository is nil")
	}
	fixedUsername := strings.TrimSpace(username)
	fixedHash := strings.TrimSpace(passwordHash)
	if fixedUsername == "" || fixedHash == "" {
		return errors.New("username and password_hash are required")
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	_, err := r.pool.Exec(ctx, `
		INSERT INTO admin_user (username, password_hash, status, created_at, updated_at)
		VALUES ($1, $2, 'ACTIVE', NOW(), NOW())
		ON CONFLICT (username)
		DO UPDATE SET
			password_hash = EXCLUDED.password_hash,
			status = 'ACTIVE',
			updated_at = NOW()
	`, fixedUsername, fixedHash)
	return err
}

func (r *Repository) CreateAdminUser(username, passwordHash string) error {
	if r == nil {
		return errors.New("repository is nil")
	}
	fixedUsername := strings.TrimSpace(username)
	fixedHash := strings.TrimSpace(passwordHash)
	if fixedUsername == "" || fixedHash == "" {
		return errors.New("username and password_hash are required")
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	_, err := r.pool.Exec(ctx, `
		INSERT INTO admin_user (username, password_hash, status, created_at, updated_at)
		VALUES ($1, $2, 'ACTIVE', NOW(), NOW())
	`, fixedUsername, fixedHash)
	if isUniqueViolation(err) {
		return ErrAdminUserExists
	}
	return err
}

func (r *Repository) CountAdminUsers() (int64, error) {
	if r == nil {
		return 0, errors.New("repository is nil")
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	var count int64
	if err := r.pool.QueryRow(ctx, `SELECT COUNT(*)::bigint FROM admin_user`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *Repository) WithAdminSetupLock(fn func() error) error {
	if r == nil {
		return errors.New("repository is nil")
	}
	if fn == nil {
		return errors.New("admin setup lock callback is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, adminSetupAdvisoryLockKey); err != nil {
		return err
	}
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer unlockCancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, adminSetupAdvisoryLockKey)
	}()

	return fn()
}

func (r *Repository) GetAdminSetupState() (AdminSetupState, error) {
	if r == nil {
		return AdminSetupState{}, errors.New("repository is nil")
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	if err := r.ensureAdminSetupStateRow(ctx); err != nil {
		return AdminSetupState{}, err
	}

	var out AdminSetupState
	var initializedAt pgtype.Timestamptz
	if err := r.pool.QueryRow(ctx, `
		SELECT initialized, initialized_admin_username, default_merchant_no, initialized_at, updated_at
		FROM admin_setup_state
		WHERE id = 1
		LIMIT 1
	`).Scan(
		&out.Initialized,
		&out.InitializedAdminUsername,
		&out.DefaultMerchantNo,
		&initializedAt,
		&out.UpdatedAt,
	); err != nil {
		return AdminSetupState{}, err
	}
	if initializedAt.Valid {
		v := initializedAt.Time.UTC()
		out.InitializedAt = &v
	}
	out.InitializedAdminUsername = strings.TrimSpace(out.InitializedAdminUsername)
	out.DefaultMerchantNo = strings.TrimSpace(out.DefaultMerchantNo)
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

func (r *Repository) SaveAdminSetupProgress(adminUsername, defaultMerchantNo string) error {
	if r == nil {
		return errors.New("repository is nil")
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	if err := r.ensureAdminSetupStateRow(ctx); err != nil {
		return err
	}

	_, err := r.pool.Exec(ctx, `
		UPDATE admin_setup_state
		SET initialized_admin_username = CASE
				WHEN NULLIF($1, '') IS NULL THEN initialized_admin_username
				ELSE $1
			END,
			default_merchant_no = CASE
				WHEN NULLIF($2, '') IS NULL THEN default_merchant_no
				ELSE $2
			END,
			updated_at = NOW()
		WHERE id = 1
	`, strings.TrimSpace(adminUsername), strings.TrimSpace(defaultMerchantNo))
	return err
}

func (r *Repository) MarkAdminSetupInitialized(adminUsername, defaultMerchantNo string) error {
	if r == nil {
		return errors.New("repository is nil")
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	if err := r.ensureAdminSetupStateRow(ctx); err != nil {
		return err
	}

	_, err := r.pool.Exec(ctx, `
		UPDATE admin_setup_state
		SET initialized = TRUE,
			initialized_admin_username = CASE
				WHEN NULLIF($1, '') IS NULL THEN initialized_admin_username
				ELSE $1
			END,
			default_merchant_no = CASE
				WHEN NULLIF($2, '') IS NULL THEN default_merchant_no
				ELSE $2
			END,
			initialized_at = COALESCE(initialized_at, NOW()),
			updated_at = NOW()
		WHERE id = 1
	`, strings.TrimSpace(adminUsername), strings.TrimSpace(defaultMerchantNo))
	return err
}

func (r *Repository) ensureAdminSetupStateRow(ctx context.Context) error {
	if r == nil {
		return errors.New("repository is nil")
	}
	if ctx == nil {
		return errors.New("context is nil")
	}
	if err := r.ensureAdminSetupStateTable(ctx); err != nil {
		return err
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO admin_setup_state (
			id,
			initialized,
			initialized_admin_username,
			default_merchant_no
		) VALUES (
			1,
			FALSE,
			'',
			''
		)
		ON CONFLICT (id) DO NOTHING
	`)
	return err
}

func (r *Repository) ensureAdminSetupStateTable(ctx context.Context) error {
	if r == nil {
		return errors.New("repository is nil")
	}
	if ctx == nil {
		return errors.New("context is nil")
	}
	_, err := r.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS admin_setup_state (
			id SMALLINT PRIMARY KEY CHECK (id = 1),
			initialized BOOLEAN NOT NULL DEFAULT FALSE,
			initialized_admin_username VARCHAR(64) NOT NULL DEFAULT '',
			default_merchant_no VARCHAR(16) NOT NULL DEFAULT '',
			initialized_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	return err
}

func (r *Repository) GetAdminUserByUsername(username string) (AdminUser, bool, error) {
	if r == nil {
		return AdminUser{}, false, errors.New("repository is nil")
	}
	fixedUsername := strings.TrimSpace(username)
	if fixedUsername == "" {
		return AdminUser{}, false, nil
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	var out AdminUser
	err := r.pool.QueryRow(ctx, `
		SELECT user_id, username, password_hash, status, created_at, updated_at
		FROM admin_user
		WHERE username = $1
		LIMIT 1
	`, fixedUsername).Scan(
		&out.UserID,
		&out.Username,
		&out.PasswordHash,
		&out.Status,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminUser{}, false, nil
	}
	if err != nil {
		return AdminUser{}, false, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, true, nil
}

func (r *Repository) InsertAdminAuditLog(entry AdminAuditLog) error {
	if r == nil {
		return errors.New("repository is nil")
	}
	requestID := strings.TrimSpace(entry.RequestID)
	if requestID == "" {
		requestID = "-"
	}
	operator := strings.TrimSpace(entry.OperatorUsername)
	if operator == "" {
		operator = "unknown"
	}
	action := strings.TrimSpace(entry.Action)
	if action == "" {
		action = "UNKNOWN"
	}
	targetType := strings.TrimSpace(entry.TargetType)
	if targetType == "" {
		targetType = "UNKNOWN"
	}
	targetID := strings.TrimSpace(entry.TargetID)
	if targetID == "" {
		targetID = "-"
	}
	merchantNo := strings.TrimSpace(entry.MerchantNo)
	resultCode := strings.TrimSpace(entry.ResultCode)
	if resultCode == "" {
		resultCode = "UNKNOWN"
	}
	resultMessage := strings.TrimSpace(entry.ResultMessage)
	if resultMessage == "" {
		resultMessage = "-"
	}

	payloadRaw := ""
	if entry.RequestPayload != nil {
		b, err := json.Marshal(entry.RequestPayload)
		if err != nil {
			payloadRaw = "{\"marshal_error\":\"payload marshal failed\"}"
		} else {
			payloadRaw = string(b)
		}
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	_, err := r.pool.Exec(ctx, `
		INSERT INTO admin_audit_log (
			request_id,
			operator_username,
			action,
			target_type,
			target_id,
			merchant_no,
			request_payload,
			result_code,
			result_message,
			created_at
		) VALUES (
			$1,
			$2,
			$3,
			$4,
			$5,
			$6,
			NULLIF($7, '')::jsonb,
			$8,
			$9,
			NOW()
		)
	`, requestID, operator, action, targetType, targetID, merchantNo, payloadRaw, resultCode, resultMessage)
	return err
}

func (r *Repository) GetAdminDashboardStats(merchantNo string) (AdminDashboardStats, error) {
	if r == nil {
		return AdminDashboardStats{}, errors.New("repository is nil")
	}
	fixedMerchantNo := strings.TrimSpace(merchantNo)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out AdminDashboardStats
	err := r.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*)::bigint FROM merchant m WHERE ($1 = '' OR m.merchant_no = $1)) AS merchant_count,
			(SELECT COUNT(*)::bigint FROM customer c WHERE ($1 = '' OR c.merchant_no = $1)) AS customer_count,
			(SELECT COUNT(*)::bigint FROM account a WHERE ($1 = '' OR a.merchant_no = $1)) AS account_count,
			(SELECT COUNT(*)::bigint FROM txn t WHERE ($1 = '' OR t.merchant_no = $1)) AS txn_count,
			(SELECT COUNT(*)::bigint FROM txn t WHERE ($1 = '' OR t.merchant_no = $1) AND t.status = 'INIT') AS txn_init_count,
			(SELECT COUNT(*)::bigint FROM txn t WHERE ($1 = '' OR t.merchant_no = $1) AND t.status = 'PAY_SUCCESS') AS txn_pay_count,
			(SELECT COUNT(*)::bigint FROM txn t WHERE ($1 = '' OR t.merchant_no = $1) AND t.status = 'RECV_SUCCESS') AS txn_recv_count,
			(SELECT COUNT(*)::bigint FROM txn t WHERE ($1 = '' OR t.merchant_no = $1) AND t.status = 'FAILED') AS txn_failed_count,
			(SELECT COUNT(*)::bigint FROM outbox_event e WHERE ($1 = '' OR e.merchant_no = $1) AND e.status = 'PENDING') AS outbox_pending_count,
			(SELECT COUNT(*)::bigint FROM outbox_event e WHERE ($1 = '' OR e.merchant_no = $1) AND e.status = 'PROCESSING') AS outbox_processing_count,
			(SELECT COUNT(*)::bigint FROM outbox_event e WHERE ($1 = '' OR e.merchant_no = $1) AND e.status = 'DEAD') AS outbox_dead_count
	`, fixedMerchantNo).Scan(
		&out.MerchantCount,
		&out.CustomerCount,
		&out.AccountCount,
		&out.TxnCount,
		&out.TxnInitCount,
		&out.TxnPayCount,
		&out.TxnRecvCount,
		&out.TxnFailedCount,
		&out.OutboxPendingCount,
		&out.OutboxProcessingCount,
		&out.OutboxDeadCount,
	)
	if err != nil {
		return AdminDashboardStats{}, err
	}
	return out, nil
}

func (r *Repository) GetAccountBookBalanceSum(accountNo string) (int64, error) {
	if r == nil {
		return 0, errors.New("repository is nil")
	}
	fixedAccountNo := strings.TrimSpace(accountNo)
	if fixedAccountNo == "" {
		return 0, errors.New("account_no is required")
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	var sum int64
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(balance), 0)::bigint
		FROM account_book
		WHERE account_no = $1
	`, fixedAccountNo).Scan(&sum)
	if err != nil {
		return 0, err
	}
	return sum, nil
}

func (r *Repository) GetAvailableAccountBookBalanceSum(accountNo string) (int64, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	return r.queries.GetAvailableAccountBookBalanceSum(ctx, dbsqlc.GetAvailableAccountBookBalanceSumParams{
		AccountNo:  accountNo,
		NowUtc:     toPGDate(time.Now().UTC()),
		NoExpireAt: toPGDate(noExpireBookDate),
	})
}

func (r *Repository) GetCustomerByNo(merchantNo, customerNo string) (service.Customer, bool, error) {
	if r == nil {
		return service.Customer{}, false, errors.New("repository is nil")
	}
	fixedMerchantNo := strings.TrimSpace(merchantNo)
	fixedCustomerNo := strings.TrimSpace(customerNo)
	if fixedMerchantNo == "" || fixedCustomerNo == "" {
		return service.Customer{}, false, nil
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	var out service.Customer
	err := r.pool.QueryRow(ctx, `
		SELECT customer_id::text, customer_no, merchant_no, out_user_id, COALESCE(default_account_no, '')
		FROM customer
		WHERE merchant_no = $1
		  AND customer_no = $2
		LIMIT 1
	`, fixedMerchantNo, fixedCustomerNo).Scan(
		&out.CustomerID,
		&out.CustomerNo,
		&out.MerchantNo,
		&out.OutUserID,
		&out.DefaultAccountNo,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.Customer{}, false, nil
	}
	if err != nil {
		return service.Customer{}, false, err
	}
	return out, true, nil
}

func (r *Repository) ListOutboxEventsForAdmin(filter AdminOutboxFilter) ([]AdminOutboxEvent, string, error) {
	if r == nil {
		return nil, "", errors.New("repository is nil")
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	rows, err := r.pool.Query(ctx, `
		SELECT
			e.id,
			e.event_id::text,
			e.txn_no::text,
			e.merchant_no,
			COALESCE(e.out_trade_no, '') AS out_trade_no,
			e.status,
			e.retry_count,
			e.next_retry_at,
			e.updated_at,
			e.created_at
		FROM outbox_event e
		WHERE ($1 = '' OR e.merchant_no = $1)
		  AND ($2 = '' OR e.status = $2)
		  AND ($3 = '' OR e.txn_no = NULLIF($3, '')::uuid)
		  AND ($4::bigint = 0 OR e.id < $4::bigint)
		ORDER BY e.id DESC
		LIMIT $5
	`, strings.TrimSpace(filter.MerchantNo), strings.TrimSpace(filter.Status), strings.TrimSpace(filter.TxnNo), filter.CursorID, pageSize+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	items := make([]AdminOutboxEvent, 0, pageSize+1)
	for rows.Next() {
		var item AdminOutboxEvent
		var nextRetry pgtype.Timestamptz
		if err := rows.Scan(
			&item.ID,
			&item.EventID,
			&item.TxnNo,
			&item.MerchantNo,
			&item.OutTradeNo,
			&item.Status,
			&item.RetryCount,
			&nextRetry,
			&item.UpdatedAt,
			&item.CreatedAt,
		); err != nil {
			return nil, "", err
		}
		if nextRetry.Valid {
			v := nextRetry.Time.UTC()
			item.NextRetryAt = &v
		}
		item.UpdatedAt = item.UpdatedAt.UTC()
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextToken := ""
	if len(items) > pageSize {
		nextToken = fmt.Sprintf("%d", items[pageSize-1].ID)
		items = items[:pageSize]
	}
	return items, nextToken, nil
}

func (r *Repository) ListAdminAuditLogs(filter AdminAuditFilter) ([]AdminAuditLogRow, string, error) {
	if r == nil {
		return nil, "", errors.New("repository is nil")
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	rows, err := r.pool.Query(ctx, `
		SELECT
			a.audit_id,
			a.request_id,
			a.operator_username,
			a.action,
			a.target_type,
			a.target_id,
			a.merchant_no,
			COALESCE(a.request_payload::text, '') AS request_payload,
			a.result_code,
			a.result_message,
			a.created_at
		FROM admin_audit_log a
		WHERE ($1 = '' OR a.operator_username = $1)
		  AND ($2 = '' OR a.action = $2)
		  AND ($3 = '' OR a.merchant_no = $3)
		  AND ($4::bigint = 0 OR a.audit_id < $4::bigint)
		ORDER BY a.audit_id DESC
		LIMIT $5
	`, strings.TrimSpace(filter.OperatorUsername), strings.TrimSpace(filter.Action), strings.TrimSpace(filter.MerchantNo), filter.CursorID, pageSize+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	items := make([]AdminAuditLogRow, 0, pageSize+1)
	for rows.Next() {
		var item AdminAuditLogRow
		if err := rows.Scan(
			&item.AuditID,
			&item.RequestID,
			&item.OperatorUsername,
			&item.Action,
			&item.TargetType,
			&item.TargetID,
			&item.MerchantNo,
			&item.RequestPayloadRaw,
			&item.ResultCode,
			&item.ResultMessage,
			&item.CreatedAt,
		); err != nil {
			return nil, "", err
		}
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextToken := ""
	if len(items) > pageSize {
		nextToken = fmt.Sprintf("%d", items[pageSize-1].AuditID)
		items = items[:pageSize]
	}
	return items, nextToken, nil
}

func (r *Repository) ListMerchantsForAdmin(cursorMerchantNo string, pageSize int) ([]service.Merchant, string, error) {
	if r == nil {
		return nil, "", errors.New("repository is nil")
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	rows, err := r.pool.Query(ctx, `
		SELECT
			merchant_id::text,
			merchant_no,
			name,
			budget_account_no,
			receivable_account_no,
			COALESCE(writeoff_account_no, '') AS writeoff_account_no
		FROM merchant
		WHERE ($1 = '' OR merchant_no > $1)
		ORDER BY merchant_no ASC
		LIMIT $2
	`, strings.TrimSpace(cursorMerchantNo), pageSize+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	items := make([]service.Merchant, 0, pageSize+1)
	for rows.Next() {
		var item service.Merchant
		if err := rows.Scan(
			&item.MerchantID,
			&item.MerchantNo,
			&item.Name,
			&item.BudgetAccountNo,
			&item.ReceivableAccountNo,
			&item.WriteoffAccountNo,
		); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextToken := ""
	if len(items) > pageSize {
		nextToken = items[pageSize-1].MerchantNo
		items = items[:pageSize]
	}
	return items, nextToken, nil
}

func (r *Repository) ListCustomersForAdmin(filter AdminCustomerFilter) ([]AdminCustomerRow, string, error) {
	if r == nil {
		return nil, "", errors.New("repository is nil")
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	rows, err := r.pool.Query(ctx, `
		SELECT
			c.customer_no,
			c.merchant_no,
			c.out_user_id,
			COALESCE(c.default_account_no, '') AS default_account_no,
			c.created_at
		FROM customer c
		WHERE ($1 = '' OR c.merchant_no = $1)
		  AND ($2 = '' OR c.out_user_id = $2)
		  AND ($3 = '' OR c.customer_no = $3)
		  AND ($4 = '' OR c.customer_no > $4)
		ORDER BY c.customer_no ASC
		LIMIT $5
	`, strings.TrimSpace(filter.MerchantNo), strings.TrimSpace(filter.OutUserID), strings.TrimSpace(filter.CustomerNo), strings.TrimSpace(filter.CursorNo), pageSize+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	items := make([]AdminCustomerRow, 0, pageSize+1)
	for rows.Next() {
		var item AdminCustomerRow
		if err := rows.Scan(
			&item.CustomerNo,
			&item.MerchantNo,
			&item.OutUserID,
			&item.DefaultAccountNo,
			&item.CreatedAt,
		); err != nil {
			return nil, "", err
		}
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextToken := ""
	if len(items) > pageSize {
		nextToken = items[pageSize-1].CustomerNo
		items = items[:pageSize]
	}
	return items, nextToken, nil
}

func (r *Repository) ListAccountsForAdmin(filter AdminAccountFilter) ([]AdminAccountRow, string, error) {
	if r == nil {
		return nil, "", errors.New("repository is nil")
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	ctx, cancel := r.withTimeout()
	defer cancel()

	rows, err := r.pool.Query(ctx, `
		SELECT
			a.account_no,
			a.merchant_no,
			COALESCE(a.customer_no, '') AS customer_no,
			COALESCE(c.out_user_id, '') AS out_user_id,
			a.account_type,
			a.allow_overdraft,
			a.max_overdraft_limit,
			a.allow_debit_out,
			a.allow_credit_in,
			a.allow_transfer,
			a.book_enabled,
			a.balance,
			a.created_at
		FROM account a
		LEFT JOIN customer c
		  ON c.merchant_no = a.merchant_no
		 AND c.customer_no = a.customer_no
		WHERE ($1 = '' OR a.merchant_no = $1)
		  AND ($2 = '' OR a.account_no = $2)
		  AND ($3 = '' OR COALESCE(a.customer_no, '') = $3)
		  AND ($4 = '' OR COALESCE(c.out_user_id, '') = $4)
		  AND ($5 = '' OR a.account_no > $5)
		ORDER BY a.account_no ASC
		LIMIT $6
	`, strings.TrimSpace(filter.MerchantNo), strings.TrimSpace(filter.AccountNo), strings.TrimSpace(filter.CustomerNo), strings.TrimSpace(filter.OutUserID), strings.TrimSpace(filter.CursorNo), pageSize+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	items := make([]AdminAccountRow, 0, pageSize+1)
	for rows.Next() {
		var item AdminAccountRow
		if err := rows.Scan(
			&item.AccountNo,
			&item.MerchantNo,
			&item.CustomerNo,
			&item.OwnerOutUserID,
			&item.AccountType,
			&item.AllowOverdraft,
			&item.MaxOverdraftLimit,
			&item.AllowDebitOut,
			&item.AllowCreditIn,
			&item.AllowTransfer,
			&item.BookEnabled,
			&item.Balance,
			&item.CreatedAt,
		); err != nil {
			return nil, "", err
		}
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextToken := ""
	if len(items) > pageSize {
		nextToken = items[pageSize-1].AccountNo
		items = items[:pageSize]
	}
	return items, nextToken, nil
}
