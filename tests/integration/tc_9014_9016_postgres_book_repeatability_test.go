package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

func TestTC9014PostgresNonBookFlowDoesNotWriteBookTables(t *testing.T) {
	repo, pool, _, debitAccountNo, creditAccountNo, txnNo := setupPostgresTransferFixture(t, service.TxnStatusInit, 160)

	processor := service.NewTransferAsyncProcessor(repo)
	processor.Enqueue(txnNo)
	waitTxnStatusRepo(t, repo, txnNo, service.TxnStatusRecvSuccess, 2*time.Second)

	accountBookCnt := queryCountBySQL(t, pool, `
SELECT COUNT(*)
FROM account_book
WHERE account_no = ANY($1::varchar[])
`, []string{debitAccountNo, creditAccountNo})
	if accountBookCnt != 0 {
		t.Fatalf("non-book flow should not write account_book, got=%d", accountBookCnt)
	}

	bookChangeCnt := queryCountBySQL(t, pool, `
SELECT COUNT(*)
FROM account_book_change_log
WHERE txn_no = $1::uuid
`, txnNo)
	if bookChangeCnt != 0 {
		t.Fatalf("non-book flow should not write account_book_change_log, got=%d", bookChangeCnt)
	}
}

func TestTC9015PostgresSetupPoolResetsSchemaData(t *testing.T) {
	poolA := setupPostgresPool(t)
	repoA := db.NewRepository(poolA)
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7e001",
	})

	merchantSvc := service.NewMerchantService(repoA, ids)
	if _, err := merchantSvc.CreateMerchant("", "repeatability-check"); err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}

	if got := queryTableCount(t, poolA, "merchant"); got != 1 {
		t.Fatalf("expected merchant count=1 before reset, got=%d", got)
	}
	if got := queryTableCount(t, poolA, "account"); got != 3 {
		t.Fatalf("expected account count=3 before reset, got=%d", got)
	}

	poolB := setupPostgresPool(t)
	for _, table := range []string{
		"merchant",
		"customer",
		"account",
		"txn",
		"account_change_log",
		"account_book",
		"account_book_change_log",
		"outbox_event",
		"webhook_config",
	} {
		if got := queryTableCount(t, poolB, table); got != 0 {
			t.Fatalf("expected %s count=0 after reset, got=%d", table, got)
		}
	}
}

func TestTC9016PostgresBookTablesPresentAfterMigration(t *testing.T) {
	pool := setupPostgresPool(t)
	assertTableExists(t, pool, "account_book")
	assertTableExists(t, pool, "account_book_change_log")
}

func queryCountBySQL(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()

	var cnt int64
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&cnt); err != nil {
		t.Fatalf("query count failed: %v", err)
	}
	return cnt
}

func queryTableCount(t *testing.T, pool *pgxpool.Pool, table string) int64 {
	t.Helper()
	return queryCountBySQL(t, pool, fmt.Sprintf("SELECT COUNT(*) FROM %s", table))
}

func assertTableExists(t *testing.T, pool *pgxpool.Pool, table string) {
	t.Helper()

	var regclass string
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass($1)", table).Scan(&regclass); err != nil {
		t.Fatalf("query table exists failed: %v", err)
	}
	if regclass == "" {
		t.Fatalf("table %s not found", table)
	}
}
