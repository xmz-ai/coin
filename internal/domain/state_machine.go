package domain

type TxnStateMachine struct {
	status string
}

func NewTxnStateMachine(initial string) *TxnStateMachine {
	return &TxnStateMachine{status: initial}
}

func (s *TxnStateMachine) Transit(next string) error {
	allowed := false
	switch s.status {
	case TxnStatusInit:
		allowed = next == TxnStatusPaySuccess || next == TxnStatusFailed
	case TxnStatusPaySuccess:
		allowed = next == TxnStatusRecvSuccess || next == TxnStatusFailed
	default:
		allowed = false
	}
	if !allowed {
		return ErrTxnStatusInvalid
	}
	s.status = next
	return nil
}

func (s *TxnStateMachine) Status() string {
	if s == nil {
		return ""
	}
	return s.status
}
