package domain

const (
	AccountTypeBudget     = "BUDGET"
	AccountTypeReceivable = "RECEIVABLE"
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
)

const (
	TxnStatusInit        = "INIT"
	TxnStatusProcessing  = "PROCESSING"
	TxnStatusPaySuccess  = "PAY_SUCCESS"
	TxnStatusRecvSuccess = "RECV_SUCCESS"
	TxnStatusFailed      = "FAILED"
)

const (
	NotifyStatusSuccess = "SUCCESS"
	NotifyStatusFailed  = "FAILED"
	NotifyStatusDead    = "DEAD"
)
