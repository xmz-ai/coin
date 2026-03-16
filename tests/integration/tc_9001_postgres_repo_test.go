package integration

import (
	"errors"
	"testing"

	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

func TestTC9001PostgresRepositoryCoreFlow(t *testing.T) {
	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001", // merchant
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c002", // duplicate merchant attempt
		"01956f4e-8c11-71aa-b2d2-2b079f7e1001", // customer
		"01956f4e-8c11-71aa-b2d2-2b079f7e1002", // duplicate customer attempt
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2001", // txn1
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2002", // txn2
	})

	ms := service.NewMerchantService(repo, ids)
	cs := service.NewCustomerService(repo, ids)
	ts := service.NewTransferService(repo, ids)

	m, err := ms.CreateMerchant("", "demo")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	if _, err := ms.CreateMerchant(m.MerchantNo, "demo2"); !errors.Is(err, service.ErrMerchantNoExists) {
		t.Fatalf("expected ErrMerchantNoExists, got %v", err)
	}

	c, err := cs.CreateCustomer(m.MerchantNo, "u_9001")
	if err != nil {
		t.Fatalf("create customer failed: %v", err)
	}
	if _, err := cs.CreateCustomer(m.MerchantNo, "u_9001"); !errors.Is(err, service.ErrCustomerExists) {
		t.Fatalf("expected ErrCustomerExists, got %v", err)
	}

	customerAccountNo := "6217701201900100011"
	if err := repo.CreateAccount(service.Account{
		AccountNo:     customerAccountNo,
		MerchantNo:    m.MerchantNo,
		CustomerNo:    c.CustomerNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
	}); err != nil {
		t.Fatalf("create customer account failed: %v", err)
	}

	resolved, ok := repo.GetAccountByCustomerNo(m.MerchantNo, c.CustomerNo)
	if !ok || resolved.AccountNo != customerAccountNo {
		t.Fatalf("account by customer resolve mismatch: %+v ok=%v", resolved, ok)
	}

	_, err = ts.Submit(service.TransferRequest{MerchantNo: m.MerchantNo, OutTradeNo: "ord_9001", Amount: 100})
	if err != nil {
		t.Fatalf("first transfer submit failed: %v", err)
	}
	_, err = ts.Submit(service.TransferRequest{MerchantNo: m.MerchantNo, OutTradeNo: "ord_9001", Amount: 200})
	if !errors.Is(err, service.ErrDuplicateOutTradeNo) {
		t.Fatalf("expected ErrDuplicateOutTradeNo, got %v", err)
	}
	if got := repo.TxnCount(); got != 1 {
		t.Fatalf("expected txn count=1, got %d", got)
	}

	repo.IncAppliedChange()
	repo.IncAppliedChange()
	if got := repo.AppliedChangeCount(); got != 3 {
		t.Fatalf("expected applied change count=3 (1 from transfer + 2 manual), got %d", got)
	}
}

