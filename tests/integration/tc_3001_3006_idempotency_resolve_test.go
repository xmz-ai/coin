package integration

import (
	"errors"
	"testing"

	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

func TestTC3001DuplicateOutTradeNoReturnsConflict(t *testing.T) {
	repo := db.NewRepository(setupPostgresPool(t))
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001", // merchant
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2001", // txn1
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2002", // txn2
	})
	ms := service.NewMerchantService(repo, ids)
	ts := service.NewTransferService(repo, ids)

	m, err := ms.CreateMerchant("", "demo")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}

	_, err = ts.Submit(service.TransferRequest{MerchantNo: m.MerchantNo, OutTradeNo: "ord_3001", Amount: 100})
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	_, err = ts.Submit(service.TransferRequest{MerchantNo: m.MerchantNo, OutTradeNo: "ord_3001", Amount: 999})
	if !errors.Is(err, service.ErrDuplicateOutTradeNo) {
		t.Fatalf("expected ErrDuplicateOutTradeNo, got %v", err)
	}
}

func TestTC3002DuplicateRequestHasNoSideEffects(t *testing.T) {
	repo := db.NewRepository(setupPostgresPool(t))
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001", // merchant
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2001", // txn1
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2002", // txn2
	})
	ms := service.NewMerchantService(repo, ids)
	ts := service.NewTransferService(repo, ids)

	m, _ := ms.CreateMerchant("", "demo")
	_, _ = ts.Submit(service.TransferRequest{MerchantNo: m.MerchantNo, OutTradeNo: "ord_3002", Amount: 100})
	beforeTxn := repo.TxnCount()
	beforeApplied := repo.AppliedChangeCount()

	_, _ = ts.Submit(service.TransferRequest{MerchantNo: m.MerchantNo, OutTradeNo: "ord_3002", Amount: 500})

	if repo.TxnCount() != beforeTxn {
		t.Fatalf("txn count changed on duplicate: before=%d after=%d", beforeTxn, repo.TxnCount())
	}
	if repo.AppliedChangeCount() != beforeApplied {
		t.Fatalf("applied change count changed on duplicate: before=%d after=%d", beforeApplied, repo.AppliedChangeCount())
	}
}

func TestTC3003AccountNoAndOutUserIDConsistentPasses(t *testing.T) {
	repo := db.NewRepository(setupPostgresPool(t))
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001", // merchant
		"01956f4e-8c11-71aa-b2d2-2b079f7e1001", // customer
	})
	ms := service.NewMerchantService(repo, ids)
	cs := service.NewCustomerService(repo, ids)
	rs := service.NewAccountResolver(repo)

	m, _ := ms.CreateMerchant("", "demo")
	if err := ms.UpsertMerchantFeatureConfig(m.MerchantNo, false, true); err != nil {
		t.Fatalf("upsert merchant feature config failed: %v", err)
	}
	c, _ := cs.CreateCustomer(m.MerchantNo, "u_3003")
	repo.CreateAccount(service.Account{AccountNo: "1000000000003003001", MerchantNo: m.MerchantNo, CustomerNo: c.CustomerNo, AccountType: "CUSTOMER", AllowDebitOut: true, AllowCreditIn: true, AllowTransfer: true})

	accountNo, err := rs.ResolveCustomerAccount(m.MerchantNo, "1000000000003003001", "u_3003")
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if accountNo != "1000000000003003001" {
		t.Fatalf("unexpected account_no: %s", accountNo)
	}
}

func TestTC3004AccountNoAndOutUserIDConflictRejected(t *testing.T) {
	repo := db.NewRepository(setupPostgresPool(t))
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001", // merchant
		"01956f4e-8c11-71aa-b2d2-2b079f7e1001", // customer1
		"01956f4e-8c11-71aa-b2d2-2b079f7e1002", // customer2
	})
	ms := service.NewMerchantService(repo, ids)
	cs := service.NewCustomerService(repo, ids)
	rs := service.NewAccountResolver(repo)

	m, _ := ms.CreateMerchant("", "demo")
	if err := ms.UpsertMerchantFeatureConfig(m.MerchantNo, false, true); err != nil {
		t.Fatalf("upsert merchant feature config failed: %v", err)
	}
	c1, _ := cs.CreateCustomer(m.MerchantNo, "u_3004_1")
	c2, _ := cs.CreateCustomer(m.MerchantNo, "u_3004_2")
	repo.CreateAccount(service.Account{AccountNo: "1000000000003004001", MerchantNo: m.MerchantNo, CustomerNo: c1.CustomerNo, AccountType: "CUSTOMER", AllowDebitOut: true, AllowCreditIn: true, AllowTransfer: true})
	repo.CreateAccount(service.Account{AccountNo: "1000000000003004002", MerchantNo: m.MerchantNo, CustomerNo: c2.CustomerNo, AccountType: "CUSTOMER", AllowDebitOut: true, AllowCreditIn: true, AllowTransfer: true})

	_, err := rs.ResolveCustomerAccount(m.MerchantNo, "1000000000003004001", "u_3004_2")
	if !errors.Is(err, service.ErrAccountResolveConflict) {
		t.Fatalf("expected ErrAccountResolveConflict, got %v", err)
	}
}

func TestTC3005OutUserIDNotUsedForMerchantSystemAccount(t *testing.T) {
	repo := db.NewRepository(setupPostgresPool(t))
	ids := idpkg.NewFixedUUIDProvider([]string{"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001"})
	ms := service.NewMerchantService(repo, ids)
	rs := service.NewAccountResolver(repo)

	m, _ := ms.CreateMerchant("", "demo")
	_, err := rs.ResolveMerchantSystemAccount(m.MerchantNo, "", "u_only", service.AccountTypeBudget)
	if !errors.Is(err, service.ErrOutUserIDNotAllowedForSystemAccount) {
		t.Fatalf("expected ErrOutUserIDNotAllowedForSystemAccount, got %v", err)
	}
}

func TestTC3006ProcessingKeyPreventsDuplicateExecution(t *testing.T) {
	guard := service.NewProcessingGuard()
	if ok := guard.TryBegin("txn_3006", service.TxnStatusPaySuccess); !ok {
		t.Fatalf("first begin should succeed")
	}
	if ok := guard.TryBegin("txn_3006", service.TxnStatusPaySuccess); ok {
		t.Fatalf("second begin should be blocked by processing key")
	}
}
