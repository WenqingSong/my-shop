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
