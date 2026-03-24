package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestTC1201AdminSetupInitializeWithoutWebhookDefaultsDisabled(t *testing.T) {
	r, repo, _ := newAdminSetupTestServer(t)

	payload := map[string]any{
		"admin_username": "admin_1201",
		"admin_password": "Passw0rd!1201",
		"merchant_name":  "Default Merchant",
	}
	req := mustJSONRequest(t, http.MethodPost, "/admin/api/v1/setup/initialize", payload)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", resp.Code, resp.Body.String())
	}

	body := decodeJSONMap(t, resp.Body.Bytes())
	data := body["data"].(map[string]any)
	merchantNo, _ := data["merchant_no"].(string)
	if merchantNo == "" {
		t.Fatalf("expected merchant_no in response")
	}

	cfg, found, err := repo.GetWebhookConfig(merchantNo)
	if err != nil {
		t.Fatalf("get webhook config failed: %v", err)
	}
	if !found {
		t.Fatalf("expected webhook config row exists")
	}
	if cfg.Enabled {
		t.Fatalf("expected webhook disabled by default, got enabled=true")
	}
	if cfg.URL != "" {
		t.Fatalf("expected empty webhook url, got %q", cfg.URL)
	}
}

func TestTC1202AdminSetupInitializeWithWebhookPersistsConfig(t *testing.T) {
	r, repo, _ := newAdminSetupTestServer(t)

	payload := map[string]any{
		"admin_username":       "admin_1202",
		"admin_password":       "Passw0rd!1202",
		"merchant_name":        "Default Merchant",
		"merchant_webhook_url": "https://merchant.example.com/webhook",
	}
	req := mustJSONRequest(t, http.MethodPost, "/admin/api/v1/setup/initialize", payload)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", resp.Code, resp.Body.String())
	}

	body := decodeJSONMap(t, resp.Body.Bytes())
	data := body["data"].(map[string]any)
	merchantNo, _ := data["merchant_no"].(string)
	if merchantNo == "" {
		t.Fatalf("expected merchant_no in response")
	}

	cfg, found, err := repo.GetWebhookConfig(merchantNo)
	if err != nil {
		t.Fatalf("get webhook config failed: %v", err)
	}
	if !found {
		t.Fatalf("expected webhook config row exists")
	}
	if !cfg.Enabled {
		t.Fatalf("expected webhook enabled, got enabled=false")
	}
	if cfg.URL != "https://merchant.example.com/webhook" {
		t.Fatalf("unexpected webhook url: %q", cfg.URL)
	}
}

func newAdminSetupTestServer(t *testing.T) (*gin.Engine, *db.Repository, *pgxpool.Pool) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	pool := setupPostgresPool(t)
	repo := db.NewRepository(pool)

	ids := idpkg.NewFixedUUIDProvider([]string{
		"01956f4e-9d22-73bc-8e11-3f5e9c7a3001",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a3002",
		"01956f4e-9d22-73bc-8e11-3f5e9c7a3003",
	})
	merchantSvc := service.NewMerchantService(repo, ids)

	cipher, err := security.NewAESGCMCipher("tc1200_admin_setup_secret_cipher")
	if err != nil {
		t.Fatalf("init secret cipher failed: %v", err)
	}
	secretManager := db.NewMerchantSecretManager(pool, cipher)

	r := api.NewRouter()
	if err := api.RegisterAdminRoutes(r, api.AdminRoutesOptions{
		Enabled:         true,
		Repo:            repo,
		MerchantService: merchantSvc,
		SecretRotator:   secretManager,
		JWTSecret:       "tc1200_admin_setup_jwt_secret",
		AccessTokenTTL:  30 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour,
	}); err != nil {
		t.Fatalf("register admin routes failed: %v", err)
	}

	return r, repo, pool
}

func mustJSONRequest(t *testing.T, method, path string, payload any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload failed: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func mustJSONRequestWithBearer(t *testing.T, method, path, token string, payload any) *http.Request {
	t.Helper()
	req := mustJSONRequest(t, method, path, payload)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}
