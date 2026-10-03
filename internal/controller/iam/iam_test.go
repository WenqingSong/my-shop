package iam_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
	gojwt "github.com/golang-jwt/jwt/v5"

	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/cmd"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

const (
	testJWTSecret     = "test-secret-0123456789-0123456789-0123456789" // >= 32 bytes
	testAdminPassword = "test-admin-password-123"
)

type result struct {
	Status  int
	Code    int
	Message string
	Data    map[string]any
}

func decode(r *gclient.Response) result {
	var env struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	_ = json.Unmarshal(r.ReadAll(), &env)
	return result{Status: r.StatusCode, Code: env.Code, Message: env.Message, Data: env.Data}
}

func request(base, method, path string, body any, headers map[string]string) (result, error) {
	c := g.Client()
	if len(headers) > 0 {
		c = c.Header(headers)
	}
	var (
		r   *gclient.Response
		err error
	)
	switch method {
	case "GET":
		r, err = c.Get(context.Background(), base+path)
	case "POST":
		r, err = c.ContentJson().Post(context.Background(), base+path, body)
	case "DELETE":
		r, err = c.Delete(context.Background(), base+path)
	default:
		return result{}, fmt.Errorf("unsupported method %s", method)
	}
	if err != nil {
		return result{}, err
	}
	defer r.Close()
	return decode(r), nil
}

func doRequest(t *testing.T, base, method, path string, body any, headers map[string]string) result {
	t.Helper()
	res, err := request(base, method, path, body, headers)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return res
}

func userPasswordHash(t *testing.T, username string) string {
	t.Helper()
	v, err := g.DB().Model("users").Ctx(context.Background()).
		Where("username", username).Value("password_hash")
	if err != nil {
		t.Fatalf("query password_hash: %v", err)
	}
	return v.String()
}

func userCount(t *testing.T, username string) int {
	t.Helper()
	n, err := g.DB().Model("users").Ctx(context.Background()).
		Where("username", username).Count()
	if err != nil {
		t.Fatalf("count user: %v", err)
	}
	return n
}

func signToken(t *testing.T, secret, sub, iss string, iat, exp time.Time) string {
	t.Helper()
	claims := auth.Claims{
		Type: auth.TypeUser,
		RegisteredClaims: gojwt.RegisteredClaims{
			Subject:   sub,
			Issuer:    iss,
			IssuedAt:  gojwt.NewNumericDate(iat),
			ExpiresAt: gojwt.NewNumericDate(exp),
		},
	}
	tok, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

// signTokenWithSid 签发一个带 sid、验签可通过但 sid 可任意指定的 token，用于构造「会话缺失」等场景。
func signTokenWithSid(t *testing.T, secret, sub, sid string) string {
	t.Helper()
	now := time.Now()
	claims := auth.Claims{
		Sid:  sid,
		Type: auth.TypeUser,
		RegisteredClaims: gojwt.RegisteredClaims{
			Subject:   sub,
			Issuer:    auth.Issuer,
			IssuedAt:  gojwt.NewNumericDate(now),
			ExpiresAt: gojwt.NewNumericDate(now.Add(time.Hour)),
		},
	}
	tok, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token with sid: %v", err)
	}
	return tok
}

// tokenUserID 解码 token 返回其 sub 声明（用户 id）。
func tokenUserID(t *testing.T, token string) int64 {
	t.Helper()
	claims, err := auth.ParseWithSecret([]byte(testJWTSecret), token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		t.Fatalf("parse subject: %v", err)
	}
	return id
}

// tokenSid 解码 token 返回其 sid 声明。
func tokenSid(t *testing.T, token string) string {
	t.Helper()
	claims, err := auth.ParseWithSecret([]byte(testJWTSecret), token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	return claims.Sid
}

// sessionFields 读取 Redis 中 sid 对应 session 的 Hash 字段。
func sessionFields(t *testing.T, sid string) map[string]string {
	t.Helper()
	v, err := g.Redis().HGetAll(context.Background(), auth.SessionKey(sid))
	if err != nil {
		t.Fatalf("hgetall session: %v", err)
	}
	return v.MapStrStr()
}

// sessionTTL 读取 sid 对应 session 的剩余 TTL（秒）。
func sessionTTL(t *testing.T, sid string) int64 {
	t.Helper()
	ttl, err := g.Redis().TTL(context.Background(), auth.SessionKey(sid))
	if err != nil {
		t.Fatalf("ttl session: %v", err)
	}
	return ttl
}

// setupIAMServer 执行 migration 建表、Bootstrap 后启动带中间件与路由的测试服务器，返回 base URL。
func setupIAMServer(t *testing.T) string {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	// Bootstrap 会执行超级管理员 seed：提供测试密码，避免「未配置密码」触发启动 fail-fast。
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if _, err := g.DB().Exec(ctx, "DELETE FROM users"); err != nil {
		t.Fatalf("clean users: %v", err)
	}

	if err := g.Redis().FlushDB(ctx); err != nil {
		t.Fatalf("clean redis: %v", err)
	}

	s := g.Server(guid.S())
	// 使用随机端口，避免与其它测试包（如 health）在并行 go test 下争用默认 8000 端口。
	s.SetPort(0)
	s.Group("/", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.Response)
		cmd.RegisterFrontendRoutes(group)
	})
	s.SetDumpRouterMap(false)
	s.Start()
	t.Cleanup(func() { _ = s.Shutdown() })

	time.Sleep(100 * time.Millisecond)
	return fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
}

func TestIAMEndToEnd(t *testing.T) {
	base := setupIAMServer(t)

	// AC-001：合法注册成功，密码以 bcrypt 哈希存储且含盐。
	reg := doRequest(t, base, "POST", "/register", map[string]any{"username": "alice", "password": "password123"}, nil)
	if reg.Status != 200 || reg.Code != 0 {
		t.Fatalf("register alice: status=%d code=%d msg=%q", reg.Status, reg.Code, reg.Message)
	}
	aliceID := int64(reg.Data["id"].(float64))
	if aliceID <= 0 || reg.Data["username"] != "alice" {
		t.Fatalf("register alice unexpected data: %v", reg.Data)
	}
	aliceHash := userPasswordHash(t, "alice")
	if len(aliceHash) != 60 || aliceHash == "password123" {
		t.Fatalf("alice password_hash not bcrypt: %q", aliceHash)
	}

	// 相同密码注册另一个用户，哈希应不同（含随机盐）。
	doRequest(t, base, "POST", "/register", map[string]any{"username": "bob", "password": "password123"}, nil)
	if bobHash := userPasswordHash(t, "bob"); bobHash == aliceHash {
		t.Fatal("expected different bcrypt hashes for same password")
	}

	// AC-002：非法 username/password 返回 400 校验错误，且不写入记录。
	invalid := []struct {
		username string
		password string
	}{
		{"ab", "password123"},                  // 用户名过短
		{"bad_name", "password123"},            // 用户名包含非法字符
		{"carol", "short"},                     // 密码过短
		{"carol", "aaaaaaaaaaaaaaaaaaaaaaaaa"}, // 密码过长（25 位）
	}
	for _, c := range invalid {
		res := doRequest(t, base, "POST", "/register", map[string]any{"username": c.username, "password": c.password}, nil)
		if res.Status != 400 || res.Code != 1001 {
			t.Fatalf("register invalid (%q): status=%d code=%d", c.username, res.Status, res.Code)
		}
		if n := userCount(t, c.username); n != 0 {
			t.Fatalf("expected no record for invalid username %q, got %d", c.username, n)
		}
	}

	// AC-003：重复注册返回 409 冲突，且不覆盖既有用户。
	dup := doRequest(t, base, "POST", "/register", map[string]any{"username": "alice", "password": "otherpass123"}, nil)
	if dup.Status != 409 || dup.Code != 2001 {
		t.Fatalf("register duplicate alice: status=%d code=%d", dup.Status, dup.Code)
	}
	if got := userPasswordHash(t, "alice"); got != aliceHash {
		t.Fatal("duplicate register should not modify existing user")
	}

	// AC-004：正确凭据登录成功，返回 token，JWT 声明符合约定。
	login := doRequest(t, base, "POST", "/login", map[string]any{"username": "alice", "password": "password123"}, nil)
	if login.Status != 200 || login.Code != 0 {
		t.Fatalf("login alice: status=%d code=%d msg=%q", login.Status, login.Code, login.Message)
	}
	accessToken, _ := login.Data["access_token"].(string)
	if accessToken == "" {
		t.Fatal("expected non-empty access_token")
	}
	if login.Data["token_type"] != "Bearer" || int(login.Data["expires_in"].(float64)) != auth.ExpiresIn {
		t.Fatalf("unexpected login data: %v", login.Data)
	}
	claims, err := auth.ParseWithSecret([]byte(testJWTSecret), accessToken)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if claims.Subject != fmt.Sprintf("%d", aliceID) {
		t.Fatalf("expected sub=%d, got %q", aliceID, claims.Subject)
	}
	if claims.Type != auth.TypeUser {
		t.Fatalf("expected type=%q, got %q", auth.TypeUser, claims.Type)
	}
	if claims.Issuer != auth.Issuer {
		t.Fatalf("expected iss=%q, got %q", auth.Issuer, claims.Issuer)
	}
	if claims.IssuedAt == nil || claims.ExpiresAt == nil {
		t.Fatal("expected iat and exp set")
	}
	if d := claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time); d != time.Duration(auth.ExpiresIn)*time.Second {
		t.Fatalf("expected exp-iat=%ds, got %v", auth.ExpiresIn, d)
	}

	// IAM V2：token 必须携带 sid，且 Redis 中已写入对应 session（user_id == sub，未撤销，TTL>0）。
	if claims.Sid == "" {
		t.Fatal("expected non-empty sid in token claims")
	}
	sf := sessionFields(t, claims.Sid)
	if sf["user_id"] != fmt.Sprintf("%d", aliceID) {
		t.Fatalf("expected session user_id=%d, got %q", aliceID, sf["user_id"])
	}
	if sf["revoked"] != "0" {
		t.Fatalf("expected session not revoked, got %q", sf["revoked"])
	}
	if ttl := sessionTTL(t, claims.Sid); ttl <= 0 {
		t.Fatalf("expected session TTL > 0, got %d", ttl)
	}

	// AC-005：错误密码与不存在用户返回相同 code/message/status（防枚举）。
	wrong := doRequest(t, base, "POST", "/login", map[string]any{"username": "alice", "password": "wrongpass123"}, nil)
	ghost := doRequest(t, base, "POST", "/login", map[string]any{"username": "ghost", "password": "password123"}, nil)
	if wrong.Status != 401 || wrong.Code != 2002 {
		t.Fatalf("login wrong password: status=%d code=%d", wrong.Status, wrong.Code)
	}
	if ghost.Status != wrong.Status || ghost.Code != wrong.Code || ghost.Message != wrong.Message {
		t.Fatalf("anti-enumeration mismatch: wrong=%+v ghost=%+v", wrong, ghost)
	}

	// AC-006：有效 token 访问 /me 返回 id 与 username，id 与 sub 一致。
	me := doRequest(t, base, "GET", "/me", nil, map[string]string{"Authorization": "Bearer " + accessToken})
	if me.Status != 200 || me.Code != 0 {
		t.Fatalf("me with valid token: status=%d code=%d", me.Status, me.Code)
	}
	if int64(me.Data["id"].(float64)) != aliceID || me.Data["username"] != "alice" {
		t.Fatalf("me unexpected data: %v", me.Data)
	}

	// AC-007：缺失/格式错误/签名无效/过期/错误 issuer 的 token 均 401 且无用户数据。
	now := time.Now()
	badCases := []struct {
		name    string
		headers map[string]string
	}{
		{"missing token", nil},
		{"bad format", map[string]string{"Authorization": "Basic abc"}},
		{"bad signature", map[string]string{"Authorization": "Bearer " + signToken(t, "wrong-secret-wrong-secret-wrong-secret!!", fmt.Sprintf("%d", aliceID), auth.Issuer, now, now.Add(time.Hour))}},
		{"expired token", map[string]string{"Authorization": "Bearer " + signToken(t, testJWTSecret, fmt.Sprintf("%d", aliceID), auth.Issuer, now.Add(-2*time.Hour), now.Add(-time.Hour))}},
		{"wrong issuer", map[string]string{"Authorization": "Bearer " + signToken(t, testJWTSecret, fmt.Sprintf("%d", aliceID), "evil", now, now.Add(time.Hour))}},
	}
	for _, c := range badCases {
		res := doRequest(t, base, "GET", "/me", nil, c.headers)
		if res.Status != 401 || res.Code != 1002 {
			t.Fatalf("me %s: status=%d code=%d", c.name, res.Status, res.Code)
		}
		if res.Data != nil {
			t.Fatalf("me %s: expected no user data, got %v", c.name, res.Data)
		}
	}
}

// TestConcurrentRegisterSameUsername 验证并发注册同名用户时仅一个成功（INV-001）。
func TestConcurrentRegisterSameUsername(t *testing.T) {
	base := setupIAMServer(t)

	const (
		n        = 10
		username = "raceuser"
		password = "password123"
	)
	results := make(chan result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := request(base, "POST", "/register", map[string]any{"username": username, "password": password}, nil)
			if err != nil {
				res = result{Code: -1}
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)

	var success, conflict int
	for res := range results {
		switch res.Code {
		case 0:
			success++
		case 2001:
			conflict++
		default:
			t.Fatalf("unexpected result code %d (status=%d)", res.Code, res.Status)
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly 1 success, got %d", success)
	}
	if conflict != n-1 {
		t.Fatalf("expected %d conflicts, got %d", n-1, conflict)
	}
	if count := userCount(t, username); count != 1 {
		t.Fatalf("expected 1 row for %q, got %d", username, count)
	}
}

// registerAndLogin 注册并登录用户，返回 (access_token, sid)。
func registerAndLogin(t *testing.T, base, username, password string) (string, string) {
	t.Helper()
	reg := doRequest(t, base, "POST", "/register", map[string]any{"username": username, "password": password}, nil)
	if reg.Status != 200 || reg.Code != 0 {
		t.Fatalf("register %s: status=%d code=%d", username, reg.Status, reg.Code)
	}
	login := doRequest(t, base, "POST", "/login", map[string]any{"username": username, "password": password}, nil)
	if login.Status != 200 || login.Code != 0 {
		t.Fatalf("login %s: status=%d code=%d", username, login.Status, login.Code)
	}
	token, _ := login.Data["access_token"].(string)
	if token == "" {
		t.Fatalf("login %s: empty access_token", username)
	}
	return token, tokenSid(t, token)
}

// loginOnly 仅登录（用户须已注册），返回 (access_token, sid)。
func loginOnly(t *testing.T, base, username, password string) (string, string) {
	t.Helper()
	login := doRequest(t, base, "POST", "/login", map[string]any{"username": username, "password": password}, nil)
	if login.Status != 200 || login.Code != 0 {
		t.Fatalf("login %s: status=%d code=%d", username, login.Status, login.Code)
	}
	token, _ := login.Data["access_token"].(string)
	if token == "" {
		t.Fatalf("login %s: empty access_token", username)
	}
	return token, tokenSid(t, token)
}

// authHeader 构造 Bearer 认证头。
func authHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// listSessions 请求会话列表并返回 items（[]map[string]any）。
func listSessions(t *testing.T, base, token string) []map[string]any {
	t.Helper()
	res := doRequest(t, base, "GET", "/sessions", nil, authHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("list sessions: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	raw, ok := res.Data["items"].([]any)
	if !ok {
		t.Fatalf("list sessions: unexpected items type %T", res.Data["items"])
	}
	items := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		items = append(items, it.(map[string]any))
	}
	return items
}

// TestLogoutRevokesSessionThenMe401 覆盖 AC-003/INV-003：登出后 session 标记 revoked，token 立即失效且 key 保留。
func TestLogoutRevokesSessionThenMe401(t *testing.T) {
	base := setupIAMServer(t)
	token, sid := registerAndLogin(t, base, "alice", "password123")

	lo := doRequest(t, base, "POST", "/logout", nil, map[string]string{"Authorization": "Bearer " + token})
	if lo.Status != 200 || lo.Code != 0 {
		t.Fatalf("logout: status=%d code=%d", lo.Status, lo.Code)
	}
	if lo.Data != nil {
		t.Fatalf("logout should return data=null, got %v", lo.Data)
	}

	// 撤销为逻辑标记：key 仍在、revoked=1、TTL 未清零。
	sf := sessionFields(t, sid)
	if sf["revoked"] != "1" {
		t.Fatalf("expected revoked=1, got %q", sf["revoked"])
	}
	if ttl := sessionTTL(t, sid); ttl <= 0 {
		t.Fatalf("expected session key retained with TTL>0 after logout, got %d", ttl)
	}

	me := doRequest(t, base, "GET", "/me", nil, map[string]string{"Authorization": "Bearer " + token})
	if me.Status != 401 || me.Code != 1002 {
		t.Fatalf("me after logout: status=%d code=%d", me.Status, me.Code)
	}
	if me.Data != nil {
		t.Fatalf("me after logout: expected no user data, got %v", me.Data)
	}
}

// TestLogoutIdempotent 覆盖 AC-006：重复登出同一 token 幂等，均返回 200/0。
func TestLogoutIdempotent(t *testing.T) {
	base := setupIAMServer(t)
	token, _ := registerAndLogin(t, base, "alice", "password123")

	for i := 0; i < 2; i++ {
		lo := doRequest(t, base, "POST", "/logout", nil, map[string]string{"Authorization": "Bearer " + token})
		if lo.Status != 200 || lo.Code != 0 {
			t.Fatalf("logout #%d: status=%d code=%d", i+1, lo.Status, lo.Code)
		}
	}

	me := doRequest(t, base, "GET", "/me", nil, map[string]string{"Authorization": "Bearer " + token})
	if me.Status != 401 || me.Code != 1002 {
		t.Fatalf("me after repeated logout: status=%d code=%d", me.Status, me.Code)
	}
}

// TestLogoutRequiresToken 覆盖 AC-006：无 token 访问 /logout 返回 401。
func TestLogoutRequiresToken(t *testing.T) {
	base := setupIAMServer(t)
	lo := doRequest(t, base, "POST", "/logout", nil, nil)
	if lo.Status != 401 || lo.Code != 1002 {
		t.Fatalf("logout without token: status=%d code=%d", lo.Status, lo.Code)
	}
}

// TestLogoutDoesNotAffectOtherSessions 覆盖 AC-007/INV-004：登出仅撤销当前 sid。
func TestLogoutDoesNotAffectOtherSessions(t *testing.T) {
	base := setupIAMServer(t)
	tokenA, _ := registerAndLogin(t, base, "alice", "password123")
	tokenB, _ := registerAndLogin(t, base, "bob", "password123")

	doRequest(t, base, "POST", "/logout", nil, map[string]string{"Authorization": "Bearer " + tokenA})

	meA := doRequest(t, base, "GET", "/me", nil, map[string]string{"Authorization": "Bearer " + tokenA})
	if meA.Status != 401 || meA.Code != 1002 {
		t.Fatalf("me with revoked tokenA: status=%d code=%d", meA.Status, meA.Code)
	}
	meB := doRequest(t, base, "GET", "/me", nil, map[string]string{"Authorization": "Bearer " + tokenB})
	if meB.Status != 200 || meB.Code != 0 {
		t.Fatalf("me with independent tokenB: status=%d code=%d", meB.Status, meB.Code)
	}
	if meB.Data["username"] != "bob" {
		t.Fatalf("me tokenB unexpected data: %v", meB.Data)
	}
}

// TestSessionMissingTokenReturns401 覆盖 AC-005：验签通过但 sid 在 Redis 不存在 → 401。
func TestSessionMissingTokenReturns401(t *testing.T) {
	base := setupIAMServer(t)
	token, sid := registerAndLogin(t, base, "alice", "password123")

	// 删除 session，模拟 TTL 到期/Redis 被清空。
	if _, err := g.Redis().Del(context.Background(), auth.SessionKey(sid)); err != nil {
		t.Fatalf("del session: %v", err)
	}

	me := doRequest(t, base, "GET", "/me", nil, map[string]string{"Authorization": "Bearer " + token})
	if me.Status != 401 || me.Code != 1002 {
		t.Fatalf("me with missing session: status=%d code=%d", me.Status, me.Code)
	}
	if me.Data != nil {
		t.Fatalf("me with missing session: expected no data, got %v", me.Data)
	}
}

// TestTokenWithoutSidReturns401 覆盖 sid 缺失场景：旧无状态 token（无 sid）不再放行。
func TestTokenWithoutSidReturns401(t *testing.T) {
	base := setupIAMServer(t)
	registerAndLogin(t, base, "alice", "password123")

	now := time.Now()
	noSid := signToken(t, testJWTSecret, "1", auth.Issuer, now, now.Add(time.Hour))
	me := doRequest(t, base, "GET", "/me", nil, map[string]string{"Authorization": "Bearer " + noSid})
	if me.Status != 401 || me.Code != 1002 {
		t.Fatalf("me with sid-less token: status=%d code=%d", me.Status, me.Code)
	}
}

// TestUnknownSidTokenReturns401 覆盖「验签通过但 sid 不在 Redis」：用不存在的 sid 构造 token。
func TestUnknownSidTokenReturns401(t *testing.T) {
	base := setupIAMServer(t)
	registerAndLogin(t, base, "alice", "password123")

	token := signTokenWithSid(t, testJWTSecret, "1", "deadbeefdeadbeefdeadbeefdeadbeef")
	me := doRequest(t, base, "GET", "/me", nil, map[string]string{"Authorization": "Bearer " + token})
	if me.Status != 401 || me.Code != 1002 {
		t.Fatalf("me with unknown sid: status=%d code=%d", me.Status, me.Code)
	}
}

// TestLogoutIgnoresClientSid 覆盖 INV-005：登出目标 sid 来自 token，忽略请求体中的 sid。
func TestLogoutIgnoresClientSid(t *testing.T) {
	base := setupIAMServer(t)
	tokenA, sidA := registerAndLogin(t, base, "alice", "password123")
	tokenB, sidB := registerAndLogin(t, base, "bob", "password123")

	// 请求体携带 bob 的 sid，但应被忽略，仍只撤销 alice（tokenA）自己的会话。
	lo := doRequest(t, base, "POST", "/logout", map[string]any{"sid": sidB}, map[string]string{"Authorization": "Bearer " + tokenA})
	if lo.Status != 200 || lo.Code != 0 {
		t.Fatalf("logout with body sid: status=%d code=%d", lo.Status, lo.Code)
	}

	if sf := sessionFields(t, sidA); sf["revoked"] != "1" {
		t.Fatalf("expected sidA revoked=1, got %q", sf["revoked"])
	}
	if sf := sessionFields(t, sidB); sf["revoked"] != "0" {
		t.Fatalf("expected sidB not revoked, got %q", sf["revoked"])
	}
	meB := doRequest(t, base, "GET", "/me", nil, map[string]string{"Authorization": "Bearer " + tokenB})
	if meB.Status != 200 || meB.Code != 0 {
		t.Fatalf("me with tokenB after logoutA: status=%d code=%d", meB.Status, meB.Code)
	}
}

// TestRedisWrongTypeFailClosed 覆盖 INV-006：Redis 查询返回错误时，鉴权 fail-closed 返回 401。
func TestRedisWrongTypeFailClosed(t *testing.T) {
	base := setupIAMServer(t)
	token, sid := registerAndLogin(t, base, "alice", "password123")

	// 将 session key 改成 string 类型，使 HGETALL 返回 WRONGTYPE 错误，模拟 Redis 查询失败。
	if _, err := g.Redis().Do(context.Background(), "SET", auth.SessionKey(sid), "not-a-hash"); err != nil {
		t.Fatalf("set wrong type: %v", err)
	}

	me := doRequest(t, base, "GET", "/me", nil, map[string]string{"Authorization": "Bearer " + token})
	if me.Status != 401 || me.Code != 1002 {
		t.Fatalf("me with redis error: expected fail-closed 401, got status=%d code=%d", me.Status, me.Code)
	}
	if me.Data != nil {
		t.Fatalf("me with redis error: expected no data, got %v", me.Data)
	}
}

// TestSessionTTLExpiryReturns401 覆盖 AC-004：TTL 到期后 key 自然消失，访问受保护接口仍返回 401。
func TestSessionTTLExpiryReturns401(t *testing.T) {
	t.Setenv("AUTH_SESSION_TTL", "1")
	base := setupIAMServer(t)
	token, sid := registerAndLogin(t, base, "alice", "password123")

	// 等待 TTL 到期，key 自然消失。
	time.Sleep(2 * time.Second)
	if n, err := g.Redis().Exists(context.Background(), auth.SessionKey(sid)); err != nil || n != 0 {
		t.Fatalf("expected session key expired (exists=0), got n=%d err=%v", n, err)
	}

	me := doRequest(t, base, "GET", "/me", nil, map[string]string{"Authorization": "Bearer " + token})
	if me.Status != 401 || me.Code != 1002 {
		t.Fatalf("me after TTL expiry: status=%d code=%d", me.Status, me.Code)
	}
}

// TestConcurrentLogoutSameToken 覆盖并发撤销原子性：同一 token 并发登出，全部幂等成功且最终 revoked=1。
func TestConcurrentLogoutSameToken(t *testing.T) {
	base := setupIAMServer(t)
	token, sid := registerAndLogin(t, base, "alice", "password123")

	const n = 10
	results := make(chan result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := request(base, "POST", "/logout", nil, map[string]string{"Authorization": "Bearer " + token})
			if err != nil {
				res = result{Code: -1}
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)

	for res := range results {
		if res.Status != 200 || res.Code != 0 {
			t.Fatalf("concurrent logout: status=%d code=%d", res.Status, res.Code)
		}
	}
	if sf := sessionFields(t, sid); sf["revoked"] != "1" {
		t.Fatalf("expected revoked=1 after concurrent logout, got %q", sf["revoked"])
	}
}

// TestListSessionsMultiDevice 覆盖 AC-001/AC-002/INV-001：
// 同一用户多次登录产生多个可区分会话，列表返回全部且 current 唯一正确，绝不返回他人会话。
func TestListSessionsMultiDevice(t *testing.T) {
	base := setupIAMServer(t)

	_, sidA1 := registerAndLogin(t, base, "alice", "password123")
	tokenA2, sidA2 := loginOnly(t, base, "alice", "password123")
	tokenB, _ := registerAndLogin(t, base, "bob", "password123")

	if sidA1 == sidA2 {
		t.Fatal("expected distinct sids for multiple logins")
	}

	items := listSessions(t, base, tokenA2)
	if len(items) != 2 {
		t.Fatalf("expected 2 sessions for alice, got %d", len(items))
	}
	var currentCount int
	for _, it := range items {
		sid, _ := it["sid"].(string)
		if sid != sidA1 && sid != sidA2 {
			t.Fatalf("unexpected sid in alice list: %q", sid)
		}
		if loginAt := int64(it["login_at"].(float64)); loginAt <= 0 {
			t.Fatalf("expected login_at > 0, got %v", it["login_at"])
		}
		if cur, _ := it["current"].(bool); cur {
			currentCount++
			if sid != sidA2 {
				t.Fatalf("expected current session to be sidA2, got %q", sid)
			}
		}
	}
	if currentCount != 1 {
		t.Fatalf("expected exactly 1 current session, got %d", currentCount)
	}

	// 换 B 的 token：列表不含 alice 的任何会话。
	bItems := listSessions(t, base, tokenB)
	if len(bItems) != 1 {
		t.Fatalf("expected 1 session for bob, got %d", len(bItems))
	}
	for _, it := range bItems {
		sid, _ := it["sid"].(string)
		if sid == sidA1 || sid == sidA2 {
			t.Fatalf("bob list must not contain alice sessions, got %q", sid)
		}
	}
}

// TestRevokeSessionById 覆盖 AC-003/INV-003：
// 撤销自己名下指定会话后其 token 立即 401，其他会话与其它用户不受影响；撤销为逻辑标记且保留 key/TTL。
func TestRevokeSessionById(t *testing.T) {
	base := setupIAMServer(t)

	tokenA1, sidA1 := registerAndLogin(t, base, "alice", "password123")
	tokenA2, sidA2 := loginOnly(t, base, "alice", "password123")
	tokenB, _ := registerAndLogin(t, base, "bob", "password123")

	// 用 A2 撤销 A1。
	del := doRequest(t, base, "DELETE", "/sessions/"+sidA1, nil, authHeader(tokenA2))
	if del.Status != 200 || del.Code != 0 {
		t.Fatalf("revoke A1: status=%d code=%d", del.Status, del.Code)
	}
	if del.Data != nil {
		t.Fatalf("revoke should return data=null, got %v", del.Data)
	}

	// A1 逻辑撤销：revoked=1、key/TTL 保留。
	sf := sessionFields(t, sidA1)
	if sf["revoked"] != "1" {
		t.Fatalf("expected A1 revoked=1, got %q", sf["revoked"])
	}
	if ttl := sessionTTL(t, sidA1); ttl <= 0 {
		t.Fatalf("expected A1 session key retained with TTL>0, got %d", ttl)
	}

	// A1 token → 401；A2 token → 200；B token → 200（不受影响）。
	meA1 := doRequest(t, base, "GET", "/me", nil, authHeader(tokenA1))
	if meA1.Status != 401 || meA1.Code != 1002 {
		t.Fatalf("me with revoked A1: status=%d code=%d", meA1.Status, meA1.Code)
	}
	meA2 := doRequest(t, base, "GET", "/me", nil, authHeader(tokenA2))
	if meA2.Status != 200 || meA2.Code != 0 {
		t.Fatalf("me with A2 after revoke A1: status=%d code=%d", meA2.Status, meA2.Code)
	}
	meB := doRequest(t, base, "GET", "/me", nil, authHeader(tokenB))
	if meB.Status != 200 || meB.Code != 0 {
		t.Fatalf("me with B after revoke A1: status=%d code=%d", meB.Status, meB.Code)
	}

	// 列表应只剩 A2。
	items := listSessions(t, base, tokenA2)
	if len(items) != 1 {
		t.Fatalf("expected 1 session after revoke A1, got %d", len(items))
	}
	if sid, _ := items[0]["sid"].(string); sid != sidA2 {
		t.Fatalf("expected remaining session to be sidA2, got %q", sid)
	}
}

// TestRevokeOthers 覆盖 AC-004：撤销其他会话后其它 token 401，当前会话保持有效。
func TestRevokeOthers(t *testing.T) {
	base := setupIAMServer(t)

	tokenA1, sidA1 := registerAndLogin(t, base, "alice", "password123")
	tokenA2, sidA2 := loginOnly(t, base, "alice", "password123")
	tokenA3, sidA3 := loginOnly(t, base, "alice", "password123")

	res := doRequest(t, base, "POST", "/sessions/revoke-others", nil, authHeader(tokenA1))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("revoke-others: status=%d code=%d", res.Status, res.Code)
	}

	meA1 := doRequest(t, base, "GET", "/me", nil, authHeader(tokenA1))
	if meA1.Status != 200 || meA1.Code != 0 {
		t.Fatalf("me with A1 after revoke-others: status=%d code=%d", meA1.Status, meA1.Code)
	}
	for _, tk := range []string{tokenA2, tokenA3} {
		me := doRequest(t, base, "GET", "/me", nil, authHeader(tk))
		if me.Status != 401 || me.Code != 1002 {
			t.Fatalf("me with revoked token after revoke-others: status=%d code=%d", me.Status, me.Code)
		}
	}
	if sf := sessionFields(t, sidA1); sf["revoked"] != "0" {
		t.Fatalf("expected A1 not revoked, got %q", sf["revoked"])
	}
	for _, sid := range []string{sidA2, sidA3} {
		if sf := sessionFields(t, sid); sf["revoked"] != "1" {
			t.Fatalf("expected %s revoked=1, got %q", sid, sf["revoked"])
		}
	}
}

// TestRevokeAll 覆盖 AC-005：全部退出后所有旧 token（含当前）均 401。
func TestRevokeAll(t *testing.T) {
	base := setupIAMServer(t)

	tokenA1, sidA1 := registerAndLogin(t, base, "alice", "password123")
	tokenA2, sidA2 := loginOnly(t, base, "alice", "password123")

	res := doRequest(t, base, "POST", "/sessions/revoke-all", nil, authHeader(tokenA1))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("revoke-all: status=%d code=%d", res.Status, res.Code)
	}

	for _, tk := range []string{tokenA1, tokenA2} {
		me := doRequest(t, base, "GET", "/me", nil, authHeader(tk))
		if me.Status != 401 || me.Code != 1002 {
			t.Fatalf("me after revoke-all: status=%d code=%d", me.Status, me.Code)
		}
	}
	for _, sid := range []string{sidA1, sidA2} {
		if sf := sessionFields(t, sid); sf["revoked"] != "1" {
			t.Fatalf("expected %s revoked=1, got %q", sid, sf["revoked"])
		}
	}
}

// TestRevokeNonOwnedSession 覆盖 AC-006/INV-002：
// 撤销非本人/不存在的会话统一 404/2011，且不产生任何撤销、不泄露存在性与归属。
func TestRevokeNonOwnedSession(t *testing.T) {
	base := setupIAMServer(t)

	tokenA, _ := registerAndLogin(t, base, "alice", "password123")
	tokenB, sidB := registerAndLogin(t, base, "bob", "password123")

	// 用户 A 撤销 B 的会话 → 404/2011，B 无变化。
	res := doRequest(t, base, "DELETE", "/sessions/"+sidB, nil, authHeader(tokenA))
	if res.Status != 404 || res.Code != 2011 {
		t.Fatalf("revoke non-owned: status=%d code=%d", res.Status, res.Code)
	}
	if sf := sessionFields(t, sidB); sf["revoked"] != "0" {
		t.Fatalf("non-owned revoke must not modify B session, got %q", sf["revoked"])
	}
	meB := doRequest(t, base, "GET", "/me", nil, authHeader(tokenB))
	if meB.Status != 200 || meB.Code != 0 {
		t.Fatalf("me with B after non-owned revoke attempt: status=%d code=%d", meB.Status, meB.Code)
	}

	// 不存在的会话 → 404/2011。
	res = doRequest(t, base, "DELETE", "/sessions/deadbeefdeadbeefdeadbeefdeadbeef", nil, authHeader(tokenA))
	if res.Status != 404 || res.Code != 2011 {
		t.Fatalf("revoke non-existent: status=%d code=%d", res.Status, res.Code)
	}
}

// TestRevokeSessionIdempotent 覆盖 AC-007/INV-003：重复撤销本人会话幂等 200。
func TestRevokeSessionIdempotent(t *testing.T) {
	base := setupIAMServer(t)

	_, sidA1 := registerAndLogin(t, base, "alice", "password123")
	tokenA2, _ := loginOnly(t, base, "alice", "password123")

	for i := 0; i < 2; i++ {
		res := doRequest(t, base, "DELETE", "/sessions/"+sidA1, nil, authHeader(tokenA2))
		if res.Status != 200 || res.Code != 0 {
			t.Fatalf("revoke A1 #%d: status=%d code=%d", i+1, res.Status, res.Code)
		}
	}
}

// TestListSessionsFiltersStaleMember 覆盖 INV-006：
// 索引中残留的已过期（Hash 不存在）会话不得作为有效会话返回。
func TestListSessionsFiltersStaleMember(t *testing.T) {
	base := setupIAMServer(t)

	_, sidA1 := registerAndLogin(t, base, "alice", "password123")
	tokenA2, sidA2 := loginOnly(t, base, "alice", "password123")

	// 删除 A1 的 session Hash（模拟 TTL 到期），保留索引中的残留成员。
	if _, err := g.Redis().Del(context.Background(), auth.SessionKey(sidA1)); err != nil {
		t.Fatalf("del session A1: %v", err)
	}

	// 用 A2 查看列表：只返回 A2，不含已过期的 A1。
	items := listSessions(t, base, tokenA2)
	if len(items) != 1 {
		t.Fatalf("expected 1 session after A1 expiry, got %d", len(items))
	}
	if sid, _ := items[0]["sid"].(string); sid != sidA2 {
		t.Fatalf("expected remaining session to be sidA2, got %q", sid)
	}
}

// TestListSessionsRedisErrorReturns500 覆盖 INV-005/AC-008：
// 会话列表枚举（索引读取）失败时不返回虚假空列表，返回 500/1000（不 fail-open）。
func TestListSessionsRedisErrorReturns500(t *testing.T) {
	base := setupIAMServer(t)
	token, _ := registerAndLogin(t, base, "alice", "password123")

	// 将用户会话索引 key 改成 string 类型，使 ZRANGE 返回 WRONGTYPE，模拟 Redis 查询失败。
	if _, err := g.Redis().Do(context.Background(), "SET", auth.SessionIndexKey(tokenUserID(t, token)), "not-a-zset"); err != nil {
		t.Fatalf("corrupt index key: %v", err)
	}

	res := doRequest(t, base, "GET", "/sessions", nil, authHeader(token))
	if res.Status != 500 || res.Code != 1000 {
		t.Fatalf("list with redis error: expected 500/1000, got status=%d code=%d", res.Status, res.Code)
	}
	if res.Data != nil {
		t.Fatalf("list with redis error: expected data=null, got %v", res.Data)
	}
}
