package domain

import "errors"

var (
	ErrMerchantNoExists         = errors.New("merchant_no exists")
	ErrCustomerExists           = errors.New("customer exists")
	ErrAccountNoExists          = errors.New("account_no exists")
	ErrInvalidMerchantNo        = errors.New("invalid merchant_no")
	ErrInvalidCustomerNo        = errors.New("invalid customer_no")
	ErrInvalidAccountNo         = errors.New("invalid account_no")
	ErrCodeAllocatorUnavailable = errors.New("code allocator unavailable")

	ErrDuplicateOutTradeNo                 = errors.New("duplicate out_trade_no")
	ErrAccountResolveFailed                = errors.New("account resolve failed")
	ErrAccountResolveConflict              = errors.New("account resolve conflict")
	ErrOutUserIDNotAllowedForSystemAccount = errors.New("out_user_id not allowed for merchant system account")

	ErrAccountForbidDebit    = errors.New("account forbid debit")
	ErrAccountForbidCredit   = errors.New("account forbid credit")
	ErrAccountForbidTransfer = errors.New("account forbid transfer")
	ErrInsufficientBalance   = errors.New("insufficient balance")

	ErrTxnStatusInvalid = errors.New("txn status invalid")
	ErrTxnNotFound      = errors.New("txn not found")

	ErrRefundAmountExceeded         = errors.New("refund amount exceeded")
	ErrRefundOriginBookTraceMissing = errors.New("refund origin book trace missing")

	ErrBookDisabled     = errors.New("book disabled")
	ErrExpireAtRequired = errors.New("expire_at required")
)
