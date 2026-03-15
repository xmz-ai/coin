package factory

import (
	"fmt"
	"time"

	clockpkg "github.com/xmz-ai/coin/internal/platform/clock"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
)

type Dependencies struct {
	Clock        clockpkg.Clock
	UUIDProvider idpkg.UUIDProvider
}

type Factory struct {
	clock        clockpkg.Clock
	uuidProvider idpkg.UUIDProvider
}

func New(deps Dependencies) *Factory {
	return &Factory{
		clock:        deps.Clock,
		uuidProvider: deps.UUIDProvider,
	}
}

type Merchant struct {
	MerchantID string
	MerchantNo string
	CreatedAt  time.Time
}

type Customer struct {
	CustomerID string
	MerchantID string
	OutUserID  string
	CreatedAt  time.Time
}

type Account struct {
	AccountNo  string
	MerchantID string
	CustomerID string
	CreatedAt  time.Time
}

type Txn struct {
	TxnNo      string
	MerchantID string
	OutTradeNo string
	AccountNo  string
	Amount     int64
	CreatedAt  time.Time
}

func (f *Factory) NewMerchant(merchantNo string) (Merchant, error) {
	id, err := f.uuidProvider.NewUUIDv7()
	if err != nil {
		return Merchant{}, fmt.Errorf("new merchant id: %w", err)
	}
	return Merchant{MerchantID: id, MerchantNo: merchantNo, CreatedAt: f.clock.NowUTC()}, nil
}

func (f *Factory) NewCustomer(merchantID, outUserID string) (Customer, error) {
	id, err := f.uuidProvider.NewUUIDv7()
	if err != nil {
		return Customer{}, fmt.Errorf("new customer id: %w", err)
	}
	return Customer{CustomerID: id, MerchantID: merchantID, OutUserID: outUserID, CreatedAt: f.clock.NowUTC()}, nil
}

func (f *Factory) NewAccount(merchantID, customerID, accountNo string) (Account, error) {
	return Account{AccountNo: accountNo, MerchantID: merchantID, CustomerID: customerID, CreatedAt: f.clock.NowUTC()}, nil
}

func (f *Factory) NewTxn(merchantID, outTradeNo, accountNo string, amount int64) (Txn, error) {
	txnNo, err := f.uuidProvider.NewUUIDv7()
	if err != nil {
		return Txn{}, fmt.Errorf("new txn no: %w", err)
	}
	return Txn{TxnNo: txnNo, MerchantID: merchantID, OutTradeNo: outTradeNo, AccountNo: accountNo, Amount: amount, CreatedAt: f.clock.NowUTC()}, nil
}
