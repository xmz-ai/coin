package service

import (
	"log"
	"sync/atomic"
	"time"
)

const defaultAsyncProfileLogInterval = 5 * time.Second

type latencySnapshot struct {
	Count      uint64
	ErrorCount uint64
	SumNs      uint64
	MaxNs      uint64
}

func (s latencySnapshot) avg() time.Duration {
	if s.Count == 0 {
		return 0
	}
	return time.Duration(s.SumNs / s.Count)
}

func (s latencySnapshot) max() time.Duration {
	return time.Duration(s.MaxNs)
}

type latencyCounter struct {
	totalCount  atomic.Uint64
	totalError  atomic.Uint64
	totalSumNs  atomic.Uint64
	totalMaxNs  atomic.Uint64
	windowCount atomic.Uint64
	windowError atomic.Uint64
	windowSumNs atomic.Uint64
	windowMaxNs atomic.Uint64
}

func (c *latencyCounter) observe(d time.Duration, err bool) {
	ns := durationToNs(d)
	c.totalCount.Add(1)
	c.totalSumNs.Add(ns)
	c.windowCount.Add(1)
	c.windowSumNs.Add(ns)
	updateMax(&c.totalMaxNs, ns)
	updateMax(&c.windowMaxNs, ns)
	if err {
		c.totalError.Add(1)
		c.windowError.Add(1)
	}
}

func (c *latencyCounter) snapshotWindowAndReset() latencySnapshot {
	return latencySnapshot{
		Count:      c.windowCount.Swap(0),
		ErrorCount: c.windowError.Swap(0),
		SumNs:      c.windowSumNs.Swap(0),
		MaxNs:      c.windowMaxNs.Swap(0),
	}
}

func (c *latencyCounter) snapshotTotal() latencySnapshot {
	return latencySnapshot{
		Count:      c.totalCount.Load(),
		ErrorCount: c.totalError.Load(),
		SumNs:      c.totalSumNs.Load(),
		MaxNs:      c.totalMaxNs.Load(),
	}
}

type depthCounter struct {
	totalMax  atomic.Uint64
	windowMax atomic.Uint64
}

func (c *depthCounter) observe(depth int) {
	if depth < 0 {
		return
	}
	d := uint64(depth)
	updateMax(&c.totalMax, d)
	updateMax(&c.windowMax, d)
}

func (c *depthCounter) snapshotWindowAndReset() uint64 {
	return c.windowMax.Swap(0)
}

func (c *depthCounter) snapshotTotal() uint64 {
	return c.totalMax.Load()
}

func updateMax(target *atomic.Uint64, value uint64) {
	for {
		curr := target.Load()
		if value <= curr {
			return
		}
		if target.CompareAndSwap(curr, value) {
			return
		}
	}
}

func durationToNs(d time.Duration) uint64 {
	if d <= 0 {
		return 0
	}
	return uint64(d)
}

type processorStageProfile struct {
	queueWait latencyCounter
	execute   latencyCounter
	queueLen  depthCounter
	dropTotal atomic.Uint64
	dropWin   atomic.Uint64
}

type asyncProcessorProfiler struct {
	logInterval time.Duration
	init        processorStageProfile
	paySuccess  processorStageProfile
}

func newAsyncProcessorProfiler(logInterval time.Duration) *asyncProcessorProfiler {
	if logInterval <= 0 {
		logInterval = defaultAsyncProfileLogInterval
	}
	return &asyncProcessorProfiler{
		logInterval: logInterval,
	}
}

func (p *asyncProcessorProfiler) startLogger() {
	if p == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(p.logInterval)
		defer ticker.Stop()
		for range ticker.C {
			p.logWindow()
		}
	}()
}

func (p *asyncProcessorProfiler) observeQueueWait(stage string, wait time.Duration) {
	if m := p.stage(stage); m != nil {
		m.queueWait.observe(wait, false)
	}
}

func (p *asyncProcessorProfiler) observeExecute(stage string, d time.Duration, err bool) {
	if m := p.stage(stage); m != nil {
		m.execute.observe(d, err)
	}
}

func (p *asyncProcessorProfiler) observeQueueDepth(stage string, depth int) {
	if m := p.stage(stage); m != nil {
		m.queueLen.observe(depth)
	}
}

func (p *asyncProcessorProfiler) observeDrop(stage string) {
	if m := p.stage(stage); m != nil {
		m.dropTotal.Add(1)
		m.dropWin.Add(1)
	}
}

func (p *asyncProcessorProfiler) stage(stage string) *processorStageProfile {
	switch stage {
	case TxnStatusInit:
		return &p.init
	case TxnStatusPaySuccess:
		return &p.paySuccess
	default:
		return nil
	}
}

func (p *asyncProcessorProfiler) logWindow() {
	if p == nil {
		return
	}
	p.logStage("init", &p.init)
	p.logStage("pay_success", &p.paySuccess)
}

func (p *asyncProcessorProfiler) logStage(label string, m *processorStageProfile) {
	queueWaitWin := m.queueWait.snapshotWindowAndReset()
	execWin := m.execute.snapshotWindowAndReset()
	queueLenWinMax := m.queueLen.snapshotWindowAndReset()
	dropsWin := m.dropWin.Swap(0)

	queueWaitTotal := m.queueWait.snapshotTotal()
	execTotal := m.execute.snapshotTotal()
	queueLenTotalMax := m.queueLen.snapshotTotal()
	dropsTotal := m.dropTotal.Load()

	log.Printf(
		"profile txn_async stage=%s window=%s queue_wait[count=%d avg=%s max=%s] execute[count=%d avg=%s max=%s err=%d] queue_depth_max=%d drops=%d total_execute[count=%d avg=%s max=%s err=%d] total_queue_depth_max=%d total_drops=%d",
		label,
		p.logInterval,
		queueWaitWin.Count,
		queueWaitWin.avg(),
		queueWaitWin.max(),
		execWin.Count,
		execWin.avg(),
		execWin.max(),
		execWin.ErrorCount,
		queueLenWinMax,
		dropsWin,
		execTotal.Count,
		execTotal.avg(),
		execTotal.max(),
		execTotal.ErrorCount,
		queueLenTotalMax,
		dropsTotal,
	)

	if queueWaitTotal.Count == 0 {
		return
	}
	log.Printf(
		"profile txn_async stage=%s total_queue_wait[count=%d avg=%s max=%s]",
		label,
		queueWaitTotal.Count,
		queueWaitTotal.avg(),
		queueWaitTotal.max(),
	)
}

type webhookProfiler struct {
	logInterval time.Duration
	queueWait   latencyCounter
	deliver     latencyCounter
	queueLen    depthCounter
	dropTotal   atomic.Uint64
	dropWin     atomic.Uint64
	retryTotal  atomic.Uint64
	retryWin    atomic.Uint64
}

func newWebhookProfiler(logInterval time.Duration) *webhookProfiler {
	if logInterval <= 0 {
		logInterval = defaultAsyncProfileLogInterval
	}
	return &webhookProfiler{
		logInterval: logInterval,
	}
}

func (p *webhookProfiler) startLogger() {
	if p == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(p.logInterval)
		defer ticker.Stop()
		for range ticker.C {
			p.logWindow()
		}
	}()
}

func (p *webhookProfiler) observeQueueWait(wait time.Duration) {
	if p == nil {
		return
	}
	p.queueWait.observe(wait, false)
}

func (p *webhookProfiler) observeDeliver(d time.Duration, err bool) {
	if p == nil {
		return
	}
	p.deliver.observe(d, err)
}

func (p *webhookProfiler) observeQueueDepth(depth int) {
	if p == nil {
		return
	}
	p.queueLen.observe(depth)
}

func (p *webhookProfiler) observeDrop() {
	if p == nil {
		return
	}
	p.dropTotal.Add(1)
	p.dropWin.Add(1)
}

func (p *webhookProfiler) observeRetry() {
	if p == nil {
		return
	}
	p.retryTotal.Add(1)
	p.retryWin.Add(1)
}

func (p *webhookProfiler) logWindow() {
	if p == nil {
		return
	}
	queueWaitWin := p.queueWait.snapshotWindowAndReset()
	deliverWin := p.deliver.snapshotWindowAndReset()
	queueLenWinMax := p.queueLen.snapshotWindowAndReset()
	dropWin := p.dropWin.Swap(0)
	retryWin := p.retryWin.Swap(0)

	queueWaitTotal := p.queueWait.snapshotTotal()
	deliverTotal := p.deliver.snapshotTotal()
	queueLenTotalMax := p.queueLen.snapshotTotal()
	dropTotal := p.dropTotal.Load()
	retryTotal := p.retryTotal.Load()

	log.Printf(
		"profile webhook window=%s queue_wait[count=%d avg=%s max=%s] deliver[count=%d avg=%s max=%s err=%d] queue_depth_max=%d drops=%d retries=%d total_deliver[count=%d avg=%s max=%s err=%d] total_queue_depth_max=%d total_drops=%d total_retries=%d",
		p.logInterval,
		queueWaitWin.Count,
		queueWaitWin.avg(),
		queueWaitWin.max(),
		deliverWin.Count,
		deliverWin.avg(),
		deliverWin.max(),
		deliverWin.ErrorCount,
		queueLenWinMax,
		dropWin,
		retryWin,
		deliverTotal.Count,
		deliverTotal.avg(),
		deliverTotal.max(),
		deliverTotal.ErrorCount,
		queueLenTotalMax,
		dropTotal,
		retryTotal,
	)

	if queueWaitTotal.Count == 0 {
		return
	}
	log.Printf(
		"profile webhook total_queue_wait[count=%d avg=%s max=%s]",
		queueWaitTotal.Count,
		queueWaitTotal.avg(),
		queueWaitTotal.max(),
	)
}
