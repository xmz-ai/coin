package integration

import (
	"testing"
	"time"

	"github.com/xmz-ai/coin/internal/service"
)

func TestTC7001QueryByTxnNo(t *testing.T) {
	svc := service.NewTxnQueryService()
	base := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)

	svc.AddTxn(service.QueryTxn{TxnNo: "txn_7001", OutTradeNo: "ord_7001", MerchantNo: "m1", OutUserID: "u1", Scene: service.SceneIssue, Status: service.TxnStatusRecvSuccess, CreatedAt: base})

	got, ok := svc.GetByTxnNo("txn_7001")
	if !ok || got.TxnNo != "txn_7001" {
		t.Fatalf("query by txn_no failed")
	}
}

func TestTC7002QueryByOutTradeNo(t *testing.T) {
	svc := service.NewTxnQueryService()
	base := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)

	svc.AddTxn(service.QueryTxn{TxnNo: "txn_7002", OutTradeNo: "ord_7002", MerchantNo: "m1", OutUserID: "u1", Scene: service.SceneIssue, Status: service.TxnStatusRecvSuccess, CreatedAt: base})

	got, ok := svc.GetByOutTradeNo("m1", "ord_7002")
	if !ok || got.TxnNo != "txn_7002" {
		t.Fatalf("query by out_trade_no failed")
	}
}

func TestTC7003ListFiltersApply(t *testing.T) {
	svc := service.NewTxnQueryService()
	base := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)

	svc.AddTxn(service.QueryTxn{TxnNo: "txn_a", OutTradeNo: "ord_a", MerchantNo: "m1", OutUserID: "u1", Scene: service.SceneIssue, Status: service.TxnStatusRecvSuccess, CreatedAt: base.Add(-1 * time.Minute)})
	svc.AddTxn(service.QueryTxn{TxnNo: "txn_b", OutTradeNo: "ord_b", MerchantNo: "m1", OutUserID: "u2", Scene: service.SceneConsume, Status: service.TxnStatusFailed, CreatedAt: base.Add(-2 * time.Minute)})

	items, _ := svc.List(service.QueryFilter{MerchantNo: "m1", OutUserID: "u1", Scene: service.SceneIssue, Status: service.TxnStatusRecvSuccess, PageSize: 10})
	if len(items) != 1 || items[0].TxnNo != "txn_a" {
		t.Fatalf("filter result mismatch: %+v", items)
	}
}

func TestTC7004SeekPaginationNoDuplicatesNoMisses(t *testing.T) {
	svc := service.NewTxnQueryService()
	base := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		svc.AddTxn(service.QueryTxn{TxnNo: "txn_7004_" + string(rune('a'+i)), OutTradeNo: "ord_7004_" + string(rune('a'+i)), MerchantNo: "m1", OutUserID: "u1", Scene: service.SceneIssue, Status: service.TxnStatusRecvSuccess, CreatedAt: base.Add(-time.Duration(i) * time.Minute)})
	}

	page1, token1 := svc.List(service.QueryFilter{MerchantNo: "m1", PageSize: 2})
	page2, token2 := svc.List(service.QueryFilter{MerchantNo: "m1", PageSize: 2, PageToken: token1})
	page3, _ := svc.List(service.QueryFilter{MerchantNo: "m1", PageSize: 2, PageToken: token2})

	seen := map[string]struct{}{}
	for _, x := range append(append(page1, page2...), page3...) {
		if _, ok := seen[x.TxnNo]; ok {
			t.Fatalf("duplicate txn in seek paging: %s", x.TxnNo)
		}
		seen[x.TxnNo] = struct{}{}
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 unique txns, got %d", len(seen))
	}
}

func TestTC7005PaginationStableWithConcurrentInsert(t *testing.T) {
	svc := service.NewTxnQueryService()
	base := time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC)

	svc.AddTxn(service.QueryTxn{TxnNo: "txn_1", OutTradeNo: "ord_1", MerchantNo: "m1", OutUserID: "u1", Scene: service.SceneIssue, Status: service.TxnStatusRecvSuccess, CreatedAt: base})
	svc.AddTxn(service.QueryTxn{TxnNo: "txn_2", OutTradeNo: "ord_2", MerchantNo: "m1", OutUserID: "u1", Scene: service.SceneIssue, Status: service.TxnStatusRecvSuccess, CreatedAt: base.Add(-1 * time.Minute)})
	svc.AddTxn(service.QueryTxn{TxnNo: "txn_3", OutTradeNo: "ord_3", MerchantNo: "m1", OutUserID: "u1", Scene: service.SceneIssue, Status: service.TxnStatusRecvSuccess, CreatedAt: base.Add(-2 * time.Minute)})

	page1, token1 := svc.List(service.QueryFilter{MerchantNo: "m1", PageSize: 2})
	svc.AddTxn(service.QueryTxn{TxnNo: "txn_new", OutTradeNo: "ord_new", MerchantNo: "m1", OutUserID: "u1", Scene: service.SceneIssue, Status: service.TxnStatusRecvSuccess, CreatedAt: base.Add(1 * time.Minute)})
	page2, _ := svc.List(service.QueryFilter{MerchantNo: "m1", PageSize: 2, PageToken: token1})

	if len(page1) != 2 || len(page2) == 0 {
		t.Fatalf("unexpected paging size: page1=%d page2=%d", len(page1), len(page2))
	}
	for _, x := range page2 {
		if x.TxnNo == "txn_new" {
			t.Fatalf("newly inserted txn should not flow back into old window")
		}
	}
}
