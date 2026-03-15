package integration

import (
	"errors"
	"testing"
	"time"

	"github.com/xmz-ai/coin/internal/service"
	"github.com/xmz-ai/coin/tests/support/memoryrepo"
)

func TestTC5001NonExpiryAccountCannotWriteBook(t *testing.T) {
	repo := memoryrepo.New()
	svc := service.NewExpiryBookService(repo)

	repo.CreateAccount(service.Account{AccountNo: "acc_non_expiry", MerchantNo: "m1", BookEnabled: false, Balance: 0, AllowDebitOut: true, AllowCreditIn: true, AllowTransfer: true})
	err := svc.Credit("acc_non_expiry", 100, time.Now().UTC().Add(24*time.Hour))
	if !errors.Is(err, service.ErrBookDisabled) {
		t.Fatalf("expected ErrBookDisabled, got %v", err)
	}
}

func TestTC5002FEFODebitOrderAndOnlyExpireAfterNow(t *testing.T) {
	repo := memoryrepo.New()
	svc := service.NewExpiryBookService(repo)
	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	svc.SetNow(now)

	repo.CreateAccount(service.Account{AccountNo: "acc_expiry", MerchantNo: "m1", BookEnabled: true, Balance: 0, AllowDebitOut: true, AllowCreditIn: true, AllowTransfer: true})
	_ = svc.Credit("acc_expiry", 100, now.Add(1*time.Hour))
	_ = svc.Credit("acc_expiry", 100, now.Add(2*time.Hour))
	_ = svc.Credit("acc_expiry", 100, now) // boundary, should not be used for debit

	parts, err := svc.Debit("acc_expiry", 150)
	if err != nil {
		t.Fatalf("debit failed: %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 debit parts, got %d", len(parts))
	}
	if !parts[0].ExpireAt.Equal(now.Add(1*time.Hour)) || parts[0].Amount != 100 {
		t.Fatalf("first part should consume earliest valid book")
	}
	if !parts[1].ExpireAt.Equal(now.Add(2*time.Hour)) || parts[1].Amount != 50 {
		t.Fatalf("second part should consume next valid book")
	}
}

func TestTC5003ExpireAtEqualNowExcluded(t *testing.T) {
	repo := memoryrepo.New()
	svc := service.NewExpiryBookService(repo)
	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	svc.SetNow(now)

	repo.CreateAccount(service.Account{AccountNo: "acc_expiry_2", MerchantNo: "m1", BookEnabled: true, Balance: 0, AllowDebitOut: true, AllowCreditIn: true, AllowTransfer: true})
	_ = svc.Credit("acc_expiry_2", 100, now)
	_ = svc.Credit("acc_expiry_2", 100, now.Add(1*time.Hour))

	parts, err := svc.Debit("acc_expiry_2", 80)
	if err != nil {
		t.Fatalf("debit failed: %v", err)
	}
	if len(parts) != 1 || !parts[0].ExpireAt.Equal(now.Add(1*time.Hour)) {
		t.Fatalf("expected only expire_at>now book to be used")
	}
}

func TestTC5004ExpiryCreditRequiresExpireAt(t *testing.T) {
	repo := memoryrepo.New()
	svc := service.NewExpiryBookService(repo)

	repo.CreateAccount(service.Account{AccountNo: "acc_expiry_3", MerchantNo: "m1", BookEnabled: true, Balance: 0, AllowDebitOut: true, AllowCreditIn: true, AllowTransfer: true})
	err := svc.Credit("acc_expiry_3", 100, time.Time{})
	if !errors.Is(err, service.ErrExpireAtRequired) {
		t.Fatalf("expected ErrExpireAtRequired, got %v", err)
	}
}

func TestTC5005AccountBalanceEqualsBookSum(t *testing.T) {
	repo := memoryrepo.New()
	svc := service.NewExpiryBookService(repo)
	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)
	svc.SetNow(now)

	repo.CreateAccount(service.Account{AccountNo: "acc_expiry_4", MerchantNo: "m1", BookEnabled: true, Balance: 0, AllowDebitOut: true, AllowCreditIn: true, AllowTransfer: true})
	_ = svc.Credit("acc_expiry_4", 100, now.Add(1*time.Hour))
	_ = svc.Credit("acc_expiry_4", 60, now.Add(2*time.Hour))
	_, _ = svc.Debit("acc_expiry_4", 30)

	ok := svc.VerifyAccountBookBalance("acc_expiry_4")
	if !ok {
		t.Fatalf("expected account.balance == sum(account_book.balance)")
	}
}
