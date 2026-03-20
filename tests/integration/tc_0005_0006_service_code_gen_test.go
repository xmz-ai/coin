package integration

import (
	"errors"
	"testing"

	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

func TestTC0005ServiceAutoGeneratesMerchantAndCustomerCodes(t *testing.T) {
	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
		"01956f4e-8c11-71aa-b2d2-2b079f7e1001",
	})
	codes := idpkg.NewRuntimeCodeProvider()

	ms := service.NewMerchantService(repo, ids, codes)
	cs := service.NewCustomerService(repo, ids, codes)

	merchant, err := ms.CreateMerchant("", "demo")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	if !idpkg.IsValidMerchantNo(merchant.MerchantNo) {
		t.Fatalf("generated merchant_no invalid: %s", merchant.MerchantNo)
	}
	if !idpkg.IsValidAccountNo(merchant.BudgetAccountNo) || !idpkg.IsValidAccountNo(merchant.ReceivableAccountNo) {
		t.Fatalf("generated system account_no invalid: budget=%s receivable=%s", merchant.BudgetAccountNo, merchant.ReceivableAccountNo)
	}

	customer, err := cs.CreateCustomer(merchant.MerchantNo, "u_001")
	if err != nil {
		t.Fatalf("create customer failed: %v", err)
	}
	if !idpkg.IsValidCustomerNo(customer.CustomerNo) {
		t.Fatalf("generated customer_no invalid: %s", customer.CustomerNo)
	}
}

func TestTC0006RejectInvalidMerchantNoOverride(t *testing.T) {
	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)
	ids := idpkg.NewFixedUUIDProvider([]string{"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001"})

	ms := service.NewMerchantService(repo, ids, idpkg.NewRuntimeCodeProvider())
	_, err := ms.CreateMerchant("1000123456789012", "demo")
	if !errors.Is(err, service.ErrInvalidMerchantNo) {
		t.Fatalf("expected ErrInvalidMerchantNo, got %v", err)
	}
}
