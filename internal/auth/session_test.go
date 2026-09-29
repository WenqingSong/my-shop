package auth

import (
	"context"
	"testing"
)

func TestNewSidFormat(t *testing.T) {
	sid, err := NewSid()
	if err != nil {
		t.Fatalf("NewSid: %v", err)
	}
	if len(sid) != 32 {
		t.Fatalf("expected 32-char hex sid, got len=%d %q", len(sid), sid)
	}
	for _, r := range sid {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Fatalf("sid contains non-hex char %q in %q", r, sid)
		}
	}
}

func TestNewSidUnique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		sid, err := NewSid()
		if err != nil {
			t.Fatalf("NewSid: %v", err)
		}
		if seen[sid] {
			t.Fatalf("duplicate sid %q", sid)
		}
		seen[sid] = true
	}
}

func TestSessionKey(t *testing.T) {
	if got := SessionKey("abc"); got != "iam:session:abc" {
		t.Fatalf("expected iam:session:abc, got %q", got)
	}
}

func TestAdminSessionKey(t *testing.T) {
	if got := AdminSessionKey("abc"); got != "iam:admin:session:abc" {
		t.Fatalf("expected iam:admin:session:abc, got %q", got)
	}
}

func TestSessionTTLDefault(t *testing.T) {
	ttl, err := SessionTTL(context.Background())
	if err != nil {
		t.Fatalf("SessionTTL: %v", err)
	}
	if ttl != defaultSessionTTL {
		t.Fatalf("expected default %d, got %d", defaultSessionTTL, ttl)
	}
}

func TestSessionTTLEnvOverride(t *testing.T) {
	t.Setenv("AUTH_SESSION_TTL", "60")
	ttl, err := SessionTTL(context.Background())
	if err != nil {
		t.Fatalf("SessionTTL: %v", err)
	}
	if ttl != 60 {
		t.Fatalf("expected 60, got %d", ttl)
	}
}

func TestSessionTTLRejectsNonPositive(t *testing.T) {
	t.Setenv("AUTH_SESSION_TTL", "0")
	if _, err := SessionTTL(context.Background()); err == nil {
		t.Fatal("expected error for non-positive TTL, got nil")
	}
}
