package service

import (
	"time"

	"github.com/xmz-ai/coin/internal/domain"
)

type WebhookConfig struct {
	URL     string
	Enabled bool
}

type MerchantFeatureConfig struct {
	AutoCreateAccountOnCustomerCreate bool
	AutoCreateCustomerOnCredit        bool
}

type OutboxEventDelivery struct {
	EventID       string
	TxnNo         string
	MerchantNo    string
	OutTradeNo    string
	BizType       string
	TransferScene string
	Amount        int64
	Status        string
	RetryCount    int
}

// Repository defines the persistence contract used by services.
// Production should provide a DB-backed implementation; tests can provide fakes.
type Repository interface {
	CreateMerchant(m domain.Merchant) error
	GetMerchantByNo(merchantNo string) (domain.Merchant, bool)
	UpsertMerchantFeatureConfig(merchantNo string, autoCreateAccountOnCustomerCreate, autoCreateCustomerOnCredit bool) error
	GetMerchantFeatureConfig(merchantNo string) (MerchantFeatureConfig, bool, error)

	CreateAccount(a domain.Account) error
	GetAccount(accountNo string) (domain.Account, bool)
	UpdateAccountCapabilities(accountNo string, allowDebitOut, allowCreditIn, allowTransfer bool)

	CreateCustomer(c domain.Customer) error
	GetCustomerByOutUserID(merchantNo, outUserID string) (domain.Customer, bool)

	GetAccountByCustomerNo(merchantNo, customerNo string) (domain.Account, bool)

	CreateTransferTxn(txn domain.TransferTxn) error
	GetTransferTxn(txnNo string) (domain.TransferTxn, bool)
	GetTransferTxnByOutTradeNo(merchantNo, outTradeNo string) (domain.TransferTxn, bool)
	ListTransferTxnsByStatus(status string, limit int) ([]domain.TransferTxn, error)
	ListStaleTransferTxnNosByStatus(status string, staleBefore time.Time, limit int) ([]string, error)
	ListTransferTxns(filter domain.TxnListFilter) ([]domain.TransferTxn, string)
	UpdateTransferTxnStatus(txnNo, status, errorCode, errorMsg string) error
	TransitionTransferTxnStatus(txnNo, fromStatus, toStatus, errorCode, errorMsg string) (bool, error)
	UpdateTransferTxnParties(txnNo, debitAccountNo, creditAccountNo string) error
	TryDecreaseTxnRefundable(txnNo string, amount int64) (left int64, ok bool, err error)
	ApplyTransferDebitStage(txnNo, debitAccountNo string, amount int64) (bool, error)
	ApplyTransferCreditStage(txnNo, creditAccountNo string, amount int64) (bool, error)
	ApplyRefundDebitStage(refundTxnNo string, amount int64) (bool, error)
	ApplyRefundCreditStage(refundTxnNo, creditAccountNo string, amount int64) (bool, error)
	TxnCount() int

	UpsertWebhookConfig(merchantNo, url string, enabled bool) error
	GetWebhookConfig(merchantNo string) (WebhookConfig, bool, error)
	ClaimDueOutboxEvents(limit int, now time.Time) ([]OutboxEventDelivery, error)
	ClaimDueOutboxEventsByTxnNo(txnNo string, limit int, now time.Time) ([]OutboxEventDelivery, error)
	MarkOutboxEventSuccess(eventID string) error
	MarkOutboxEventRetry(eventID string, retryCount int, nextRetryAt time.Time, dead bool) error
	InsertNotifyLog(txnNo, status string, retries int) error

	IncTxnCompensationRun()
	IncNotifyCompensationRun()
}
