package service

import "github.com/xmz-ai/coin/internal/domain"

const (
	AccountTypeBudget     = domain.AccountTypeBudget
	AccountTypeReceivable = domain.AccountTypeReceivable

	BizTypeTransfer = domain.BizTypeTransfer
	BizTypeRefund   = domain.BizTypeRefund

	SceneIssue   = domain.SceneIssue
	SceneConsume = domain.SceneConsume
	SceneP2P     = domain.SceneP2P
	SceneAdjust  = domain.SceneAdjust

	TxnStatusInit        = domain.TxnStatusInit
	TxnStatusProcessing  = domain.TxnStatusProcessing
	TxnStatusPaySuccess  = domain.TxnStatusPaySuccess
	TxnStatusRecvSuccess = domain.TxnStatusRecvSuccess
	TxnStatusFailed      = domain.TxnStatusFailed

	NotifyStatusSuccess = domain.NotifyStatusSuccess
	NotifyStatusFailed  = domain.NotifyStatusFailed
	NotifyStatusDead    = domain.NotifyStatusDead
)

var (
	ErrMerchantNoExists         = domain.ErrMerchantNoExists
	ErrCustomerExists           = domain.ErrCustomerExists
	ErrAccountNoExists          = domain.ErrAccountNoExists
	ErrInvalidMerchantNo        = domain.ErrInvalidMerchantNo
	ErrInvalidCustomerNo        = domain.ErrInvalidCustomerNo
	ErrInvalidAccountNo         = domain.ErrInvalidAccountNo
	ErrCodeAllocatorUnavailable = domain.ErrCodeAllocatorUnavailable

	ErrDuplicateOutTradeNo                 = domain.ErrDuplicateOutTradeNo
	ErrAccountResolveFailed                = domain.ErrAccountResolveFailed
	ErrAccountResolveConflict              = domain.ErrAccountResolveConflict
	ErrOutUserIDNotAllowedForSystemAccount = domain.ErrOutUserIDNotAllowedForSystemAccount

	ErrAccountForbidDebit    = domain.ErrAccountForbidDebit
	ErrAccountForbidCredit   = domain.ErrAccountForbidCredit
	ErrAccountForbidTransfer = domain.ErrAccountForbidTransfer
	ErrInsufficientBalance   = domain.ErrInsufficientBalance

	ErrTxnStatusInvalid = domain.ErrTxnStatusInvalid
	ErrTxnNotFound      = domain.ErrTxnNotFound

	ErrRefundAmountExceeded     = domain.ErrRefundAmountExceeded
	ErrRefundBreakdownInvalid   = domain.ErrRefundBreakdownInvalid
	ErrRefundAccountNotInOrigin = domain.ErrRefundAccountNotInOrigin

	ErrBookDisabled     = domain.ErrBookDisabled
	ErrExpireAtRequired = domain.ErrExpireAtRequired
)

type Merchant = domain.Merchant
type Customer = domain.Customer
type Account = domain.Account
type TransferTxn = domain.TransferTxn
type TxnListFilter = domain.TxnListFilter
type AccountImpact = domain.AccountImpact
type OriginTxn = domain.OriginTxn
type RefundPart = domain.RefundPart
type BookPart = domain.BookPart
type OutboxEvent = domain.OutboxEvent
type NotifyLog = domain.NotifyLog
type TxnStateMachine = domain.TxnStateMachine

func NewTxnStateMachine(initial string) *TxnStateMachine {
	return domain.NewTxnStateMachine(initial)
}
