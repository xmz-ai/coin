package service

type AccountResolver struct {
	repo                Repository
	customerProvisioner CustomerAccountProvisioner
}

type CustomerAccountProvisioner interface {
	EnsureCustomerAccountForCredit(merchantNo, outUserID string) (string, error)
}

func NewAccountResolver(repo Repository, customerProvisioners ...CustomerAccountProvisioner) *AccountResolver {
	var provisioner CustomerAccountProvisioner
	if len(customerProvisioners) > 0 {
		provisioner = customerProvisioners[0]
	}
	return &AccountResolver{
		repo:                repo,
		customerProvisioner: provisioner,
	}
}

func (r *AccountResolver) ResolveCustomerAccount(merchantNo, accountNo, outUserID string) (string, error) {
	m, ok := r.repo.GetMerchantByNo(merchantNo)
	if !ok {
		return "", nil
	}

	var fromAccount string
	if accountNo != "" {
		a, ok := r.repo.GetAccount(accountNo)
		if ok && a.MerchantNo == m.MerchantNo {
			fromAccount = a.AccountNo
		}
	}

	var fromOutUser string
	if outUserID != "" {
		c, ok := r.repo.GetCustomerByOutUserID(merchantNo, outUserID)
		if ok {
			a, ok := r.repo.GetAccountByCustomerNo(merchantNo, c.CustomerNo)
			if ok {
				fromOutUser = a.AccountNo
			}
		}
	}

	if fromAccount != "" && fromOutUser != "" && fromAccount != fromOutUser {
		return "", ErrAccountResolveConflict
	}
	if fromAccount != "" {
		return fromAccount, nil
	}
	if fromOutUser != "" {
		return fromOutUser, nil
	}
	return "", nil
}

func (r *AccountResolver) ResolveMerchantSystemAccount(merchantNo, accountNo, outUserID, accountType string) (string, error) {
	if outUserID != "" {
		return "", ErrOutUserIDNotAllowedForSystemAccount
	}

	m, ok := r.repo.GetMerchantByNo(merchantNo)
	if !ok {
		return "", nil
	}

	if accountNo != "" {
		a, ok := r.repo.GetAccount(accountNo)
		if ok && a.MerchantNo == m.MerchantNo {
			return a.AccountNo, nil
		}
		return "", nil
	}
	if accountType == AccountTypeBudget {
		return m.BudgetAccountNo, nil
	}
	if accountType == AccountTypeReceivable {
		return m.ReceivableAccountNo, nil
	}
	return "", nil
}

func (r *AccountResolver) EnsureCustomerAccountForCredit(merchantNo, outUserID string) (string, error) {
	if r.customerProvisioner == nil {
		return "", nil
	}
	return r.customerProvisioner.EnsureCustomerAccountForCredit(merchantNo, outUserID)
}
