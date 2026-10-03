package auth

import (
	"context"
	"regexp"
	"testing"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestNewRefreshTokenFormat(t *testing.T) {
	tok, err := NewRefreshToken()
	if err != nil {
		t.Fatalf("NewRefreshToken: %v", err)
	}
	if len(tok) != 64 || !hex64.MatchString(tok) {
		t.Fatalf("expected 64-char hex refresh token, got len=%d %q", len(tok), tok)
	}
}

func TestNewRefreshTokenUnique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		tok, err := NewRefreshToken()
		if err != nil {
			t.Fatalf("NewRefreshToken: %v", err)
		}
		if seen[tok] {
			t.Fatalf("duplicate refresh token %q", tok)
		}
		seen[tok] = true
	}
}

func TestHashRefreshTokenFormatAndDeterminism(t *testing.T) {
	const plaintext = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	h1 := HashRefreshToken(plaintext)
	h2 := HashRefreshToken(plaintext)
	if h1 != h2 {
		t.Fatalf("hash must be deterministic: %q != %q", h1, h2)
	}
	if len(h1) != 64 || !hex64.MatchString(h1) {
		t.Fatalf("expected 64-char hex hash, got len=%d %q", len(h1), h1)
	}
	if h1 == plaintext {
		t.Fatal("hash must not equal plaintext")
	}
}

func TestNewFamilyIDFormat(t *testing.T) {
	id, err := NewFamilyID()
	if err != nil {
		t.Fatalf("NewFamilyID: %v", err)
	}
	if len(id) != 32 {
		t.Fatalf("expected 32-char hex family_id, got len=%d %q", len(id), id)
	}
}

func TestRefreshTTLDefault(t *testing.T) {
	ttl, err := RefreshTTL(context.Background())
	if err != nil {
		t.Fatalf("RefreshTTL: %v", err)
	}
	if ttl != defaultRefreshTTL {
		t.Fatalf("expected default %d, got %d", defaultRefreshTTL, ttl)
	}
}

func TestRefreshTTLEnvOverride(t *testing.T) {
	t.Setenv("AUTH_REFRESH_TTL", "3600")
	ttl, err := RefreshTTL(context.Background())
	if err != nil {
		t.Fatalf("RefreshTTL: %v", err)
	}
	if ttl != 3600 {
		t.Fatalf("expected 3600, got %d", ttl)
	}
}

func TestRefreshTTLRejectsNonPositive(t *testing.T) {
	t.Setenv("AUTH_REFRESH_TTL", "0")
	if _, err := RefreshTTL(context.Background()); err == nil {
		t.Fatal("expected error for non-positive refresh TTL, got nil")
	}
}
