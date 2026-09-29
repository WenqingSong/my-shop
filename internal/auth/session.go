// Package auth 中的会话管理：登录在 Redis 建立有状态 session（iam:session:{sid}），
// 鉴权中间件与 logout 均通过本模块访问，禁止散落 iam:session: 字面量。
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/gogf/gf/v2/frame/g"
)

const (
	// sessionKeyPrefix 是 Redis session key 前缀，完整 key 为 iam:session:{sid}。
	sessionKeyPrefix = "iam:session:"
	// sessionFieldUserID 是 session Hash 中存储的用户 id 字段（十进制字符串）。
	sessionFieldUserID = "user_id"
	// sessionFieldRevoked 是 session Hash 中存储的撤销标记字段（"0"/"1"）。
	sessionFieldRevoked = "revoked"
	// defaultSessionTTL 是会话默认 TTL（秒），与 JWT exp 对齐。
	defaultSessionTTL = 3600
	// adminSessionKeyPrefix 是管理员会话 key 前缀，完整 key 为 iam:admin:session:{sid}。
	adminSessionKeyPrefix = "iam:admin:session:"
	// sessionFieldAdminID 是管理员会话 Hash 中存储的管理员 id 字段（十进制字符串）。
	sessionFieldAdminID = "admin_id"
)

// revokeScript 原子撤销：仅当 key 存在时置 revoked=1，保持剩余 TTL，不创建新 key。
// 返回 1 表示 key 存在并已撤销，0 表示 key 不存在（视为已登出）。
const revokeScript = `
if redis.call('EXISTS', KEYS[1]) == 1 then
  redis.call('HSET', KEYS[1], ARGV[1], '1')
  return 1
end
return 0
`

// NewSid 生成 128-bit（16 字节）密码学随机会话 id，编码为 32 位小写 hex。
// 碰撞概率可忽略，不做碰撞重试。
func NewSid() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成会话 sid: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// SessionKey 返回 sid 对应的 Redis session key。
func SessionKey(sid string) string {
	return sessionKeyPrefix + sid
}

// SessionTTL 返回会话 TTL（秒），默认 3600，必须大于 0。
// 可经 auth.session.ttl 配置或 AUTH_SESSION_TTL 环境变量覆盖。
func SessionTTL(ctx context.Context) (int64, error) {
	v, err := g.Cfg().GetEffective(ctx, "auth.session.ttl", defaultSessionTTL)
	if err != nil {
		return 0, fmt.Errorf("读取 auth.session.ttl: %w", err)
	}
	ttl := v.Int64()
	if ttl <= 0 {
		return 0, errors.New("auth.session.ttl 必须大于 0")
	}
	return ttl, nil
}

// CreateSession 写入会话 Hash（user_id、revoked=0）并设置 TTL，初始未撤销。
// 写失败返回 error，调用方应视为登录失败（保证「返回的 token 必有有效 session」）。
func CreateSession(ctx context.Context, sid string, userID int64, ttl int64) error {
	key := SessionKey(sid)
	if _, err := g.Redis().HSet(ctx, key, map[string]any{
		sessionFieldUserID:  strconv.FormatInt(userID, 10),
		sessionFieldRevoked: "0",
	}); err != nil {
		return fmt.Errorf("写入会话: %w", err)
	}
	if _, err := g.Redis().Expire(ctx, key, ttl); err != nil {
		return fmt.Errorf("设置会话 TTL: %w", err)
	}
	return nil
}

// ValidateSession 校验会话是否有效：key 存在、未撤销且 user_id 与给定 userID 一致。
// 返回 (valid bool, err error)：err 非 nil 表示 Redis 查询失败，调用方必须 fail-closed（401）。
func ValidateSession(ctx context.Context, sid string, userID int64) (bool, error) {
	v, err := g.Redis().HGetAll(ctx, SessionKey(sid))
	if err != nil {
		return false, fmt.Errorf("查询会话: %w", err)
	}
	fields := v.MapStrStr()
	if len(fields) == 0 {
		// session 不存在（TTL 到期或被清理）。
		return false, nil
	}
	if fields[sessionFieldRevoked] == "1" {
		return false, nil
	}
	if fields[sessionFieldUserID] != strconv.FormatInt(userID, 10) {
		return false, nil
	}
	return true, nil
}

// RevokeSession 原子撤销会话（仅当 key 存在时置 revoked=1），保持剩余 TTL，不创建新 key。
// key 不存在视为已登出，返回 nil。Redis 错误返回 error，调用方不应吞掉后返回成功。
func RevokeSession(ctx context.Context, sid string) error {
	if _, err := g.Redis().Do(ctx, "EVAL", revokeScript, 1, SessionKey(sid), sessionFieldRevoked); err != nil {
		return fmt.Errorf("撤销会话: %w", err)
	}
	return nil
}

// AdminSessionKey 返回 sid 对应的管理员会话 Redis key。
func AdminSessionKey(sid string) string {
	return adminSessionKeyPrefix + sid
}

// CreateAdminSession 写入管理员会话 Hash（admin_id、revoked=0）并设置 TTL，初始未撤销。
// 写失败返回 error，调用方应视为登录失败（保证「返回的 token 必有有效 session」）。
func CreateAdminSession(ctx context.Context, sid string, adminID int64, ttl int64) error {
	key := AdminSessionKey(sid)
	if _, err := g.Redis().HSet(ctx, key, map[string]any{
		sessionFieldAdminID: strconv.FormatInt(adminID, 10),
		sessionFieldRevoked: "0",
	}); err != nil {
		return fmt.Errorf("写入管理员会话: %w", err)
	}
	if _, err := g.Redis().Expire(ctx, key, ttl); err != nil {
		return fmt.Errorf("设置管理员会话 TTL: %w", err)
	}
	return nil
}

// ValidateAdminSession 校验管理员会话是否有效：key 存在、未撤销且 admin_id 与给定 adminID 一致。
// 返回 (valid bool, err error)：err 非 nil 表示 Redis 查询失败，调用方必须 fail-closed（401）。
func ValidateAdminSession(ctx context.Context, sid string, adminID int64) (bool, error) {
	v, err := g.Redis().HGetAll(ctx, AdminSessionKey(sid))
	if err != nil {
		return false, fmt.Errorf("查询管理员会话: %w", err)
	}
	fields := v.MapStrStr()
	if len(fields) == 0 {
		return false, nil
	}
	if fields[sessionFieldRevoked] == "1" {
		return false, nil
	}
	if fields[sessionFieldAdminID] != strconv.FormatInt(adminID, 10) {
		return false, nil
	}
	return true, nil
}

// RevokeAdminSession 原子撤销管理员会话（仅当 key 存在时置 revoked=1），保持剩余 TTL，不创建新 key。
// key 不存在视为已登出，返回 nil。Redis 错误返回 error，调用方不应吞掉后返回成功。
func RevokeAdminSession(ctx context.Context, sid string) error {
	if _, err := g.Redis().Do(ctx, "EVAL", revokeScript, 1, AdminSessionKey(sid), sessionFieldRevoked); err != nil {
		return fmt.Errorf("撤销管理员会话: %w", err)
	}
	return nil
}
