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

	"github.com/gogf/gf/v2/database/gredis"
	"github.com/gogf/gf/v2/frame/g"
)

const (
	// sessionKeyPrefix 是 Redis session key 前缀，完整 key 为 iam:session:{sid}。
	sessionKeyPrefix = "iam:session:"
	// sessionFieldUserID 是 session Hash 中存储的用户 id 字段（十进制字符串）。
	sessionFieldUserID = "user_id"
	// sessionFieldRevoked 是 session Hash 中存储的撤销标记字段（"0"/"1"）。
	sessionFieldRevoked = "revoked"
	// sessionFieldLoginAt 是 session Hash 中存储的登录时间字段（unix 秒，十进制字符串）。
	sessionFieldLoginAt = "login_at"
	// sessionFieldUserAgent 是 session Hash 中存储的客户端 User-Agent 字段。
	sessionFieldUserAgent = "user_agent"
	// sessionFieldIP 是 session Hash 中存储的客户端 IP 字段。
	sessionFieldIP = "ip"
	// defaultSessionTTL 是会话默认 TTL（秒），与 JWT exp 对齐。
	defaultSessionTTL = 3600
	// adminSessionKeyPrefix 是管理员会话 key 前缀，完整 key 为 iam:admin:session:{sid}。
	adminSessionKeyPrefix = "iam:admin:session:"
	// sessionFieldAdminID 是管理员会话 Hash 中存储的管理员 id 字段（十进制字符串）。
	sessionFieldAdminID = "admin_id"
	// userSessionIndexPrefix 是前台用户会话索引 key 前缀，完整 key 为 iam:user:{userID}:sessions。
	userSessionIndexPrefix = "iam:user:"
	// userSessionIndexSuffix 是前台用户会话索引 key 后缀。
	userSessionIndexSuffix = ":sessions"
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

// revokeOthersScript 原子撤销除当前 sid 外的全部会话：
// 读索引 → 对非当前 sid 的会话若存在则置 revoked=1（保留 TTL、不新建 key）→ 从索引移除已撤销成员（保留当前成员）。
// KEYS[1]=索引 key，KEYS[2]=session key 前缀；ARGV[1]=revoked 字段名，ARGV[2]=当前 sid。
const revokeOthersScript = `
local members = redis.call('ZRANGE', KEYS[1], 0, -1)
local n = 0
for i = 1, #members do
  local sid = members[i]
  if sid ~= ARGV[2] then
    local key = KEYS[2] .. sid
    if redis.call('EXISTS', key) == 1 then
      redis.call('HSET', key, ARGV[1], '1')
    end
    redis.call('ZREM', KEYS[1], sid)
    n = n + 1
  end
end
return n
`

// revokeAllScript 原子撤销全部会话（含当前）：
// 读索引 → 对每个会话若存在则置 revoked=1（保留 TTL、不新建 key）→ 清空索引。
// KEYS[1]=索引 key，KEYS[2]=session key 前缀；ARGV[1]=revoked 字段名。
const revokeAllScript = `
local members = redis.call('ZRANGE', KEYS[1], 0, -1)
local n = 0
for i = 1, #members do
  local key = KEYS[2] .. members[i]
  if redis.call('EXISTS', key) == 1 then
    redis.call('HSET', key, ARGV[1], '1')
  end
  n = n + 1
end
redis.call('DEL', KEYS[1])
return n
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

// SessionMeta 是登录时写入会话的元数据（用于会话列表区分设备）。
type SessionMeta struct {
	// LoginAt 是登录 unix 秒（必需）。
	LoginAt int64
	// UserAgent 是客户端 User-Agent（设备信息）。
	UserAgent string
	// IP 是客户端 IP（设备信息）。
	IP string
}

// CreateSession 写入会话 Hash（user_id、revoked=0、登录元数据）并设置 TTL，初始未撤销。
// 随后将会话 sid 写入用户会话索引（ZSET），索引为提交点。
// 任一步写失败返回 error，调用方应视为登录失败（保证「返回的 token 必有有效 session」）。
func CreateSession(ctx context.Context, sid string, userID int64, ttl int64, meta SessionMeta) error {
	key := SessionKey(sid)
	if _, err := g.Redis().HSet(ctx, key, map[string]any{
		sessionFieldUserID:    strconv.FormatInt(userID, 10),
		sessionFieldRevoked:   "0",
		sessionFieldLoginAt:   strconv.FormatInt(meta.LoginAt, 10),
		sessionFieldUserAgent: meta.UserAgent,
		sessionFieldIP:        meta.IP,
	}); err != nil {
		return fmt.Errorf("写入会话: %w", err)
	}
	if _, err := g.Redis().Expire(ctx, key, ttl); err != nil {
		return fmt.Errorf("设置会话 TTL: %w", err)
	}
	if err := AddSessionToIndex(ctx, userID, sid, meta.LoginAt, ttl); err != nil {
		return err
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

// SessionIndexKey 返回 userID 对应的前台用户会话索引 key（iam:user:{userID}:sessions）。
func SessionIndexKey(userID int64) string {
	return userSessionIndexPrefix + strconv.FormatInt(userID, 10) + userSessionIndexSuffix
}

// AddSessionToIndex 将会话 sid 以登录时间为 score 写入用户会话索引（ZSET），并刷新索引 TTL。
// 索引仅为枚举优化，Session Hash 才是归属与撤销状态的权威事实。
func AddSessionToIndex(ctx context.Context, userID int64, sid string, loginAt, ttl int64) error {
	key := SessionIndexKey(userID)
	if _, err := g.Redis().ZAdd(ctx, key, &gredis.ZAddOption{}, gredis.ZAddMember{
		Score:  float64(loginAt),
		Member: sid,
	}); err != nil {
		return fmt.Errorf("写入会话索引: %w", err)
	}
	if _, err := g.Redis().Expire(ctx, key, ttl); err != nil {
		return fmt.Errorf("设置会话索引 TTL: %w", err)
	}
	return nil
}

// ListSessionIndex 返回用户会话索引中的全部 sid（按 score 升序）。
// Redis 查询失败返回 error，调用方必须 fail-closed（不返回空列表）。
func ListSessionIndex(ctx context.Context, userID int64) ([]string, error) {
	vars, err := g.Redis().ZRange(ctx, SessionIndexKey(userID), 0, -1)
	if err != nil {
		return nil, fmt.Errorf("读取会话索引: %w", err)
	}
	return vars.Strings(), nil
}

// RemoveSessionFromIndex 从用户会话索引中移除指定 sid（用于清理 stale member）。
func RemoveSessionFromIndex(ctx context.Context, userID int64, sid string) error {
	if _, err := g.Redis().ZRem(ctx, SessionIndexKey(userID), sid); err != nil {
		return fmt.Errorf("移除会话索引成员: %w", err)
	}
	return nil
}

// SessionFields 返回会话 Hash 的全部字段；不存在时返回空 map。
// err 非 nil 表示 Redis 查询失败，调用方必须 fail-closed。
func SessionFields(ctx context.Context, sid string) (map[string]string, error) {
	v, err := g.Redis().HGetAll(ctx, SessionKey(sid))
	if err != nil {
		return nil, fmt.Errorf("查询会话: %w", err)
	}
	return v.MapStrStr(), nil
}

// SessionBelongsTo 判断会话是否属于指定用户（仅校验存在 + user_id 绑定，不校验撤销状态）。
// 用于撤销指定会话前的归属校验；Redis 查询失败返回 error（调用方 fail-closed）。
func SessionBelongsTo(ctx context.Context, sid string, userID int64) (bool, error) {
	fields, err := SessionFields(ctx, sid)
	if err != nil {
		return false, err
	}
	if len(fields) == 0 {
		return false, nil
	}
	return fields[sessionFieldUserID] == strconv.FormatInt(userID, 10), nil
}

// SessionInfo 是会话 Hash 的解析结果（用于会话列表展示与过滤）。
type SessionInfo struct {
	// UserID 是会话绑定的用户 id（解析失败为 0）。
	UserID int64
	// Revoked 标记会话是否已撤销。
	Revoked bool
	// LoginAt 是登录时间（unix 秒，解析失败为 0）。
	LoginAt int64
	// UserAgent 是客户端 User-Agent。
	UserAgent string
	// IP 是客户端 IP。
	IP string
}

// GetSession 读取并解析会话 Hash；不存在返回 (nil, nil)。err 非 nil 表示 Redis 查询失败。
func GetSession(ctx context.Context, sid string) (*SessionInfo, error) {
	fields, err := SessionFields(ctx, sid)
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, nil
	}
	userID, _ := strconv.ParseInt(fields[sessionFieldUserID], 10, 64)
	loginAt, _ := strconv.ParseInt(fields[sessionFieldLoginAt], 10, 64)
	return &SessionInfo{
		UserID:    userID,
		Revoked:   fields[sessionFieldRevoked] == "1",
		LoginAt:   loginAt,
		UserAgent: fields[sessionFieldUserAgent],
		IP:        fields[sessionFieldIP],
	}, nil
}

// RevokeOtherSessions 原子撤销除 currentSid 外的该用户全部会话，并从索引移除被撤销成员（保留当前成员）。
// Redis 错误返回 error，调用方不应吞掉后返回成功。
func RevokeOtherSessions(ctx context.Context, userID int64, currentSid string) error {
	if _, err := g.Redis().Do(ctx, "EVAL", revokeOthersScript, 2,
		SessionIndexKey(userID), sessionKeyPrefix, sessionFieldRevoked, currentSid); err != nil {
		return fmt.Errorf("撤销其他会话: %w", err)
	}
	return nil
}

// RevokeAllSessions 原子撤销该用户全部会话（含当前），并清空索引。
// Redis 错误返回 error，调用方不应吞掉后返回成功。
func RevokeAllSessions(ctx context.Context, userID int64) error {
	if _, err := g.Redis().Do(ctx, "EVAL", revokeAllScript, 2,
		SessionIndexKey(userID), sessionKeyPrefix, sessionFieldRevoked); err != nil {
		return fmt.Errorf("撤销全部会话: %w", err)
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
