package domain

import "time"

type TransferTxn struct {
	TxnNo            string
	MerchantNo       string
	OutTradeNo       string
	Title            string
	Remark           string
	BizType          string
	TransferScene    string
	DebitAccountNo   string
	CreditAccountNo  string
	CreditExpireAt   time.Time
	Amount           int64
	RefundOfTxnNo    string
	RefundableAmount int64
	Status           string
	ErrorCode        string
	ErrorMsg         string
	CreatedAt        time.Time
}

type TxnListFilter struct {
	MerchantNo string
	OutUserID  string
	Scene      string
	Status     string
	StartTime  *time.Time
	EndTime    *time.Time
	PageSize   int
	PageToken  string
}

type AccountChangeLog struct {
	ChangeID      int64
	TxnNo         string
	AccountNo     string
	Delta         int64
	BalanceBefore int64
	BalanceAfter  int64
	Title         string
	Remark        string
	CreatedAt     time.Time
}

type AccountChangeLogListFilter struct {
	MerchantNo string
	AccountNo  string
	PageSize   int
	PageToken  string
}

type AccountImpact struct {
	AccountNo string
	Delta     int64
}

type OriginTxn struct {
	TxnNo            string
	RefundableAmount int64
	AccountImpacts   []AccountImpact
}

type RefundPart struct {
	AccountNo string
	Amount    int64
}

type BookPart struct {
	ExpireAt time.Time
	Amount   int64
}

type AccountBook struct {
	BookNo    string
	AccountNo string
	ExpireAt  time.Time
	Balance   int64
}

type BookCreditChangeLog struct {
	ChangeID  int64
	TxnNo     string
	Delta     int64
	CreatedAt time.Time
	Title     string
}

type OutboxEvent struct {
	EventID    string
	TxnNo      string
	MerchantNo string
	OutTradeNo string
	RetryCount int
	Status     string
}

type NotifyLog struct {
	TxnNo   string
	Status  string
	Retries int
}
