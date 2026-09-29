package auth

import (
	"context"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
)

func testSecret() []byte {
	return []byte("0123456789abcdef0123456789abcdef") // 32 字节
}

func TestGenerateClaims(t *testing.T) {
	token, err := GenerateWithSecret(testSecret(), TypeUser, 42, "sid-abc123")
	if err != nil {
		t.Fatalf("GenerateWithSecret: %v", err)
	}
	claims, err := ParseWithSecret(testSecret(), token)
	if err != nil {
		t.Fatalf("ParseWithSecret: %v", err)
	}
	if claims.Subject != "42" {
		t.Fatalf("expected sub %q, got %q", "42", claims.Subject)
	}
	if claims.Type != TypeUser {
		t.Fatalf("expected type %q, got %q", TypeUser, claims.Type)
	}
	if claims.Sid != "sid-abc123" {
		t.Fatalf("expected sid %q, got %q", "sid-abc123", claims.Sid)
	}
	if claims.Issuer != Issuer {
		t.Fatalf("expected iss %q, got %q", Issuer, claims.Issuer)
	}
	if claims.IssuedAt == nil || claims.ExpiresAt == nil {
		t.Fatal("expected iat and exp to be set")
	}
	if d := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time); d != time.Duration(ExpiresIn)*time.Second {
		t.Fatalf("expected exp-iat=%ds, got %v", ExpiresIn, d)
	}
}

func TestGenerateTypeAdmin(t *testing.T) {
	token, err := GenerateWithSecret(testSecret(), TypeAdmin, 7, "sid-admin")
	if err != nil {
		t.Fatalf("GenerateWithSecret: %v", err)
	}
	claims, err := ParseWithSecret(testSecret(), token)
	if err != nil {
		t.Fatalf("ParseWithSecret: %v", err)
	}
	if claims.Type != TypeAdmin {
		t.Fatalf("expected type %q, got %q", TypeAdmin, claims.Type)
	}
	if claims.Subject != "7" {
		t.Fatalf("expected sub %q, got %q", "7", claims.Subject)
	}
}

func TestParseRejectsWrongSecret(t *testing.T) {
	token, err := GenerateWithSecret(testSecret(), TypeUser, 1, "sid-1")
	if err != nil {
		t.Fatalf("GenerateWithSecret: %v", err)
	}
	if _, err := ParseWithSecret([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), token); err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}

func TestParseRejectsWrongIssuer(t *testing.T) {
	now := time.Now()
	claims := Claims{RegisteredClaims: gojwt.RegisteredClaims{
		Subject:   "1",
		Issuer:    "evil",
		IssuedAt:  gojwt.NewNumericDate(now),
		ExpiresAt: gojwt.NewNumericDate(now.Add(time.Hour)),
	}}
	token, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString(testSecret())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := ParseWithSecret(testSecret(), token); err == nil {
		t.Fatal("expected error for wrong issuer, got nil")
	}
}

func TestParseRejectsExpiredToken(t *testing.T) {
	now := time.Now()
	claims := Claims{RegisteredClaims: gojwt.RegisteredClaims{
		Subject:   "1",
		Issuer:    Issuer,
		IssuedAt:  gojwt.NewNumericDate(now.Add(-2 * time.Hour)),
		ExpiresAt: gojwt.NewNumericDate(now.Add(-time.Hour)),
	}}
	token, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString(testSecret())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := ParseWithSecret(testSecret(), token); err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestParseRejectsMissingExp(t *testing.T) {
	now := time.Now()
	claims := Claims{RegisteredClaims: gojwt.RegisteredClaims{
		Subject:  "1",
		Issuer:   Issuer,
		IssuedAt: gojwt.NewNumericDate(now),
	}}
	token, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString(testSecret())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := ParseWithSecret(testSecret(), token); err == nil {
		t.Fatal("expected error for missing exp, got nil")
	}
}

func TestSecretFromEnv(t *testing.T) {
	const want = "0123456789abcdef0123456789abcdef"
	t.Setenv("AUTH_JWT_SECRET", want)
	secret, err := Secret(context.Background())
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}
	if string(secret) != want {
		t.Fatalf("expected secret %q, got %q", want, secret)
	}
}

func TestSecretRejectsTooShort(t *testing.T) {
	t.Setenv("AUTH_JWT_SECRET", "short")
	if _, err := Secret(context.Background()); err == nil {
		t.Fatal("expected error for short secret, got nil")
	}
}
