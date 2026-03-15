package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xmz-ai/coin/tests/support/memoryrepo"
)

func TestTC1123APICreditBalanceAndChangeLogs(t *testing.T) {
	r, repo, merchantNo, secret := newTxnAPITestServer(t)

	merchant, ok := repo.GetMerchantByNo(merchantNo)
	if !ok {
		t.Fatalf("merchant not found")
	}
	budgetBefore, ok := repo.GetAccount(merchant.BudgetAccountNo)
	if !ok {
		t.Fatalf("budget account not found")
	}
	creditBefore, ok := repo.GetAccount(testCreditAccountNo)
	if !ok {
		t.Fatalf("credit account not found")
	}

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/credit", merchantNo, secret, "nonce-1123", map[string]any{
		"out_trade_no": "ord_1123",
		"user_id":      "u_1101",
		"amount":       120,
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	txnNo := decodeJSONMap(t, resp.Body.Bytes())["data"].(map[string]any)["txn_no"].(string)
	waitTxnStatus(t, r, merchantNo, secret, txnNo, "RECV_SUCCESS")

	budgetAfter, _ := repo.GetAccount(merchant.BudgetAccountNo)
	creditAfter, _ := repo.GetAccount(testCreditAccountNo)
	if budgetAfter.Balance != budgetBefore.Balance-120 {
		t.Fatalf("unexpected budget balance: before=%d after=%d", budgetBefore.Balance, budgetAfter.Balance)
	}
	if creditAfter.Balance != creditBefore.Balance+120 {
		t.Fatalf("unexpected credit balance: before=%d after=%d", creditBefore.Balance, creditAfter.Balance)
	}
	assertTxnAccountChanges(t, repo, txnNo, map[string]int64{
		merchant.BudgetAccountNo: -120,
		testCreditAccountNo:      120,
	})
}

func TestTC1124APIDebitBalanceAndChangeLogs(t *testing.T) {
	r, repo, merchantNo, secret := newTxnAPITestServer(t)

	merchant, ok := repo.GetMerchantByNo(merchantNo)
	if !ok {
		t.Fatalf("merchant not found")
	}
	debitBefore, ok := repo.GetAccount(testDebitAccountNo)
	if !ok {
		t.Fatalf("debit account not found")
	}
	recvBefore, ok := repo.GetAccount(merchant.ReceivableAccountNo)
	if !ok {
		t.Fatalf("receivable account not found")
	}

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/debit", merchantNo, secret, "nonce-1124", map[string]any{
		"out_trade_no":      "ord_1124",
		"debit_out_user_id": "u_1100",
		"amount":            30,
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	txnNo := decodeJSONMap(t, resp.Body.Bytes())["data"].(map[string]any)["txn_no"].(string)
	waitTxnStatus(t, r, merchantNo, secret, txnNo, "RECV_SUCCESS")

	debitAfter, _ := repo.GetAccount(testDebitAccountNo)
	recvAfter, _ := repo.GetAccount(merchant.ReceivableAccountNo)
	if debitAfter.Balance != debitBefore.Balance-30 {
		t.Fatalf("unexpected debit balance: before=%d after=%d", debitBefore.Balance, debitAfter.Balance)
	}
	if recvAfter.Balance != recvBefore.Balance+30 {
		t.Fatalf("unexpected receivable balance: before=%d after=%d", recvBefore.Balance, recvAfter.Balance)
	}
	assertTxnAccountChanges(t, repo, txnNo, map[string]int64{
		testDebitAccountNo:           -30,
		merchant.ReceivableAccountNo: 30,
	})
}

func TestTC1125APITransferBalanceAndChangeLogs(t *testing.T) {
	r, repo, merchantNo, secret := newTxnAPITestServer(t)

	fromBefore, ok := repo.GetAccount(testDebitAccountNo)
	if !ok {
		t.Fatalf("from account not found")
	}
	toBefore, ok := repo.GetAccount(testCreditAccountNo)
	if !ok {
		t.Fatalf("to account not found")
	}

	req := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/transfer", merchantNo, secret, "nonce-1125", map[string]any{
		"out_trade_no":    "ord_1125",
		"transfer_scene":  "P2P",
		"from_account_no": testDebitAccountNo,
		"to_account_no":   testCreditAccountNo,
		"amount":          40,
	})
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", resp.Code, resp.Body.String())
	}
	txnNo := decodeJSONMap(t, resp.Body.Bytes())["data"].(map[string]any)["txn_no"].(string)
	waitTxnStatus(t, r, merchantNo, secret, txnNo, "RECV_SUCCESS")

	fromAfter, _ := repo.GetAccount(testDebitAccountNo)
	toAfter, _ := repo.GetAccount(testCreditAccountNo)
	if fromAfter.Balance != fromBefore.Balance-40 {
		t.Fatalf("unexpected from balance: before=%d after=%d", fromBefore.Balance, fromAfter.Balance)
	}
	if toAfter.Balance != toBefore.Balance+40 {
		t.Fatalf("unexpected to balance: before=%d after=%d", toBefore.Balance, toAfter.Balance)
	}
	assertTxnAccountChanges(t, repo, txnNo, map[string]int64{
		testDebitAccountNo:  -40,
		testCreditAccountNo: 40,
	})
}

func TestTC1126APIRefundBalanceAndChangeLogs(t *testing.T) {
	r, repo, merchantNo, secret := newTxnAPITestServer(t)

	debitBefore, _ := repo.GetAccount(testDebitAccountNo)
	creditBefore, _ := repo.GetAccount(testCreditAccountNo)

	originReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/transfer", merchantNo, secret, "nonce-1126-origin", map[string]any{
		"out_trade_no":    "ord_1126_origin",
		"transfer_scene":  "P2P",
		"from_account_no": testDebitAccountNo,
		"to_account_no":   testCreditAccountNo,
		"amount":          60,
	})
	originResp := httptest.NewRecorder()
	r.ServeHTTP(originResp, originReq)
	if originResp.Code != http.StatusCreated {
		t.Fatalf("origin expected 201, got %d body=%s", originResp.Code, originResp.Body.String())
	}
	originTxnNo := decodeJSONMap(t, originResp.Body.Bytes())["data"].(map[string]any)["txn_no"].(string)
	waitTxnStatus(t, r, merchantNo, secret, originTxnNo, "RECV_SUCCESS")

	refundReq := signedAPIRequest(t, http.MethodPost, "/api/v1/transactions/refund", merchantNo, secret, "nonce-1126-refund", map[string]any{
		"out_trade_no":     "ord_1126_refund",
		"refund_of_txn_no": originTxnNo,
		"amount":           20,
	})
	refundResp := httptest.NewRecorder()
	r.ServeHTTP(refundResp, refundReq)
	if refundResp.Code != http.StatusOK {
		t.Fatalf("refund expected 200, got %d body=%s", refundResp.Code, refundResp.Body.String())
	}
	refundTxnNo := decodeJSONMap(t, refundResp.Body.Bytes())["data"].(map[string]any)["txn_no"].(string)

	debitAfter, _ := repo.GetAccount(testDebitAccountNo)
	creditAfter, _ := repo.GetAccount(testCreditAccountNo)
	if debitAfter.Balance != debitBefore.Balance-60+20 {
		t.Fatalf("unexpected debit balance: before=%d after=%d", debitBefore.Balance, debitAfter.Balance)
	}
	if creditAfter.Balance != creditBefore.Balance+60-20 {
		t.Fatalf("unexpected credit balance: before=%d after=%d", creditBefore.Balance, creditAfter.Balance)
	}
	assertTxnAccountChanges(t, repo, refundTxnNo, map[string]int64{
		testCreditAccountNo: -20,
		testDebitAccountNo:  20,
	})
}

func assertTxnAccountChanges(t *testing.T, repo *memoryrepo.Repo, txnNo string, expected map[string]int64) {
	t.Helper()

	changes := repo.ListAccountChangesByTxnNo(txnNo)
	if len(changes) != len(expected) {
		t.Fatalf("unexpected change count for txn=%s: got=%d expected=%d", txnNo, len(changes), len(expected))
	}

	actual := make(map[string]int64, len(changes))
	for _, item := range changes {
		actual[item.AccountNo] += item.Delta
	}
	for accountNo, delta := range expected {
		if actual[accountNo] != delta {
			t.Fatalf("unexpected delta for txn=%s account=%s: got=%d expected=%d", txnNo, accountNo, actual[accountNo], delta)
		}
	}
}
