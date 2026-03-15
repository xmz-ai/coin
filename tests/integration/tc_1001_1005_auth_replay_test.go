package integration

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xmz-ai/coin/internal/api"
)

func TestTC1001SignatureValidPasses(t *testing.T) {
	now := time.Unix(1_710_000_000, 0).UTC()
	r := newAuthRouter(now, api.StaticMerchantSecretProvider{"mch_001": "sec_001"})
	body := map[string]any{"amount": 100}
	req := newSignedRequest(t, "mch_001", "sec_001", now, "nonce-1001", "/api/v1/auth-check", body)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestTC1002SignatureInvalidRejected(t *testing.T) {
	now := time.Unix(1_710_000_000, 0).UTC()
	r := newAuthRouter(now, api.StaticMerchantSecretProvider{"mch_001": "sec_001"})
	body := map[string]any{"amount": 100}
	req := newSignedRequest(t, "mch_001", "wrong_secret", now, "nonce-1002", "/api/v1/auth-check", body)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "INVALID_SIGNATURE") {
		t.Fatalf("expected INVALID_SIGNATURE, got %s", w.Body.String())
	}
}

func TestTC1003TimestampOutOfWindowRejected(t *testing.T) {
	now := time.Unix(1_710_000_000, 0).UTC()
	r := newAuthRouter(now, api.StaticMerchantSecretProvider{"mch_001": "sec_001"})
	body := map[string]any{"amount": 100}
	req := newSignedRequest(t, "mch_001", "sec_001", now.Add(-10*time.Minute), "nonce-1003", "/api/v1/auth-check", body)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "TIMESTAMP_OUT_OF_WINDOW") {
		t.Fatalf("expected TIMESTAMP_OUT_OF_WINDOW, got %s", w.Body.String())
	}
}

func TestTC1004NonceReplayAccepted(t *testing.T) {
	now := time.Unix(1_710_000_000, 0).UTC()
	r := newAuthRouter(now, api.StaticMerchantSecretProvider{"mch_001": "sec_001"})
	body := map[string]any{"amount": 100}

	req1 := newSignedRequest(t, "mch_001", "sec_001", now, "nonce-dup", "/api/v1/auth-check", body)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request expected 200, got %d body=%s", w1.Code, w1.Body.String())
	}

	req2 := newSignedRequest(t, "mch_001", "sec_001", now, "nonce-dup", "/api/v1/auth-check", body)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w2.Code, w2.Body.String())
	}
}

func TestTC1005MissingAuthHeadersRejected(t *testing.T) {
	r := newAuthRouter(time.Unix(1_710_000_000, 0).UTC(), api.StaticMerchantSecretProvider{"mch_001": "sec_001"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth-check", bytes.NewBufferString(`{"amount":100}`))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "AUTH_HEADER_MISSING") {
		t.Fatalf("expected AUTH_HEADER_MISSING, got %s", w.Body.String())
	}
}

func newAuthRouter(now time.Time, provider api.MerchantSecretProvider) *gin.Engine {
	gin.SetMode(gin.TestMode)
	mw := api.NewAuthMiddleware(api.AuthMiddlewareConfig{
		SecretProvider: provider,
		NowFn:          func() time.Time { return now },
		TimeWindow:     5 * time.Minute,
	})

	r := gin.New()
	r.POST("/api/v1/auth-check", mw, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": "SUCCESS", "message": "ok"})
	})
	return r
}

func newSignedRequest(t *testing.T, merchantNo, secret string, ts time.Time, nonce, path string, body map[string]any) *http.Request {
	t.Helper()

	rawBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	tsText := strconv.FormatInt(ts.UnixMilli(), 10)
	bodyHash := sha256.Sum256(rawBody)
	signingString := strings.Join([]string{
		http.MethodPost,
		path,
		merchantNo,
		tsText,
		nonce,
		hex.EncodeToString(bodyHash[:]),
	}, "\n")

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingString))
	signature := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBuffer(rawBody))
	req.Header.Set("X-Merchant-No", merchantNo)
	req.Header.Set("X-Timestamp", tsText)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", signature)
	req.Header.Set("Content-Type", "application/json")
	return req
}
