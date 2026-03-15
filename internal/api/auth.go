package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type MerchantSecretProvider interface {
	GetActiveSecret(ctx context.Context, merchantNo string) (secret string, ok bool, err error)
}

type StaticMerchantSecretProvider map[string]string

func (p StaticMerchantSecretProvider) GetActiveSecret(_ context.Context, merchantNo string) (string, bool, error) {
	v, ok := p[merchantNo]
	return v, ok, nil
}

type AuthHandler struct {
	middleware gin.HandlerFunc
}

type AuthMiddlewareConfig struct {
	SecretProvider MerchantSecretProvider
	NowFn          func() time.Time
	TimeWindow     time.Duration
}

func NewAuthHandler(provider MerchantSecretProvider, now time.Time) *AuthHandler {
	nowFn := func() time.Time { return now.UTC() }
	return &AuthHandler{
		middleware: NewAuthMiddleware(AuthMiddlewareConfig{
			SecretProvider: provider,
			NowFn:          nowFn,
			TimeWindow:     5 * time.Minute,
		}),
	}
}

func (h *AuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	engine := gin.New()
	engine.Any("/*path", h.middleware, func(c *gin.Context) {
		writeCode(c.Writer, http.StatusOK, "SUCCESS", "ok")
	})
	engine.ServeHTTP(w, r)
}

func NewAuthMiddleware(cfg AuthMiddlewareConfig) gin.HandlerFunc {
	window := cfg.TimeWindow
	if window <= 0 {
		window = 5 * time.Minute
	}

	nowFn := cfg.NowFn
	if nowFn == nil {
		nowFn = time.Now
	}

	return func(c *gin.Context) {
		merchantNo := strings.TrimSpace(c.GetHeader("X-Merchant-No"))
		timestamp := strings.TrimSpace(c.GetHeader("X-Timestamp"))
		nonce := strings.TrimSpace(c.GetHeader("X-Nonce"))
		signature := strings.TrimSpace(c.GetHeader("X-Signature"))

		if merchantNo == "" || timestamp == "" || nonce == "" || signature == "" {
			abortWithCode(c, http.StatusBadRequest, "AUTH_HEADER_MISSING", "missing auth header")
			return
		}

		if cfg.SecretProvider == nil {
			abortWithCode(c, http.StatusInternalServerError, "INTERNAL_ERROR", "auth provider not configured")
			return
		}

		secret, ok, err := cfg.SecretProvider.GetActiveSecret(c.Request.Context(), merchantNo)
		if err != nil {
			abortWithCode(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load merchant credential failed")
			return
		}
		if !ok {
			abortWithCode(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "signature verify failed")
			return
		}

		ts, ok := parseTimestamp(timestamp)
		if !ok {
			abortWithCode(c, http.StatusUnauthorized, "TIMESTAMP_OUT_OF_WINDOW", "timestamp parse failed")
			return
		}
		if absDuration(nowFn().UTC().Sub(ts)) > window {
			abortWithCode(c, http.StatusUnauthorized, "TIMESTAMP_OUT_OF_WINDOW", "timestamp out of window")
			return
		}

		rawBody, err := readBody(c.Request)
		if err != nil {
			abortWithCode(c, http.StatusBadRequest, "INVALID_PARAM", "read body failed")
			return
		}

		bodyHash := sha256.Sum256(rawBody)
		signingString := strings.Join([]string{
			c.Request.Method,
			c.Request.URL.Path,
			merchantNo,
			timestamp,
			nonce,
			hex.EncodeToString(bodyHash[:]),
		}, "\n")

		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(signingString))
		expected := hex.EncodeToString(mac.Sum(nil))
		got := strings.ToLower(signature)
		if !hmac.Equal([]byte(expected), []byte(got)) {
			abortWithCode(c, http.StatusUnauthorized, "INVALID_SIGNATURE", "signature verify failed")
			return
		}

		c.Set("merchant_no", merchantNo)
		c.Next()
	}
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return []byte{}, nil
	}
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(rawBody))
	return rawBody, nil
}

func MerchantNoFromContext(c *gin.Context) (string, bool) {
	v, ok := c.Get("merchant_no")
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", false
	}
	return s, true
}

func abortWithCode(c *gin.Context, httpCode int, code, message string) {
	c.AbortWithStatusJSON(httpCode, gin.H{
		"code":       code,
		"message":    message,
		"request_id": getRequestID(c),
	})
}

func parseTimestamp(v string) (time.Time, bool) {
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil && ms >= 0 {
		return time.UnixMilli(ms).UTC(), true
	}

	ts, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	return ts.UTC(), true
}

func absDuration(v time.Duration) time.Duration {
	if v == math.MinInt64 {
		return time.Duration(math.MaxInt64)
	}
	if v < 0 {
		return -v
	}
	return v
}

func writeCode(w http.ResponseWriter, httpCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":       code,
		"message":    message,
		"request_id": newRequestID(),
	})
}
