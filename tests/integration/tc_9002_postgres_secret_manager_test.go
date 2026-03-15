package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xmz-ai/coin/internal/api"
	"github.com/xmz-ai/coin/internal/db"
	"github.com/xmz-ai/coin/internal/platform/security"
)

func TestTC9002PostgresMerchantSecretRotation(t *testing.T) {
	pool := setupPostgresPool(t)

	merchantNo := "1000123456789012"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO merchant (merchant_id, merchant_no, name, budget_account_no, receivable_account_no)
		VALUES ($1, $2, $3, $4, $5)
	`, "01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001", merchantNo, "demo", "6217709012010000010", "6217709012020000010"); err != nil {
		t.Fatalf("seed merchant failed: %v", err)
	}

	cipher, err := security.NewAESGCMCipher("tc9002_local_secret_cipher_key")
	if err != nil {
		t.Fatalf("init cipher failed: %v", err)
	}
	manager := db.NewMerchantSecretManager(pool, cipher)

	secretV1, versionV1, err := manager.RotateSecret(context.Background(), merchantNo)
	if err != nil {
		t.Fatalf("rotate v1 failed: %v", err)
	}
	if versionV1 != 1 {
		t.Fatalf("expected version 1, got %d", versionV1)
	}
	gotV1, ok, err := manager.GetActiveSecret(context.Background(), merchantNo)
	if err != nil {
		t.Fatalf("get active secret v1 failed: %v", err)
	}
	if !ok || gotV1 != secretV1 {
		t.Fatalf("unexpected active secret v1: ok=%v got=%q", ok, gotV1)
	}

	secretV2, versionV2, err := manager.RotateSecret(context.Background(), merchantNo)
	if err != nil {
		t.Fatalf("rotate v2 failed: %v", err)
	}
	if versionV2 != 2 {
		t.Fatalf("expected version 2, got %d", versionV2)
	}
	if secretV2 == secretV1 {
		t.Fatalf("expected rotated secret to change")
	}

	gotV2, ok, err := manager.GetActiveSecret(context.Background(), merchantNo)
	if err != nil {
		t.Fatalf("get active secret v2 failed: %v", err)
	}
	if !ok || gotV2 != secretV2 {
		t.Fatalf("unexpected active secret v2: ok=%v got=%q", ok, gotV2)
	}

	var activeCount, totalCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			COUNT(*) FILTER (WHERE active=true),
			COUNT(*)
		FROM merchant_api_credential
		WHERE merchant_no=$1
	`, merchantNo).Scan(&activeCount, &totalCount); err != nil {
		t.Fatalf("count credential rows failed: %v", err)
	}
	if activeCount != 1 || totalCount != 2 {
		t.Fatalf("unexpected credential rows, active=%d total=%d", activeCount, totalCount)
	}

	var latestCiphertext string
	if err := pool.QueryRow(context.Background(), `
		SELECT secret_ciphertext
		FROM merchant_api_credential
		WHERE merchant_no=$1 AND secret_version=$2
	`, merchantNo, versionV2).Scan(&latestCiphertext); err != nil {
		t.Fatalf("load latest ciphertext failed: %v", err)
	}
	if strings.Contains(latestCiphertext, secretV2) {
		t.Fatalf("secret stored in plaintext")
	}

	now := time.Unix(1_710_000_000, 0).UTC()
	auth := api.NewAuthMiddleware(api.AuthMiddlewareConfig{
		SecretProvider: manager,
		NowFn:          func() time.Time { return now },
		TimeWindow:     5 * time.Minute,
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/v1/auth-check", auth, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": "SUCCESS"})
	})

	oldReq := newSignedRequest(t, merchantNo, secretV1, now, "tc9002-old", "/api/v1/auth-check", map[string]any{"amount": 1})
	oldResp := httptest.NewRecorder()
	r.ServeHTTP(oldResp, oldReq)
	if oldResp.Code != http.StatusUnauthorized {
		t.Fatalf("old secret expected 401, got %d body=%s", oldResp.Code, oldResp.Body.String())
	}
	if !strings.Contains(oldResp.Body.String(), "INVALID_SIGNATURE") {
		t.Fatalf("old secret expected INVALID_SIGNATURE, got %s", oldResp.Body.String())
	}

	newReq := newSignedRequest(t, merchantNo, secretV2, now, "tc9002-new", "/api/v1/auth-check", map[string]any{"amount": 1})
	newResp := httptest.NewRecorder()
	r.ServeHTTP(newResp, newReq)
	if newResp.Code != http.StatusOK {
		t.Fatalf("new secret expected 200, got %d body=%s", newResp.Code, newResp.Body.String())
	}
}
