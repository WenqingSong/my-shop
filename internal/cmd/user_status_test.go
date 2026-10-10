package cmd

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定 IAM V5「用户账号状态管理」的行为：禁用/启用普通用户、禁用即时失效（含已签发 JWT）、
// 重新启用不复活旧凭证、权限与审计、幂等与不存在场景。

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"github.com/gogf/gf/v2/frame/g"
)

// userStatusPut 调用 PUT /admin/users/:id/status（禁用/启用用户），返回统一响应。
func userStatusPut(t *testing.T, base string, userID int64, body any, headers map[string]string) isoResult {
	t.Helper()
	c := g.Client()
	if len(headers) > 0 {
		c = c.Header(headers)
	}
	r, err := c.ContentJson().Put(context.Background(), base+"/admin/users/"+strconv.FormatInt(userID, 10)+"/status", body)
	if err != nil {
		t.Fatalf("PUT user status: %v", err)
	}
	defer r.Close()
	return isoDecode(r)
}

// isoFrontendLoginFull 前台登录并返回 (access, refresh, sid)。
func isoFrontendLoginFull(t *testing.T, base, username, password string) (string, string, string) {
	t.Helper()
	res := isoDo(t, base, "POST", "/login", map[string]any{"username": username, "password": password}, nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("frontend login %s: status=%d code=%d msg=%q", username, res.Status, res.Code, res.Message)
	}
	access, _ := res.Data["access_token"].(string)
	refresh, _ := res.Data["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("frontend login %s: empty tokens", username)
	}
	return access, refresh, isoClaims(t, access).Sid
}

// grantUserPermission 直接为管理员授予指定权限（seed 已幂等写入 permission code）。
func grantUserPermission(t *testing.T, adminID int64, code string) {
	t.Helper()
	ctx := context.Background()
	pid, err := g.DB().Model("permissions").Ctx(ctx).Where("code", code).Value("id")
	if err != nil || pid == nil || pid.IsEmpty() {
		t.Fatalf("query permission %s: err=%v", code, err)
	}
	rid, err := g.DB().Model("roles").Ctx(ctx).Data(g.Map{
		"name": "role-" + code + "-" + strconv.FormatInt(adminID, 10),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert role for %s: %v", code, err)
	}
	if _, err := g.DB().Model("role_permissions").Ctx(ctx).Data(g.Map{"role_id": rid, "permission_id": pid.Int64()}).Insert(); err != nil {
		t.Fatalf("insert role_permission for %s: %v", code, err)
	}
	if _, err := g.DB().Model("admin_roles").Ctx(ctx).Data(g.Map{"admin_id": adminID, "role_id": rid}).Insert(); err != nil {
		t.Fatalf("insert admin_role for %s: %v", code, err)
	}
}

// userStatusAndEpoch 读取用户 status 与 auth_epoch。
func userStatusAndEpoch(t *testing.T, userID int64) (int, int64) {
	t.Helper()
	rec, err := g.DB().Model("users").Ctx(context.Background()).
		Fields("status", "auth_epoch").Where("id", userID).One()
	if err != nil || rec == nil || rec.IsEmpty() {
		t.Fatalf("query user %d status: err=%v", userID, err)
	}
	return rec["status"].Int(), rec["auth_epoch"].Int64()
}

// userAuditCount 统计指定目标用户、指定动作的审计行数。
func userAuditCount(t *testing.T, targetUserID int64, action string) int {
	t.Helper()
	m := g.DB().Model("user_status_audits").Ctx(context.Background()).Where("target_user_id", targetUserID)
	if action != "" {
		m = m.Where("action", action)
	}
	n, err := m.Count()
	if err != nil {
		t.Fatalf("count audit for user %d: %v", targetUserID, err)
	}
	return n
}

// TestUserStatusDisableRevokesAccessRefreshAndReEnable 覆盖 AC-002/003/004/005/006/007 与 INV-001/INV-003/INV-005：
// 禁用后登录/刷新/旧 access 均拒绝；多设备全部失效；重新启用后重新登录成功但旧凭证不复活；版本仅禁用递增。
func TestUserStatusDisableRevokesAccessRefreshAndReEnable(t *testing.T) {
	base := setupIsolationServer(t)

	aliceID := isoInsertUser(t, "alice", "password123")
	access1, refresh1, _ := isoFrontendLoginFull(t, base, "alice", "password123")
	access2, _, _ := isoFrontendLoginFull(t, base, "alice", "password123")

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// 禁用前旧 token 可用。
	if me := isoDo(t, base, "GET", "/me", nil, isoAuthHeader(access1)); me.Status != 200 || me.Code != 0 {
		t.Fatalf("me before disable: status=%d code=%d", me.Status, me.Code)
	}

	// 禁用 alice。
	if res := userStatusPut(t, base, aliceID, map[string]any{"status": 0, "reason": "违规封禁"}, isoAuthHeader(adminToken)); res.Status != 200 || res.Code != 0 {
		t.Fatalf("disable alice: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}

	// AC-004/005：禁用后旧 access token（含多设备）均 401。
	for i, tok := range []string{access1, access2} {
		if me := isoDo(t, base, "GET", "/me", nil, isoAuthHeader(tok)); me.Status != 401 || me.Code != 1002 {
			t.Fatalf("me with old access #%d after disable: status=%d code=%d", i+1, me.Status, me.Code)
		}
	}

	// AC-002：禁用后登录被拒（401，无 token、无会话）。
	login := isoDo(t, base, "POST", "/login", map[string]any{"username": "alice", "password": "password123"}, nil)
	if login.Status != 401 || login.Code != 1002 {
		t.Fatalf("login disabled alice: status=%d code=%d", login.Status, login.Code)
	}
	if login.Data != nil {
		t.Fatalf("login disabled alice must not produce tokens, got %v", login.Data)
	}

	// AC-003：禁用后 refresh 被拒（401，无新 token）。
	// 说明：禁用事务已撤销该用户全部 refresh family（revoked_reason='revoked'），
	// 故旧 refresh 在 respondRefreshRow 先命中「已撤销」→ 2012（而非状态/版本校验的 1002）；
	// 1002 出现在「登录与禁用并发」中新根未被 family 撤销命中的场景。两者均 401 且不签发新 token。
	refresh := isoDo(t, base, "POST", "/refresh", map[string]any{"refresh_token": refresh1}, nil)
	if refresh.Status != 401 || refresh.Code != 2012 {
		t.Fatalf("refresh disabled alice: status=%d code=%d", refresh.Status, refresh.Code)
	}
	if refresh.Data != nil {
		t.Fatalf("refresh disabled alice must not produce tokens, got %v", refresh.Data)
	}

	// AC-013/INV-005：状态落库 + 仅禁用递增版本 + 审计仅一条 disable。
	if st, epoch := userStatusAndEpoch(t, aliceID); st != 0 || epoch != 1 {
		t.Fatalf("expected status=0 auth_epoch=1, got status=%d epoch=%d", st, epoch)
	}
	if n := userAuditCount(t, aliceID, "disable"); n != 1 {
		t.Fatalf("expected 1 disable audit, got %d", n)
	}

	// AC-010：重复禁用幂等（成功，不重复递增版本、不重复写审计）。
	if res := userStatusPut(t, base, aliceID, map[string]any{"status": 0, "reason": "重复禁用"}, isoAuthHeader(adminToken)); res.Status != 200 || res.Code != 0 {
		t.Fatalf("re-disable alice: status=%d code=%d", res.Status, res.Code)
	}
	if _, epoch := userStatusAndEpoch(t, aliceID); epoch != 1 {
		t.Fatalf("expected auth_epoch unchanged (1) after idempotent disable, got %d", epoch)
	}
	if n := userAuditCount(t, aliceID, "disable"); n != 1 {
		t.Fatalf("expected still 1 disable audit after idempotent disable, got %d", n)
	}

	// AC-006：重新启用后重新登录成功并可访问。
	if res := userStatusPut(t, base, aliceID, map[string]any{"status": 1, "reason": "申诉通过"}, isoAuthHeader(adminToken)); res.Status != 200 || res.Code != 0 {
		t.Fatalf("re-enable alice: status=%d code=%d", res.Status, res.Code)
	}
	access3, _, _ := isoFrontendLoginFull(t, base, "alice", "password123")
	if me := isoDo(t, base, "GET", "/me", nil, isoAuthHeader(access3)); me.Status != 200 || me.Code != 0 {
		t.Fatalf("me with new access after re-enable: status=%d code=%d", me.Status, me.Code)
	}

	// AC-007/INV-003：启用不递减版本，旧凭证（旧 access/旧 refresh）仍 401，不复活。
	if st, epoch := userStatusAndEpoch(t, aliceID); st != 1 || epoch != 1 {
		t.Fatalf("expected status=1 auth_epoch=1 after re-enable, got status=%d epoch=%d", st, epoch)
	}
	for i, tok := range []string{access1, access2} {
		if me := isoDo(t, base, "GET", "/me", nil, isoAuthHeader(tok)); me.Status != 401 || me.Code != 1002 {
			t.Fatalf("old access #%d after re-enable should still be 401, got status=%d code=%d", i+1, me.Status, me.Code)
		}
	}
	refreshOld := isoDo(t, base, "POST", "/refresh", map[string]any{"refresh_token": refresh1}, nil)
	if refreshOld.Status != 401 {
		t.Fatalf("old refresh after re-enable should still be 401, got status=%d code=%d", refreshOld.Status, refreshOld.Code)
	}
}

// TestUserStatusAuthorizationAndNotFound 覆盖 AC-008/009/011/012 与 INV-002/INV-004：
// 前台用户 token、无权限管理员、仅读权限管理员均 403 且无写入；持 user:status 与超级管理员可操作；
// 目标不存在 404 无写入；目标 id 来自 URL 路径。
func TestUserStatusAuthorizationAndNotFound(t *testing.T) {
	base := setupIsolationServer(t)

	aliceID := isoInsertUser(t, "alice", "password123")
	aliceToken, _ := isoFrontendLogin(t, base, "alice", "password123")

	// 无权限普通管理员 bob。
	bobID := isoInsertAdmin(t, "bob", "bobpass123")
	bobToken, _ := isoAdminLogin(t, base, "bob", "bobpass123")

	// 仅读权限管理员 carol。
	carolID := isoInsertAdmin(t, "carol", "carolpass123")
	grantUserPermission(t, carolID, "user:read")
	carolToken, _ := isoAdminLogin(t, base, "carol", "carolpass123")

	// 持有 user:status 的管理员 dave。
	daveID := isoInsertAdmin(t, "dave", "davepass123")
	grantUserPermission(t, daveID, "user:status")
	daveToken, _ := isoAdminLogin(t, base, "dave", "davepass123")

	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// AC-008：普通用户 token 调用状态管理接口 → 403，且无写入。
	if res := userStatusPut(t, base, aliceID, map[string]any{"status": 0, "reason": "x"}, isoAuthHeader(aliceToken)); res.Status != 403 || res.Code != 1003 {
		t.Fatalf("user token on status API: status=%d code=%d", res.Status, res.Code)
	}

	// AC-009：无权限管理员 → 403 且无写入。
	if res := userStatusPut(t, base, aliceID, map[string]any{"status": 0, "reason": "x"}, isoAuthHeader(bobToken)); res.Status != 403 || res.Code != 1003 {
		t.Fatalf("no-perm admin on status API: status=%d code=%d", res.Status, res.Code)
	}

	// 仅 user:read 的管理员不能 PUT（需 user:status）→ 403 且无写入。
	if res := userStatusPut(t, base, aliceID, map[string]any{"status": 0, "reason": "x"}, isoAuthHeader(carolToken)); res.Status != 403 || res.Code != 1003 {
		t.Fatalf("read-only admin on PUT status: status=%d code=%d", res.Status, res.Code)
	}

	// 拒绝路径无写入：alice 仍启用、版本 0、无审计。
	if st, epoch := userStatusAndEpoch(t, aliceID); st != 1 || epoch != 0 {
		t.Fatalf("rejected paths must not write: status=%d epoch=%d", st, epoch)
	}
	if n := userAuditCount(t, aliceID, ""); n != 0 {
		t.Fatalf("rejected paths must not write audit, got %d", n)
	}

	// 仅 user:read 的管理员可以 GET（查询）。
	get := isoDo(t, base, "GET", "/admin/users/"+strconv.FormatInt(aliceID, 10)+"/status", nil, isoAuthHeader(carolToken))
	if get.Status != 200 || get.Code != 0 {
		t.Fatalf("read-only admin GET status: status=%d code=%d", get.Status, get.Code)
	}

	// 持 user:status 的管理员可禁用。
	if res := userStatusPut(t, base, aliceID, map[string]any{"status": 0, "reason": "dave 禁用"}, isoAuthHeader(daveToken)); res.Status != 200 || res.Code != 0 {
		t.Fatalf("user:status admin disable: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	// 超级管理员可启用。
	if res := userStatusPut(t, base, aliceID, map[string]any{"status": 1, "reason": "超管恢复"}, isoAuthHeader(adminToken)); res.Status != 200 || res.Code != 0 {
		t.Fatalf("super admin enable: status=%d code=%d", res.Status, res.Code)
	}

	// AC-011：目标不存在 → 404 且无写入。
	if res := userStatusPut(t, base, 999999, map[string]any{"status": 0, "reason": "x"}, isoAuthHeader(adminToken)); res.Status != 404 || res.Code != 2015 {
		t.Fatalf("disable non-existent: status=%d code=%d", res.Status, res.Code)
	}
	_ = bobID // bob 仅用于构造无权限管理员，无需再使用。
}

// TestUserStatusConcurrentDisableIdempotent 覆盖 INV-005 的并发面：多个并发禁用同一用户，
// 由事务内 SELECT ... FOR UPDATE 串行化——仅一次实际迁移（auth_epoch 递增一次、仅一条审计），其余幂等。
func TestUserStatusConcurrentDisableIdempotent(t *testing.T) {
	base := setupIsolationServer(t)

	aliceID := isoInsertUser(t, "alice", "password123")
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	const n = 8
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := g.Client().Header(isoAuthHeader(adminToken)).ContentJson().
				Put(context.Background(), base+"/admin/users/"+strconv.FormatInt(aliceID, 10)+"/status",
					map[string]any{"status": 0, "reason": "并发禁用"})
			if err != nil {
				statuses[i] = -1
				return
			}
			defer r.Close()
			statuses[i] = r.StatusCode
		}(i)
	}
	wg.Wait()

	for i, code := range statuses {
		if code != 200 {
			t.Fatalf("concurrent disable #%d: status=%d want 200", i, code)
		}
	}
	if st, epoch := userStatusAndEpoch(t, aliceID); st != 0 || epoch != 1 {
		t.Fatalf("expected status=0 auth_epoch=1 after concurrent disable, got status=%d epoch=%d", st, epoch)
	}
	if n := userAuditCount(t, aliceID, "disable"); n != 1 {
		t.Fatalf("expected exactly 1 disable audit after concurrent disable, got %d", n)
	}
}

// TestUserStatusAuditFields 覆盖 AC-013：实际迁移落审计（含操作者/目标/前后状态/原因/结果），不含敏感信息。
func TestUserStatusAuditFields(t *testing.T) {
	base := setupIsolationServer(t)

	aliceID := isoInsertUser(t, "alice", "password123")
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	if res := userStatusPut(t, base, aliceID, map[string]any{"status": 0, "reason": "测试封禁原因"}, isoAuthHeader(adminToken)); res.Status != 200 || res.Code != 0 {
		t.Fatalf("disable alice: status=%d code=%d", res.Status, res.Code)
	}

	type auditRow struct {
		TargetUserId     int64  `orm:"target_user_id"`
		OperatorAdminId  int64  `orm:"operator_admin_id"`
		OperatorUsername string `orm:"operator_username"`
		Action           string `orm:"action"`
		BeforeStatus     int    `orm:"before_status"`
		AfterStatus      int    `orm:"after_status"`
		Reason           string `orm:"reason"`
		Result           int    `orm:"result"`
	}
	var rows []auditRow
	if err := g.DB().Model("user_status_audits").Ctx(context.Background()).
		Where("target_user_id", aliceID).Scan(&rows); err != nil {
		t.Fatalf("scan audit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(rows))
	}
	row := rows[0]
	if row.TargetUserId != aliceID {
		t.Fatalf("target_user_id=%d want %d", row.TargetUserId, aliceID)
	}
	if row.OperatorAdminId <= 0 || row.OperatorUsername == "" {
		t.Fatalf("operator not recorded: admin_id=%d username=%q", row.OperatorAdminId, row.OperatorUsername)
	}
	if row.Action != "disable" || row.BeforeStatus != 1 || row.AfterStatus != 0 {
		t.Fatalf("audit fields mismatch: action=%q before=%d after=%d", row.Action, row.BeforeStatus, row.AfterStatus)
	}
	if row.Reason != "测试封禁原因" || row.Result != 1 {
		t.Fatalf("audit reason/result mismatch: reason=%q result=%d", row.Reason, row.Result)
	}
}
