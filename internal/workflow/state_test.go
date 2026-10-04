package workflow

import "testing"

// TestPhaseSetHasExactly13Values 保证 phase 集合恰好为 13 值，防止实现与 Contract/Design 漂移。
func TestPhaseSetHasExactly13Values(t *testing.T) {
	if len(allPhases) != 13 {
		t.Fatalf("phase 集合应为 13 值，实际 %d：%v", len(allPhases), allPhases)
	}
	if len(phaseSet) != 13 {
		t.Fatalf("phaseSet 大小应为 13，实际 %d", len(phaseSet))
	}
	seen := map[Phase]bool{}
	for _, p := range allPhases {
		if seen[p] {
			t.Fatalf("phase 重复：%s", p)
		}
		seen[p] = true
		if !ValidPhase(p) {
			t.Fatalf("ValidPhase(%s) 应为 true", p)
		}
	}
	if ValidPhase("NOT_A_PHASE") {
		t.Fatal("ValidPhase(NOT_A_PHASE) 应为 false")
	}
}

// TestTransitionAuthorityCompleteness 保证 18 条转换全部定义且端点均为合法 phase。
func TestTransitionAuthorityCompleteness(t *testing.T) {
	if len(transitionAuthority) != 18 {
		t.Fatalf("Transition Authority 表应为 18 条，实际 %d", len(transitionAuthority))
	}
	for i, tr := range transitionAuthority {
		if tr.From == tr.To {
			t.Fatalf("转换 #%d 自环非法：%s -> %s", i+1, tr.From, tr.To)
		}
		if !ValidPhase(tr.From) || !ValidPhase(tr.To) {
			t.Fatalf("转换 #%d 端点非法：%s -> %s", i+1, tr.From, tr.To)
		}
		if tr.DecisionAuthority == "" {
			t.Fatalf("转换 #%d 缺少 Decision Authority", i+1)
		}
		if tr.FileWriter == "" {
			t.Fatalf("转换 #%d 缺少 File Writer", i+1)
		}
	}
}

func TestPathClassification(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		neutral     bool
		substantial bool
	}{
		{"findings 白名单", ".agent/tasks/demo/findings.md", true, false},
		{"core-logic 白名单", ".agent/tasks/demo/core-logic.md", true, false},
		{"delivery 白名单", ".agent/tasks/demo/delivery.md", true, false},
		{"state.yaml 白名单", ".agent/tasks/demo/state.yaml", true, false},
		{"生产代码 internal", "internal/foo.go", false, true},
		{"生产代码 api", "api/handler.go", false, true},
		{"主入口", "main.go", false, true},
		{"业务测试", "internal/foo_test.go", false, true},
		{"Contract", ".agent/tasks/demo/contract.md", false, true},
		{"Task 定义", ".agent/tasks/demo/task.md", false, true},
		{"Design", "docs/design/foo.md", false, true},
		{"migration SQL", "internal/migrations/sql/20261001000009_x.up.sql", false, true},
		{"runtime config", "manifest/config/app.yaml", false, true},
		{"Registry", ".agent/registry/migrations.md", false, true},
		{"README（default-deny 也触发）", "README.md", false, true},
		{"docs/agent（default-deny 也触发）", "docs/agent/coder.md", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isReviewNeutral(tt.path); got != tt.neutral {
				t.Fatalf("isReviewNeutral(%s) = %v, 期望 %v", tt.path, got, tt.neutral)
			}
			if got := isSubstantialChange(tt.path); got != tt.substantial {
				t.Fatalf("isSubstantialChange(%s) = %v, 期望 %v", tt.path, got, tt.substantial)
			}
		})
	}
}
