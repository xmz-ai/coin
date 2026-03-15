package unit

import (
	"errors"
	"testing"

	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
	"github.com/xmz-ai/coin/tests/support/memoryrepo"
)

func TestTC0004RuntimeCodeProviderGeneratesRuleCompliantCodes(t *testing.T) {
	provider := idpkg.NewRuntimeCodeProvider()

	merchantNo, err := provider.NewMerchantNo()
	if err != nil {
		t.Fatalf("new merchant no failed: %v", err)
	}
	if !idpkg.IsValidMerchantNo(merchantNo) {
		t.Fatalf("merchant_no invalid: %s", merchantNo)
	}

	customerNo, err := provider.NewCustomerNo()
	if err != nil {
		t.Fatalf("new customer no failed: %v", err)
	}
	if !idpkg.IsValidCustomerNo(customerNo) {
		t.Fatalf("customer_no invalid: %s", customerNo)
	}

	accountNo, err := provider.NewAccountNo("1000179451308670", service.AccountTypeBudget)
	if err != nil {
		t.Fatalf("new account no failed: %v", err)
	}
	if !idpkg.IsValidAccountNo(accountNo) {
		t.Fatalf("account_no invalid: %s", accountNo)
	}
}

func TestTC0005ServiceAutoGeneratesMerchantAndCustomerCodes(t *testing.T) {
	repo := memoryrepo.New()
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
	repo := memoryrepo.New()
	ids := idpkg.NewFixedUUIDProvider([]string{"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001"})

	ms := service.NewMerchantService(repo, ids, idpkg.NewRuntimeCodeProvider())
	_, err := ms.CreateMerchant("1000123456789012", "demo")
	if !errors.Is(err, service.ErrInvalidMerchantNo) {
		t.Fatalf("expected ErrInvalidMerchantNo, got %v", err)
	}
}
