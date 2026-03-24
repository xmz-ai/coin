package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTC1203AdminPatchAccountCapabilityOnlyChangesMutableFlags(t *testing.T) {
	r, repo, _ := newAdminSetupTestServer(t)

	initReq := mustJSONRequest(t, http.MethodPost, "/admin/api/v1/setup/initialize", map[string]any{
		"admin_username": "admin_1203",
		"admin_password": "Passw0rd!1203",
		"merchant_name":  "Mutable Capability Merchant",
	})
	initResp := httptest.NewRecorder()
	r.ServeHTTP(initResp, initReq)
	if initResp.Code != http.StatusCreated {
		t.Fatalf("initialize expected 201, got %d body=%s", initResp.Code, initResp.Body.String())
	}
	initBody := decodeJSONMap(t, initResp.Body.Bytes())
	merchantNo, _ := initBody["data"].(map[string]any)["merchant_no"].(string)
	if merchantNo == "" {
		t.Fatalf("expected merchant_no in initialize response")
	}

	loginReq := mustJSONRequest(t, http.MethodPost, "/admin/api/v1/auth/login", map[string]any{
		"username": "admin_1203",
		"password": "Passw0rd!1203",
	})
	loginResp := httptest.NewRecorder()
	r.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d body=%s", loginResp.Code, loginResp.Body.String())
	}
	loginBody := decodeJSONMap(t, loginResp.Body.Bytes())
	accessToken, _ := loginBody["data"].(map[string]any)["access_token"].(string)
	if accessToken == "" {
		t.Fatalf("expected access_token in login response")
	}

	createReq := mustJSONRequestWithBearer(t, http.MethodPost, "/admin/api/v1/accounts", accessToken, map[string]any{
		"merchant_no":  merchantNo,
		"owner_type":   "MERCHANT",
		"account_type": "CUSTOM",
		"capability": map[string]any{
			"allow_overdraft":     true,
			"max_overdraft_limit": int64(88),
			"allow_transfer":      true,
			"allow_credit_in":     true,
			"allow_debit_out":     true,
			"book_enabled":        true,
		},
	})
	createResp := httptest.NewRecorder()
	r.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create account expected 201, got %d body=%s", createResp.Code, createResp.Body.String())
	}
	createBody := decodeJSONMap(t, createResp.Body.Bytes())
	accountNo, _ := createBody["data"].(map[string]any)["account_no"].(string)
	if accountNo == "" {
		t.Fatalf("expected account_no in create response")
	}

	before, ok := repo.GetAccount(accountNo)
	if !ok {
		t.Fatalf("account not found after create")
	}
	if !before.AllowOverdraft || before.MaxOverdraftLimit != 88 || !before.BookEnabled {
		t.Fatalf("unexpected immutable fields before patch: %+v", before)
	}

	patchReq := mustJSONRequestWithBearer(t, http.MethodPatch, "/admin/api/v1/accounts/"+accountNo+"/capability", accessToken, map[string]any{
		"allow_transfer":      false,
		"allow_credit_in":     false,
		"allow_debit_out":     false,
		"allow_overdraft":     false,
		"max_overdraft_limit": int64(0),
		"book_enabled":        false,
	})
	patchResp := httptest.NewRecorder()
	r.ServeHTTP(patchResp, patchReq)
	if patchResp.Code != http.StatusOK {
		t.Fatalf("patch capability expected 200, got %d body=%s", patchResp.Code, patchResp.Body.String())
	}

	after, ok := repo.GetAccount(accountNo)
	if !ok {
		t.Fatalf("account not found after patch")
	}
	if after.AllowTransfer || after.AllowCreditIn || after.AllowDebitOut {
		t.Fatalf("expected mutable capability flags patched to false, got %+v", after)
	}
	if !after.AllowOverdraft {
		t.Fatalf("allow_overdraft should remain immutable, got %+v", after)
	}
	if after.MaxOverdraftLimit != 88 {
		t.Fatalf("max_overdraft_limit should remain immutable, got %+v", after)
	}
	if !after.BookEnabled {
		t.Fatalf("book_enabled should remain immutable, got %+v", after)
	}
	if after.MerchantNo != before.MerchantNo || after.AccountType != before.AccountType || after.CustomerNo != before.CustomerNo {
		t.Fatalf("account identity fields changed unexpectedly: before=%+v after=%+v", before, after)
	}
}
