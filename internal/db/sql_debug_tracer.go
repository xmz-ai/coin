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
	maxSQLDebugTrackedTxns    = 10000
)

type sampledTxnSQLTracer struct {
	slowThreshold time.Duration

	mu        sync.Mutex
	connTxnNo map[*pgx.Conn]string
	connInTxn map[*pgx.Conn]bool
	txnState  map[string]*sampledTxnState
	txnAlias  map[string]string
}

type sampledTxnState struct {
	scenario    string
	txnNo       string
	outTradeNo  string
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
	scenario  string
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
		connTxnNo:     map[*pgx.Conn]string{},
		connInTxn:     map[*pgx.Conn]bool{},
		txnState:      map[string]*sampledTxnState{},
		txnAlias:      map[string]string{},
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

	if matched := t.matchTxnStateByTxnNosLocked(argTxnNos); matched != nil {
		state.scenario = matched.scenario
		state.txnNo = matched.txnNo
		if t.connInTxn[conn] {
			t.connTxnNo[conn] = matched.txnNo
		}
	}

	if state.txnNo == "" && t.connInTxn[conn] {
		if connTxnNo, ok := t.connTxnNo[conn]; ok {
			state.txnNo = connTxnNo
			if txnState, ok := t.txnState[connTxnNo]; ok {
				state.scenario = txnState.scenario
			}
		}
	}

	if state.txnNo != "" {
		if _, ok := t.txnState[state.txnNo]; !ok {
			state.scenario = ""
			state.txnNo = ""
			delete(t.connTxnNo, conn)
		}
	}

	if isTxnEnd && t.connInTxn[conn] {
		state.clearConn = true
		if state.txnNo == "" {
			if connTxnNo, ok := t.connTxnNo[conn]; ok {
				if txnState, ok := t.txnState[connTxnNo]; ok {
					state.scenario = txnState.scenario
					state.txnNo = connTxnNo
				}
			}
		}
	}

	t.mu.Unlock()

	if state.scenario == "" {
		return ctx
	}
	return context.WithValue(ctx, sqlDebugTraceStateKey{}, state)
}

func (t *sampledTxnSQLTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	rawState := ctx.Value(sqlDebugTraceStateKey{})
	state, ok := rawState.(sqlDebugTraceState)
	if !ok {
		return
	}
	if state.scenario == "" {
		if state.clearConn {
			t.mu.Lock()
			delete(t.connTxnNo, state.conn)
			delete(t.connInTxn, state.conn)
			t.mu.Unlock()
		}
		return
	}

	elapsed := time.Since(state.start)
	slow := elapsed >= t.slowThreshold

	var summaryLines []string
	t.mu.Lock()
	if txnState, ok := t.txnState[state.txnNo]; ok {
		t.recordTimingLocked(txnState, state.queryName, state.sql, elapsed)
		if state.isOutbox {
			t.markOutboxSeenLocked(txnState)
		}
		if state.clearConn {
			delete(t.connTxnNo, state.conn)
			delete(t.connInTxn, state.conn)
			summaryLines = t.buildSummaryIfReadyLocked(txnState)
		}
	} else if state.clearConn {
		delete(t.connTxnNo, state.conn)
		delete(t.connInTxn, state.conn)
	}
	t.mu.Unlock()

	if data.Err != nil {
		log.Printf("[sql-debug] scenario=%s txn_no=%s elapsed_ms=%.3f slow=%t query=%s sql=%q err=%v",
			state.scenario, state.txnNo, elapsed.Seconds()*1000, slow, queryDisplayName(state.queryName, state.sql), state.sql, data.Err)
	} else {
		log.Printf("[sql-debug] scenario=%s txn_no=%s elapsed_ms=%.3f slow=%t query=%s sql=%q",
			state.scenario, state.txnNo, elapsed.Seconds()*1000, slow, queryDisplayName(state.queryName, state.sql), state.sql)
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
	outTradeNo := stringFromArgAt(args, 2)
	scenario := scenarioFromOutTradeNo(outTradeNo)
	if scenario == "" {
		return
	}
	refundOfTxnNo := uuidFromArgAt(args, 10)
	if _, exists := t.txnState[txnNo]; exists {
		t.txnAlias[txnNo] = txnNo
		if refundOfTxnNo != "" {
			t.txnAlias[refundOfTxnNo] = txnNo
		}
		return
	}

	if len(t.txnState) >= maxSQLDebugTrackedTxns {
		return
	}

	t.txnState[txnNo] = &sampledTxnState{
		scenario:    scenario,
		txnNo:       txnNo,
		outTradeNo:  outTradeNo,
		originTxnNo: refundOfTxnNo,
		stats:       map[string]*queryTimingStat{},
	}
	t.txnAlias[txnNo] = txnNo
	if refundOfTxnNo != "" {
		t.txnAlias[refundOfTxnNo] = txnNo
		log.Printf("[sql-debug] sampled scenario=%s txn_no=%s origin_txn_no=%s out_trade_no=%s", scenario, txnNo, refundOfTxnNo, outTradeNo)
		return
	}
	log.Printf("[sql-debug] sampled scenario=%s txn_no=%s out_trade_no=%s", scenario, txnNo, outTradeNo)
}

func (t *sampledTxnSQLTracer) matchTxnStateByTxnNosLocked(argTxnNos []string) *sampledTxnState {
	if len(argTxnNos) == 0 {
		return nil
	}
	for _, argTxnNo := range argTxnNos {
		primaryTxnNo := t.txnAlias[argTxnNo]
		if primaryTxnNo == "" {
			primaryTxnNo = argTxnNo
		}
		txnState := t.txnState[primaryTxnNo]
		if txnState == nil || txnState.summarized {
			continue
		}
		return txnState
	}
	return nil
}

func (t *sampledTxnSQLTracer) recordTimingLocked(txnState *sampledTxnState, queryName, sql string, elapsed time.Duration) {
	if txnState == nil {
		return
	}
	key := queryName
	if key == "" {
		key = sql
	}
	if txnState.stats == nil {
		txnState.stats = map[string]*queryTimingStat{}
	}
	stat, ok := txnState.stats[key]
	if !ok {
		stat = &queryTimingStat{
			queryName: queryName,
			sql:       sql,
		}
		txnState.stats[key] = stat
	}
	stat.count++
	stat.total += elapsed
	if elapsed > stat.max {
		stat.max = elapsed
	}
}

func (t *sampledTxnSQLTracer) markOutboxSeenLocked(txnState *sampledTxnState) {
	if txnState == nil {
		return
	}
	txnState.outboxSeen = true
}

func (t *sampledTxnSQLTracer) buildSummaryIfReadyLocked(txnState *sampledTxnState) []string {
	if txnState == nil || txnState.summarized || !txnState.outboxSeen || txnState.txnNo == "" {
		return nil
	}
	txnState.summarized = true

	stats := make([]*queryTimingStat, 0, len(txnState.stats))
	for _, stat := range txnState.stats {
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

	lines := []string{}
	if txnState.originTxnNo != "" {
		lines = append(lines, fmt.Sprintf("[sql-debug][summary] scenario=%s txn_no=%s origin_txn_no=%s out_trade_no=%s sql_count=%d",
			txnState.scenario, txnState.txnNo, txnState.originTxnNo, txnState.outTradeNo, len(stats)))
	} else {
		lines = append(lines, fmt.Sprintf("[sql-debug][summary] scenario=%s txn_no=%s out_trade_no=%s sql_count=%d",
			txnState.scenario, txnState.txnNo, txnState.outTradeNo, len(stats)))
	}

	for _, stat := range stats {
		avg := time.Duration(0)
		if stat.count > 0 {
			avg = stat.total / time.Duration(stat.count)
		}
		label := queryDisplayName(stat.queryName, stat.sql)
		lines = append(lines, fmt.Sprintf(
			"[sql-debug][summary] scenario=%s txn_no=%s query=%s count=%d total_ms=%.3f avg_ms=%.3f max_ms=%.3f sql=%q",
			txnState.scenario,
			txnState.txnNo,
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

func scenarioFromOutTradeNo(outTradeNo string) string {
	outTradeNo = strings.TrimSpace(outTradeNo)
	switch {
	case strings.HasPrefix(outTradeNo, "ord_perf_book_to_book_transfer_"):
		return "book_to_book_transfer"
	case strings.HasPrefix(outTradeNo, "ord_perf_book_to_book_refund_"):
		return "book_to_book_refund"
	case strings.HasPrefix(outTradeNo, "ord_perf_book_transfer_"):
		return "book_transfer"
	case strings.HasPrefix(outTradeNo, "ord_perf_book_refund_"):
		return "book_refund"
	case strings.HasPrefix(outTradeNo, "ord_perf_transfer_"):
		return "transfer"
	case strings.HasPrefix(outTradeNo, "ord_perf_refund_"):
		return "refund"
	default:
		return ""
	}
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

func stringFromArgAt(args []any, idx int) string {
	if idx < 0 || idx >= len(args) {
		return ""
	}
	return stringFromAny(args[idx])
}

func stringFromAny(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case []byte:
		return strings.TrimSpace(string(t))
	case pgtype.Text:
		if !t.Valid {
			return ""
		}
		return strings.TrimSpace(t.String)
	case *pgtype.Text:
		if t == nil || !t.Valid {
			return ""
		}
		return strings.TrimSpace(t.String)
	default:
		return ""
	}
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
