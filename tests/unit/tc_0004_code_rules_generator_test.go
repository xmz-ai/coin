package unit

import (
	"testing"

	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
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
