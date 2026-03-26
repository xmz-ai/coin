package domain

const (
	AccountTypeBudget     = "BUDGET"
	AccountTypeReceivable = "RECEIVABLE"
	AccountTypeWriteoff   = "WRITEOFF"
)

const (
	BizTypeTransfer = "TRANSFER"
	BizTypeRefund   = "REFUND"
)

const (
	SceneIssue   = "ISSUE"
	SceneConsume = "CONSUME"
	SceneP2P     = "P2P"
	SceneAdjust  = "ADJUST"
	SceneExpireWriteoff = "EXPIRE_WRITEOFF"
)

const (
	TxnStatusInit        = "INIT"
	TxnStatusPaySuccess  = "PAY_SUCCESS"
	TxnStatusRecvSuccess = "RECV_SUCCESS"
	TxnStatusFailed      = "FAILED"
)

const (
	NotifyStatusSuccess = "SUCCESS"
	NotifyStatusFailed  = "FAILED"
	NotifyStatusDead    = "DEAD"
)
