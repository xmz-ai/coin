package api

import (
	"strings"
	"testing"
	"time"
)

func TestAdminTokenManagerIssueAndParse(t *testing.T) {
	now := time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC)
	mgr, err := newAdminTokenManager("secret_123", 10*time.Minute, time.Hour, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatalf("newAdminTokenManager failed: %v", err)
	}

	access, refresh, _, _, err := mgr.IssueTokenPair("admin")
	if err != nil {
		t.Fatalf("IssueTokenPair failed: %v", err)
	}
	if access == "" || refresh == "" {
		t.Fatalf("expected non-empty tokens")
	}
	if strings.Count(access, ".") != 2 || strings.Count(refresh, ".") != 2 {
		t.Fatalf("expected JWT-like token format")
	}

	claims, err := mgr.ParseAndValidate(access, adminTokenTypeAccess)
	if err != nil {
		t.Fatalf("ParseAndValidate access failed: %v", err)
	}
	if claims.Subject != "admin" {
		t.Fatalf("unexpected subject: %s", claims.Subject)
	}

	if _, err := mgr.ParseAndValidate(refresh, adminTokenTypeAccess); err == nil {
		t.Fatalf("expected token type validation error")
	}
}

func TestAdminTokenManagerDetectsExpiredToken(t *testing.T) {
	now := time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC)
	issuer, err := newAdminTokenManager("secret_123", time.Minute, time.Minute, func() time.Time {
		return now
	})
	if err != nil {
		t.Fatalf("newAdminTokenManager issuer failed: %v", err)
	}
	access, _, _, _, err := issuer.IssueTokenPair("admin")
	if err != nil {
		t.Fatalf("IssueTokenPair failed: %v", err)
	}

	validator, err := newAdminTokenManager("secret_123", time.Minute, time.Minute, func() time.Time {
		return now.Add(2 * time.Minute)
	})
	if err != nil {
		t.Fatalf("newAdminTokenManager validator failed: %v", err)
	}
	if _, err := validator.ParseAndValidate(access, adminTokenTypeAccess); err == nil {
		t.Fatalf("expected expired token error")
	}
}
