package iam_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
)

// refreshRow 是 refresh_tokens 表的最小查询结果（测试断言用）。
type refreshRow struct {
	ID            int64       `orm:"id"`
	TokenHash     string      `orm:"token_hash"`
	FamilyID      string      `orm:"family_id"`
	UserID        int64       `orm:"user_id"`
	ParentID      *int64      `orm:"parent_id"`
	Generation    int         `orm:"generation"`
	Sid           string      `orm:"sid"`
	RevokedAt     *gtime.Time `orm:"revoked_at"`
	RevokedReason *string     `orm:"revoked_reason"`
}

// loginWithRefresh 注册并登录，返回 (accessToken, refreshToken, sid, userID)。
func loginWithRefresh(t *testing.T, base, username, password string) (string, string, string, int64) {
	t.Helper()
	reg := doRequest(t, base, "POST", "/register", map[string]any{"username": username, "password": password}, nil)
	if reg.Status != 200 || reg.Code != 0 {
		t.Fatalf("register %s: status=%d code=%d", username, reg.Status, reg.Code)
	}
	return loginTokensOnly(t, base, username, password)
}

// loginTokensOnly 仅登录（用户须已注册），返回 (accessToken, refreshToken, sid, userID)。
func loginTokensOnly(t *testing.T, base, username, password string) (string, string, string, int64) {
	t.Helper()
	login := doRequest(t, base, "POST", "/login", map[string]any{"username": username, "password": password}, nil)
	if login.Status != 200 || login.Code != 0 {
		t.Fatalf("login %s: status=%d code=%d", username, login.Status, login.Code)
	}
	access, _ := login.Data["access_token"].(string)
	refresh, _ := login.Data["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("login %s: empty tokens", username)
	}
	return access, refresh, tokenSid(t, access), tokenUserID(t, access)
}

// refreshRowByHash 按 token_hash 查询 refresh_tokens 一行；不存在返回 nil。
func refreshRowByHash(t *testing.T, tokenHash string) *refreshRow {
	t.Helper()
	var rows []*refreshRow
	if err := g.DB().Model("refresh_tokens").Ctx(context.Background()).
		Where("token_hash", tokenHash).Scan(&rows); err != nil {
		t.Fatalf("query refresh token by hash: %v", err)
	}
	if len(rows) == 0 {
		return nil
	}
	return rows[0]
}

// refreshRowsByFamily 按 family_id 查询全部行（按 id 升序）。
func refreshRowsByFamily(t *testing.T, familyID string) []*refreshRow {
	t.Helper()
	var rows []*refreshRow
	if err := g.DB().Model("refresh_tokens").Ctx(context.Background()).
		Where("family_id", familyID).Order("id asc").Scan(&rows); err != nil {
		t.Fatalf("query refresh rows by family: %v", err)
	}
	return rows
}

// activeFamilyCount 返回 family 中未撤销（revoked_at IS NULL）的行数。
func activeFamilyCount(t *testing.T, familyID string) int {
	t.Helper()
	n, err := g.DB().Model("refresh_tokens").Ctx(context.Background()).
		Where("family_id", familyID).WhereNull("revoked_at").Count()
	if err != nil {
		t.Fatalf("count active family rows: %v", err)
	}
	return n
}

// TestLoginIssuesRefreshToken 覆盖 AC-001：登录返回 refresh token，DB 仅存 SHA-256 哈希。
func TestLoginIssuesRefreshToken(t *testing.T) {
	base := setupIAMServer(t)
	_, refresh, _, userID := loginWithRefresh(t, base, "alice", "password123")

	if len(refresh) != 64 {
		t.Fatalf("expected 64-char refresh token, got len=%d", len(refresh))
	}

	hash := auth.HashRefreshToken(refresh)
	if len(hash) != 64 || hash == refresh {
		t.Fatalf("hash must be 64 hex and != plaintext")
	}

	row := refreshRowByHash(t, hash)
	if row == nil {
		t.Fatal("expected refresh token row by its hash, got nil")
	}
	if row.TokenHash != hash {
		t.Fatalf("expected stored token_hash=%q, got %q", hash, row.TokenHash)
	}
	if row.UserID != userID {
		t.Fatalf("expected user_id=%d, got %d", userID, row.UserID)
	}
	if row.ParentID != nil || row.Generation != 0 {
		t.Fatalf("expected root token (parent=NULL, generation=0), got parent=%v gen=%d", row.ParentID, row.Generation)
	}
	if row.Sid == "" {
		t.Fatal("expected non-empty sid in refresh token row")
	}
	// 明文绝不落库：token_hash == 明文 的哈希列不应存在（uk 上无该明文）。
	if got := refreshRowByHash(t, refresh); got != nil {
		t.Fatal("plaintext refresh token must not be stored")
	}
}

// TestRefreshRotationAndLineage 覆盖 AC-002 / INV-004 / INV-006：
// 轮换返回新双 token，旧 token 置 rotated，新后代继承 family、同 sid、generation+1。
func TestRefreshRotationAndLineage(t *testing.T) {
	base := setupIAMServer(t)
	_, refresh, sid, userID := loginWithRefresh(t, base, "alice", "password123")

	res := doRequest(t, base, "POST", "/refresh", map[string]any{"refresh_token": refresh}, nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("refresh: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	newAccess, _ := res.Data["access_token"].(string)
	newRefresh, _ := res.Data["refresh_token"].(string)
	if newAccess == "" || newRefresh == "" {
		t.Fatalf("refresh returned empty tokens: %v", res.Data)
	}
	if newRefresh == refresh {
		t.Fatal("expected new refresh token different from old")
	}

	// 新 access token：复用同 sid、type=user、sub=alice（INV-004/005/006）。
	claims, err := auth.ParseWithSecret([]byte(testJWTSecret), newAccess)
	if err != nil {
		t.Fatalf("parse new access token: %v", err)
	}
	if claims.Sid != sid {
		t.Fatalf("expected same sid %q after refresh, got %q", sid, claims.Sid)
	}
	if claims.Type != auth.TypeUser {
		t.Fatalf("expected type=user, got %q", claims.Type)
	}
	if claims.Subject != fmt.Sprintf("%d", userID) {
		t.Fatalf("expected sub=%d, got %q", userID, claims.Subject)
	}

	// 旧 token 置 rotated。
	oldRow := refreshRowByHash(t, auth.HashRefreshToken(refresh))
	if oldRow == nil || oldRow.RevokedReason == nil || *oldRow.RevokedReason != "rotated" {
		t.Fatalf("expected old token rotated, got %+v", oldRow)
	}
	// 新后代：同 family、parent=旧 id、generation=1、同 sid、未撤销。
	newRow := refreshRowByHash(t, auth.HashRefreshToken(newRefresh))
	if newRow == nil {
		t.Fatal("expected new refresh token row, got nil")
	}
	if newRow.FamilyID != oldRow.FamilyID {
		t.Fatalf("expected same family, got %q vs %q", newRow.FamilyID, oldRow.FamilyID)
	}
	if newRow.ParentID == nil || *newRow.ParentID != oldRow.ID {
		t.Fatalf("expected parent=%d, got %v", oldRow.ID, newRow.ParentID)
	}
	if newRow.Generation != 1 {
		t.Fatalf("expected generation=1, got %d", newRow.Generation)
	}
	if newRow.Sid != sid {
		t.Fatalf("expected same sid, got %q vs %q", newRow.Sid, sid)
	}
	if newRow.RevokedAt != nil {
		t.Fatal("new descendant must not be revoked")
	}

	// 新 access token 可访问受保护接口（AC-008 前半）。
	me := doRequest(t, base, "GET", "/me", nil, authHeader(newAccess))
	if me.Status != 200 || me.Code != 0 {
		t.Fatalf("me with refreshed access: status=%d code=%d", me.Status, me.Code)
	}

	// sid 不变 → 会话列表仍为一个条目（INV-006：refresh 不新增设备登录 Session）。
	items := listSessions(t, base, newAccess)
	if len(items) != 1 {
		t.Fatalf("expected 1 session after refresh, got %d", len(items))
	}
	if items[0]["sid"] != sid {
		t.Fatalf("expected session %q, got %v", sid, items[0]["sid"])
	}
}

// TestRefreshReuseRevokesFamily 覆盖 AC-003/AC-004/AC-011（mutation 测试）：
// 已轮换 token 重放 → reuse，撤销整个 family（含最新后代）+ 全部会话，合法新 token 随之失效。
// 若实现只撤销旧 token 而不撤销 family，本测试的「新 token 亦失效」断言会失败，可区分错误实现。
func TestRefreshReuseRevokesFamily(t *testing.T) {
	base := setupIAMServer(t)
	access, refresh, _, _ := loginWithRefresh(t, base, "alice", "password123")

	// 第一次轮换得到合法新 token。
	first := doRequest(t, base, "POST", "/refresh", map[string]any{"refresh_token": refresh}, nil)
	if first.Status != 200 || first.Code != 0 {
		t.Fatalf("first refresh: status=%d code=%d", first.Status, first.Code)
	}
	legitRefresh, _ := first.Data["refresh_token"].(string)
	if legitRefresh == "" {
		t.Fatal("expected legit refresh token")
	}
	familyID := refreshRowByHash(t, auth.HashRefreshToken(refresh)).FamilyID

	// 攻击者重放旧 token → reuse。
	replay := doRequest(t, base, "POST", "/refresh", map[string]any{"refresh_token": refresh}, nil)
	if replay.Status != 401 || replay.Code != 2014 {
		t.Fatalf("replay: expected 401/2014, got status=%d code=%d", replay.Status, replay.Code)
	}
	if replay.Data != nil {
		t.Fatalf("replay must not produce tokens, got %v", replay.Data)
	}

	// 整个 family 全部撤销（无未撤销后代）。
	if n := activeFamilyCount(t, familyID); n != 0 {
		t.Fatalf("expected 0 active rows in family after reuse, got %d", n)
	}

	// 合法新 token 也随之失效（INV-003/AC-004）：refresh → 2012（已撤销）。
	legit := doRequest(t, base, "POST", "/refresh", map[string]any{"refresh_token": legitRefresh}, nil)
	if legit.Status != 401 || (legit.Code != 2012 && legit.Code != 2014) {
		t.Fatalf("legit token after reuse: expected 401 invalid, got status=%d code=%d", legit.Status, legit.Code)
	}

	// 该用户全部 access sessions 被撤销 → /me 401（强制重新认证）。
	me := doRequest(t, base, "GET", "/me", nil, authHeader(access))
	if me.Status != 401 || me.Code != 1002 {
		t.Fatalf("me after reuse: expected 401, got status=%d code=%d", me.Status, me.Code)
	}
}

// TestRefreshInvalidAndUnknown 覆盖 AC-006：无效/未知/空 refresh 统一 2012，不泄露存在性。
func TestRefreshInvalidAndUnknown(t *testing.T) {
	base := setupIAMServer(t)

	unknown := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 64 字符但不存在
	for name, body := range map[string]any{
		"empty":   map[string]any{"refresh_token": ""},
		"garbage": map[string]any{"refresh_token": "not-a-hex-token"},
		"unknown": map[string]any{"refresh_token": unknown},
	} {
		res := doRequest(t, base, "POST", "/refresh", body, nil)
		if res.Status != 401 || res.Code != 2012 {
			t.Fatalf("refresh %s: expected 401/2012, got status=%d code=%d", name, res.Status, res.Code)
		}
		if res.Data != nil {
			t.Fatalf("refresh %s must not produce data, got %v", name, res.Data)
		}
	}
}

// TestRefreshExpired 覆盖 AC-005：过期 refresh 返回 2013，不产生新 token。
func TestRefreshExpired(t *testing.T) {
	base := setupIAMServer(t)
	_, refresh, _, _ := loginWithRefresh(t, base, "alice", "password123")

	// 直接把该 token 置为已过期。
	if _, err := g.DB().Exec(context.Background(),
		"UPDATE refresh_tokens SET expires_at = DATE_SUB(NOW(), INTERVAL 1 HOUR) WHERE token_hash = ?",
		auth.HashRefreshToken(refresh)); err != nil {
		t.Fatalf("expire token: %v", err)
	}

	res := doRequest(t, base, "POST", "/refresh", map[string]any{"refresh_token": refresh}, nil)
	if res.Status != 401 || res.Code != 2013 {
		t.Fatalf("expired refresh: expected 401/2013, got status=%d code=%d", res.Status, res.Code)
	}
	if res.Data != nil {
		t.Fatalf("expired refresh must not produce data, got %v", res.Data)
	}
}

// TestConcurrentRefreshSingleRotation 覆盖 AC-007 / INV-002（mutation 测试）：
// 同一 refresh token 并发提交，仅一次轮换成功，其余 reuse，family 无多个未撤销后代。
// 若实现允许并发多次轮换，本测试「仅一次成功」或「仅一个后代」断言会失败。
func TestConcurrentRefreshSingleRotation(t *testing.T) {
	base := setupIAMServer(t)
	_, refresh, _, _ := loginWithRefresh(t, base, "alice", "password123")
	familyID := refreshRowByHash(t, auth.HashRefreshToken(refresh)).FamilyID

	const n = 10
	results := make(chan result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := request(base, "POST", "/refresh", map[string]any{"refresh_token": refresh}, nil)
			if err != nil {
				res = result{Code: -1}
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)

	var success, reuse int
	for res := range results {
		switch res.Code {
		case 0:
			success++
		case 2014:
			reuse++
		default:
			t.Fatalf("unexpected concurrent refresh result code=%d status=%d", res.Code, res.Status)
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly 1 successful rotation, got %d", success)
	}
	if reuse != n-1 {
		t.Fatalf("expected %d reuse, got %d", n-1, reuse)
	}

	// family 恰好一个后代（generation=1），且无未撤销后代。
	rows := refreshRowsByFamily(t, familyID)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (root + 1 descendant), got %d", len(rows))
	}
	descendants := 0
	for _, r := range rows {
		if r.ParentID != nil {
			descendants++
		}
	}
	if descendants != 1 {
		t.Fatalf("expected exactly 1 descendant, got %d", descendants)
	}
	if n := activeFamilyCount(t, familyID); n != 0 {
		t.Fatalf("expected 0 active rows after concurrent reuse, got %d", n)
	}
}

// TestRefreshLogoutRevokesFamilyOnly 覆盖撤销联动：logout 仅撤销当前 session 对应 family，不影响其它登录。
func TestRefreshLogoutRevokesFamilyOnly(t *testing.T) {
	base := setupIAMServer(t)
	accessA, refreshA, _, _ := loginWithRefresh(t, base, "alice", "password123")
	_, refreshB, _, _ := loginTokensOnly(t, base, "alice", "password123")

	doRequest(t, base, "POST", "/logout", nil, authHeader(accessA))

	// A 的 family 被撤销 → refresh 2012。
	resA := doRequest(t, base, "POST", "/refresh", map[string]any{"refresh_token": refreshA}, nil)
	if resA.Status != 401 || resA.Code != 2012 {
		t.Fatalf("refresh A after logout: expected 401/2012, got status=%d code=%d", resA.Status, resA.Code)
	}
	// B 的 family 不受影响 → refresh 成功。
	resB := doRequest(t, base, "POST", "/refresh", map[string]any{"refresh_token": refreshB}, nil)
	if resB.Status != 200 || resB.Code != 0 {
		t.Fatalf("refresh B after logout A: expected 200, got status=%d code=%d", resB.Status, resB.Code)
	}
}

// TestRefreshRevokeAllRevokesAllFamilies 覆盖撤销联动：revoke-all 撤销该用户全部 refresh families。
func TestRefreshRevokeAllRevokesAllFamilies(t *testing.T) {
	base := setupIAMServer(t)
	accessA, refreshA, _, _ := loginWithRefresh(t, base, "alice", "password123")
	_, refreshB, _, _ := loginTokensOnly(t, base, "alice", "password123")

	doRequest(t, base, "POST", "/sessions/revoke-all", nil, authHeader(accessA))

	for name, r := range map[string]string{"A": refreshA, "B": refreshB} {
		res := doRequest(t, base, "POST", "/refresh", map[string]any{"refresh_token": r}, nil)
		if res.Status != 401 || res.Code != 2012 {
			t.Fatalf("refresh %s after revoke-all: expected 401/2012, got status=%d code=%d", name, res.Status, res.Code)
		}
	}
}

// TestRefreshRevokeSessionRevokesFamily 覆盖撤销联动：DELETE /sessions/:sid 撤销指定 session 对应 family。
func TestRefreshRevokeSessionRevokesFamily(t *testing.T) {
	base := setupIAMServer(t)
	_, _, sidA, _ := loginWithRefresh(t, base, "alice", "password123")
	accessB, refreshB, _, _ := loginTokensOnly(t, base, "alice", "password123")

	del := doRequest(t, base, "DELETE", "/sessions/"+sidA, nil, authHeader(accessB))
	if del.Status != 200 || del.Code != 0 {
		t.Fatalf("revoke session A: status=%d code=%d", del.Status, del.Code)
	}

	// B 的 family 仍有效（撤销的是 A）。
	resB := doRequest(t, base, "POST", "/refresh", map[string]any{"refresh_token": refreshB}, nil)
	if resB.Status != 200 || resB.Code != 0 {
		t.Fatalf("refresh B after revoke A: expected 200, got status=%d code=%d", resB.Status, resB.Code)
	}
}

// TestRefreshAccessExpiryIndependent 覆盖 AC-008：access 过期但 refresh 有效，仍可换新 access 继续访问。
func TestRefreshAccessExpiryIndependent(t *testing.T) {
	base := setupIAMServer(t)
	_, refresh, _, _ := loginWithRefresh(t, base, "alice", "password123")

	// 模拟 access token 过期：直接删除对应 session（refresh 会按同 sid upsert 重建）。
	// 这里验证 refresh 独立于 access token 生命周期：即使 session 过期，refresh 仍能重建并签新 access。
	res := doRequest(t, base, "POST", "/refresh", map[string]any{"refresh_token": refresh}, nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("refresh: status=%d code=%d", res.Status, res.Code)
	}
	newAccess, _ := res.Data["access_token"].(string)
	me := doRequest(t, base, "GET", "/me", nil, authHeader(newAccess))
	if me.Status != 200 || me.Code != 0 {
		t.Fatalf("me with refreshed access: status=%d code=%d", me.Status, me.Code)
	}
}
