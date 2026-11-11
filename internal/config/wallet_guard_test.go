package config

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// 钱包 Mock 充值属于「凭空造钱」能力：严格部署模式下必须禁止启用。
// 本文件只覆盖该安全闸门，不改变既有严格模式用例。

const walletGuardYAMLTemplate = `
deployment:
  require_external_endpoints: %t
wallet:
  mock_recharge:
    enabled: %t
`

// TestMockRechargeForbiddenInStrictMode 覆盖：严格模式 + 开关开启 → 启动校验失败。
func TestMockRechargeForbiddenInStrictMode(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, fmt.Sprintf(walletGuardYAMLTemplate, true, true))

	err := loader.ValidateMockRechargeDisabled(context.Background())
	if err == nil {
		t.Fatal("严格模式下开启 Mock 充值必须返回错误")
	}
	if !strings.Contains(err.Error(), "WALLET_MOCK_RECHARGE_ENABLED") {
		t.Fatalf("错误信息应指出环境变量名，实际 %q", err.Error())
	}
}

// TestMockRechargeAllowedInStrictModeWhenOff 覆盖：严格模式 + 开关关闭 → 通过。
func TestMockRechargeAllowedInStrictModeWhenOff(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, fmt.Sprintf(walletGuardYAMLTemplate, true, false))

	if err := loader.ValidateMockRechargeDisabled(context.Background()); err != nil {
		t.Fatalf("严格模式下关闭 Mock 充值应通过: %v", err)
	}
}

// TestMockRechargeAllowedOutsideStrictMode 覆盖：非严格模式（本地开发）不干预。
func TestMockRechargeAllowedOutsideStrictMode(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, fmt.Sprintf(walletGuardYAMLTemplate, false, true))

	if err := loader.ValidateMockRechargeDisabled(context.Background()); err != nil {
		t.Fatalf("非严格模式不应阻止本地开启 Mock 充值: %v", err)
	}
}

// TestMockRechargeEnvOverridesYAML 覆盖：环境变量优先于 YAML，不得用 YAML 规避闸门。
func TestMockRechargeEnvOverridesYAML(t *testing.T) {
	isolateRegisteredEnv(t)
	loader := newTestLoader(t, fmt.Sprintf(walletGuardYAMLTemplate, true, false))
	t.Setenv(MustSpecOf(KeyWalletMockRechargeEnabled).Env, "true")

	if err := loader.ValidateMockRechargeDisabled(context.Background()); err == nil {
		t.Fatal("环境变量开启 Mock 充值时严格模式必须拒绝")
	}
}
