package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xmz-ai/coin/internal/db"
	"golang.org/x/crypto/bcrypt"
)

const (
	adminUsernameContextKey = "admin_username"

	adminTokenTypeAccess  = "access"
	adminTokenTypeRefresh = "refresh"
)

type adminTokenClaims struct {
	Subject   string `json:"sub"`
	TokenType string `json:"typ"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

type adminTokenManager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	nowFn      func() time.Time
}

func newAdminTokenManager(secret string, accessTTL, refreshTTL time.Duration, nowFn func() time.Time) (*adminTokenManager, error) {
	fixedSecret := strings.TrimSpace(secret)
	if fixedSecret == "" {
		return nil, errors.New("admin jwt secret is required")
	}
	if accessTTL <= 0 {
		accessTTL = 30 * time.Minute
	}
	if refreshTTL <= 0 {
		refreshTTL = 7 * 24 * time.Hour
	}
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	return &adminTokenManager{
		secret:     []byte(fixedSecret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		nowFn:      nowFn,
	}, nil
}

func (m *adminTokenManager) IssueTokenPair(username string) (accessToken string, refreshToken string, accessExpireAt time.Time, refreshExpireAt time.Time, err error) {
	if m == nil {
		return "", "", time.Time{}, time.Time{}, errors.New("admin token manager is nil")
	}
	fixedUsername := strings.TrimSpace(username)
	if fixedUsername == "" {
		return "", "", time.Time{}, time.Time{}, errors.New("username is required")
	}

	now := m.nowFn().UTC()
	accessExpireAt = now.Add(m.accessTTL)
	refreshExpireAt = now.Add(m.refreshTTL)

	accessToken, err = m.sign(adminTokenClaims{
		Subject:   fixedUsername,
		TokenType: adminTokenTypeAccess,
		IssuedAt:  now.Unix(),
		ExpiresAt: accessExpireAt.Unix(),
	})
	if err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}
	refreshToken, err = m.sign(adminTokenClaims{
		Subject:   fixedUsername,
		TokenType: adminTokenTypeRefresh,
		IssuedAt:  now.Unix(),
		ExpiresAt: refreshExpireAt.Unix(),
	})
	if err != nil {
		return "", "", time.Time{}, time.Time{}, err
	}
	return accessToken, refreshToken, accessExpireAt, refreshExpireAt, nil
}

func (m *adminTokenManager) ParseAndValidate(token, expectedType string) (adminTokenClaims, error) {
	if m == nil {
		return adminTokenClaims{}, errors.New("admin token manager is nil")
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return adminTokenClaims{}, errors.New("invalid token format")
	}

	signed := parts[0] + "." + parts[1]
	sig, err := m.signRaw(signed)
	if err != nil {
		return adminTokenClaims{}, err
	}
	if !hmac.Equal([]byte(sig), []byte(parts[2])) {
		return adminTokenClaims{}, errors.New("invalid token signature")
	}

	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return adminTokenClaims{}, errors.New("invalid token payload")
	}
	var claims adminTokenClaims
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return adminTokenClaims{}, errors.New("invalid token claims")
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return adminTokenClaims{}, errors.New("invalid token subject")
	}
	if strings.TrimSpace(claims.TokenType) != strings.TrimSpace(expectedType) {
		return adminTokenClaims{}, errors.New("invalid token type")
	}
	nowUnix := m.nowFn().UTC().Unix()
	if claims.ExpiresAt <= nowUnix {
		return adminTokenClaims{}, errors.New("token expired")
	}
	return claims, nil
}

func (m *adminTokenManager) sign(claims adminTokenClaims) (string, error) {
	headerRaw, err := json.Marshal(map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	})
	if err != nil {
		return "", err
	}
	payloadRaw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString(headerRaw)
	payload := base64.RawURLEncoding.EncodeToString(payloadRaw)
	signed := header + "." + payload
	sig, err := m.signRaw(signed)
	if err != nil {
		return "", err
	}
	return signed + "." + sig, nil
}

func (m *adminTokenManager) signRaw(signed string) (string, error) {
	mac := hmac.New(sha256.New, m.secret)
	if _, err := mac.Write([]byte(signed)); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

type adminAuthController struct {
	repo   *db.Repository
	tokens *adminTokenManager
}

func newAdminAuthController(repo *db.Repository, tokens *adminTokenManager) (*adminAuthController, error) {
	if repo == nil {
		return nil, errors.New("admin auth repo is required")
	}
	if tokens == nil {
		return nil, errors.New("admin token manager is required")
	}
	return &adminAuthController{repo: repo, tokens: tokens}, nil
}

func (a *adminAuthController) BootstrapDefaultUser(username, password string) error {
	if a == nil || a.repo == nil {
		return errors.New("admin auth controller not configured")
	}
	fixedUsername := strings.TrimSpace(username)
	fixedPassword := strings.TrimSpace(password)
	if fixedUsername == "" || fixedPassword == "" {
		return errors.New("admin bootstrap credentials are required")
	}
	hashRaw, err := bcrypt.GenerateFromPassword([]byte(fixedPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}
	return a.repo.EnsureAdminUser(fixedUsername, string(hashRaw))
}

func (a *adminAuthController) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authz := strings.TrimSpace(c.GetHeader("Authorization"))
		if authz == "" {
			writeError(c, http.StatusUnauthorized, "ADMIN_AUTH_REQUIRED", "missing authorization header")
			c.Abort()
			return
		}
		parts := strings.SplitN(authz, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "Bearer") {
			writeError(c, http.StatusUnauthorized, "ADMIN_AUTH_REQUIRED", "invalid authorization header")
			c.Abort()
			return
		}
		token := strings.TrimSpace(parts[1])
		claims, err := a.tokens.ParseAndValidate(token, adminTokenTypeAccess)
		if err != nil {
			writeError(c, http.StatusUnauthorized, "ADMIN_AUTH_INVALID", err.Error())
			c.Abort()
			return
		}
		c.Set(adminUsernameContextKey, claims.Subject)
		c.Next()
	}
}

func adminUsernameFromContext(c *gin.Context) (string, bool) {
	if c == nil {
		return "", false
	}
	v, ok := c.Get(adminUsernameContextKey)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", false
	}
	return strings.TrimSpace(s), true
}

type adminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type adminRefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (a *adminAuthController) handleLogin(c *gin.Context) {
	if a == nil || a.repo == nil || a.tokens == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin auth not configured")
		return
	}
	var req adminLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	username := strings.TrimSpace(req.Username)
	password := strings.TrimSpace(req.Password)
	if username == "" || password == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "username and password are required")
		return
	}

	user, found, err := a.repo.GetAdminUserByUsername(username)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load admin user failed")
		return
	}
	if !found || !strings.EqualFold(strings.TrimSpace(user.Status), "ACTIVE") {
		writeError(c, http.StatusUnauthorized, "ADMIN_AUTH_INVALID", "invalid username or password")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		writeError(c, http.StatusUnauthorized, "ADMIN_AUTH_INVALID", "invalid username or password")
		return
	}

	accessToken, refreshToken, accessExpireAt, refreshExpireAt, err := a.tokens.IssueTokenPair(user.Username)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "issue token failed")
		return
	}

	writeSuccess(c, gin.H{
		"access_token":             accessToken,
		"refresh_token":            refreshToken,
		"token_type":               "Bearer",
		"access_token_expires_at":  accessExpireAt.UTC().Format(time.RFC3339),
		"refresh_token_expires_at": refreshExpireAt.UTC().Format(time.RFC3339),
		"user": gin.H{
			"username": user.Username,
		},
	})
}

func (a *adminAuthController) handleRefresh(c *gin.Context) {
	if a == nil || a.repo == nil || a.tokens == nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "admin auth not configured")
		return
	}
	var req adminRefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "invalid request body")
		return
	}
	token := strings.TrimSpace(req.RefreshToken)
	if token == "" {
		writeError(c, http.StatusBadRequest, "INVALID_PARAM", "refresh_token is required")
		return
	}

	claims, err := a.tokens.ParseAndValidate(token, adminTokenTypeRefresh)
	if err != nil {
		writeError(c, http.StatusUnauthorized, "ADMIN_AUTH_INVALID", err.Error())
		return
	}
	user, found, err := a.repo.GetAdminUserByUsername(claims.Subject)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "load admin user failed")
		return
	}
	if !found || !strings.EqualFold(strings.TrimSpace(user.Status), "ACTIVE") {
		writeError(c, http.StatusUnauthorized, "ADMIN_AUTH_INVALID", "admin user inactive")
		return
	}

	accessToken, refreshToken, accessExpireAt, refreshExpireAt, err := a.tokens.IssueTokenPair(user.Username)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "issue token failed")
		return
	}

	writeSuccess(c, gin.H{
		"access_token":             accessToken,
		"refresh_token":            refreshToken,
		"token_type":               "Bearer",
		"access_token_expires_at":  accessExpireAt.UTC().Format(time.RFC3339),
		"refresh_token_expires_at": refreshExpireAt.UTC().Format(time.RFC3339),
		"user": gin.H{
			"username": user.Username,
		},
	})
}

func (a *adminAuthController) handleMe(c *gin.Context) {
	username, ok := adminUsernameFromContext(c)
	if !ok {
		writeError(c, http.StatusUnauthorized, "ADMIN_AUTH_INVALID", "admin context missing")
		return
	}
	writeSuccess(c, gin.H{
		"username": username,
	})
}

func (a *adminAuthController) handleLogout(c *gin.Context) {
	writeSuccess(c, gin.H{"ok": true})
}
