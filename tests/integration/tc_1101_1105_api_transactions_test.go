package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xmz-ai/coin/internal/api"
	"github.com/xmz-ai/coin/internal/db"
	idpkg "github.com/xmz-ai/coin/internal/platform/id"
	"github.com/xmz-ai/coin/internal/platform/security"
	"github.com/xmz-ai/coin/internal/service"
)

const (
	testDebitAccountNo  = "6217701201001106001"
	testCreditAccountNo = "6217701201001106002"
)

func TestTC1101APICreditSuccess(t *testing.T) {
	r, repo, _, merchantNo, secret := newTxnAPITestServer(t)

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1101", map[string]any{
		"out_trade_no": "ord_1101",
		"user_id":      "u_1101",
		"amount":       100,
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}

	body := decodeJSONMap(t, w.Body.Bytes())
	if body["code"] != "SUCCESS" {
		t.Fatalf("expected SUCCESS, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	txnNo, _ := data["txn_no"].(string)
	if txnNo == "" {
		t.Fatalf("expected txn_no to be non-empty")
	}
	if data["status"] != service.TxnStatusInit {
		t.Fatalf("expected status %s, got %v", service.TxnStatusInit, data["status"])
	}
	waitTxnStatus(t, r, merchantNo, secret, txnNo, service.TxnStatusRecvSuccess)
	if got := repo.TxnCount(); got != 1 {
		t.Fatalf("expected txn count 1, got %d", got)
	}
}

func TestTC1102APICreditDuplicateReturns409(t *testing.T) {
	r, repo, _, merchantNo, secret := newTxnAPITestServer(t)

	first := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1102-1", map[string]any{
		"out_trade_no": "ord_1102",
		"user_id":      "u_1101",
		"amount":       100,
	})
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, first)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first submit expected 201, got %d body=%s", w1.Code, w1.Body.String())
	}

	second := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1102-2", map[string]any{
		"out_trade_no": "ord_1102",
		"user_id":      "u_1101",
		"amount":       500,
	})
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, second)
	if w2.Code != http.StatusConflict {
		t.Fatalf("second submit expected 409, got %d body=%s", w2.Code, w2.Body.String())
	}

	body := decodeJSONMap(t, w2.Body.Bytes())
	if body["code"] != "DUPLICATE_OUT_TRADE_NO" {
		t.Fatalf("expected DUPLICATE_OUT_TRADE_NO, got %v", body["code"])
	}
	if got := repo.TxnCount(); got != 1 {
		t.Fatalf("expected txn count remain 1, got %d", got)
	}
}

func TestTC1102APICreditBookEnabledRequiresExpireInDays(t *testing.T) {
	r, repo, pool, merchantNo, secret := newTxnAPITestServer(t)

	creditAccount, ok := repo.GetAccount(testCreditAccountNo)
	if !ok {
		t.Fatalf("credit account not found")
	}
	setAccountBookEnabled(t, pool, creditAccount.AccountNo, true)

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1102-book", map[string]any{
		"out_trade_no": "ord_1102_book",
		"user_id":      "u_1101",
		"amount":       100,
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	body := decodeJSONMap(t, resp.Body.Bytes())
	if body["code"] != "INVALID_PARAM" {
		t.Fatalf("expected INVALID_PARAM, got %v", body["code"])
	}
}

func TestTC1102APICreditBookEnabledNormalizesExpireInDaysToUTCDate(t *testing.T) {
	r, repo, pool, merchantNo, secret := newTxnAPITestServer(t)

	creditAccount, ok := repo.GetAccount(testCreditAccountNo)
	if !ok {
		t.Fatalf("credit account not found")
	}
	creditAccount.BookEnabled = true
	setAccountBookEnabled(t, pool, creditAccount.AccountNo, true)

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1102-book-norm", map[string]any{
		"out_trade_no":    "ord_1102_book_norm",
		"user_id":         "u_1101",
		"amount":          100,
		"expire_in_days":  1,
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", resp.Code, resp.Body.String())
	}

	body := decodeJSONMap(t, resp.Body.Bytes())
	txnNo, _ := body["data"].(map[string]any)["txn_no"].(string)
	if txnNo == "" {
		t.Fatalf("expected txn_no in response")
	}
	waitTxnStatus(t, r, merchantNo, secret, txnNo, service.TxnStatusRecvSuccess)

	txn, ok := repo.GetTransferTxn(txnNo)
	if !ok {
		t.Fatalf("txn not found")
	}
	want := time.Date(2024, 3, 10, 0, 0, 0, 0, time.UTC)
	if !txn.CreditExpireAt.UTC().Equal(want) {
		t.Fatalf("unexpected normalized credit_expire_at: got=%s want=%s", txn.CreditExpireAt.UTC(), want)
	}
}

func TestTC1103APIGetByOutTradeNo(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	createReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1103-1", map[string]any{
		"out_trade_no": "ord_1103",
		"user_id":      "u_1101",
		"amount":       100,
	})
	createResp := httptest.NewRecorder()
	r.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create expected 201, got %d body=%s", createResp.Code, createResp.Body.String())
	}

	queryReq := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions/by-out-trade-no/ord_1103", merchantNo, secret, "nonce-1103-2", nil)
	queryResp := httptest.NewRecorder()
	r.ServeHTTP(queryResp, queryReq)
	if queryResp.Code != http.StatusOK {
		t.Fatalf("query expected 200, got %d body=%s", queryResp.Code, queryResp.Body.String())
	}

	body := decodeJSONMap(t, queryResp.Body.Bytes())
	data := body["data"].(map[string]any)
	if data["out_trade_no"] != "ord_1103" {
		t.Fatalf("unexpected out_trade_no: %v", data["out_trade_no"])
	}
}

func TestTC1104APIGetByTxnNo(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	createReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1104-1", map[string]any{
		"out_trade_no": "ord_1104",
		"user_id":      "u_1101",
		"amount":       100,
	})
	createResp := httptest.NewRecorder()
	r.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create expected 201, got %d body=%s", createResp.Code, createResp.Body.String())
	}

	createBody := decodeJSONMap(t, createResp.Body.Bytes())
	txnNo := createBody["data"].(map[string]any)["txn_no"].(string)

	queryReq := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions/"+txnNo, merchantNo, secret, "nonce-1104-2", nil)
	queryResp := httptest.NewRecorder()
	r.ServeHTTP(queryResp, queryReq)
	if queryResp.Code != http.StatusOK {
		t.Fatalf("query expected 200, got %d body=%s", queryResp.Code, queryResp.Body.String())
	}

	body := decodeJSONMap(t, queryResp.Body.Bytes())
	data := body["data"].(map[string]any)
	if data["txn_no"] != txnNo {
		t.Fatalf("unexpected txn_no: %v", data["txn_no"])
	}
}

func TestTC1105APIListTransactionsSeekPagination(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	for i := 1; i <= 3; i++ {
		outTradeNo := "ord_1105_" + strconv.Itoa(i)
		req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1105-"+strconv.Itoa(i), map[string]any{
			"out_trade_no": outTradeNo,
			"user_id":      "u_1101",
			"amount":       100,
		})
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)
		if resp.Code != http.StatusCreated {
			t.Fatalf("create %d expected 201, got %d body=%s", i, resp.Code, resp.Body.String())
		}
	}

	page1Req := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions?page_size=2", merchantNo, secret, "nonce-1105-page1", nil)
	page1Resp := httptest.NewRecorder()
	r.ServeHTTP(page1Resp, page1Req)
	if page1Resp.Code != http.StatusOK {
		t.Fatalf("page1 expected 200, got %d body=%s", page1Resp.Code, page1Resp.Body.String())
	}
	page1 := decodeJSONMap(t, page1Resp.Body.Bytes())
	page1Data := page1["data"].(map[string]any)
	page1Items := page1Data["items"].([]any)
	if len(page1Items) != 2 {
		t.Fatalf("expected page1 items=2, got %d", len(page1Items))
	}
	nextToken := page1Data["next_page_token"].(string)
	if nextToken == "" {
		t.Fatalf("expected next_page_token")
	}

	page2Req := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions?page_size=2&page_token="+url.QueryEscape(nextToken), merchantNo, secret, "nonce-1105-page2", nil)
	page2Resp := httptest.NewRecorder()
	r.ServeHTTP(page2Resp, page2Req)
	if page2Resp.Code != http.StatusOK {
		t.Fatalf("page2 expected 200, got %d body=%s", page2Resp.Code, page2Resp.Body.String())
	}
	page2 := decodeJSONMap(t, page2Resp.Body.Bytes())
	page2Data := page2["data"].(map[string]any)
	page2Items := page2Data["items"].([]any)
	if len(page2Items) != 1 {
		t.Fatalf("expected page2 items=1, got %d", len(page2Items))
	}

	seen := map[string]struct{}{}
	all := append(page1Items, page2Items...)
	for _, row := range all {
		m := row.(map[string]any)
		txnNo := m["txn_no"].(string)
		if _, ok := seen[txnNo]; ok {
			t.Fatalf("duplicate txn_no in pagination: %s", txnNo)
		}
		seen[txnNo] = struct{}{}
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 unique txns, got %d", len(seen))
	}

	for i := 1; i < len(all); i++ {
		prev := all[i-1].(map[string]any)
		curr := all[i].(map[string]any)
		prevAt, err := time.Parse(time.RFC3339Nano, prev["created_at"].(string))
		if err != nil {
			t.Fatalf("parse prev created_at failed: %v", err)
		}
		currAt, err := time.Parse(time.RFC3339Nano, curr["created_at"].(string))
		if err != nil {
			t.Fatalf("parse curr created_at failed: %v", err)
		}
		prevAt = prevAt.UTC()
		currAt = currAt.UTC()
		if prevAt.Before(currAt) {
			t.Fatalf("expected DESC created_at ordering, got prev=%s curr=%s", prevAt, currAt)
		}
		if prevAt.Equal(currAt) && prev["txn_no"].(string) < curr["txn_no"].(string) {
			t.Fatalf("expected DESC txn_no ordering on same created_at, got prev=%s curr=%s", prev["txn_no"].(string), curr["txn_no"].(string))
		}
	}
}

func TestTC1106APIDebitSuccessAndDuplicate409(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/debit", merchantNo, secret, "nonce-1106-1", map[string]any{
		"out_trade_no":     "ord_1106",
		"biz_type":         "TRANSFER",
		"transfer_scene":   "CONSUME",
		"debit_account_no": testDebitAccountNo,
		"amount":           20,
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("debit expected 201, got %d body=%s", resp.Code, resp.Body.String())
	}

	dupReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/debit", merchantNo, secret, "nonce-1106-2", map[string]any{
		"out_trade_no":     "ord_1106",
		"transfer_scene":   "CONSUME",
		"debit_account_no": testDebitAccountNo,
		"amount":           30,
	})
	dupResp := httptest.NewRecorder()
	r.ServeHTTP(dupResp, dupReq)
	if dupResp.Code != http.StatusConflict {
		t.Fatalf("duplicate debit expected 409, got %d body=%s", dupResp.Code, dupResp.Body.String())
	}
	body := decodeJSONMap(t, dupResp.Body.Bytes())
	if body["code"] != "DUPLICATE_OUT_TRADE_NO" {
		t.Fatalf("expected DUPLICATE_OUT_TRADE_NO, got %v", body["code"])
	}
}

func TestTC1107APITransferForbidTransfer(t *testing.T) {
	r, repo, _, merchantNo, secret := newTxnAPITestServer(t)
	repo.UpdateAccountCapabilities(testDebitAccountNo, true, true, false)

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/transfer", merchantNo, secret, "nonce-1107-1", map[string]any{
		"out_trade_no":    "ord_1107",
		"biz_type":        "TRANSFER",
		"transfer_scene":  "P2P",
		"from_account_no": testDebitAccountNo,
		"to_account_no":   testCreditAccountNo,
		"amount":          10,
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("transfer forbid expected 403, got %d body=%s", resp.Code, resp.Body.String())
	}
	body := decodeJSONMap(t, resp.Body.Bytes())
	if body["code"] != "ACCOUNT_FORBID_TRANSFER" {
		t.Fatalf("expected ACCOUNT_FORBID_TRANSFER, got %v", body["code"])
	}
}

func TestTC1108APITransferSuccess(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/transfer", merchantNo, secret, "nonce-1108-1", map[string]any{
		"out_trade_no":    "ord_1108",
		"biz_type":        "TRANSFER",
		"transfer_scene":  "P2P",
		"from_account_no": testDebitAccountNo,
		"to_account_no":   testCreditAccountNo,
		"amount":          15,
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("transfer expected 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	submitBody := decodeJSONMap(t, resp.Body.Bytes())
	if submitBody["code"] != "SUCCESS" {
		t.Fatalf("expected SUCCESS, got %v", submitBody["code"])
	}
	submitData := submitBody["data"].(map[string]any)
	txnNo, _ := submitData["txn_no"].(string)
	if txnNo == "" {
		t.Fatalf("expected txn_no")
	}
	if submitData["status"] != service.TxnStatusInit {
		t.Fatalf("expected status INIT, got %v", submitData["status"])
	}
	waitTxnStatus(t, r, merchantNo, secret, txnNo, service.TxnStatusRecvSuccess)

	queryReq := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions/by-out-trade-no/ord_1108", merchantNo, secret, "nonce-1108-2", nil)
	queryResp := httptest.NewRecorder()
	r.ServeHTTP(queryResp, queryReq)
	if queryResp.Code != http.StatusOK {
		t.Fatalf("query expected 200, got %d body=%s", queryResp.Code, queryResp.Body.String())
	}
	body := decodeJSONMap(t, queryResp.Body.Bytes())
	scene := body["data"].(map[string]any)["transfer_scene"]
	if scene != service.SceneP2P {
		t.Fatalf("expected transfer_scene P2P, got %v", scene)
	}
}

func TestTC1109APIRefundSuccess(t *testing.T) {
	r, repo, _, merchantNo, secret := newTxnAPITestServer(t)

	originReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/transfer", merchantNo, secret, "nonce-1109-origin", map[string]any{
		"out_trade_no":    "ord_1109_origin",
		"transfer_scene":  "P2P",
		"from_account_no": testDebitAccountNo,
		"to_account_no":   testCreditAccountNo,
		"amount":          60,
	})
	originResp := httptest.NewRecorder()
	r.ServeHTTP(originResp, originReq)
	if originResp.Code != http.StatusCreated {
		t.Fatalf("origin transfer expected 201, got %d body=%s", originResp.Code, originResp.Body.String())
	}
	originBody := decodeJSONMap(t, originResp.Body.Bytes())
	originTxnNo := originBody["data"].(map[string]any)["txn_no"].(string)
	waitTxnStatus(t, r, merchantNo, secret, originTxnNo, service.TxnStatusRecvSuccess)

	refundReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/refund", merchantNo, secret, "nonce-1109-refund", map[string]any{
		"out_trade_no":     "ord_1109_refund",
		"biz_type":         "REFUND",
		"refund_of_txn_no": originTxnNo,
		"amount":           20,
		"refund_breakdown": []map[string]any{
			{"account_no": testDebitAccountNo, "amount": 20},
		},
	})
	refundResp := httptest.NewRecorder()
	r.ServeHTTP(refundResp, refundReq)
	if refundResp.Code != http.StatusCreated {
		t.Fatalf("refund expected 201, got %d body=%s", refundResp.Code, refundResp.Body.String())
	}

	body := decodeJSONMap(t, refundResp.Body.Bytes())
	if body["code"] != "SUCCESS" {
		t.Fatalf("expected SUCCESS, got %v", body["code"])
	}
	refundData := body["data"].(map[string]any)
	refundTxnNo, _ := refundData["txn_no"].(string)
	if refundTxnNo == "" {
		t.Fatalf("expected refund txn_no")
	}
	if refundData["status"] != service.TxnStatusInit {
		t.Fatalf("expected refund submit status INIT, got %v", refundData["status"])
	}
	waitTxnStatus(t, r, merchantNo, secret, refundTxnNo, service.TxnStatusRecvSuccess)

	originTxn, ok := repo.GetTransferTxn(originTxnNo)
	if !ok {
		t.Fatalf("origin txn not found")
	}
	if originTxn.RefundableAmount != 40 {
		t.Fatalf("expected origin refundable_amount=40, got %d", originTxn.RefundableAmount)
	}

	events, err := repo.ClaimDueOutboxEventsByTxnNo(refundTxnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim refund outbox events failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 refund outbox event, got %+v", events)
	}
}

func TestTC1110APIRefundAmountExceeded(t *testing.T) {
	r, repo, _, merchantNo, secret := newTxnAPITestServer(t)

	originReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/transfer", merchantNo, secret, "nonce-1110-origin", map[string]any{
		"out_trade_no":    "ord_1110_origin",
		"transfer_scene":  "P2P",
		"from_account_no": testDebitAccountNo,
		"to_account_no":   testCreditAccountNo,
		"amount":          30,
	})
	originResp := httptest.NewRecorder()
	r.ServeHTTP(originResp, originReq)
	if originResp.Code != http.StatusCreated {
		t.Fatalf("origin transfer expected 201, got %d body=%s", originResp.Code, originResp.Body.String())
	}
	originTxnNo := decodeJSONMap(t, originResp.Body.Bytes())["data"].(map[string]any)["txn_no"].(string)
	waitTxnStatus(t, r, merchantNo, secret, originTxnNo, service.TxnStatusRecvSuccess)

	refundReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/refund", merchantNo, secret, "nonce-1110-refund", map[string]any{
		"out_trade_no":     "ord_1110_refund",
		"refund_of_txn_no": originTxnNo,
		"amount":           50,
	})
	refundResp := httptest.NewRecorder()
	r.ServeHTTP(refundResp, refundReq)
	if refundResp.Code != http.StatusCreated {
		t.Fatalf("refund submit expected 201, got %d body=%s", refundResp.Code, refundResp.Body.String())
	}
	refundBody := decodeJSONMap(t, refundResp.Body.Bytes())
	refundData := refundBody["data"].(map[string]any)
	refundTxnNo, _ := refundData["txn_no"].(string)
	if refundTxnNo == "" {
		t.Fatalf("expected refund txn_no")
	}
	if refundData["status"] != service.TxnStatusInit {
		t.Fatalf("expected refund submit status INIT, got %v", refundData["status"])
	}
	waitTxnStatus(t, r, merchantNo, secret, refundTxnNo, service.TxnStatusFailed)

	queryReq := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions/"+refundTxnNo, merchantNo, secret, "nonce-1110-query", nil)
	queryResp := httptest.NewRecorder()
	r.ServeHTTP(queryResp, queryReq)
	if queryResp.Code != http.StatusOK {
		t.Fatalf("query expected 200, got %d body=%s", queryResp.Code, queryResp.Body.String())
	}
	body := decodeJSONMap(t, queryResp.Body.Bytes())
	data := body["data"].(map[string]any)
	if data["status"] != service.TxnStatusFailed {
		t.Fatalf("expected FAILED status, got %v", data["status"])
	}
	if data["error_code"] != "REFUND_AMOUNT_EXCEEDED" {
		t.Fatalf("expected REFUND_AMOUNT_EXCEEDED, got %v", data["error_code"])
	}

	originTxn, ok := repo.GetTransferTxn(originTxnNo)
	if !ok {
		t.Fatalf("origin txn not found")
	}
	if originTxn.RefundableAmount != 30 {
		t.Fatalf("expected origin refundable_amount=30, got %d", originTxn.RefundableAmount)
	}
	events, err := repo.ClaimDueOutboxEventsByTxnNo(refundTxnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim refund outbox events failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 refund outbox event on failed refund, got %+v", events)
	}
}

func TestTC1111APIRefundBreakdownInvalid(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	originReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/transfer", merchantNo, secret, "nonce-1111-origin", map[string]any{
		"out_trade_no":    "ord_1111_origin",
		"transfer_scene":  "P2P",
		"from_account_no": testDebitAccountNo,
		"to_account_no":   testCreditAccountNo,
		"amount":          40,
	})
	originResp := httptest.NewRecorder()
	r.ServeHTTP(originResp, originReq)
	if originResp.Code != http.StatusCreated {
		t.Fatalf("origin transfer expected 201, got %d body=%s", originResp.Code, originResp.Body.String())
	}
	originTxnNo := decodeJSONMap(t, originResp.Body.Bytes())["data"].(map[string]any)["txn_no"].(string)
	waitTxnStatus(t, r, merchantNo, secret, originTxnNo, service.TxnStatusRecvSuccess)

	refundReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/refund", merchantNo, secret, "nonce-1111-refund", map[string]any{
		"out_trade_no":     "ord_1111_refund",
		"refund_of_txn_no": originTxnNo,
		"amount":           20,
		"refund_breakdown": []map[string]any{
			{"account_no": testDebitAccountNo, "amount": 10},
		},
	})
	refundResp := httptest.NewRecorder()
	r.ServeHTTP(refundResp, refundReq)
	if refundResp.Code != http.StatusBadRequest {
		t.Fatalf("refund breakdown invalid expected 400, got %d body=%s", refundResp.Code, refundResp.Body.String())
	}
}

func TestTC1112APIRefundConcurrentNoOverRefund(t *testing.T) {
	r, repo, _, merchantNo, secret := newTxnAPITestServer(t)

	originReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/transfer", merchantNo, secret, "nonce-1112-origin", map[string]any{
		"out_trade_no":    "ord_1112_origin",
		"transfer_scene":  "P2P",
		"from_account_no": testDebitAccountNo,
		"to_account_no":   testCreditAccountNo,
		"amount":          100,
	})
	originResp := httptest.NewRecorder()
	r.ServeHTTP(originResp, originReq)
	if originResp.Code != http.StatusCreated {
		t.Fatalf("origin transfer expected 201, got %d body=%s", originResp.Code, originResp.Body.String())
	}
	originTxnNo := decodeJSONMap(t, originResp.Body.Bytes())["data"].(map[string]any)["txn_no"].(string)
	waitTxnStatus(t, r, merchantNo, secret, originTxnNo, service.TxnStatusRecvSuccess)

	type result struct {
		code int
		body map[string]any
	}
	ch := make(chan result, 2)
	var wg sync.WaitGroup
	for i := 1; i <= 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/refund", merchantNo, secret, "nonce-1112-refund-"+strconv.Itoa(i), map[string]any{
				"out_trade_no":     "ord_1112_refund_" + strconv.Itoa(i),
				"refund_of_txn_no": originTxnNo,
				"amount":           80,
			})
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)
			ch <- result{code: resp.Code, body: decodeJSONMap(t, resp.Body.Bytes())}
		}(i)
	}
	wg.Wait()
	close(ch)

	submitted := make([]string, 0, 2)
	for item := range ch {
		if item.code != http.StatusCreated || item.body["code"] != "SUCCESS" {
			t.Fatalf("expected submit 201 SUCCESS, got code=%d body=%+v", item.code, item.body)
		}
		txnNo, _ := item.body["data"].(map[string]any)["txn_no"].(string)
		if txnNo == "" {
			t.Fatalf("expected refund txn_no in submit response")
		}
		submitted = append(submitted, txnNo)
	}

	success := 0
	exceeded := 0
	for i, txnNo := range submitted {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			queryReq := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions/"+txnNo, merchantNo, secret, "nonce-1112-query-"+strconv.Itoa(i), nil)
			queryResp := httptest.NewRecorder()
			r.ServeHTTP(queryResp, queryReq)
			if queryResp.Code != http.StatusOK {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			data := decodeJSONMap(t, queryResp.Body.Bytes())["data"].(map[string]any)
			status, _ := data["status"].(string)
			if status == service.TxnStatusRecvSuccess {
				success++
				break
			}
			if status == service.TxnStatusFailed && data["error_code"] == "REFUND_AMOUNT_EXCEEDED" {
				exceeded++
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if success != 1 || exceeded != 1 {
		t.Fatalf("expected success=1 exceeded=1, got success=%d exceeded=%d", success, exceeded)
	}

	originTxn, ok := repo.GetTransferTxn(originTxnNo)
	if !ok {
		t.Fatalf("origin txn not found")
	}
	if originTxn.RefundableAmount != 20 {
		t.Fatalf("expected origin refundable_amount=20, got %d", originTxn.RefundableAmount)
	}

	failedTxnNo := ""
	for _, txnNo := range submitted {
		queryReq := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions/"+txnNo, merchantNo, secret, "nonce-1112-final-"+txnNo, nil)
		queryResp := httptest.NewRecorder()
		r.ServeHTTP(queryResp, queryReq)
		if queryResp.Code != http.StatusOK {
			t.Fatalf("query expected 200, got %d body=%s", queryResp.Code, queryResp.Body.String())
		}
		data := decodeJSONMap(t, queryResp.Body.Bytes())["data"].(map[string]any)
		if data["status"] == service.TxnStatusFailed {
			failedTxnNo = txnNo
		}
	}
	if failedTxnNo == "" {
		t.Fatalf("expected one failed refund txn")
	}
	events, err := repo.ClaimDueOutboxEventsByTxnNo(failedTxnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim refund outbox events failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 refund outbox event on failed refund, got %+v", events)
	}
}

func TestTC1113APIRefundCrossMerchantOriginRejected(t *testing.T) {
	r, repo, _, merchantNo, secret := newTxnAPITestServer(t)

	otherMerchantSvc := service.NewMerchantService(repo, idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2f13",
	}))
	otherMerchant, err := otherMerchantSvc.CreateMerchant("", "other")
	if err != nil {
		t.Fatalf("create other merchant failed: %v", err)
	}

	otherOriginTxnNo := "01956f4e-9d22-73bc-8e11-3f5e9c7a2f14"
	if err := repo.CreateTransferTxn(service.TransferTxn{
		TxnNo:            otherOriginTxnNo,
		MerchantNo:       otherMerchant.MerchantNo,
		OutTradeNo:       "ord_1113_origin_other",
		BizType:          "TRANSFER",
		TransferScene:    service.SceneP2P,
		DebitAccountNo:   otherMerchant.BudgetAccountNo,
		CreditAccountNo:  otherMerchant.ReceivableAccountNo,
		Amount:           50,
		RefundableAmount: 50,
		Status:           service.TxnStatusRecvSuccess,
	}); err != nil {
		t.Fatalf("seed other origin txn failed: %v", err)
	}

	refundReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/refund", merchantNo, secret, "nonce-1113-refund", map[string]any{
		"out_trade_no":     "ord_1113_refund",
		"refund_of_txn_no": otherOriginTxnNo,
		"amount":           10,
	})
	refundResp := httptest.NewRecorder()
	r.ServeHTTP(refundResp, refundReq)
	if refundResp.Code != http.StatusCreated {
		t.Fatalf("refund submit expected 201, got %d body=%s", refundResp.Code, refundResp.Body.String())
	}
	refundBody := decodeJSONMap(t, refundResp.Body.Bytes())
	refundData := refundBody["data"].(map[string]any)
	refundTxnNo, _ := refundData["txn_no"].(string)
	if refundTxnNo == "" {
		t.Fatalf("expected refund txn_no")
	}
	if refundData["status"] != service.TxnStatusInit {
		t.Fatalf("expected refund submit status INIT, got %v", refundData["status"])
	}
	waitTxnStatus(t, r, merchantNo, secret, refundTxnNo, service.TxnStatusFailed)

	queryReq := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions/"+refundTxnNo, merchantNo, secret, "nonce-1113-query", nil)
	queryResp := httptest.NewRecorder()
	r.ServeHTTP(queryResp, queryReq)
	if queryResp.Code != http.StatusOK {
		t.Fatalf("query expected 200, got %d body=%s", queryResp.Code, queryResp.Body.String())
	}
	data := decodeJSONMap(t, queryResp.Body.Bytes())["data"].(map[string]any)
	if data["status"] != service.TxnStatusFailed {
		t.Fatalf("expected FAILED status, got %v", data["status"])
	}
	if data["error_code"] != "REFUND_ORIGIN_NOT_FOUND" {
		t.Fatalf("expected REFUND_ORIGIN_NOT_FOUND, got %v", data["error_code"])
	}
	events, err := repo.ClaimDueOutboxEventsByTxnNo(refundTxnNo, 10, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("claim refund outbox events failed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected 0 refund outbox event on failed refund, got %+v", events)
	}
}

func TestTC1114APIDebitByOutUserIDSuccess(t *testing.T) {
	r, repo, _, merchantNo, secret := newTxnAPITestServer(t)

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/debit", merchantNo, secret, "nonce-1114-1", map[string]any{
		"out_trade_no":      "ord_1114",
		"debit_out_user_id": "u_1100",
		"amount":            20,
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("debit expected 201, got %d body=%s", resp.Code, resp.Body.String())
	}

	queryReq := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions/by-out-trade-no/ord_1114", merchantNo, secret, "nonce-1114-2", nil)
	queryResp := httptest.NewRecorder()
	r.ServeHTTP(queryResp, queryReq)
	if queryResp.Code != http.StatusOK {
		t.Fatalf("query expected 200, got %d body=%s", queryResp.Code, queryResp.Body.String())
	}
	body := decodeJSONMap(t, queryResp.Body.Bytes())
	data := body["data"].(map[string]any)
	if data["debit_account_no"] != testDebitAccountNo {
		t.Fatalf("unexpected debit_account_no: %v", data["debit_account_no"])
	}
	merchant, ok := repo.GetMerchantByNo(merchantNo)
	if !ok {
		t.Fatalf("merchant not found")
	}
	if data["credit_account_no"] != merchant.ReceivableAccountNo {
		t.Fatalf("unexpected credit_account_no: %v", data["credit_account_no"])
	}
}

func TestTC1115APIDebitResolveConflict(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/debit", merchantNo, secret, "nonce-1115-1", map[string]any{
		"out_trade_no":      "ord_1115",
		"debit_account_no":  testDebitAccountNo,
		"debit_out_user_id": "u_1101",
		"amount":            20,
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", resp.Code, resp.Body.String())
	}
	body := decodeJSONMap(t, resp.Body.Bytes())
	if body["code"] != "ACCOUNT_RESOLVE_CONFLICT" {
		t.Fatalf("expected ACCOUNT_RESOLVE_CONFLICT, got %v", body["code"])
	}
}

func TestTC1116APITransferByOutUserIDSuccess(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/transfer", merchantNo, secret, "nonce-1116-1", map[string]any{
		"out_trade_no":     "ord_1116",
		"from_out_user_id": "u_1100",
		"to_out_user_id":   "u_1101",
		"amount":           15,
		"transfer_scene":   "P2P",
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("transfer expected 201, got %d body=%s", resp.Code, resp.Body.String())
	}

	queryReq := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions/by-out-trade-no/ord_1116", merchantNo, secret, "nonce-1116-2", nil)
	queryResp := httptest.NewRecorder()
	r.ServeHTTP(queryResp, queryReq)
	if queryResp.Code != http.StatusOK {
		t.Fatalf("query expected 200, got %d body=%s", queryResp.Code, queryResp.Body.String())
	}
	body := decodeJSONMap(t, queryResp.Body.Bytes())
	data := body["data"].(map[string]any)
	if data["debit_account_no"] != testDebitAccountNo {
		t.Fatalf("unexpected debit_account_no: %v", data["debit_account_no"])
	}
	if data["credit_account_no"] != testCreditAccountNo {
		t.Fatalf("unexpected credit_account_no: %v", data["credit_account_no"])
	}
}

func TestTC1117APITransferBookEnabledRequiresToExpireAt(t *testing.T) {
	r, repo, pool, merchantNo, secret := newTxnAPITestServer(t)

	creditAccount, ok := repo.GetAccount(testCreditAccountNo)
	if !ok {
		t.Fatalf("credit account not found")
	}
	creditAccount.BookEnabled = true
	setAccountBookEnabled(t, pool, creditAccount.AccountNo, true)

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/transfer", merchantNo, secret, "nonce-1117-1", map[string]any{
		"out_trade_no":     "ord_1117",
		"from_out_user_id": "u_1100",
		"to_out_user_id":   "u_1101",
		"amount":           10,
		"transfer_scene":   "P2P",
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	body := decodeJSONMap(t, resp.Body.Bytes())
	if body["code"] != "INVALID_PARAM" {
		t.Fatalf("expected INVALID_PARAM, got %v", body["code"])
	}
}

func TestTC1118APITransferBookEnabledAcceptsToExpireAt(t *testing.T) {
	r, repo, pool, merchantNo, secret := newTxnAPITestServer(t)

	creditAccount, ok := repo.GetAccount(testCreditAccountNo)
	if !ok {
		t.Fatalf("credit account not found")
	}
	creditAccount.BookEnabled = true
	setAccountBookEnabled(t, pool, creditAccount.AccountNo, true)

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/transfer", merchantNo, secret, "nonce-1118-1", map[string]any{
		"out_trade_no":     "ord_1118",
		"from_out_user_id": "u_1100",
		"to_out_user_id":   "u_1101",
		"amount":           10,
		"to_expire_at":     "2026-12-31T23:59:59Z",
		"transfer_scene":   "P2P",
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", resp.Code, resp.Body.String())
	}

	body := decodeJSONMap(t, resp.Body.Bytes())
	txnNo, _ := body["data"].(map[string]any)["txn_no"].(string)
	if txnNo == "" {
		t.Fatalf("expected txn_no in response")
	}
	waitTxnStatus(t, r, merchantNo, secret, txnNo, service.TxnStatusRecvSuccess)

	txn, ok := repo.GetTransferTxn(txnNo)
	if !ok {
		t.Fatalf("txn not found")
	}
	want := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	if !txn.CreditExpireAt.UTC().Equal(want) {
		t.Fatalf("unexpected normalized credit_expire_at: got=%s want=%s", txn.CreditExpireAt.UTC(), want)
	}
}

func TestTC1119APIListTransactionsFilterByOutUserID(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	issueReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1119-issue", map[string]any{
		"out_trade_no": "ord_1119_issue",
		"user_id":      "u_1101",
		"amount":       11,
	})
	issueResp := httptest.NewRecorder()
	r.ServeHTTP(issueResp, issueReq)
	if issueResp.Code != http.StatusCreated {
		t.Fatalf("issue expected 201, got %d body=%s", issueResp.Code, issueResp.Body.String())
	}

	consumeReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/debit", merchantNo, secret, "nonce-1119-consume", map[string]any{
		"out_trade_no":      "ord_1119_consume",
		"debit_out_user_id": "u_1100",
		"amount":            12,
	})
	consumeResp := httptest.NewRecorder()
	r.ServeHTTP(consumeResp, consumeReq)
	if consumeResp.Code != http.StatusCreated {
		t.Fatalf("consume expected 201, got %d body=%s", consumeResp.Code, consumeResp.Body.String())
	}

	listReq := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions?out_user_id=u_1100&page_size=20", merchantNo, secret, "nonce-1119-list", nil)
	listResp := httptest.NewRecorder()
	r.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d body=%s", listResp.Code, listResp.Body.String())
	}
	body := decodeJSONMap(t, listResp.Body.Bytes())
	items := body["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].(map[string]any)["out_trade_no"] != "ord_1119_consume" {
		t.Fatalf("unexpected out_trade_no: %v", items[0].(map[string]any)["out_trade_no"])
	}
}

func TestTC1120APIListTransactionsFilterByTimeRange(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	firstReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1120-1", map[string]any{
		"out_trade_no": "ord_1120_1",
		"user_id":      "u_1101",
		"amount":       10,
	})
	firstResp := httptest.NewRecorder()
	r.ServeHTTP(firstResp, firstReq)
	if firstResp.Code != http.StatusCreated {
		t.Fatalf("first expected 201, got %d body=%s", firstResp.Code, firstResp.Body.String())
	}

	firstQueryReq := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions/by-out-trade-no/ord_1120_1", merchantNo, secret, "nonce-1120-q1", nil)
	firstQueryResp := httptest.NewRecorder()
	r.ServeHTTP(firstQueryResp, firstQueryReq)
	if firstQueryResp.Code != http.StatusOK {
		t.Fatalf("first query expected 200, got %d body=%s", firstQueryResp.Code, firstQueryResp.Body.String())
	}
	firstCreatedAt := decodeJSONMap(t, firstQueryResp.Body.Bytes())["data"].(map[string]any)["created_at"].(string)

	time.Sleep(10 * time.Millisecond)

	secondReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1120-2", map[string]any{
		"out_trade_no": "ord_1120_2",
		"user_id":      "u_1101",
		"amount":       20,
	})
	secondResp := httptest.NewRecorder()
	r.ServeHTTP(secondResp, secondReq)
	if secondResp.Code != http.StatusCreated {
		t.Fatalf("second expected 201, got %d body=%s", secondResp.Code, secondResp.Body.String())
	}

	rangeReq := signedAPIRequest(
		t,
		http.MethodGet,
		"/api/v1/transactions?start_time="+url.QueryEscape(firstCreatedAt)+"&end_time="+url.QueryEscape(firstCreatedAt)+"&page_size=20",
		merchantNo,
		secret,
		"nonce-1120-range",
		nil,
	)
	rangeResp := httptest.NewRecorder()
	r.ServeHTTP(rangeResp, rangeReq)
	if rangeResp.Code != http.StatusOK {
		t.Fatalf("range query expected 200, got %d body=%s", rangeResp.Code, rangeResp.Body.String())
	}
	body := decodeJSONMap(t, rangeResp.Body.Bytes())
	items := body["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].(map[string]any)["out_trade_no"] != "ord_1120_1" {
		t.Fatalf("unexpected out_trade_no: %v", items[0].(map[string]any)["out_trade_no"])
	}
}

func TestTC1121APIListTransactionsInvalidStartTime(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	req := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions?start_time=bad-time", merchantNo, secret, "nonce-1121", nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", resp.Code, resp.Body.String())
	}
	body := decodeJSONMap(t, resp.Body.Bytes())
	if body["code"] != "INVALID_PARAM" {
		t.Fatalf("expected INVALID_PARAM, got %v", body["code"])
	}
}

func TestTC1122APIMerchantMeReturnsConfigAndRequestID(t *testing.T) {
	r, repo, _, merchantNo, secret := newTxnAPITestServer(t)

	req := signedAPIRequest(t, http.MethodGet, "/api/v1/merchants/me", merchantNo, secret, "nonce-1122", nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	body := decodeJSONMap(t, resp.Body.Bytes())
	if body["request_id"] == "" {
		t.Fatalf("expected request_id")
	}
	data := body["data"].(map[string]any)
	if data["merchant_no"] != merchantNo {
		t.Fatalf("unexpected merchant_no: %v", data["merchant_no"])
	}
	if data["status"] != "ACTIVE" {
		t.Fatalf("unexpected status: %v", data["status"])
	}
	if data["secret_version"] != float64(1) {
		t.Fatalf("unexpected secret_version: %v", data["secret_version"])
	}
	merchant, ok := repo.GetMerchantByNo(merchantNo)
	if !ok {
		t.Fatalf("merchant not found")
	}
	if data["name"] != merchant.Name {
		t.Fatalf("unexpected name: %v", data["name"])
	}
	if data["budget_account_no"] != merchant.BudgetAccountNo {
		t.Fatalf("unexpected budget_account_no: %v", data["budget_account_no"])
	}
	if data["receivable_account_no"] != merchant.ReceivableAccountNo {
		t.Fatalf("unexpected receivable_account_no: %v", data["receivable_account_no"])
	}
}

func TestTC1127APIGetWebhookConfigDefault(t *testing.T) {
	r, _, _, merchantNo, secret := newTxnAPITestServer(t)

	req := signedAPIRequest(t, http.MethodGet, "/api/v1/webhooks/config", merchantNo, secret, "nonce-1127", nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.Code, resp.Body.String())
	}

	body := decodeJSONMap(t, resp.Body.Bytes())
	if body["code"] != "SUCCESS" {
		t.Fatalf("expected SUCCESS, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	if data["url"] != "" {
		t.Fatalf("expected empty url, got %v", data["url"])
	}
	if data["enabled"] != false {
		t.Fatalf("expected enabled=false, got %v", data["enabled"])
	}
	retryPolicy, ok := data["retry_policy"].(map[string]any)
	if !ok {
		t.Fatalf("expected retry_policy object")
	}
	if retryPolicy["max_retries"] != float64(8) {
		t.Fatalf("unexpected max_retries: %v", retryPolicy["max_retries"])
	}
	backoff, ok := retryPolicy["backoff"].([]any)
	if !ok || len(backoff) != 5 {
		t.Fatalf("unexpected backoff policy: %v", retryPolicy["backoff"])
	}
}

func TestTC1128APIPutWebhookConfigThenGet(t *testing.T) {
	r, repo, _, merchantNo, secret := newTxnAPITestServer(t)

	putReq := signedAPIRequest(t, http.MethodPut, "/api/v1/webhooks/config", merchantNo, secret, "nonce-1128-put", map[string]any{
		"url":     "https://merchant.example.com/coin/webhook",
		"enabled": true,
	})
	putResp := httptest.NewRecorder()
	r.ServeHTTP(putResp, putReq)
	if putResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", putResp.Code, putResp.Body.String())
	}

	cfg, found, err := repo.GetWebhookConfig(merchantNo)
	if err != nil {
		t.Fatalf("get webhook config from repo failed: %v", err)
	}
	if !found {
		t.Fatalf("expected webhook config persisted")
	}
	if cfg.URL != "https://merchant.example.com/coin/webhook" || !cfg.Enabled {
		t.Fatalf("unexpected persisted webhook config: %+v", cfg)
	}

	getReq := signedAPIRequest(t, http.MethodGet, "/api/v1/webhooks/config", merchantNo, secret, "nonce-1128-get", nil)
	getResp := httptest.NewRecorder()
	r.ServeHTTP(getResp, getReq)
	if getResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", getResp.Code, getResp.Body.String())
	}
	body := decodeJSONMap(t, getResp.Body.Bytes())
	if body["code"] != "SUCCESS" {
		t.Fatalf("expected SUCCESS, got %v", body["code"])
	}
	data := body["data"].(map[string]any)
	if data["url"] != "https://merchant.example.com/coin/webhook" {
		t.Fatalf("unexpected url: %v", data["url"])
	}
	if data["enabled"] != true {
		t.Fatalf("expected enabled=true, got %v", data["enabled"])
	}
}

func waitTxnStatus(t *testing.T, r *gin.Engine, merchantNo, secret, txnNo, targetStatus string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	poll := 0
	for time.Now().Before(deadline) {
		poll++
		req := signedAPIRequest(t, http.MethodGet, "/api/v1/transactions/"+txnNo, merchantNo, secret, "nonce-wait-"+strconv.Itoa(poll), nil)
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		body := decodeJSONMap(t, resp.Body.Bytes())
		data, ok := body["data"].(map[string]any)
		if !ok {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		status, _ := data["status"].(string)
		if status == targetStatus {
			return
		}
		if targetStatus != service.TxnStatusFailed && status == service.TxnStatusFailed {
			t.Fatalf("txn %s async processing failed: %+v", txnNo, data)
		}
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("txn %s did not reach target status %s in time", txnNo, targetStatus)
}

func newTxnAPITestServer(t *testing.T) (*gin.Engine, *db.Repository, *pgxpool.Pool, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)
	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2001",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2002",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2003",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2004",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2005",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2006",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2007",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2008",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2009",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2010",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2011",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a2012",
	})

	merchantSvc := service.NewMerchantService(repo, ids)
	merchant, err := merchantSvc.CreateMerchant("", "demo")
	if err != nil {
		t.Fatalf("create merchant failed: %v", err)
	}
	merchantNo := merchant.MerchantNo
	customerSvc := service.NewCustomerService(repo, ids)
	debitCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, "u_1100")
	if err != nil {
		t.Fatalf("create customer failed: %v", err)
	}
	creditCustomer, err := customerSvc.CreateCustomer(merchant.MerchantNo, "u_1101")
	if err != nil {
		t.Fatalf("create customer failed: %v", err)
	}
	if err := repo.CreateAccount(service.Account{
		AccountNo:     testDebitAccountNo,
		MerchantNo:    merchant.MerchantNo,
		CustomerNo:    debitCustomer.CustomerNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
		Balance:       1_000_000,
	}); err != nil {
		t.Fatalf("create debit account failed: %v", err)
	}
	if err := repo.CreateAccount(service.Account{
		AccountNo:     testCreditAccountNo,
		MerchantNo:    merchant.MerchantNo,
		CustomerNo:    creditCustomer.CustomerNo,
		AccountType:   "CUSTOMER",
		AllowDebitOut: true,
		AllowCreditIn: true,
		AllowTransfer: true,
	}); err != nil {
		t.Fatalf("create credit account failed: %v", err)
	}

	transferSvc := service.NewTransferService(repo, ids)
	transferRoutingSvc := service.NewTransferRoutingService(repo)
	asyncTransferSvc := service.NewTransferAsyncProcessor(repo)
	accountResolver := service.NewAccountResolver(repo)
	querySvc := service.NewTxnQueryService(repo)
	base := time.Unix(1_710_000_000, 0).UTC()
	tick := 0
	businessHandler := api.NewBusinessHandler(transferSvc, repo, transferRoutingSvc, asyncTransferSvc, nil, accountResolver, repo, querySvc, repo, func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * time.Millisecond)
	})

	cipher, err := security.NewAESGCMCipher("tc1100_local_secret_cipher_key")
	if err != nil {
		t.Fatalf("init secret cipher failed: %v", err)
	}
	secretManager := db.NewMerchantSecretManager(pool, cipher)
	secret, _, err := secretManager.RotateSecret(context.Background(), merchantNo)
	if err != nil {
		t.Fatalf("rotate merchant secret failed: %v", err)
	}
	baseNow := base
	authMw := api.NewAuthMiddleware(api.AuthMiddlewareConfig{
		SecretProvider: secretManager,
		NowFn:          func() time.Time { return baseNow },
		TimeWindow:     5 * time.Minute,
	})

	r := api.NewRouter()
	api.RegisterProtectedRoutes(r, api.ProtectedRoutesOptions{
		AuthMiddleware: authMw,
		SecretRotator:  secretManager,
		Business:       businessHandler,
	})
	return r, repo, pool, merchantNo, secret
}

func setAccountBookEnabled(t *testing.T, pool *pgxpool.Pool, accountNo string, enabled bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE account
		SET book_enabled = $1
		WHERE account_no = $2
	`, enabled, strings.TrimSpace(accountNo)); err != nil {
		t.Fatalf("update account book_enabled failed: %v", err)
	}
}

func signedAPIRequest(t *testing.T, method, rawURL, merchantNo, secret, nonce string, payload any) *http.Request {
	t.Helper()

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url failed: %v", err)
	}

	var bodyBytes []byte
	if payload != nil {
		bodyBytes, err = json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload failed: %v", err)
		}
	} else {
		bodyBytes = []byte{}
	}

	ts := time.Unix(1_710_000_000, 0).UTC()
	tsText := strconv.FormatInt(ts.UnixMilli(), 10)
	bodyHash := sha256.Sum256(bodyBytes)
	signingString := method + "\n" + u.Path + "\n" + merchantNo + "\n" + tsText + "\n" + nonce + "\n" + hex.EncodeToString(bodyHash[:])

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingString))
	signature := hex.EncodeToString(mac.Sum(nil))

	var bodyReader *bytes.Reader
	if len(bodyBytes) == 0 {
		bodyReader = bytes.NewReader(nil)
	} else {
		bodyReader = bytes.NewReader(bodyBytes)
	}
	req := httptest.NewRequest(method, rawURL, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Merchant-No", merchantNo)
	req.Header.Set("X-Timestamp", tsText)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", signature)
	return req
}

func decodeJSONMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode json failed: %v raw=%s", err, string(raw))
	}
	return out
}
