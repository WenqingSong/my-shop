package termination

import (
	"context"
	"strings"
	"testing"
	"time"
)

// frozenBudget 返回 T2-03 冻结的默认预算（5s + 10s + 30s + 15s = 60s）。
func frozenBudget() Budget {
	return Budget{
		PreStopDelay:           DefaultPreStopDelay,
		HTTPDrainTimeout:       DefaultHTTPDrainTimeout,
		RuntimeShutdownTimeout: DefaultRuntimeShutdownTimeout,
		TerminationGracePeriod: DefaultTerminationGracePeriod,
	}
}

// TestDefaultBudgetSatisfiesFrozenContract 断言冻结数值与预算关系：
// preStop 5s + drain 10s + runtime 30s = 45s，宽限期 60s，安全余量恰好 15s。
func TestDefaultBudgetSatisfiesFrozenContract(t *testing.T) {
	budget := frozenBudget()
	if got := budget.Required(); got != 45*time.Second {
		t.Fatalf("required_termination_budget = %s，期望 45s", got)
	}
	if got := budget.SafetyMargin(); got != DefaultSafetyMargin {
		t.Fatalf("safety_margin = %s，期望 %s", got, DefaultSafetyMargin)
	}
	if err := budget.Validate(); err != nil {
		t.Fatalf("冻结预算应通过校验: %v", err)
	}
	if budget.TerminationGracePeriod <= 30*time.Second {
		t.Fatal("宽限期不得被压缩到 Kubernetes 默认 30s：runtime 关闭预算本身即 30s")
	}
}

// TestBudgetFieldsAreFrozen 断言启动日志字段名与取值稳定，供进程级测试自动断言。
func TestBudgetFieldsAreFrozen(t *testing.T) {
	fields := frozenBudget().Fields()
	want := map[string]string{
		"pre_stop_delay":              "5s",
		"http_drain_timeout":          "10s",
		"runtime_shutdown_timeout":    "30s",
		"required_termination_budget": "45s",
		"termination_grace_period":    "60s",
		"safety_margin":               "15s",
	}
	for key, expected := range want {
		got, ok := fields[key]
		if !ok {
			t.Fatalf("终止预算日志缺少字段 %q", key)
		}
		if got != expected {
			t.Fatalf("%s = %v，期望 %q", key, got, expected)
		}
	}
}

// TestValidateRejectsInsufficientGrace 断言宽限期不足时必须失败：
// 例如把 runtime 预算放大到 45s 而不调整 60s 宽限期，安全余量只剩 0s。
func TestValidateRejectsInsufficientGrace(t *testing.T) {
	budget := frozenBudget()
	budget.RuntimeShutdownTimeout = 45 * time.Second
	err := budget.Validate()
	if err == nil {
		t.Fatal("宽限期不足时必须返回错误")
	}
	if !strings.Contains(err.Error(), "终止预算不足") || !strings.Contains(err.Error(), "termination_grace_period") {
		t.Fatalf("错误信息应明确指出预算不足与宽限期配置项，实际: %v", err)
	}
}

// TestValidateRejectsNonPositive 断言各段预算必须为正数。
func TestValidateRejectsNonPositive(t *testing.T) {
	cases := map[string]func(*Budget){
		"pre_stop_delay":           func(b *Budget) { b.PreStopDelay = 0 },
		"http_drain_timeout":       func(b *Budget) { b.HTTPDrainTimeout = -time.Second },
		"runtime_shutdown_timeout": func(b *Budget) { b.RuntimeShutdownTimeout = 0 },
		"termination_grace_period": func(b *Budget) { b.TerminationGracePeriod = 0 },
	}
	for name, mutate := range cases {
		budget := frozenBudget()
		mutate(&budget)
		if err := budget.Validate(); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s 非正数时应报错并指出字段名，实际: %v", name, err)
		}
	}
}

// TestLoadReadsLifecycleConfigFromEnv 断言部署期契约可由环境变量覆盖，
// 且覆盖后仍按同一预算公式校验。
func TestLoadReadsLifecycleConfigFromEnv(t *testing.T) {
	t.Setenv("LIFECYCLE_PRE_STOP_DELAY", "7s")
	t.Setenv("LIFECYCLE_TERMINATION_GRACE_PERIOD", "62s")

	budget, err := Load(context.Background(), 10*time.Second, 30*time.Second)
	if err != nil {
		t.Fatalf("7s + 10s + 30s + 15s = 62s 应通过校验: %v", err)
	}
	if budget.PreStopDelay != 7*time.Second || budget.TerminationGracePeriod != 62*time.Second {
		t.Fatalf("lifecycle 配置读取错误: %+v", budget)
	}
	if got := budget.SafetyMargin(); got != 15*time.Second {
		t.Fatalf("safety_margin = %s，期望 15s", got)
	}
}

// TestLoadRejectsBudgetOverflow 断言扩大运行期预算而不扩大宽限期必须启动失败，
// 防止「缩短 runtime 预算去迎合 Kubernetes 默认 30s」的反向妥协。
func TestLoadRejectsBudgetOverflow(t *testing.T) {
	t.Setenv("LIFECYCLE_PRE_STOP_DELAY", "5s")
	t.Setenv("LIFECYCLE_TERMINATION_GRACE_PERIOD", "55s")

	if _, err := Load(context.Background(), 10*time.Second, 30*time.Second); err == nil {
		t.Fatal("55s 宽限期不足以覆盖 5s+10s+30s+15s，必须失败")
	}
}

// TestLoadRejectsInvalidDuration 断言非法 duration 写法必须报错，不能静默回落默认值。
func TestLoadRejectsInvalidDuration(t *testing.T) {
	t.Setenv("LIFECYCLE_PRE_STOP_DELAY", "5")

	if _, err := Load(context.Background(), 10*time.Second, 30*time.Second); err == nil ||
		!strings.Contains(err.Error(), "Go duration") {
		t.Fatalf("非法 duration 应报错并指出配置项，实际: %v", err)
	}
}

// TestDurationText 覆盖日志文本格式（整秒输出 Ns，便于自动化断言）。
func TestDurationText(t *testing.T) {
	cases := map[time.Duration]string{
		60 * time.Second:        "60s",
		1500 * time.Millisecond: "1.5s",
		250 * time.Millisecond:  "250ms",
	}
	for input, expected := range cases {
		if got := durationText(input); got != expected {
			t.Fatalf("durationText(%s) = %q，期望 %q", input, got, expected)
		}
	}
}
