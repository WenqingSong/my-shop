// Package auth 提供 IAM JWT access token 的签发与校验。
package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gogf/gf/v2/frame/g"
	gojwt "github.com/golang-jwt/jwt/v5"
)

const (
	// Issuer 是 IAM 签发 token 的 issuer 声明。
	Issuer = "surgecart"
	// ExpiresIn 是 access token 的有效期（秒），固定 1 小时。
	ExpiresIn = 3600
	// minSecretLength 是 JWT 签名密钥的最小长度（字节）。
	minSecretLength = 32

	// TypeUser 是前台用户 token 的类型标识。
	TypeUser = "user"
	// TypeAdmin 是后台管理员 token 的类型标识。
	TypeAdmin = "admin"
)

// Claims 是 IAM 签发的 JWT 声明。
type Claims struct {
	gojwt.RegisteredClaims
	// Sid 是本次登录会话的唯一标识，作为 JWT 与 Redis session 之间的桥梁。
	Sid string `json:"sid,omitempty"`
	// Type 是 token 类型："user"（前台用户）或 "admin"（后台管理员）。
	Type string `json:"type,omitempty"`
}

// Secret 读取并校验 JWT 签名密钥（auth.jwt.secret / AUTH_JWT_SECRET）。
// 空或过短返回错误，用于启动时 fail-fast 与运行时读取。
func Secret(ctx context.Context) ([]byte, error) {
	v, err := g.Cfg().GetEffective(ctx, "auth.jwt.secret")
	if err != nil {
		return nil, fmt.Errorf("读取 auth.jwt.secret: %w", err)
	}
	if v == nil || v.String() == "" {
		return nil, errors.New("未配置 auth.jwt.secret")
	}
	secret := v.String()
	if len(secret) < minSecretLength {
		return nil, fmt.Errorf("auth.jwt.secret 长度至少为 %d 字节", minSecretLength)
	}
	return []byte(secret), nil
}

// Generate 为指定主体（用户或管理员，由 typ 区分）签发含 sid 的 access token（密钥来自配置）。
func Generate(ctx context.Context, typ string, id int64, sid string) (string, error) {
	secret, err := Secret(ctx)
	if err != nil {
		return "", err
	}
	return GenerateWithSecret(secret, typ, id, sid)
}

// GenerateWithSecret 使用给定密钥签发含 sid 的 access token，便于单元测试。
func GenerateWithSecret(secret []byte, typ string, id int64, sid string) (string, error) {
	now := time.Now()
	claims := Claims{
		Sid:  sid,
		Type: typ,
		RegisteredClaims: gojwt.RegisteredClaims{
			Subject:   strconv.FormatInt(id, 10),
			Issuer:    Issuer,
			IssuedAt:  gojwt.NewNumericDate(now),
			ExpiresAt: gojwt.NewNumericDate(now.Add(ExpiresIn * time.Second)),
		},
	}
	return gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString(secret)
}

// Parse 校验并解析 access token（密钥来自配置）。
func Parse(ctx context.Context, tokenString string) (*Claims, error) {
	secret, err := Secret(ctx)
	if err != nil {
		return nil, err
	}
	return ParseWithSecret(secret, tokenString)
}

// ParseWithSecret 使用给定密钥校验并解析 token，便于单元测试。
func ParseWithSecret(secret []byte, tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := gojwt.ParseWithClaims(tokenString, claims,
		func(t *gojwt.Token) (any, error) {
			if _, ok := t.Method.(*gojwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("非预期的签名算法: %v", t.Header["alg"])
			}
			return secret, nil
		},
		gojwt.WithIssuer(Issuer),
		gojwt.WithExpirationRequired(),
		gojwt.WithValidMethods([]string{gojwt.SigningMethodHS256.Alg()}),
	)
	if err != nil {
		return nil, err
	}
	if token == nil || !token.Valid {
		return nil, errors.New("无效的 token")
	}
	return claims, nil
}
