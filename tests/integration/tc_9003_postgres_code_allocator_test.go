package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
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

func TestTC9003PostgresCodeAllocatorConcurrentUniqueness(t *testing.T) {
	pool := setupPostgresPool(t)
	repoA := db.NewRepository(pool)
	repoB := db.NewRepository(pool)

	const total = 500
	got := make(map[string]struct{}, total)
	errCh := make(chan error, total)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			var (
				code string
				err  error
			)
			if i%2 == 0 {
				code, err = repoA.NewMerchantNo()
			} else {
				code, err = repoB.NewMerchantNo()
			}
			if err != nil {
				errCh <- fmt.Errorf("new merchant_no failed: %w", err)
				return
			}
			if !idpkg.IsValidMerchantNo(code) {
				errCh <- fmt.Errorf("invalid merchant_no: %s", code)
				return
			}

			mu.Lock()
			if _, exists := got[code]; exists {
				mu.Unlock()
				errCh <- fmt.Errorf("duplicate merchant_no: %s", code)
				return
			}
			got[code] = struct{}{}
			mu.Unlock()
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != total {
		t.Fatalf("expected %d merchant_no values, got %d", total, len(got))
	}
}

func TestTC9004PostgresCodeAllocatorNoDuplicateAfterRestart(t *testing.T) {
	pool := setupPostgresPool(t)
	repo1 := db.NewRepository(pool)

	firstBatch := make(map[string]struct{}, 200)
	for i := 0; i < 200; i++ {
		code, err := repo1.NewMerchantNo()
		if err != nil {
			t.Fatalf("repo1 new merchant_no failed: %v", err)
		}
		if !idpkg.IsValidMerchantNo(code) {
			t.Fatalf("repo1 invalid merchant_no: %s", code)
		}
		firstBatch[code] = struct{}{}
	}

	// Simulate process restart: a new repository instance with empty in-memory cache.
	repo2 := db.NewRepository(pool)
	for i := 0; i < 200; i++ {
		code, err := repo2.NewMerchantNo()
		if err != nil {
			t.Fatalf("repo2 new merchant_no failed: %v", err)
		}
		if !idpkg.IsValidMerchantNo(code) {
			t.Fatalf("repo2 invalid merchant_no: %s", code)
		}
		if _, exists := firstBatch[code]; exists {
			t.Fatalf("merchant_no duplicated after restart: %s", code)
		}
	}
}

func TestTC9005PostgresAccountNoConflictDoesNotOverwrite(t *testing.T) {
	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)

	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
	})
	ms := service.NewMerchantService(repo, ids)

	merchant, err := ms.CreateMerchant("", "demo")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}

	accountNo, err := repo.NewAccountNo(merchant.MerchantNo, "CUSTOMER")
	if err != nil {
		t.Fatalf("new account_no failed: %v", err)
	}
	if !idpkg.IsValidAccountNo(accountNo) {
		t.Fatalf("invalid account_no: %s", accountNo)
	}

	err = repo.CreateAccount(service.Account{
		AccountNo:      accountNo,
		MerchantNo:     merchant.MerchantNo,
		AccountType:    "CUSTOMER",
		AllowDebitOut:  true,
		AllowCreditIn:  true,
		AllowTransfer:  true,
		BookEnabled:    false,
		Balance:        100,
		AllowOverdraft: false,
	})
	if err != nil {
		t.Fatalf("first create account failed: %v", err)
	}

	err = repo.CreateAccount(service.Account{
		AccountNo:         accountNo,
		MerchantNo:        merchant.MerchantNo,
		AccountType:       "CUSTOMER",
		AllowDebitOut:     false,
		AllowCreditIn:     false,
		AllowTransfer:     false,
		BookEnabled:       true,
		Balance:           999,
		AllowOverdraft:    true,
		MaxOverdraftLimit: 777,
	})
	if !errors.Is(err, service.ErrAccountNoExists) {
		t.Fatalf("expected ErrAccountNoExists, got %v", err)
	}

	stored, ok := repo.GetAccount(accountNo)
	if !ok {
		t.Fatalf("stored account not found after conflict")
	}
	if stored.Balance != 100 {
		t.Fatalf("account overwritten: expected balance=100 got=%d", stored.Balance)
	}
	if !stored.AllowDebitOut || !stored.AllowCreditIn || !stored.AllowTransfer {
		t.Fatalf("account capability overwritten: %+v", stored)
	}
	if stored.BookEnabled {
		t.Fatalf("account book_enabled overwritten: %+v", stored)
	}
	if stored.AllowOverdraft {
		t.Fatalf("account allow_overdraft overwritten: %+v", stored)
	}
}

func setupPostgresPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("COIN_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("COIN_TEST_POSTGRES_DSN not set")
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
