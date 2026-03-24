package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

func TestTC2001MerchantOnboardingCreatesBudgetAndReceivableAccounts(t *testing.T) {
	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)
	ids := idpkg.NewFixedUUIDProvider([]string{"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001"})
	svc := service.NewMerchantService(repo, ids)

	m, err := svc.CreateMerchant("", "demo")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	if m.BudgetAccountNo == "" || m.ReceivableAccountNo == "" {
		t.Fatalf("expected budget and receivable account numbers to be generated")
	}
	if !idpkg.IsValidMerchantNo(m.MerchantNo) {
		t.Fatalf("generated merchant_no invalid: %s", m.MerchantNo)
	}
	if !idpkg.IsValidAccountNo(m.BudgetAccountNo) || !idpkg.IsValidAccountNo(m.ReceivableAccountNo) {
		t.Fatalf("generated account_no invalid: budget=%s receivable=%s", m.BudgetAccountNo, m.ReceivableAccountNo)
	}
	if m.BudgetAccountNo == m.ReceivableAccountNo {
		t.Fatalf("expected budget and receivable account numbers to be different")
	}

	budget, ok := repo.GetAccount(m.BudgetAccountNo)
	if !ok || budget.MerchantNo != m.MerchantNo || budget.AccountType != service.AccountTypeBudget {
		t.Fatalf("budget account binding mismatch")
	}
	if !budget.AllowOverdraft || budget.MaxOverdraftLimit != 0 {
		t.Fatalf("budget account overdraft defaults mismatch: allow_overdraft=%v max_overdraft_limit=%d", budget.AllowOverdraft, budget.MaxOverdraftLimit)
	}
	if budget.BookEnabled {
		t.Fatalf("merchant budget account should not enable book ledger by default")
	}
	recv, ok := repo.GetAccount(m.ReceivableAccountNo)
	if !ok || recv.MerchantNo != m.MerchantNo || recv.AccountType != service.AccountTypeReceivable {
		t.Fatalf("receivable account binding mismatch")
	}
	if recv.BookEnabled {
		t.Fatalf("merchant receivable account should not enable book ledger by default")
	}
	if books := queryAccountBooksByAccount(t, pool, m.BudgetAccountNo); len(books) != 0 {
		t.Fatalf("merchant budget account should not create account_book rows, got %+v", books)
	}
	if books := queryAccountBooksByAccount(t, pool, m.ReceivableAccountNo); len(books) != 0 {
		t.Fatalf("merchant receivable account should not create account_book rows, got %+v", books)
	}
}

func TestTC2002MerchantNoUniqueConstraint(t *testing.T) {
	repo := db.NewRepository(setupPostgresPool(t))
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c002",
	})
	svc := service.NewMerchantService(repo, ids)

	first, err := svc.CreateMerchant("", "demo")
	if err != nil {
		t.Fatalf("first create merchant failed: %v", err)
	}
	if _, err := svc.CreateMerchant(first.MerchantNo, "demo2"); !errors.Is(err, service.ErrMerchantNoExists) {
		t.Fatalf("expected ErrMerchantNoExists, got %v", err)
	}
}

func TestTC2002MerchantOnboardingRollsBackWhenSecondDefaultAccountCreateFails(t *testing.T) {
	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)
	ids := idpkg.NewFixedUUIDProvider([]string{"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001"})
	merchantNo, err := repo.NewMerchantNo()
	if err != nil {
		t.Fatalf("generate merchant_no failed: %v", err)
	}
	runtimeCodes := idpkg.NewRuntimeCodeProvider()
	budgetAccountNo, err := runtimeCodes.NewAccountNo(merchantNo, service.AccountTypeBudget)
	if err != nil {
		t.Fatalf("generate budget account_no failed: %v", err)
	}
	receivableAccountNo, err := runtimeCodes.NewAccountNo(merchantNo, service.AccountTypeReceivable)
	if err != nil {
		t.Fatalf("generate receivable account_no failed: %v", err)
	}
	codes := idpkg.NewFixedCodeProvider(nil, nil, []string{
		budgetAccountNo,
		receivableAccountNo,
	})
	svc := service.NewMerchantService(repo, ids, codes)

	if err := repo.CreateAccount(service.Account{
		AccountNo:     receivableAccountNo,
		MerchantNo:    "9999999999999999",
		AccountType:   service.AccountTypeReceivable,
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
	}); err != nil {
		t.Fatalf("seed conflicting account failed: %v", err)
	}

	_, err = svc.CreateMerchant(merchantNo, "demo")
	if !errors.Is(err, service.ErrAccountNoExists) {
		t.Fatalf("expected ErrAccountNoExists, got %v", err)
	}
	if _, ok := repo.GetMerchantByNo(merchantNo); ok {
		t.Fatalf("expected merchant create to roll back")
	}
	if _, ok := repo.GetAccount(budgetAccountNo); ok {
		t.Fatalf("expected first default account create to roll back")
	}
	if got := queryCountBySQL(t, pool, "SELECT COUNT(*) FROM merchant WHERE merchant_no = $1", merchantNo); got != 0 {
		t.Fatalf("expected rolled back merchant count=0, got=%d", got)
	}
	if got := queryCountBySQL(t, pool, "SELECT COUNT(*) FROM account WHERE merchant_no = $1", merchantNo); got != 0 {
		t.Fatalf("expected rolled back account count=0, got=%d", got)
	}

	var seededMerchantNo string
	if err := pool.QueryRow(context.Background(), "SELECT merchant_no FROM account WHERE account_no = $1", receivableAccountNo).Scan(&seededMerchantNo); err != nil {
		t.Fatalf("query seeded conflicting account failed: %v", err)
	}
	if seededMerchantNo != "9999999999999999" {
		t.Fatalf("expected seeded conflicting account to remain, got merchant_no=%s", seededMerchantNo)
	}
}

func TestTC2003CreateCustomerSuccess(t *testing.T) {
	repo := db.NewRepository(setupPostgresPool(t))
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
		"01956f4e-8c11-71aa-b2d2-2b079f7e1001",
	})
	ms := service.NewMerchantService(repo, ids)
	cs := service.NewCustomerService(repo, ids)

	m, err := ms.CreateMerchant("", "demo")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	c, err := cs.CreateCustomer(m.MerchantNo, "u_90001")
	if err != nil {
		t.Fatalf("create customer failed: %v", err)
	}
	if c.MerchantNo != m.MerchantNo || c.OutUserID != "u_90001" {
		t.Fatalf("customer binding mismatch")
	}
	if !idpkg.IsValidCustomerNo(c.CustomerNo) {
		t.Fatalf("generated customer_no invalid: %s", c.CustomerNo)
	}
}

func TestTC2003CreateCustomerAutoCreatesDefaultAccountWhenFeatureEnabled(t *testing.T) {
	repo := db.NewRepository(setupPostgresPool(t))
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
		"01956f4e-8c11-71aa-b2d2-2b079f7e1001",
	})
	ms := service.NewMerchantService(repo, ids)
	cs := service.NewCustomerService(repo, ids)

	m, err := ms.CreateMerchant("", "demo")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	if err := ms.UpsertMerchantFeatureConfig(m.MerchantNo, true, false); err != nil {
		t.Fatalf("upsert merchant feature config failed: %v", err)
	}

	c, err := cs.CreateCustomer(m.MerchantNo, "u_90001_auto")
	if err != nil {
		t.Fatalf("create customer failed: %v", err)
	}
	account, ok := repo.GetAccountByCustomerNo(m.MerchantNo, c.CustomerNo)
	if !ok {
		t.Fatalf("expected default account created for customer_no=%s", c.CustomerNo)
	}
	if account.CustomerNo != c.CustomerNo || account.MerchantNo != m.MerchantNo {
		t.Fatalf("default account binding mismatch: %+v", account)
	}
	if account.AccountType != "CUSTOMER" {
		t.Fatalf("expected account_type CUSTOMER, got %s", account.AccountType)
	}
	if !account.BookEnabled {
		t.Fatalf("expected default customer account book_enabled=true")
	}
}

func TestTC2004CustomerUniqueOnMerchantAndOutUserID(t *testing.T) {
	repo := db.NewRepository(setupPostgresPool(t))
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
		"01956f4e-8c11-71aa-b2d2-2b079f7e1001",
		"01956f4e-8c11-71aa-b2d2-2b079f7e1002",
	})
	ms := service.NewMerchantService(repo, ids)
	cs := service.NewCustomerService(repo, ids)

	m, err := ms.CreateMerchant("", "demo")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	if _, err := cs.CreateCustomer(m.MerchantNo, "u_90001"); err != nil {
		t.Fatalf("first create customer failed: %v", err)
	}
	if _, err := cs.CreateCustomer(m.MerchantNo, "u_90001"); !errors.Is(err, service.ErrCustomerExists) {
		t.Fatalf("expected ErrCustomerExists, got %v", err)
	}
}

func TestTC2005QueryMerchantConfigAndCustomerByOutUserID(t *testing.T) {
	repo := db.NewRepository(setupPostgresPool(t))
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
		"01956f4e-8c11-71aa-b2d2-2b079f7e1001",
	})
	ms := service.NewMerchantService(repo, ids)
	cs := service.NewCustomerService(repo, ids)

	m, err := ms.CreateMerchant("", "demo")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	created, err := cs.CreateCustomer(m.MerchantNo, "u_90001")
	if err != nil {
		t.Fatalf("create customer failed: %v", err)
	}

	cfg, ok := ms.GetMerchantConfigByNo(m.MerchantNo)
	if !ok {
		t.Fatalf("merchant config not found")
	}
	if cfg.MerchantID != m.MerchantID || cfg.BudgetAccountNo == "" || cfg.ReceivableAccountNo == "" {
		t.Fatalf("merchant config mismatch")
	}

	got, ok := cs.GetCustomerByOutUserID(m.MerchantNo, "u_90001")
	if !ok {
		t.Fatalf("customer not found")
	}
	if got.CustomerID != created.CustomerID {
		t.Fatalf("customer mismatch")
	}
}
