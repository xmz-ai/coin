package service

import (
	"errors"
	"hash/fnv"
	"log"
	"strings"
	"sync"
	"time"
)

const (
	defaultAsyncInitWorkers       = 17
	defaultAsyncPaySuccessWorkers = 17
	defaultAsyncInitQueueSize     = 65536
	defaultAsyncPaySuccessQueue   = 65536
)

// TransferAsyncProcessor processes transfer/refund transactions asynchronously
// by stage queues: INIT -> PAY_SUCCESS.
type StageProcessingGuardWithError interface {
	TryBeginWithError(txnNo, stage string) (bool, error)
}

type TxnNotifyDispatcher interface {
	Enqueue(txnNo string)
}

type TransferAsyncProcessorOptions struct {
	InitWorkers          int
	PaySuccessWorkers    int
	InitQueueSize        int
	PaySuccessQueue      int
	ProfilingEnabled     bool
	ProfilingLogInterval time.Duration
}

func (o TransferAsyncProcessorOptions) withDefaults() TransferAsyncProcessorOptions {
	if o.InitWorkers <= 0 {
		o.InitWorkers = defaultAsyncInitWorkers
	}
	if o.PaySuccessWorkers <= 0 {
		o.PaySuccessWorkers = defaultAsyncPaySuccessWorkers
	}
	if o.InitQueueSize <= 0 {
		o.InitQueueSize = defaultAsyncInitQueueSize
	}
	if o.PaySuccessQueue <= 0 {
		o.PaySuccessQueue = defaultAsyncPaySuccessQueue
	}
	if o.ProfilingEnabled && o.ProfilingLogInterval <= 0 {
		o.ProfilingLogInterval = defaultAsyncProfileLogInterval
	}
	return o
}

type TransferAsyncProcessor struct {
	repo               Repository
	guard              StageProcessingGuard
	initQueue          *stageQueue
	paySuccessQueue    *stageQueue
	profiler           *asyncProcessorProfiler
	webhookDispatcher  TxnNotifyDispatcher
	webhookDispatcherM sync.RWMutex
}

type stageQueueItem struct {
	txnNo      string
	enqueuedAt time.Time
}

type stageQueue struct {
	workerQueues []chan stageQueueItem
	maxPending   int
	mu           sync.Mutex
	pending      map[string]struct{}
}

func newStageQueueWithWorkers(workerCount, size int) *stageQueue {
	if workerCount <= 0 {
		workerCount = 1
	}
	if size <= 0 {
		size = 1
	}
	if workerCount > size {
		workerCount = size
	}
	queues := make([]chan stageQueueItem, workerCount)
	for i := 0; i < workerCount; i++ {
		// Keep per-worker channel large enough so one hot shard can consume
		// the full global queue budget when needed.
		queues[i] = make(chan stageQueueItem, size)
	}
	return &stageQueue{
		workerQueues: queues,
		maxPending:   size,
		pending:      map[string]struct{}{},
	}
}

func (q *stageQueue) Enqueue(txnNo string) bool {
	return q.EnqueueWithRoute(txnNo, "")
}

func (q *stageQueue) EnqueueWithRoute(txnNo, routeKey string) bool {
	if q == nil {
		return false
	}
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" {
		return false
	}
	workerIndex := q.workerIndex(routeKey, txnNo)
	if workerIndex < 0 || workerIndex >= len(q.workerQueues) {
		return false
	}
	workerQueue := q.workerQueues[workerIndex]

	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.pending[txnNo]; exists {
		return true
	}
	if q.maxPending > 0 && len(q.pending) >= q.maxPending {
		return false
	}
	select {
	case workerQueue <- stageQueueItem{txnNo: txnNo, enqueuedAt: time.Now()}:
		q.pending[txnNo] = struct{}{}
		return true
	default:
		return false
	}
}

func (q *stageQueue) workerIndex(routeKey, fallback string) int {
	if q == nil || len(q.workerQueues) == 0 {
		return -1
	}
	key := strings.TrimSpace(routeKey)
	if key == "" {
		key = strings.TrimSpace(fallback)
	}
	if key == "" {
		return 0
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(key))
	return int(hasher.Sum32() % uint32(len(q.workerQueues)))
}

func (q *stageQueue) Depth() int {
	if q == nil {
		return 0
	}
	total := 0
	for _, workerQueue := range q.workerQueues {
		total += len(workerQueue)
	}
	return total
}

func (q *stageQueue) Done(txnNo string) {
	if q == nil {
		return
	}
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" {
		return
	}
	q.mu.Lock()
	delete(q.pending, txnNo)
	q.mu.Unlock()
}

func NewTransferAsyncProcessor(repo Repository) *TransferAsyncProcessor {
	return NewTransferAsyncProcessorWithGuardAndOptions(repo, NewProcessingGuard(), TransferAsyncProcessorOptions{})
}

func NewTransferAsyncProcessorWithGuard(repo Repository, guard StageProcessingGuard) *TransferAsyncProcessor {
	return NewTransferAsyncProcessorWithGuardAndOptions(repo, guard, TransferAsyncProcessorOptions{})
}

func NewTransferAsyncProcessorWithGuardAndOptions(repo Repository, guard StageProcessingGuard, opts TransferAsyncProcessorOptions) *TransferAsyncProcessor {
	if guard == nil {
		guard = NewProcessingGuard()
	}
	opts = opts.withDefaults()
	p := &TransferAsyncProcessor{
		repo:            repo,
		guard:           guard,
		initQueue:       newStageQueueWithWorkers(opts.InitWorkers, opts.InitQueueSize),
		paySuccessQueue: newStageQueueWithWorkers(opts.PaySuccessWorkers, opts.PaySuccessQueue),
	}
	if opts.ProfilingEnabled {
		p.profiler = newAsyncProcessorProfiler(opts.ProfilingLogInterval)
		p.profiler.startLogger()
	}
	p.startWorkers(TxnStatusInit, p.initQueue)
	p.startWorkers(TxnStatusPaySuccess, p.paySuccessQueue)
	return p
}

func (p *TransferAsyncProcessor) SetWebhookDispatcher(dispatcher TxnNotifyDispatcher) {
	if p == nil {
		return
	}
	p.webhookDispatcherM.Lock()
	p.webhookDispatcher = dispatcher
	p.webhookDispatcherM.Unlock()
}

func (p *TransferAsyncProcessor) startWorkers(stage string, queue *stageQueue) {
	if queue == nil {
		return
	}
	for workerIdx := range queue.workerQueues {
		workerQueue := queue.workerQueues[workerIdx]
		go func(expectedStatus string, q *stageQueue, ch <-chan stageQueueItem) {
			for item := range ch {
				txnNo := item.txnNo
				if p.profiler != nil {
					p.profiler.observeQueueWait(expectedStatus, time.Since(item.enqueuedAt))
					p.profiler.observeQueueDepth(expectedStatus, q.Depth())
				}
				func() {
					defer q.Done(txnNo)
					startedAt := time.Now()
					err := p.processStage(txnNo, expectedStatus)
					if p.profiler != nil {
						p.profiler.observeExecute(expectedStatus, time.Since(startedAt), err != nil)
					}
				}()
			}
		}(stage, queue, workerQueue)
	}
}

func (p *TransferAsyncProcessor) Enqueue(txnNo string) {
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" || p == nil || p.repo == nil {
		return
	}
	txn, ok := p.repo.GetTransferTxn(txnNo)
	if !ok {
		return
	}
	_ = p.EnqueueTxn(txn)
}

func (p *TransferAsyncProcessor) EnqueueTxn(txn TransferTxn) bool {
	if p == nil {
		return false
	}
	txnNo := strings.TrimSpace(txn.TxnNo)
	if txnNo == "" {
		return false
	}
	status := strings.TrimSpace(txn.Status)
	if status == "" {
		status = TxnStatusInit
	}
	return p.enqueueByStatusWithTxn(txnNo, status, &txn)
}

func (p *TransferAsyncProcessor) EnqueueByStatus(txnNo, status string) bool {
	return p.enqueueByStatusWithTxn(txnNo, status, nil)
}

func (p *TransferAsyncProcessor) enqueueByStatusWithTxn(txnNo, status string, txn *TransferTxn) bool {
	if p == nil {
		return false
	}
	txnNo = strings.TrimSpace(txnNo)
	status = strings.TrimSpace(status)
	if txnNo == "" {
		return false
	}
	routeKey := p.stageRouteKey(status, txnNo, txn)

	switch status {
	case TxnStatusInit:
		ok := p.initQueue.EnqueueWithRoute(txnNo, routeKey)
		if p.profiler != nil {
			if ok {
				p.profiler.observeQueueDepth(TxnStatusInit, p.initQueue.Depth())
			} else {
				p.profiler.observeDrop(TxnStatusInit)
			}
		}
		return ok
	case TxnStatusPaySuccess:
		ok := p.paySuccessQueue.EnqueueWithRoute(txnNo, routeKey)
		if p.profiler != nil {
			if ok {
				p.profiler.observeQueueDepth(TxnStatusPaySuccess, p.paySuccessQueue.Depth())
			} else {
				p.profiler.observeDrop(TxnStatusPaySuccess)
			}
		}
		return ok
	case TxnStatusRecvSuccess, TxnStatusFailed:
		return true
	default:
		return false
	}
}

// Process processes exactly one stage based on current txn status.
func (p *TransferAsyncProcessor) Process(txnNo string) error {
	txnNo = strings.TrimSpace(txnNo)
	if txnNo == "" || p == nil || p.repo == nil {
		return ErrTxnNotFound
	}

	txn, ok := p.repo.GetTransferTxn(txnNo)
	if !ok {
		return ErrTxnNotFound
	}
	return p.processStage(txnNo, txn.Status)
}

func (p *TransferAsyncProcessor) processStage(txnNo, expectedStatus string) error {
	txnNo = strings.TrimSpace(txnNo)
	expectedStatus = strings.TrimSpace(expectedStatus)
	if txnNo == "" {
		return ErrTxnNotFound
	}
	if expectedStatus == "" {
		txn, ok := p.repo.GetTransferTxn(txnNo)
		if !ok {
			return ErrTxnNotFound
		}
		expectedStatus = txn.Status
	}
	if expectedStatus == TxnStatusRecvSuccess || expectedStatus == TxnStatusFailed {
		return nil
	}

	ok, err := p.tryStage(txnNo, expectedStatus)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	defer p.endStage(txnNo, expectedStatus)

	return p.processStageWithApplyResult(txnNo, expectedStatus)
}

func (p *TransferAsyncProcessor) processStageWithApplyResult(txnNo, expectedStatus string) error {
	result, err := p.repo.ApplyTxnStage(txnNo, expectedStatus)
	if err != nil {
		if result.BizType != BizTypeTransfer && result.BizType != BizTypeRefund {
			return err
		}
		switch expectedStatus {
		case TxnStatusInit:
			if result.BizType == BizTypeRefund {
				return p.handleStageError(txnNo, TxnStatusInit, p.refundDebitErrorCode(err), err)
			}
			return p.handleStageError(txnNo, TxnStatusInit, "DEBIT_FAILED", err)
		case TxnStatusPaySuccess:
			if result.BizType == BizTypeRefund {
				log.Printf("warn: refund credit stage failed, txn_no=%s status=%s err=%v", txnNo, TxnStatusPaySuccess, err)
				// Keep refund txn in PAY_SUCCESS to allow compensation retry
				// when second-stage credit fails after debit has succeeded.
				return err
			}
			return p.handleStageError(txnNo, TxnStatusPaySuccess, "CREDIT_FAILED", err)
		default:
			_ = p.fail(txnNo, expectedStatus, "TXN_STATUS_INVALID", "unknown txn status")
			return ErrTxnStatusInvalid
		}
	}
	if result.BizType != BizTypeTransfer && result.BizType != BizTypeRefund {
		return nil
	}

	stageTxn := TransferTxn{
		TxnNo:           txnNo,
		BizType:         result.BizType,
		DebitAccountNo:  result.DebitAccountNo,
		CreditAccountNo: result.CreditAccountNo,
	}
	if !result.Applied {
		stageTxn.Status = result.CurrentStatus
		if stageTxn.Status == "" {
			p.enqueueByCurrentStatus(txnNo)
			return nil
		}
		_ = p.enqueueByStatusWithTxn(txnNo, stageTxn.Status, &stageTxn)
		return nil
	}

	switch expectedStatus {
	case TxnStatusInit:
		// Route PAY_SUCCESS by credit account using data read in the same tx.
		stageTxn.Status = TxnStatusPaySuccess
		_ = p.enqueueByStatusWithTxn(txnNo, TxnStatusPaySuccess, &stageTxn)
		return nil
	case TxnStatusPaySuccess:
		p.notifyWebhook(txnNo)
		return nil
	default:
		_ = p.fail(txnNo, expectedStatus, "TXN_STATUS_INVALID", "unknown txn status")
		return ErrTxnStatusInvalid
	}
}

func (p *TransferAsyncProcessor) enqueueByCurrentStatus(txnNo string) {
	txn, ok := p.repo.GetTransferTxn(txnNo)
	if !ok {
		return
	}
	p.enqueueByCurrentStatusTxn(txn)
}

func (p *TransferAsyncProcessor) enqueueByCurrentStatusTxn(txn TransferTxn) {
	_ = p.enqueueByStatusWithTxn(txn.TxnNo, txn.Status, &txn)
}

func (p *TransferAsyncProcessor) stageRouteKey(status, txnNo string, txn *TransferTxn) string {
	status = strings.TrimSpace(status)
	txnNo = strings.TrimSpace(txnNo)
	if status != TxnStatusInit && status != TxnStatusPaySuccess {
		return txnNo
	}
	target := txn
	if target == nil {
		if p.repo == nil {
			return txnNo
		}
		loaded, ok := p.repo.GetTransferTxn(txnNo)
		if !ok {
			return txnNo
		}
		target = &loaded
	}
	primaryKey := routeKeyForTxnStage(*target, status)
	if primaryKey != "" {
		return primaryKey
	}
	if target.BizType == BizTypeRefund && p.repo != nil {
		originTxnNo := strings.TrimSpace(target.RefundOfTxnNo)
		if originTxnNo != "" {
			originTxn, ok := p.repo.GetTransferTxn(originTxnNo)
			if ok {
				if status == TxnStatusInit {
					if key := strings.TrimSpace(originTxn.CreditAccountNo); key != "" {
						return key
					}
				}
				if status == TxnStatusPaySuccess {
					if key := strings.TrimSpace(originTxn.DebitAccountNo); key != "" {
						return key
					}
				}
			}
			return originTxnNo
		}
	}
	if txnNo != "" {
		return txnNo
	}
	return strings.TrimSpace(target.TxnNo)
}

func routeKeyForTxnStage(txn TransferTxn, status string) string {
	status = strings.TrimSpace(status)
	switch status {
	case TxnStatusInit:
		if key := strings.TrimSpace(txn.DebitAccountNo); key != "" {
			return key
		}
	case TxnStatusPaySuccess:
		if key := strings.TrimSpace(txn.CreditAccountNo); key != "" {
			return key
		}
	}
	return ""
}

func (p *TransferAsyncProcessor) notifyWebhook(txnNo string) {
	p.webhookDispatcherM.RLock()
	dispatcher := p.webhookDispatcher
	p.webhookDispatcherM.RUnlock()
	if dispatcher == nil {
		return
	}
	dispatcher.Enqueue(txnNo)
}

func (p *TransferAsyncProcessor) fail(txnNo, fromStatus, errorCode, errorMsg string) error {
	_, err := p.repo.TransitionTransferTxnStatus(txnNo, fromStatus, TxnStatusFailed, errorCode, errorMsg)
	return err
}

func (p *TransferAsyncProcessor) tryStage(txnNo, stage string) (bool, error) {
	if p.guard == nil {
		return false, ErrProcessingGuardUnavailable
	}
	if g, ok := p.guard.(StageProcessingGuardWithError); ok {
		return g.TryBeginWithError(txnNo, stage)
	}
	if p.guard.TryBegin(txnNo, stage) {
		return true, nil
	}
	return false, nil
}

func (p *TransferAsyncProcessor) endStage(txnNo, stage string) {
	if p.guard == nil {
		return
	}
	g, ok := p.guard.(StageProcessingGuardEnder)
	if !ok {
		return
	}
	g.End(txnNo, stage)
}

func (p *TransferAsyncProcessor) handleStageError(txnNo, fromStatus, errorCode string, err error) error {
	if err == nil {
		return nil
	}
	if p.shouldFailOnError(err) {
		_ = p.fail(txnNo, fromStatus, errorCode, err.Error())
	}
	return err
}

func (p *TransferAsyncProcessor) shouldFailOnError(err error) bool {
	switch {
	case errors.Is(err, ErrTxnNotFound):
		return true
	case errors.Is(err, ErrTxnStatusInvalid):
		return true
	case errors.Is(err, ErrAccountResolveFailed):
		return true
	case errors.Is(err, ErrAccountResolveConflict):
		return true
	case errors.Is(err, ErrOutUserIDNotAllowedForSystemAccount):
		return true
	case errors.Is(err, ErrAccountForbidDebit):
		return true
	case errors.Is(err, ErrAccountForbidCredit):
		return true
	case errors.Is(err, ErrAccountForbidTransfer):
		return true
	case errors.Is(err, ErrInsufficientBalance):
		return true
	case errors.Is(err, ErrRefundAmountExceeded):
		return true
	case errors.Is(err, ErrRefundOriginBookTraceMissing):
		return true
	case errors.Is(err, ErrBookDisabled):
		return true
	case errors.Is(err, ErrExpireAtRequired):
		return true
	default:
		return false
	}
}

func (p *TransferAsyncProcessor) refundDebitErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrTxnNotFound):
		return "REFUND_ORIGIN_NOT_FOUND"
	case errors.Is(err, ErrTxnStatusInvalid):
		return "REFUND_ORIGIN_INVALID"
	case errors.Is(err, ErrRefundAmountExceeded):
		return "REFUND_AMOUNT_EXCEEDED"
	default:
		return "REFUND_DEBIT_FAILED"
	}
}

func (p *TransferAsyncProcessor) refundCreditErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrTxnNotFound):
		return "REFUND_ORIGIN_NOT_FOUND"
	case errors.Is(err, ErrRefundOriginBookTraceMissing):
		return "REFUND_ORIGIN_BOOK_TRACE_MISSING"
	default:
		return "REFUND_CREDIT_FAILED"
	}
}
