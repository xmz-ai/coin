package integration

import (
	"errors"
	"testing"

	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/service"
)

func TestTC4001IssueDefaultsDebitToBudget(t *testing.T) {
	repo, m, from, to := setupTransferAccounts(t)
	svc := service.NewTransferRoutingService(repo)

	res, err := svc.Resolve(service.TransferRoutingRequest{
		MerchantNo:      m.MerchantNo,
		Scene:           service.SceneIssue,
		DebitAccountNo:  "",
		CreditAccountNo: to,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if res.DebitAccountNo != m.BudgetAccountNo {
		t.Fatalf("expected budget account, got %s", res.DebitAccountNo)
	}
	if from == m.BudgetAccountNo {
		_ = from
	}
}

func TestTC4002IssueExplicitDebitOverridesDefault(t *testing.T) {
	repo, m, from, to := setupTransferAccounts(t)
	svc := service.NewTransferRoutingService(repo)

	res, err := svc.Resolve(service.TransferRoutingRequest{
		MerchantNo:      m.MerchantNo,
		Scene:           service.SceneIssue,
		DebitAccountNo:  from,
		CreditAccountNo: to,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if res.DebitAccountNo != from {
		t.Fatalf("expected explicit debit %s, got %s", from, res.DebitAccountNo)
	}
}

func TestTC4003ConsumeDefaultsCreditToReceivable(t *testing.T) {
	repo, m, from, _ := setupTransferAccounts(t)
	svc := service.NewTransferRoutingService(repo)

	res, err := svc.Resolve(service.TransferRoutingRequest{
		MerchantNo:     m.MerchantNo,
		Scene:          service.SceneConsume,
		DebitAccountNo: from,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if res.CreditAccountNo != m.ReceivableAccountNo {
		t.Fatalf("expected receivable account, got %s", res.CreditAccountNo)
	}
}

func TestTC4004ConsumeExplicitCreditOverridesDefault(t *testing.T) {
	repo, m, from, to := setupTransferAccounts(t)
	svc := service.NewTransferRoutingService(repo)

	res, err := svc.Resolve(service.TransferRoutingRequest{
		MerchantNo:      m.MerchantNo,
		Scene:           service.SceneConsume,
		DebitAccountNo:  from,
		CreditAccountNo: to,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if res.CreditAccountNo != to {
		t.Fatalf("expected explicit credit %s, got %s", to, res.CreditAccountNo)
	}
}

func TestTC4005P2PRequiresBothSides(t *testing.T) {
	repo, m, from, _ := setupTransferAccounts(t)
	svc := service.NewTransferRoutingService(repo)

	_, err := svc.Resolve(service.TransferRoutingRequest{
		MerchantNo:     m.MerchantNo,
		Scene:          service.SceneP2P,
		DebitAccountNo: from,
	})
	if !errors.Is(err, service.ErrAccountResolveFailed) {
		t.Fatalf("expected ErrAccountResolveFailed, got %v", err)
	}
}

func TestTC4006ForbidDebitRejected(t *testing.T) {
	repo, m, from, to := setupTransferAccounts(t)
	repo.UpdateAccountCapabilities(from, false, true, true)
	svc := service.NewTransferRoutingService(repo)

	_, err := svc.Resolve(service.TransferRoutingRequest{
		MerchantNo:      m.MerchantNo,
		Scene:           service.SceneIssue,
		DebitAccountNo:  from,
		CreditAccountNo: to,
	})
	if !errors.Is(err, service.ErrAccountForbidDebit) {
		t.Fatalf("expected ErrAccountForbidDebit, got %v", err)
	}
}

func TestTC4007ForbidCreditRejected(t *testing.T) {
	repo, m, from, to := setupTransferAccounts(t)
	repo.UpdateAccountCapabilities(to, true, false, true)
	svc := service.NewTransferRoutingService(repo)

	_, err := svc.Resolve(service.TransferRoutingRequest{
		MerchantNo:      m.MerchantNo,
		Scene:           service.SceneIssue,
		DebitAccountNo:  from,
		CreditAccountNo: to,
	})
	if !errors.Is(err, service.ErrAccountForbidCredit) {
		t.Fatalf("expected ErrAccountForbidCredit, got %v", err)
	}
}

func TestTC4008ForbidTransferRejected(t *testing.T) {
	repo, m, from, to := setupTransferAccounts(t)
	repo.UpdateAccountCapabilities(from, true, true, false)
	svc := service.NewTransferRoutingService(repo)

	_, err := svc.Resolve(service.TransferRoutingRequest{
		MerchantNo:      m.MerchantNo,
		Scene:           service.SceneP2P,
		DebitAccountNo:  from,
		CreditAccountNo: to,
	})
	if !errors.Is(err, service.ErrAccountForbidTransfer) {
		t.Fatalf("expected ErrAccountForbidTransfer, got %v", err)
	}
}

func TestTC4008P2PToSideForbidTransferAllowed(t *testing.T) {
	repo, m, from, to := setupTransferAccounts(t)
	repo.UpdateAccountCapabilities(to, true, true, false)
	svc := service.NewTransferRoutingService(repo)

	res, err := svc.Resolve(service.TransferRoutingRequest{
		MerchantNo:      m.MerchantNo,
		Scene:           service.SceneP2P,
		DebitAccountNo:  from,
		CreditAccountNo: to,
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if res.DebitAccountNo != from || res.CreditAccountNo != to {
		t.Fatalf("unexpected routing result: %+v", res)
	}
}

func TestTC4009StateMachineValidTransitions(t *testing.T) {
	sm := service.NewTxnStateMachine(service.TxnStatusInit)
	if err := sm.Transit(service.TxnStatusPaySuccess); err != nil {
		t.Fatalf("init->pay_success failed: %v", err)
	}
	if err := sm.Transit(service.TxnStatusRecvSuccess); err != nil {
		t.Fatalf("pay_success->recv_success failed: %v", err)
	}
}

func TestTC4010StateMachineInvalidTransitionRejected(t *testing.T) {
	sm := service.NewTxnStateMachine(service.TxnStatusInit)
	if err := sm.Transit(service.TxnStatusRecvSuccess); !errors.Is(err, service.ErrTxnStatusInvalid) {
		t.Fatalf("expected ErrTxnStatusInvalid, got %v", err)
	}
}

func TestTC4011CrossMerchantAccountRejected(t *testing.T) {
	repo, m, from, _ := setupTransferAccounts(t)
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-8c11-71aa-b2d2-2b079f7e1020",
	})
	ms := service.NewMerchantService(repo, ids)
	otherMerchant, err := ms.CreateMerchant("", "other")
	if err != nil {
		t.Fatalf("create other merchant failed: %v", err)
	}

	svc := service.NewTransferRoutingService(repo)
	_, err = svc.Resolve(service.TransferRoutingRequest{
		MerchantNo:      m.MerchantNo,
		Scene:           service.SceneP2P,
		DebitAccountNo:  from,
		CreditAccountNo: otherMerchant.BudgetAccountNo,
	})
	if !errors.Is(err, service.ErrAccountResolveFailed) {
		t.Fatalf("expected ErrAccountResolveFailed, got %v", err)
	}
}

func setupTransferAccounts(t *testing.T) (*db.Repository, service.Merchant, string, string) {
	t.Helper()
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
	c, err := cs.CreateCustomer(m.MerchantNo, "u_4001")
	if err != nil {
		t.Fatalf("create customer failed: %v", err)
	}
	from := "6217701201004001001"
	to := "6217701201004001002"
	if err := repo.CreateAccount(service.Account{AccountNo: from, MerchantNo: m.MerchantNo, CustomerNo: c.CustomerNo, AccountType: "CUSTOMER", AllowDebitOut: true, AllowCreditIn: true, AllowTransfer: true}); err != nil {
		t.Fatalf("create from account failed: %v", err)
	}
	if err := repo.CreateAccount(service.Account{AccountNo: to, MerchantNo: m.MerchantNo, CustomerNo: c.CustomerNo, AccountType: "CUSTOMER", AllowDebitOut: true, AllowCreditIn: true, AllowTransfer: true}); err != nil {
		t.Fatalf("create to account failed: %v", err)
	}
	return repo, m, from, to
}
