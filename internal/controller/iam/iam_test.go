package iam_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// setupIAMServer 配置数据库、幂等建表并启动带中间件与路由的测试服务器，返回 base URL。
func setupIAMServer(t *testing.T) string {
	t.Helper()
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	// Bootstrap 会执行超级管理员 seed：提供测试密码，避免「未配置密码」触发启动 fail-fast。
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)

	ctx := context.Background()
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
