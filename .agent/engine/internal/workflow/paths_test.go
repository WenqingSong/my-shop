package workflow

import "testing"

// TestWhitelistTaskScoped 覆盖 task-scoped 白名单：禁止 basename-only，
// 其他 task / 其他目录的同名文件必须判 substantive。
func TestWhitelistTaskScoped(t *testing.T) {
	tests := []struct {
		name         string
		slug         string
		path         string
		reviewNeut   bool
		deliveryNeut bool
	}{
		{"本 task state.yaml", "demo", ".agent/tasks/demo/state.yaml", true, true},
		{"本 task findings.md", "demo", ".agent/tasks/demo/findings.md", true, false},
		{"本 task core-logic.md", "demo", ".agent/tasks/demo/core-logic.md", true, false},
		{"本 task owner-decision.md", "demo", ".agent/tasks/demo/owner-decision.md", true, false},
		{"本 task delivery.md", "demo", ".agent/tasks/demo/delivery.md", true, true},
		{"其他 task state.yaml", "demo", ".agent/tasks/other/state.yaml", false, false},
		{"其他 task findings.md", "demo", ".agent/tasks/other/findings.md", false, false},
		{"本 task 子目录 state.yaml", "demo", ".agent/tasks/demo/sub/state.yaml", false, false},
		{"生产代码", "demo", "internal/foo.go", false, false},
		{"业务测试", "demo", "internal/foo_test.go", false, false},
		{"contract.md", "demo", ".agent/tasks/demo/contract.md", false, false},
		{"task.md", "demo", ".agent/tasks/demo/task.md", false, false},
		{"Design 同名 state.yaml", "demo", "docs/design/state.yaml", false, false},
		{"Registry 同名", "demo", ".agent/registry/state.yaml", false, false},
		// DANGER 循环证据文件：仅当前 task 目录下精确文件名豁免。
		{"本 task loop-ledger.md", "demo", ".agent/tasks/demo/loop-ledger.md", true, true},
		{"本 task danger-report.md", "demo", ".agent/tasks/demo/danger-report.md", true, true},
		{"其他 task loop-ledger.md", "demo", ".agent/tasks/other/loop-ledger.md", false, false},
		{"其他 task danger-report.md", "demo", ".agent/tasks/other/danger-report.md", false, false},
		{"本 task 子目录 loop-ledger.md", "demo", ".agent/tasks/demo/sub/loop-ledger.md", false, false},
		{"本 task 子目录 danger-report.md", "demo", ".agent/tasks/demo/sub/danger-report.md", false, false},
		{"近似文件名 loop-ledger-old.md", "demo", ".agent/tasks/demo/loop-ledger-old.md", false, false},
		{"大小写近似 LOOP-LEDGER.md", "demo", ".agent/tasks/demo/LOOP-LEDGER.md", false, false},
		{"生产 SQL", "demo", "db/V1__baseline.sql", false, false},
		{"Design 文档", "demo", "docs/design/foo.md", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isReviewNeutral(tt.slug, tt.path); got != tt.reviewNeut {
				t.Fatalf("isReviewNeutral(%q, %q) = %v, 期望 %v", tt.slug, tt.path, got, tt.reviewNeut)
			}
			if got := isDeliveryNeutral(tt.slug, tt.path); got != tt.deliveryNeut {
				t.Fatalf("isDeliveryNeutral(%q, %q) = %v, 期望 %v", tt.slug, tt.path, got, tt.deliveryNeut)
			}
		})
	}
}
