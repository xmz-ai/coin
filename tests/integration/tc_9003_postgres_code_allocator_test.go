package integration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

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
	if stored.Balance != 0 {
		t.Fatalf("account overwritten: expected balance=0 got=%d", stored.Balance)
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

func TestTC9006CreateBookEnabledAccountAutoInitNoExpireBook(t *testing.T) {
	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)

	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c002",
	})
	ms := service.NewMerchantService(repo, ids)

	merchant, err := ms.CreateMerchant("", "demo-book-init")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	accountNo, err := repo.NewAccountNo(merchant.MerchantNo, "CUSTOMER")
	if err != nil {
		t.Fatalf("new account_no failed: %v", err)
	}

	if err := repo.CreateAccount(service.Account{
		AccountNo:      accountNo,
		MerchantNo:     merchant.MerchantNo,
		AccountType:    "CUSTOMER",
		AllowDebitOut:  true,
		AllowCreditIn:  true,
		AllowTransfer:  true,
		BookEnabled:    true,
		AllowOverdraft: false,
	}); err != nil {
		t.Fatalf("create book-enabled account failed: %v", err)
	}

	books := queryAccountBooksByAccount(t, pool, accountNo)
	if len(books) != 1 {
		t.Fatalf("expected 1 no-expire account_book row, got %d rows=%+v", len(books), books)
	}
	noExpire := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	if !books[0].ExpireAt.Equal(noExpire) || books[0].Balance != 0 {
		t.Fatalf("unexpected no-expire account_book row: %+v", books[0])
	}

	var sum int64
	if err := pool.QueryRow(context.Background(), `
SELECT COALESCE(SUM(balance), 0)::bigint
FROM account_book
WHERE account_no = $1
`, accountNo).Scan(&sum); err != nil {
		t.Fatalf("query account_book sum failed: %v", err)
	}
	account, ok := repo.GetAccount(accountNo)
	if !ok {
		t.Fatalf("account not found: %s", accountNo)
	}
	if account.Balance != sum {
		t.Fatalf("expected account.balance == sum(account_book.balance), got account=%d sum=%d", account.Balance, sum)
	}
}
