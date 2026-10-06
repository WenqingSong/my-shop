package workflow

import (
	"strings"
	"testing"
)

// mustState 用版本化 parseState 解析测试 state.yaml。
func mustState(t *testing.T, yml string) State {
	t.Helper()
	s, err := parseState([]byte(yml))
	if err != nil {
		t.Fatalf("解析测试 state.yaml 失败: %v", err)
	}
	return s
}

// issueCheck 断言 issues 中存在包含指定 check 子串的问题。
func issueCheck(t *testing.T, issues []Issue, checkSubstr string) bool {
	t.Helper()
	for _, iss := range issues {
		if strings.Contains(iss.Check, checkSubstr) {
			return true
		}
	}
	return false
}

// TestSchemaValidationLegal 覆盖合法完整 v3 state → 无 issue。
func TestSchemaValidationLegal(t *testing.T) {
	s := mustState(t, `
schema_version: 3
task_id: demo
contract:
  status: APPROVED
  target: a1b2c3d
resources:
  reservations: {}
review:
  status: CLEAN
  target: c1d2e3f
owner:
  status: ACCEPTED
  review_target: c1d2e3f
delivery:
  status: PASS
  review_target: c1d2e3f
  feature_head: f1a2b3c
  develop_base: d1e2f3a
blocked:
  active: false
  by: ""
  reason: ""
`)
	if issues := schemaValidation(s); len(issues) != 0 {
		t.Fatalf("合法状态应无 issue，实际 %v", issues)
	}
}

// TestSchemaValidationIllegal 覆盖各枚举非法与 task_id 空。
func TestSchemaValidationIllegal(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantCheck string
	}{
		{"task_id 空", "schema_version: 3\ntask_id: \"\"\n", "schema"},
		{"contract.status 非法", "schema_version: 3\ntask_id: t\ncontract:\n  status: BOGUS\n", "contract.status enum"},
		{"review.status 非法(STALE)", "schema_version: 3\ntask_id: t\nreview:\n  status: STALE\n", "review.status enum"},
		{"owner.status 非法", "schema_version: 3\ntask_id: t\nowner:\n  status: BOGUS\n", "owner.status enum"},
		{"delivery.status 非法(CONDITIONAL_PASS)", "schema_version: 3\ntask_id: t\ndelivery:\n  status: CONDITIONAL_PASS\n", "delivery.status enum"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := schemaValidation(mustState(t, tt.yaml))
			if !issueCheck(t, issues, tt.wantCheck) {
				t.Fatalf("期望 issue 含 %q，实际 %v", tt.wantCheck, issues)
			}
		})
	}
}
