package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

type AsyncService struct {
	mu sync.Mutex

	seq       int
	outbox    []OutboxEvent
	notify    map[string][]NotifyLog
	txnStatus map[string]string
}

func NewAsyncService() *AsyncService {
	return &AsyncService{
		outbox:    make([]OutboxEvent, 0),
		notify:    map[string][]NotifyLog{},
		txnStatus: map[string]string{},
	}
}

func (s *AsyncService) nextID() string {
	s.seq++
	return "id_" + string(rune('a'+s.seq))
}

func (s *AsyncService) RecordMainTxnSuccess(merchantNo, outTradeNo string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	txnNo := s.nextID()
	s.txnStatus[txnNo] = TxnStatusRecvSuccess
	s.outbox = append(s.outbox, OutboxEvent{EventID: s.nextID(), TxnNo: txnNo, MerchantNo: merchantNo, OutTradeNo: outTradeNo, Status: "PENDING"})
	return txnNo
}

func (s *AsyncService) RecordStuckTxn(merchantNo, outTradeNo string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	txnNo := s.nextID()
	s.txnStatus[txnNo] = TxnStatusInit
	s.outbox = append(s.outbox, OutboxEvent{EventID: s.nextID(), TxnNo: txnNo, MerchantNo: merchantNo, OutTradeNo: outTradeNo, Status: "PENDING"})
	return txnNo
}

func (s *AsyncService) ListOutboxPending() []OutboxEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := make([]OutboxEvent, 0)
	for _, e := range s.outbox {
		if e.Status == "PENDING" {
			res = append(res, e)
		}
	}
	return res
}

func (s *AsyncService) ListOutboxDead() []OutboxEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := make([]OutboxEvent, 0)
	for _, e := range s.outbox {
		if e.Status == NotifyStatusDead {
			res = append(res, e)
		}
	}
	return res
}

func (s *AsyncService) ProcessOutbox(deliver func(OutboxEvent) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.outbox {
		e := s.outbox[i]
		if e.Status != "PENDING" {
			continue
		}
		if deliver(e) {
			s.outbox[i].Status = NotifyStatusSuccess
			s.notify[e.TxnNo] = append(s.notify[e.TxnNo], NotifyLog{TxnNo: e.TxnNo, Status: NotifyStatusSuccess, Retries: s.outbox[i].RetryCount})
			continue
		}
		s.outbox[i].RetryCount++
		if s.outbox[i].RetryCount >= 3 {
			s.outbox[i].Status = NotifyStatusDead
			s.notify[e.TxnNo] = append(s.notify[e.TxnNo], NotifyLog{TxnNo: e.TxnNo, Status: NotifyStatusDead, Retries: s.outbox[i].RetryCount})
		} else {
			s.notify[e.TxnNo] = append(s.notify[e.TxnNo], NotifyLog{TxnNo: e.TxnNo, Status: NotifyStatusFailed, Retries: s.outbox[i].RetryCount})
		}
	}
}

func (s *AsyncService) ListNotifyLogs(txnNo string) []NotifyLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	logs := s.notify[txnNo]
	out := make([]NotifyLog, len(logs))
	copy(out, logs)
	return out
}

func (s *AsyncService) RunCompensation() {
	s.RunTxnCompensation()
}

func (s *AsyncService) RunTxnCompensation() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for txnNo, st := range s.txnStatus {
		if st == TxnStatusInit || st == TxnStatusPaySuccess {
			s.txnStatus[txnNo] = TxnStatusRecvSuccess
		}
	}
}

func (s *AsyncService) RunNotifyCompensation(deliver func(OutboxEvent) bool) {
	s.ProcessOutbox(deliver)
}

func (s *AsyncService) SignWebhook(secret string, body []byte, timestamp, nonce string) string {
	payload := timestamp + "\n" + nonce + "\n" + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *AsyncService) GetTxnStatus(txnNo string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.txnStatus[txnNo]
}
