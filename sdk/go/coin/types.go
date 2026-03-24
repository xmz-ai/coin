package coin

import "time"

type MerchantProfile struct {
	MerchantNo                        string `json:"merchant_no"`
	Name                              string `json:"name"`
	Status                            string `json:"status"`
	BudgetAccountNo                   string `json:"budget_account_no"`
	ReceivableAccountNo               string `json:"receivable_account_no"`
	SecretVersion                     int    `json:"secret_version"`
	AutoCreateAccountOnCustomerCreate bool   `json:"auto_create_account_on_customer_create"`
	AutoCreateCustomerOnCredit        bool   `json:"auto_create_customer_on_credit"`
}

type TxnSubmitResponse struct {
	TxnNo  string `json:"txn_no"`
	Status string `json:"status"`
}

type Txn struct {
	TxnNo            string    `json:"txn_no"`
	OutTradeNo       string    `json:"out_trade_no"`
	Title            string    `json:"title"`
	Remark           string    `json:"remark"`
	TransferScene    string    `json:"transfer_scene"`
	Status           string    `json:"status"`
	Amount           int64     `json:"amount"`
	RefundableAmount int64     `json:"refundable_amount"`
	DebitAccountNo   string    `json:"debit_account_no"`
	CreditAccountNo  string    `json:"credit_account_no"`
	ErrorCode        string    `json:"error_code"`
	ErrorMsg         string    `json:"error_msg"`
	CreatedAt        time.Time `json:"created_at"`
}

type ListTransactionsResponse struct {
	Items         []Txn  `json:"items"`
	NextPageToken string `json:"next_page_token"`
}

type CreditRequest struct {
	OutTradeNo      string `json:"out_trade_no"`
	Title           string `json:"title,omitempty"`
	Remark          string `json:"remark,omitempty"`
	DebitAccountNo  string `json:"debit_account_no,omitempty"`
	CreditAccountNo string `json:"credit_account_no,omitempty"`
	UserID          string `json:"user_id,omitempty"`
	ExpireInDays    int64  `json:"expire_in_days,omitempty"`
	Amount          int64  `json:"amount"`
}

type DebitRequest struct {
	OutTradeNo      string `json:"out_trade_no"`
	Title           string `json:"title,omitempty"`
	Remark          string `json:"remark,omitempty"`
	BizType         string `json:"biz_type,omitempty"`
	TransferScene   string `json:"transfer_scene,omitempty"`
	DebitAccountNo  string `json:"debit_account_no,omitempty"`
	DebitOutUserID  string `json:"debit_out_user_id,omitempty"`
	CreditAccountNo string `json:"credit_account_no,omitempty"`
	CreditOutUserID string `json:"credit_out_user_id,omitempty"`
	Amount          int64  `json:"amount"`
}

type TransferRequest struct {
	OutTradeNo     string `json:"out_trade_no"`
	Title          string `json:"title,omitempty"`
	Remark         string `json:"remark,omitempty"`
	BizType        string `json:"biz_type,omitempty"`
	TransferScene  string `json:"transfer_scene,omitempty"`
	FromAccountNo  string `json:"from_account_no,omitempty"`
	FromOutUserID  string `json:"from_out_user_id,omitempty"`
	ToAccountNo    string `json:"to_account_no,omitempty"`
	ToOutUserID    string `json:"to_out_user_id,omitempty"`
	ToExpireInDays int64  `json:"to_expire_in_days,omitempty"`
	Amount         int64  `json:"amount"`
}

type RefundRequest struct {
	OutTradeNo    string `json:"out_trade_no"`
	Title         string `json:"title,omitempty"`
	Remark        string `json:"remark,omitempty"`
	BizType       string `json:"biz_type,omitempty"`
	RefundOfTxnNo string `json:"refund_of_txn_no"`
	Amount        int64  `json:"amount"`
}

type ListTransactionsRequest struct {
	StartTime     *time.Time
	EndTime       *time.Time
	Status        string
	TransferScene string
	OutUserID     string
	PageSize      int
	PageToken     string
}

type AccountChangeLog struct {
	ChangeID      int64     `json:"change_id"`
	TxnNo         string    `json:"txn_no"`
	AccountNo     string    `json:"account_no"`
	Delta         int64     `json:"delta"`
	BalanceBefore int64     `json:"balance_before"`
	BalanceAfter  int64     `json:"balance_after"`
	Title         string    `json:"title"`
	Remark        string    `json:"remark"`
	CreatedAt     time.Time `json:"created_at"`
}

type ListAccountChangeLogsRequest struct {
	PageSize  int
	PageToken string
}

type ListAccountChangeLogsResponse struct {
	Items         []AccountChangeLog `json:"items"`
	NextPageToken string             `json:"next_page_token"`
}
