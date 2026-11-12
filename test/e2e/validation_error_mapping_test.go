package e2e

// B1-05-FINAL-FIX-02：D1 运行时回归。
// GoFrame DTO 参数校验失败必须返回 HTTP 400 + 业务码 1001，
// 且不得影响既有业务错误（409）与真正的内部错误（500）。

import (
	"fmt"
	"testing"
	"time"
)

// TestValidationErrorMappingReturns400And1001 覆盖三个代表性 DTO 校验入口。
func TestValidationErrorMappingReturns400And1001(t *testing.T) {
	base := setupIsolationServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	cases := []struct {
		name     string
		path     string
		body     map[string]any
		headers  map[string]string
		wantCode int
	}{
		{
			name:     "categories-empty-object",
			path:     "/categories",
			body:     map[string]any{},
			headers:  isoAuthHeader(adminToken),
			wantCode: 1001,
		},
		{
			name:     "categories-empty-name",
			path:     "/categories",
			body:     map[string]any{"name": ""},
			headers:  isoAuthHeader(adminToken),
			wantCode: 1001,
		},
		{
			name:     "admin-login-missing-fields",
			path:     "/admin/login",
			body:     map[string]any{},
			headers:  nil,
			wantCode: 1001,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := isoDo(t, base, "POST", tc.path, tc.body, tc.headers)
			if res.Status != 400 {
				t.Fatalf("%s: HTTP 状态 = %d, want 400（响应 code=%d message=%q）", tc.name, res.Status, res.Code, res.Message)
			}
			if res.Code != tc.wantCode {
				t.Fatalf("%s: code = %d, want %d", tc.name, res.Code, tc.wantCode)
			}
			if res.Message == "" || res.Message == "内部错误" {
				t.Fatalf("%s: 参数错误 message 不应为内部错误，实际 %q", tc.name, res.Message)
			}
		})
	}
}

// TestBusinessErrorMappingUnchanged 锁定正常业务错误仍按码表返回 409。
func TestBusinessErrorMappingUnchanged(t *testing.T) {
	base := setupIsolationServer(t)
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	categoryName := fmt.Sprintf("b105fix02-dup-%d", time.Now().UnixNano())
	first := isoDo(t, base, "POST", "/categories", map[string]any{"name": categoryName}, isoAuthHeader(adminToken))
	if first.Status != 200 || first.Code != 0 {
		t.Fatalf("首次创建分类失败: status=%d code=%d message=%q", first.Status, first.Code, first.Message)
	}

	second := isoDo(t, base, "POST", "/categories", map[string]any{"name": categoryName}, isoAuthHeader(adminToken))
	if second.Status != 409 {
		t.Fatalf("同级重名 HTTP 状态 = %d, want 409（code=%d message=%q）", second.Status, second.Code, second.Message)
	}
	if second.Code != 3002 {
		t.Fatalf("同级重名 code = %d, want 3002", second.Code)
	}
}
