package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

type pgAccountChange struct {
	AccountNo    string
	Delta        int64
	BalanceAfter int64
}

type pgBookRow struct {
	BookNo    string
	ExpireAt  time.Time
	Balance   int64
	AccountNo string
}

type pgBookChange struct {
	AccountNo    string
	BookNo       string
	Delta        int64
	BalanceAfter int64
	ExpireAt     time.Time
}

type pgOutboxEvent struct {
	EventID    string
	TxnNo      string
	MerchantNo string
	OutTradeNo string
	Status     string
	RetryCount int
	NextRetry  *time.Time
}

var (
	autoPGOnce       sync.Once
	autoPGDSN        string
	autoPGContainer  string
	autoPGStartedNow bool
	autoPGErr        error
)

func loadMigrationSQL(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	upPaths, err := filepath.Glob(filepath.Join(repoRoot, "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("glob up migrations failed: %v", err)
	}
	if len(upPaths) == 0 {
		t.Fatalf("no up migrations found")
	}
	sort.Strings(upPaths)

	var sql strings.Builder
	for _, p := range upPaths {
		upBytes, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatalf("read up migration failed: %v", readErr)
		}
		sql.Write(upBytes)
		sql.WriteByte('\n')
	}
	return sql.String()
}

func setupPostgresPool(t testing.TB) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("COIN_TEST_POSTGRES_DSN")
	if dsn == "" {
		var err error
		dsn, err = ensureAutoPostgresDSN()
		if err != nil {
			t.Fatalf("bootstrap postgres failed: %v", err)
		}
	}

	adminPool, err := db.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect postgres failed: %v", err)
	}
	t.Cleanup(adminPool.Close)

	schemaName := testSchemaName(t.Name())
	if _, err := adminPool.Exec(context.Background(), "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create test schema failed: %v", err)
	}
	t.Logf("postgres test schema: %s", schemaName)

	pool, err := newPoolWithSearchPath(context.Background(), dsn, schemaName)
	if err != nil {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE")
		t.Fatalf("connect postgres with search_path failed: %v", err)
	}
	keepSchema := os.Getenv("COIN_TEST_PG_KEEP_SCHEMA") == "1"
	t.Cleanup(func() {
		pool.Close()
		if keepSchema {
			t.Logf("keeping test schema %s because COIN_TEST_PG_KEEP_SCHEMA=1", schemaName)
			return
		}
		if _, err := adminPool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE"); err != nil {
			t.Errorf("drop test schema failed: %v", err)
		}
	})

	upSQL := loadMigrationSQL(t)
	if _, err := pool.Exec(context.Background(), upSQL); err != nil {
		t.Fatalf("apply up migration failed: %v", err)
	}
	return pool
}

func ensureAutoPostgresDSN() (string, error) {
	autoPGOnce.Do(func() {
		dsn := strings.TrimSpace(os.Getenv("COIN_TEST_POSTGRES_DSN"))
		if dsn != "" {
			autoPGDSN = dsn
			return
		}

		if _, err := exec.LookPath("docker"); err != nil {
			autoPGErr = fmt.Errorf("COIN_TEST_POSTGRES_DSN not set and docker not found: %w", err)
			return
		}

		container := getenvWithDefault("COIN_TEST_PG_CONTAINER", "coin-test-postgres")
		port := getenvWithDefault("COIN_TEST_PG_PORT", "55432")
		user := getenvWithDefault("COIN_TEST_PG_USER", "postgres")
		password := getenvWithDefault("COIN_TEST_PG_PASSWORD", "postgres")
		dbName := getenvWithDefault("COIN_TEST_PG_DB", "coin_test")
		image := getenvWithDefault("COIN_TEST_PG_IMAGE", "postgres:16-alpine")
		autoPGContainer = container

		running, err := dockerContainerRunning(container)
		if err != nil {
			autoPGErr = err
			return
		}
		if !running {
			exists, err := dockerContainerExists(container)
			if err != nil {
				autoPGErr = err
				return
			}
			if exists {
				if out, err := exec.Command("docker", "rm", "-f", container).CombinedOutput(); err != nil {
					autoPGErr = fmt.Errorf("docker rm stale container %s failed: %v output=%s", container, err, strings.TrimSpace(string(out)))
					return
				}
			}
			out, err := exec.Command(
				"docker", "run", "-d", "--rm",
				"--name", container,
				"-e", "POSTGRES_USER="+user,
				"-e", "POSTGRES_PASSWORD="+password,
				"-e", "POSTGRES_DB="+dbName,
				"-p", port+":5432",
				image,
			).CombinedOutput()
			if err != nil {
				autoPGErr = fmt.Errorf("docker run postgres failed: %v output=%s", err, strings.TrimSpace(string(out)))
				return
			}
			autoPGStartedNow = true
		}

		if err := waitPostgresReady(container, user, dbName, 60*time.Second); err != nil {
			autoPGErr = err
			return
		}

		autoPGDSN = fmt.Sprintf("postgres://%s:%s@localhost:%s/%s?sslmode=disable", user, password, port, dbName)
		_ = os.Setenv("COIN_TEST_POSTGRES_DSN", autoPGDSN)
	})

	if autoPGErr != nil {
		return "", autoPGErr
	}
	return autoPGDSN, nil
}

func stopAutoPostgresContainer() {
	if !autoPGStartedNow || strings.TrimSpace(autoPGContainer) == "" {
		return
	}
	_, _ = exec.Command("docker", "stop", autoPGContainer).CombinedOutput()
}

func dockerContainerRunning(name string) (bool, error) {
	out, err := exec.Command("docker", "ps", "--format", "{{.Names}}").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("docker ps failed: %v output=%s", err, strings.TrimSpace(string(out)))
	}
	return hasExactLine(string(out), name), nil
}

func dockerContainerExists(name string) (bool, error) {
	out, err := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("docker ps -a failed: %v output=%s", err, strings.TrimSpace(string(out)))
	}
	return hasExactLine(string(out), name), nil
}

func waitPostgresReady(container, user, dbName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := exec.Command("docker", "exec", container, "pg_isready", "-U", user, "-d", dbName).Run(); err == nil {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("postgres container %s not ready before timeout", container)
}

func hasExactLine(output, name string) bool {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

func getenvWithDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func newPoolWithSearchPath(ctx context.Context, dsn, schemaName string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schemaName + ",public"
	cfg.MaxConns = 10

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func testSchemaName(testName string) string {
	base := strings.ToLower(testName)
	base = strings.ReplaceAll(base, "/", "_")

	var b strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}

	safe := b.String()
	if safe == "" {
		safe = "it"
	}
	if len(safe) > 30 {
		safe = safe[:30]
	}
	return "it_" + safe + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func setupPostgresTransferFixture(t testing.TB, txnStatus string, amount int64) (*db.Repository, *pgxpool.Pool, service.Merchant, string, string, string) {
	t.Helper()

	pool := setupPostgresPool(t)
	repoImpl := db.NewRepository(pool)
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7d001",
		"01956f4e-8c11-71aa-b2d2-2b079f7e2001",
		"01956f4e-8c11-71aa-b2d2-2b079f7e2002",
	})

	merchantSvc := service.NewMerchantService(repoImpl, ids)
	customerSvc := service.NewCustomerService(repoImpl, ids)

	merchant, err := merchantSvc.CreateMerchant("", "pg-accounting")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	debitCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, "u_9010_debit")
	if err != nil {
		t.Fatalf("create debit customer failed: %v", err)
	}
	creditCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, "u_9010_credit")
	if err != nil {
		t.Fatalf("create credit customer failed: %v", err)
	}

	debitAccountNo := "6217701201901001001"
	creditAccountNo := "6217701201901001002"
	if err := repoImpl.CreateAccount(service.Account{
		AccountNo:     debitAccountNo,
		MerchantNo:    merchant.MerchantNo,
		CustomerNo:    debitCustomer.CustomerNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
		Balance:       1000,
	}); err != nil {
		t.Fatalf("create debit account failed: %v", err)
	}
	if err := repoImpl.CreateAccount(service.Account{
		AccountNo:     creditAccountNo,
		MerchantNo:    merchant.MerchantNo,
		CustomerNo:    creditCustomer.CustomerNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
		Balance:       200,
	}); err != nil {
		t.Fatalf("create credit account failed: %v", err)
	}

	txnNo := fmt.Sprintf("01956f4e-9d22-73bc-8e11-3f5e9c7a91%02d", amount%100)
	if err := repoImpl.CreateTransferTxn(service.TransferTxn{
		TxnNo:            txnNo,
		MerchantNo:       merchant.MerchantNo,
		OutTradeNo:       fmt.Sprintf("ord_901x_%d_%s", amount, txnStatus),
		BizType:          "TRANSFER",
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   debitAccountNo,
		CreditAccountNo:  creditAccountNo,
		Amount:           amount,
		RefundableAmount: amount,
		Status:           txnStatus,
	}); err != nil {
		t.Fatalf("create txn failed: %v", err)
	}

	return repoImpl, pool, merchant, debitAccountNo, creditAccountNo, txnNo
}

func queryAccountChangesByTxnNo(t *testing.T, pool *pgxpool.Pool, txnNo string) []pgAccountChange {
	t.Helper()

	rows, err := pool.Query(context.Background(), `
SELECT account_no, delta, balance_after
FROM account_change_log
WHERE txn_no = $1::uuid
ORDER BY change_id ASC
`, txnNo)
	if err != nil {
		t.Fatalf("query change log failed: %v", err)
	}
	defer rows.Close()

	out := make([]pgAccountChange, 0)
	for rows.Next() {
		var item pgAccountChange
		if err := rows.Scan(&item.AccountNo, &item.Delta, &item.BalanceAfter); err != nil {
			t.Fatalf("scan change log failed: %v", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate change log failed: %v", err)
	}
	return out
}

func queryAccountBooksByAccount(t *testing.T, pool *pgxpool.Pool, accountNo string) []pgBookRow {
	t.Helper()

	rows, err := pool.Query(context.Background(), `
SELECT book_no::text, account_no, expire_at, balance
FROM account_book
WHERE account_no = $1
ORDER BY expire_at ASC, book_no ASC
`, accountNo)
	if err != nil {
		t.Fatalf("query account_book failed: %v", err)
	}
	defer rows.Close()

	out := make([]pgBookRow, 0)
	for rows.Next() {
		var item pgBookRow
		if err := rows.Scan(&item.BookNo, &item.AccountNo, &item.ExpireAt, &item.Balance); err != nil {
			t.Fatalf("scan account_book failed: %v", err)
		}
		item.ExpireAt = item.ExpireAt.UTC()
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate account_book failed: %v", err)
	}
	return out
}

func queryBookChangesByTxnNo(t *testing.T, pool *pgxpool.Pool, txnNo string) []pgBookChange {
	t.Helper()

	rows, err := pool.Query(context.Background(), `
SELECT account_no, book_no::text, delta, balance_after, expire_at
FROM account_book_change_log
WHERE txn_no = $1::uuid
ORDER BY change_id ASC
`, txnNo)
	if err != nil {
		t.Fatalf("query account_book_change_log failed: %v", err)
	}
	defer rows.Close()

	out := make([]pgBookChange, 0)
	for rows.Next() {
		var item pgBookChange
		if err := rows.Scan(&item.AccountNo, &item.BookNo, &item.Delta, &item.BalanceAfter, &item.ExpireAt); err != nil {
			t.Fatalf("scan account_book_change_log failed: %v", err)
		}
		item.ExpireAt = item.ExpireAt.UTC()
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate account_book_change_log failed: %v", err)
	}
	return out
}

func queryOutboxEventsByTxnNo(t *testing.T, pool *pgxpool.Pool, txnNo string) []pgOutboxEvent {
	t.Helper()

	rows, err := pool.Query(context.Background(), `
SELECT event_id::text, txn_no::text, merchant_no, COALESCE(out_trade_no, ''), status, retry_count, next_retry_at
FROM outbox_event
WHERE txn_no = $1::uuid
ORDER BY created_at ASC, id ASC
`, txnNo)
	if err != nil {
		t.Fatalf("query outbox_event failed: %v", err)
	}
	defer rows.Close()

	out := make([]pgOutboxEvent, 0)
	for rows.Next() {
		var (
			item      pgOutboxEvent
			retryCnt  int32
			nextRetry *time.Time
		)
		if err := rows.Scan(&item.EventID, &item.TxnNo, &item.MerchantNo, &item.OutTradeNo, &item.Status, &retryCnt, &nextRetry); err != nil {
			t.Fatalf("scan outbox_event failed: %v", err)
		}
		item.RetryCount = int(retryCnt)
		if nextRetry != nil {
			v := nextRetry.UTC()
			item.NextRetry = &v
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate outbox_event failed: %v", err)
	}
	return out
}
