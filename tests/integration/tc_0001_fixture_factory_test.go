package integration

import (
	"testing"
	"time"

	clockpkg "github.com/xmz-ai/coin/internal/platform/clock"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/testkit/factory"
)

func TestTC0001FixtureFactoryCreatesLinkedData(t *testing.T) {
	fixed := time.Date(2026, 3, 13, 9, 30, 0, 0, time.UTC)
	clk := clockpkg.NewFixed(fixed)
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001", // merchant_id
		"01956f4e-8c11-71aa-b2d2-2b079f7e1001", // customer_id
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2001", // txn_no
	})

	f := factory.New(factory.Dependencies{Clock: clk, UUIDProvider: ids})

	merchant, err := f.NewMerchant("1000123456789012")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	if merchant.MerchantNo != "1000123456789012" {
		t.Fatalf("merchant_no mismatch: %s", merchant.MerchantNo)
	}
	if !merchant.CreatedAt.Equal(fixed) {
		t.Fatalf("merchant created_at mismatch: %v", merchant.CreatedAt)
	}

	customer, err := f.NewCustomer(merchant.MerchantID, "u_001")
	if err != nil {
		t.Fatalf("create customer failed: %v", err)
	}
	if customer.MerchantID != merchant.MerchantID {
		t.Fatalf("customer merchant_id mismatch: %s vs %s", customer.MerchantID, merchant.MerchantID)
	}

	account, err := f.NewAccount(merchant.MerchantID, customer.CustomerID, "6217701201001234567")
	if err != nil {
		t.Fatalf("create account failed: %v", err)
	}
	if account.CustomerID != customer.CustomerID {
		t.Fatalf("account customer_id mismatch: %s vs %s", account.CustomerID, customer.CustomerID)
	}

	txn, err := f.NewTxn(merchant.MerchantID, "ord_20260313_000001", account.AccountNo, 100)
	if err != nil {
		t.Fatalf("create txn failed: %v", err)
	}
	if txn.MerchantID != merchant.MerchantID {
		t.Fatalf("txn merchant_id mismatch: %s vs %s", txn.MerchantID, merchant.MerchantID)
	}
	if txn.OutTradeNo != "ord_20260313_000001" {
		t.Fatalf("txn out_trade_no mismatch: %s", txn.OutTradeNo)
	}
	if txn.Amount != 100 {
		t.Fatalf("txn amount mismatch: %d", txn.Amount)
	}
}
