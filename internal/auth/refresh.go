// Package auth 中的 refresh token 生成、SHA-256 哈希与 Token Family 血缘工具（仅前台用户域）。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/gogf/gf/v2/frame/g"
)

const (
	// refreshTokenBytes 是 refresh token 明文的熵源字节数（32 字节 = 256 bit）。
	refreshTokenBytes = 32
	// familyIDBytes 是 family_id 的熵源字节数（16 字节 = 128 bit）。
	familyIDBytes = 16
	// defaultRefreshTTL 是 refresh token 默认绝对有效期（秒），30 天。
	defaultRefreshTTL = 30 * 24 * 3600
)

// NewRefreshToken 生成高熵一次性 refresh token 明文：crypto/rand 32 字节 → 64 字符 hex。
// 明文仅应在签发响应中出现一次，不得进入日志、错误响应或持久介质（仅存哈希）。
func NewRefreshToken() (string, error) {
	b := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 refresh token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// NewFamilyID 生成 16 字节 crypto/rand → 32 字符 hex 的 family_id（登录生命周期内不变）。
func NewFamilyID() (string, error) {
	b := make([]byte, familyIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 family_id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// HashRefreshToken 计算 refresh token 明文的 SHA-256 hex 哈希（64 字符）。
// 高熵随机串使用快速哈希，不用 bcrypt。
func HashRefreshToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// RefreshTTL 返回 refresh token 绝对有效期（秒），默认 30 天，必须大于 0。
// 可经 auth.refresh.ttl 配置或 AUTH_REFRESH_TTL 环境变量覆盖。
func RefreshTTL(ctx context.Context) (int64, error) {
	v, err := g.Cfg().GetEffective(ctx, "auth.refresh.ttl", defaultRefreshTTL)
	if err != nil {
		return 0, fmt.Errorf("读取 auth.refresh.ttl: %w", err)
	}
	ttl := v.Int64()
	if ttl <= 0 {
		return 0, errors.New("auth.refresh.ttl 必须大于 0")
	}
	return ttl, nil
}
