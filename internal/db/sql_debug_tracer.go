package db

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	sqlDebugTraceEnv          = "COIN_SQL_DEBUG_TRACE"
	sqlDebugSlowMSEnv         = "COIN_SQL_DEBUG_SLOW_MS"
	defaultSQLDebugSlowMS     = 20
	maxSQLDebugSummaryEntries = 100
)

type sampledTxnFlow string

const (
	sampledTxnFlowNone    sampledTxnFlow = ""
	sampledTxnFlowForward sampledTxnFlow = "forward"
	sampledTxnFlowReverse sampledTxnFlow = "reverse"
)

type sampledTxnSQLTracer struct {
	slowThreshold time.Duration

	mu        sync.Mutex
	connFlow  map[*pgx.Conn]sampledTxnFlow
	connInTxn map[*pgx.Conn]bool
	forward   sampledTxnFlowState
	reverse   sampledTxnFlowState
}

type sampledTxnFlowState struct {
	txnNo       string
	originTxnNo string
	outboxSeen  bool
	summarized  bool
	stats       map[string]*queryTimingStat
}

type queryTimingStat struct {
	queryName string
	sql       string
	count     int
	total     time.Duration
	max       time.Duration
}

type sqlDebugTraceStateKey struct{}

type sqlDebugTraceState struct {
	start     time.Time
	conn      *pgx.Conn
	flow      sampledTxnFlow
	txnNo     string
	queryName string
	sql       string
	isOutbox  bool
	clearConn bool
}

func newTxnSQLDebugTracerFromEnv() *sampledTxnSQLTracer {
	if !isTruthyEnv(os.Getenv(sqlDebugTraceEnv)) {
		return nil
	}
	slowMS := defaultSQLDebugSlowMS
	if raw := strings.TrimSpace(os.Getenv(sqlDebugSlowMSEnv)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			slowMS = parsed
		}
	}

	tracer := &sampledTxnSQLTracer{
		slowThreshold: time.Duration(slowMS) * time.Millisecond,
		connFlow:      map[*pgx.Conn]sampledTxnFlow{},
		connInTxn:     map[*pgx.Conn]bool{},
		forward: sampledTxnFlowState{
			stats: map[string]*queryTimingStat{},
		},
		reverse: sampledTxnFlowState{
			stats: map[string]*queryTimingStat{},
		},
	}
	log.Printf("[sql-debug] enabled env=%s slow_ms=%d", sqlDebugTraceEnv, slowMS)
	return tracer
}

func (t *sampledTxnSQLTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	state := sqlDebugTraceState{
		start:     time.Now(),
		conn:      conn,
		queryName: extractSQLCQueryName(data.SQL),
		sql:       compactSQL(data.SQL),
	}
	sqlLower := strings.ToLower(state.sql)
	state.isOutbox = strings.Contains(sqlLower, "insert into outbox_event")
	isTxnBegin := sqlIsTxnBegin(sqlLower)
	isTxnEnd := sqlIsTxnEnd(sqlLower)
	argTxnNos := extractUUIDArgs(data.Args)

	t.mu.Lock()
	if isTxnBegin {
		t.connInTxn[conn] = true
	}
	t.captureSampleTargetsLocked(sqlLower, data.Args)

	flow, txnNo := t.matchFlowByTxnNosLocked(argTxnNos)
	if flow != sampledTxnFlowNone && t.connInTxn[conn] {
		t.connFlow[conn] = flow
	}
	if flow == sampledTxnFlowNone && t.connInTxn[conn] {
		if connFlow, ok := t.connFlow[conn]; ok {
			flow = connFlow
			txnNo = t.flowTxnNoLocked(connFlow)
		}
	}
	if isTxnEnd && t.connInTxn[conn] {
		if connFlow, ok := t.connFlow[conn]; ok {
			flow = connFlow
			txnNo = t.flowTxnNoLocked(connFlow)
		}
		state.clearConn = true
	}

	state.flow = flow
	state.txnNo = txnNo
	t.mu.Unlock()

	if state.flow == sampledTxnFlowNone {
		return ctx
	}
	return context.WithValue(ctx, sqlDebugTraceStateKey{}, state)
}

func (t *sampledTxnSQLTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	rawState := ctx.Value(sqlDebugTraceStateKey{})
	state, ok := rawState.(sqlDebugTraceState)
	if !ok || state.flow == sampledTxnFlowNone {
		return
	}

	elapsed := time.Since(state.start)
	slow := elapsed >= t.slowThreshold

	var summaryLines []string
	t.mu.Lock()
	t.recordTimingLocked(state.flow, state.queryName, state.sql, elapsed)
	if state.isOutbox {
		t.markOutboxSeenLocked(state.flow)
	}
	if state.clearConn {
		delete(t.connFlow, state.conn)
		delete(t.connInTxn, state.conn)
		summaryLines = t.buildSummaryIfReadyLocked(state.flow)
	}
	t.mu.Unlock()

	if data.Err != nil {
		log.Printf("[sql-debug] flow=%s txn_no=%s elapsed_ms=%.3f slow=%t query=%s sql=%q err=%v",
			state.flow, state.txnNo, elapsed.Seconds()*1000, slow, queryDisplayName(state.queryName, state.sql), state.sql, data.Err)
	} else {
		log.Printf("[sql-debug] flow=%s txn_no=%s elapsed_ms=%.3f slow=%t query=%s sql=%q",
			state.flow, state.txnNo, elapsed.Seconds()*1000, slow, queryDisplayName(state.queryName, state.sql), state.sql)
	}
	for _, line := range summaryLines {
		log.Print(line)
	}
}

func (t *sampledTxnSQLTracer) captureSampleTargetsLocked(sqlLower string, args []any) {
	if !strings.Contains(sqlLower, "insert into txn") {
		return
	}
	txnNo := uuidFromArgAt(args, 0)
	if txnNo == "" {
		return
	}
	refundOfTxnNo := uuidFromArgAt(args, 10)
	if refundOfTxnNo == "" {
		if t.forward.txnNo == "" {
			t.forward.txnNo = txnNo
			log.Printf("[sql-debug] sampled forward txn_no=%s", txnNo)
		}
		return
	}
	if t.reverse.txnNo == "" {
		t.reverse.txnNo = txnNo
		t.reverse.originTxnNo = refundOfTxnNo
		log.Printf("[sql-debug] sampled reverse txn_no=%s origin_txn_no=%s", txnNo, refundOfTxnNo)
	}
}

func (t *sampledTxnSQLTracer) matchFlowByTxnNosLocked(argTxnNos []string) (sampledTxnFlow, string) {
	if len(argTxnNos) == 0 {
		return sampledTxnFlowNone, ""
	}

	// Reverse flow takes precedence because it touches both refund txn_no and origin txn_no.
	if !t.reverse.summarized && t.reverse.txnNo != "" && (containsString(argTxnNos, t.reverse.txnNo) || containsString(argTxnNos, t.reverse.originTxnNo)) {
		return sampledTxnFlowReverse, t.reverse.txnNo
	}
	if !t.forward.summarized && t.forward.txnNo != "" && containsString(argTxnNos, t.forward.txnNo) {
		return sampledTxnFlowForward, t.forward.txnNo
	}
	return sampledTxnFlowNone, ""
}

func (t *sampledTxnSQLTracer) flowTxnNoLocked(flow sampledTxnFlow) string {
	switch flow {
	case sampledTxnFlowForward:
		return t.forward.txnNo
	case sampledTxnFlowReverse:
		return t.reverse.txnNo
	default:
		return ""
	}
}

func (t *sampledTxnSQLTracer) flowStateLocked(flow sampledTxnFlow) *sampledTxnFlowState {
	switch flow {
	case sampledTxnFlowForward:
		return &t.forward
	case sampledTxnFlowReverse:
		return &t.reverse
	default:
		return nil
	}
}

func (t *sampledTxnSQLTracer) recordTimingLocked(flow sampledTxnFlow, queryName, sql string, elapsed time.Duration) {
	flowState := t.flowStateLocked(flow)
	if flowState == nil {
		return
	}
	key := queryName
	if key == "" {
		key = sql
	}
	if flowState.stats == nil {
		flowState.stats = map[string]*queryTimingStat{}
	}
	stat, ok := flowState.stats[key]
	if !ok {
		stat = &queryTimingStat{
			queryName: queryName,
			sql:       sql,
		}
		flowState.stats[key] = stat
	}
	stat.count++
	stat.total += elapsed
	if elapsed > stat.max {
		stat.max = elapsed
	}
}

func (t *sampledTxnSQLTracer) markOutboxSeenLocked(flow sampledTxnFlow) {
	flowState := t.flowStateLocked(flow)
	if flowState == nil {
		return
	}
	flowState.outboxSeen = true
}

func (t *sampledTxnSQLTracer) buildSummaryIfReadyLocked(flow sampledTxnFlow) []string {
	flowState := t.flowStateLocked(flow)
	if flowState == nil || flowState.summarized || !flowState.outboxSeen || flowState.txnNo == "" {
		return nil
	}
	flowState.summarized = true

	stats := make([]*queryTimingStat, 0, len(flowState.stats))
	for _, stat := range flowState.stats {
		stats = append(stats, stat)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].total == stats[j].total {
			return stats[i].max > stats[j].max
		}
		return stats[i].total > stats[j].total
	})
	if len(stats) > maxSQLDebugSummaryEntries {
		stats = stats[:maxSQLDebugSummaryEntries]
	}

	lines := []string{
		fmt.Sprintf("[sql-debug][summary] flow=%s txn_no=%s sql_count=%d", flow, flowState.txnNo, len(stats)),
	}
	for _, stat := range stats {
		avg := time.Duration(0)
		if stat.count > 0 {
			avg = stat.total / time.Duration(stat.count)
		}
		label := queryDisplayName(stat.queryName, stat.sql)
		lines = append(lines, fmt.Sprintf(
			"[sql-debug][summary] flow=%s query=%s count=%d total_ms=%.3f avg_ms=%.3f max_ms=%.3f sql=%q",
			flow,
			label,
			stat.count,
			stat.total.Seconds()*1000,
			avg.Seconds()*1000,
			stat.max.Seconds()*1000,
			stat.sql,
		))
	}
	return lines
}

func queryDisplayName(queryName, sql string) string {
	if strings.TrimSpace(queryName) == "" {
		trimmed := strings.TrimSpace(sql)
		if len(trimmed) > 48 {
			trimmed = trimmed[:48] + "..."
		}
		return "raw_sql:" + trimmed
	}
	return queryName
}

func sqlIsTxnEnd(sqlLower string) bool {
	trimmed := strings.TrimSpace(sqlLower)
	return strings.HasPrefix(trimmed, "commit") || strings.HasPrefix(trimmed, "rollback")
}

func sqlIsTxnBegin(sqlLower string) bool {
	trimmed := strings.TrimSpace(sqlLower)
	return strings.HasPrefix(trimmed, "begin")
}

func compactSQL(sql string) string {
	compact := strings.Join(strings.Fields(sql), " ")
	if len(compact) > 240 {
		return compact[:240] + "..."
	}
	return compact
}

func extractSQLCQueryName(sql string) string {
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "-- name:") {
			return ""
		}
		parts := strings.Fields(trimmed)
		if len(parts) >= 3 {
			return strings.TrimSpace(parts[2])
		}
		return ""
	}
	return ""
}

func extractUUIDArgs(args []any) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	seen := make(map[string]struct{}, len(args))
	for _, arg := range args {
		txnNo := uuidFromAny(arg)
		if txnNo == "" {
			continue
		}
		if _, ok := seen[txnNo]; ok {
			continue
		}
		seen[txnNo] = struct{}{}
		out = append(out, txnNo)
	}
	return out
}

func uuidFromArgAt(args []any, idx int) string {
	if idx < 0 || idx >= len(args) {
		return ""
	}
	return uuidFromAny(args[idx])
}

func uuidFromAny(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return normalizeUUID(t)
	case []byte:
		return normalizeUUID(string(t))
	case pgtype.Text:
		if !t.Valid {
			return ""
		}
		return normalizeUUID(t.String)
	case *pgtype.Text:
		if t == nil || !t.Valid {
			return ""
		}
		return normalizeUUID(t.String)
	case pgtype.UUID:
		if !t.Valid {
			return ""
		}
		return normalizeUUID(uuid.UUID(t.Bytes).String())
	case *pgtype.UUID:
		if t == nil || !t.Valid {
			return ""
		}
		return normalizeUUID(uuid.UUID(t.Bytes).String())
	default:
		return ""
	}
}

func normalizeUUID(raw string) string {
	parsed, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.String())
}

func containsString(items []string, target string) bool {
	if target == "" {
		return false
	}
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func isTruthyEnv(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		return false
	}
}
