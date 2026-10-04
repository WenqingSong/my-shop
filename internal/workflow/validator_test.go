package workflow

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func mustState(t *testing.T, yml string) State {
	t.Helper()
	var s State
	if err := yaml.Unmarshal([]byte(yml), &s); err != nil {
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

func TestValidateStateSchemaLegal(t *testing.T) {
	// 覆盖测试要求 1：合法完整 Task → PASS。
	s := mustState(t, `
task: demo-task
phase: IMPLEMENTING
review:
  status: CLEAN
  target_base: abc123
  target_paths:
    - internal/foo.go
owner_verification:
  status: PENDING
delivery:
  status: NONE
required_resources:
  error_code_domains: []
  migrations: []
blocked:
  is_blocked: false
  reason: ""
`)
	if issues := ValidateStateSchema(s); len(issues) != 0 {
		t.Fatalf("合法状态应无 issue，实际 %v", issues)
	}
}

func TestValidateStateSchemaIllegalPhase(t *testing.T) {
	// 覆盖测试要求 2：非法 phase → FAIL。
	s := mustState(t, "task: demo-task\nphase: BOGUS\n")
	issues := ValidateStateSchema(s)
	if !issueCheck(t, issues, "phase enum") {
		t.Fatalf("非法 phase 应报 phase enum issue，实际 %v", issues)
	}
}

func TestValidateStateSchemaInvalidCombinations(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantCheck string
	}{
		{
			// 测试要求 3：DELIVERING + PENDING → FAIL。
			name:      "DELIVERING+PENDING",
			yaml:      "task: t\nphase: DELIVERING\nreview:\n  status: CLEAN\n  target_base: x\n  target_paths: [a]\nowner_verification:\n  status: PENDING\ndelivery:\n  status: PASS\n",
			wantCheck: "Owner Verification Gate (INV-007)",
		},
		{
			// 测试要求 4：DONE + delivery != PASS → FAIL。
			name:      "DONE+delivery!=PASS",
			yaml:      "task: t\nphase: DONE\nreview:\n  status: CLEAN\n  target_base: x\n  target_paths: [a]\nowner_verification:\n  status: ACCEPTED\ndelivery:\n  status: NONE\n",
			wantCheck: "Delivery Gate (INV-009)",
		},
		{
			// 测试要求 9：STALE 后仍处于 DELIVERING → FAIL。
			name:      "STALE+DELIVERING",
			yaml:      "task: t\nphase: DELIVERING\nreview:\n  status: STALE\nowner_verification:\n  status: PENDING\ndelivery:\n  status: NONE\n",
			wantCheck: "Lifecycle Consistency (INV-005)",
		},
		{
			// 测试要求 9：STALE 后仍处于 WAITING_FOR_OWNER_ACCEPTANCE → FAIL。
			name:      "STALE+WAITING_FOR_OWNER_ACCEPTANCE",
			yaml:      "task: t\nphase: WAITING_FOR_OWNER_ACCEPTANCE\nreview:\n  status: STALE\nowner_verification:\n  status: PENDING\ndelivery:\n  status: NONE\n",
			wantCheck: "Lifecycle Consistency (INV-005)",
		},
		{
			// 测试要求 16：旧 Owner Verification（ACCEPTED）不得用于新版本（STALE）→ FAIL。
			name:      "STALE+ACCEPTED",
			yaml:      "task: t\nphase: READY_FOR_REVIEW\nreview:\n  status: STALE\nowner_verification:\n  status: ACCEPTED\ndelivery:\n  status: NONE\n",
			wantCheck: "Lifecycle Consistency (INV-005)",
		},
		{
			// STALE + delivery != NONE（旧交付结论未失效）→ FAIL。
			name:      "STALE+delivery!=NONE",
			yaml:      "task: t\nphase: READY_FOR_REVIEW\nreview:\n  status: STALE\nowner_verification:\n  status: PENDING\ndelivery:\n  status: PASS\n",
			wantCheck: "Lifecycle Consistency (INV-005)",
		},
		{
			// 测试要求 17：CLEAN + target_base 空 → FAIL。
			name:      "CLEAN+target_base空",
			yaml:      "task: t\nphase: IN_REVIEW\nreview:\n  status: CLEAN\n  target_base: \"\"\n  target_paths: [a]\nowner_verification:\n  status: PENDING\ndelivery:\n  status: NONE\n",
			wantCheck: "Review Target (INV-004)",
		},
		{
			// 测试要求 17：CLEAN + target_paths 空 → FAIL。
			name:      "CLEAN+target_paths空",
			yaml:      "task: t\nphase: IN_REVIEW\nreview:\n  status: CLEAN\n  target_base: x\n  target_paths: []\nowner_verification:\n  status: PENDING\ndelivery:\n  status: NONE\n",
			wantCheck: "Review Target (INV-004)",
		},
		{
			// 测试要求 15：Owner Verification ACCEPTED 的合法状态（WAITING_FOR_OWNER_ACCEPTANCE + ACCEPTED + CLEAN）→ 无 issue。
			name:      "ACCEPTED合法",
			yaml:      "task: t\nphase: WAITING_FOR_OWNER_ACCEPTANCE\nreview:\n  status: CLEAN\n  target_base: x\n  target_paths: [a]\nowner_verification:\n  status: ACCEPTED\ndelivery:\n  status: NONE\n",
			wantCheck: "",
		},
		{
			// Deliverer Gate：DELIVERING + review.status != CLEAN → FAIL。
			name:      "DELIVERING+review!=CLEAN",
			yaml:      "task: t\nphase: DELIVERING\nreview:\n  status: NONE\nowner_verification:\n  status: ACCEPTED\ndelivery:\n  status: NONE\n",
			wantCheck: "Delivery Gate (INV-007)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := ValidateStateSchema(mustState(t, tt.yaml))
			if tt.wantCheck == "" {
				if len(issues) != 0 {
					t.Fatalf("期望无 issue，实际 %v", issues)
				}
				return
			}
			if !issueCheck(t, issues, tt.wantCheck) {
				t.Fatalf("期望 issue 含 %q，实际 %v", tt.wantCheck, issues)
			}
		})
	}
}

func TestCheckReviewValidity(t *testing.T) {
	// 覆盖测试要求 5/6/7/8：Review-neutral 不失效，实质变化判 STALE。
	base := mustState(t, `
task: demo-task
phase: WAITING_FOR_OWNER_ACCEPTANCE
review:
  status: CLEAN
  target_base: abc123
  target_paths:
    - internal/foo.go
    - internal/foo_test.go
    - docs/design/foo.md
    - .agent/tasks/demo-task/contract.md
owner_verification:
  status: ACCEPTED
delivery:
  status: NONE
`)

	tests := []struct {
		name    string
		changed []string
		stale   bool
	}{
		{"Review-neutral findings.md", []string{".agent/tasks/demo-task/findings.md"}, false},
		{"Review-neutral core-logic.md", []string{".agent/tasks/demo-task/core-logic.md"}, false},
		{"Review-neutral delivery.md", []string{".agent/tasks/demo-task/delivery.md"}, false},
		{"Review-neutral state.yaml", []string{".agent/tasks/demo-task/state.yaml"}, false},
		{"生产代码变化", []string{"internal/foo.go"}, true},
		{"业务测试变化", []string{"internal/foo_test.go"}, true},
		{"Contract 变化", []string{".agent/tasks/demo-task/contract.md"}, true},
		{"Design 变化", []string{"docs/design/foo.md"}, true},
		{"target_paths 之外的非白名单文件（default-deny 触发）", []string{"docs/agent/coder.md"}, true},
		{"无关 README（default-deny 触发）", []string{"README.md"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := checkReviewValidity("demo-task", base, tt.changed)
			gotStale := len(issues) > 0
			if gotStale != tt.stale {
				t.Fatalf("changed=%v 期望 stale=%v，实际 issues=%v", tt.changed, tt.stale, issues)
			}
			if gotStale {
				for _, iss := range issues {
					if iss.Expected != "review.status=STALE" {
						t.Fatalf("期望 expected=review.status=STALE，实际 %v", iss)
					}
				}
			}
		})
	}
}
