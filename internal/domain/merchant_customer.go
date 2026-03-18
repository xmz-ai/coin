package domain

type Merchant struct {
	MerchantID          string
	MerchantNo          string
	Name                string
	BudgetAccountNo     string
	ReceivableAccountNo string
}

type Customer struct {
	CustomerID       string
	CustomerNo       string
	MerchantNo       string
	OutUserID        string
	DefaultAccountNo string
}
