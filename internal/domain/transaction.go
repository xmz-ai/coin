package domain

import "time"

type TransferTxn struct {
	TxnNo            string
	MerchantNo       string
	OutTradeNo       string
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
