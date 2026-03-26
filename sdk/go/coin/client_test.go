package coin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCreditSignsHeadersAndBody(t *testing.T) {
	const (
		merchantNo = "1000123456789012"
		secret     = "msk_test_secret"
		nonce      = "nonce-fixed"
	)
	fixedNow := time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s", r.Method)
		}
		if r.URL.Path != "/api/v1/transactions/credit" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.Header.Get("X-Merchant-No"); got != merchantNo {
			t.Fatalf("merchant header=%s", got)
		}
		if got := r.Header.Get("X-Nonce"); got != nonce {
			t.Fatalf("nonce=%s", got)
		}

		var req CreditRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if req.OutTradeNo != "ord_001" || req.UserID != "u_1" || req.Amount != 100 {
			t.Fatalf("unexpected req: %+v", req)
		}

		expectedTS := "1774173600000"
		if got := r.Header.Get("X-Timestamp"); got != expectedTS {
			t.Fatalf("timestamp=%s", got)
		}
		expectedSig := signature(http.MethodPost, "/api/v1/transactions/credit", merchantNo, expectedTS, nonce, []byte(`{"out_trade_no":"ord_001","user_id":"u_1","amount":100}`), secret)
		if got := r.Header.Get("X-Signature"); got != expectedSig {
			t.Fatalf("signature mismatch\nwant=%s\ngot=%s", expectedSig, got)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"code":"SUCCESS","message":"ok","request_id":"req_1","data":{"txn_no":"txn_1","status":"INIT"}}`))
	}))
	defer ts.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:        ts.URL,
		MerchantNo:     merchantNo,
		MerchantSecret: secret,
		Now:            func() time.Time { return fixedNow },
		NonceGenerator: func() string { return nonce },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Transactions.Credit(context.Background(), CreditRequest{
		OutTradeNo: "ord_001",
		UserID:     "u_1",
		Amount:     100,
	})
	if err != nil {
		t.Fatalf("credit: %v", err)
	}
	if resp.TxnNo != "txn_1" || resp.Status != "INIT" {
		t.Fatalf("unexpected resp: %+v", resp)
	}
}

func TestReturnsAPIErrorOnBusinessFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"DUPLICATE_OUT_TRADE_NO","message":"duplicate","request_id":"req_dup"}`))
	}))
	defer ts.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:        ts.URL,
		MerchantNo:     "1000123456789012",
		MerchantSecret: "s",
		NonceGenerator: func() string { return "n" },
		Now:            func() time.Time { return time.UnixMilli(0).UTC() },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Transactions.Credit(context.Background(), CreditRequest{
		OutTradeNo: "ord_dup",
		UserID:     "u_1",
		Amount:     1,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.Code != "DUPLICATE_OUT_TRADE_NO" || apiErr.HTTPStatus != http.StatusConflict || apiErr.RequestID != "req_dup" {
		t.Fatalf("unexpected api err: %+v", apiErr)
	}
}

func TestListTransactionsBuildsQuery(t *testing.T) {
	start := time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/transactions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("start_time") != "2026-03-21T12:00:00Z" {
			t.Fatalf("start_time=%s", q.Get("start_time"))
		}
		if q.Get("end_time") != "2026-03-22T12:00:00Z" {
			t.Fatalf("end_time=%s", q.Get("end_time"))
		}
		if q.Get("status") != "RECV_SUCCESS" || q.Get("transfer_scene") != "ISSUE" {
			t.Fatalf("status/scene=%s/%s", q.Get("status"), q.Get("transfer_scene"))
		}
		if q.Get("out_user_id") != "u_100" || q.Get("page_size") != "50" || q.Get("page_token") != "tok_1" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":"SUCCESS","message":"ok","request_id":"req_list","data":{"items":[{"txn_no":"t1","out_trade_no":"o1","transfer_scene":"ISSUE","status":"RECV_SUCCESS","amount":10,"refundable_amount":10,"debit_account_no":"a1","credit_account_no":"a2","error_code":"","error_msg":"","created_at":"2026-03-22T12:00:00Z"}],"next_page_token":"tok_2"}}`))
	}))
	defer ts.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:        ts.URL,
		MerchantNo:     "1000123456789012",
		MerchantSecret: "s",
		NonceGenerator: func() string { return "n" },
		Now:            func() time.Time { return time.UnixMilli(0).UTC() },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Transactions.List(context.Background(), ListTransactionsRequest{
		StartTime:     &start,
		EndTime:       &end,
		Status:        "recv_success",
		TransferScene: "issue",
		OutUserID:     "u_100",
		PageSize:      50,
		PageToken:     "tok_1",
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if resp.NextPageToken != "tok_2" || len(resp.Items) != 1 || resp.Items[0].TxnNo != "t1" {
		t.Fatalf("unexpected list resp: %+v", resp)
	}
}

func TestValidationError(t *testing.T) {
	client, err := NewClient(ClientOptions{
		BaseURL:        "https://example.com",
		MerchantNo:     "1000123456789012",
		MerchantSecret: "s",
		NonceGenerator: func() string { return "n" },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.Transactions.Credit(context.Background(), CreditRequest{OutTradeNo: "", Amount: 1, UserID: "u"})
	if err == nil || !strings.Contains(err.Error(), "out_trade_no") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestJoinURLPathKeepsLeadingSlash(t *testing.T) {
	cases := []struct {
		base string
		p    string
		want string
	}{
		{base: "", p: "/api/v1/transactions", want: "/api/v1/transactions"},
		{base: "/v2", p: "/api/v1/transactions", want: "/v2/api/v1/transactions"},
		{base: "v2", p: "/api/v1/transactions", want: "/v2/api/v1/transactions"},
	}
	for _, tc := range cases {
		got := joinURLPath(tc.base, tc.p)
		if got != tc.want {
			t.Fatalf("joinURLPath(%q, %q)=%q want=%q", tc.base, tc.p, got, tc.want)
		}
	}
}

func TestCreditTrimsOutTradeNoBeforeSend(t *testing.T) {
	const (
		merchantNo = "1000123456789012"
		secret     = "msk_test_secret"
		nonce      = "nonce-fixed"
	)
	fixedNow := time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC)
	expectedTS := "1774173600000"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req CreditRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if req.OutTradeNo != "ord_001" {
			t.Fatalf("out_trade_no not trimmed: %q", req.OutTradeNo)
		}
		expectedSig := signature(http.MethodPost, "/api/v1/transactions/credit", merchantNo, expectedTS, nonce, []byte(`{"out_trade_no":"ord_001","user_id":"u_1","amount":100}`), secret)
		if got := r.Header.Get("X-Signature"); got != expectedSig {
			t.Fatalf("signature mismatch\nwant=%s\ngot=%s", expectedSig, got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"code":"SUCCESS","message":"ok","request_id":"req_1","data":{"txn_no":"txn_1","status":"INIT"}}`))
	}))
	defer ts.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:        ts.URL,
		MerchantNo:     merchantNo,
		MerchantSecret: secret,
		Now:            func() time.Time { return fixedNow },
		NonceGenerator: func() string { return nonce },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Transactions.Credit(context.Background(), CreditRequest{
		OutTradeNo: "  ord_001  ",
		UserID:     "u_1",
		Amount:     100,
	})
	if err != nil {
		t.Fatalf("credit: %v", err)
	}
}

func TestResponseBodyTooLarge(t *testing.T) {
	huge := strings.Repeat("x", maxResponseBodyBytes+1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":"SUCCESS","message":"ok","request_id":"req_1","data":"` + huge + `"}`))
	}))
	defer ts.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:        ts.URL,
		MerchantNo:     "1000123456789012",
		MerchantSecret: "s",
		NonceGenerator: func() string { return "n" },
		Now:            func() time.Time { return time.UnixMilli(0).UTC() },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Merchant.Me(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.Code != "RESPONSE_TOO_LARGE" {
		t.Fatalf("unexpected code: %s", apiErr.Code)
	}
}

func TestMerchantMeParsesWriteoffAccountNo(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/merchants/me" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":"SUCCESS","message":"ok","request_id":"req_merchant","data":{"merchant_no":"1000123456789012","name":"demo","status":"ACTIVE","budget_account_no":"6217701000000000001","receivable_account_no":"6217701000000000002","writeoff_account_no":"6217701000000000003","secret_version":1,"auto_create_account_on_customer_create":true,"auto_create_customer_on_credit":true}}`))
	}))
	defer ts.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:        ts.URL,
		MerchantNo:     "1000123456789012",
		MerchantSecret: "s",
		NonceGenerator: func() string { return "n" },
		Now:            func() time.Time { return time.UnixMilli(0).UTC() },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Merchant.Me(context.Background())
	if err != nil {
		t.Fatalf("merchant me: %v", err)
	}
	if resp.WriteoffAccountNo != "6217701000000000003" {
		t.Fatalf("unexpected writeoff_account_no: %s", resp.WriteoffAccountNo)
	}
}

func TestCustomerBalanceParsesAvailableBalance(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/customers/balance" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if got := r.URL.Query().Get("out_user_id"); got != "u_1" {
			t.Fatalf("out_user_id=%s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":"SUCCESS","message":"ok","request_id":"req_balance","data":{"out_user_id":"u_1","account_no":"6217701000000000010","balance":150,"available_balance":100,"book_enabled":true}}`))
	}))
	defer ts.Close()

	client, err := NewClient(ClientOptions{
		BaseURL:        ts.URL,
		MerchantNo:     "1000123456789012",
		MerchantSecret: "s",
		NonceGenerator: func() string { return "n" },
		Now:            func() time.Time { return time.UnixMilli(0).UTC() },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Customers.GetBalance(context.Background(), "u_1")
	if err != nil {
		t.Fatalf("customer balance: %v", err)
	}
	if resp.Balance != 150 || resp.AvailableBalance != 100 {
		t.Fatalf("unexpected balances: %+v", resp)
	}
}
