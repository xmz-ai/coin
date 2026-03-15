package domain

type Account struct {
	AccountNo         string
	MerchantNo        string
	CustomerNo        string
	AccountType       string
	AllowOverdraft    bool
	MaxOverdraftLimit int64
	AllowDebitOut     bool
	AllowCreditIn     bool
	AllowTransfer     bool
	BookEnabled       bool
	Balance           int64
}

func (a Account) CanDebitOut() error {
	if !a.AllowDebitOut {
		return ErrAccountForbidDebit
	}
	return nil
}

func (a Account) CanDebit(amount int64) error {
	if err := a.CanDebitOut(); err != nil {
		return err
	}
	if amount <= 0 {
		return ErrInsufficientBalance
	}
	if !a.AllowOverdraft {
		if a.Balance < amount {
			return ErrInsufficientBalance
		}
		return nil
	}
	if a.MaxOverdraftLimit == 0 {
		return nil
	}
	if a.Balance+a.MaxOverdraftLimit < amount {
		return ErrInsufficientBalance
	}
	return nil
}

func (a Account) CanCredit() error {
	if !a.AllowCreditIn {
		return ErrAccountForbidCredit
	}
	return nil
}

func (a Account) CanTransfer() error {
	if !a.AllowTransfer {
		return ErrAccountForbidTransfer
	}
	return nil
}
