package termination

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gogf/gf/v2/encoding/gyaml"
	"github.com/gogf/gf/v2/util/gconv"
)

// 本文件是 T2-03-06 的预算一致性自动化检查：
// 应用默认值（Go 常量）、运行期配置（manifest/config/config.yaml）与容器编排
// （docker-compose.yml 的 stop_grace_period）必须落在同一组冻结数值上。
//
// 后续 T2-06 的 Kubernetes 清单必须复用同一组数值（terminationGracePeriodSeconds=60s、
// preStop=5s）；届时在清单落地处补充同等断言，而不是改这里的期望值。

// repoRoot 返回仓库根目录（go test 的工作目录是包目录）。
func repoRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("无法定位仓库根目录（%s）: %v", root, err)
	}
	return root
}

// yamlRoot 读取并解析 YAML 文件为 map。
func yamlRoot(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	root, err := gyaml.Decode(data)
	if err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
	return root
}

// yamlSection 返回指定键的子 map，缺失即失败。
func yamlSection(t *testing.T, root map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := root[key]
	if !ok {
		t.Fatalf("缺少配置节 %q", key)
	}
	section := gconv.Map(value)
	if section == nil {
		t.Fatalf("配置节 %q 不是 map", key)
	}
	return section
}

// TestBudgetContractMatchesRuntimeConfig 断言 manifest/config/config.yaml 中的
// HTTP drain、runtime shutdown 与部署期契约与冻结值一致。
func TestBudgetContractMatchesRuntimeConfig(t *testing.T) {
	path := filepath.Join(repoRoot(t), "manifest", "config", "config.yaml")
	root := yamlRoot(t, path)

	server := yamlSection(t, root, "server")
	gracefulSeconds := gconv.Int(server["graceful_timeout"])
	if got := time.Duration(gracefulSeconds) * time.Second; got != DefaultHTTPDrainTimeout {
		t.Fatalf("server.graceful_timeout = %ds，期望 %s（HTTP drain 契约）", gracefulSeconds, DefaultHTTPDrainTimeout)
	}

	flashSale := yamlSection(t, root, "flash_sale")
	shutdown, err := time.ParseDuration(gconv.String(flashSale["shutdown_timeout"]))
	if err != nil {
		t.Fatalf("flash_sale.shutdown_timeout 非法: %v", err)
	}
	if shutdown != DefaultRuntimeShutdownTimeout {
		t.Fatalf("flash_sale.shutdown_timeout = %s，期望 %s（Runtime 关闭契约）", shutdown, DefaultRuntimeShutdownTimeout)
	}

	lifecycle := yamlSection(t, root, "lifecycle")
	preStop, err := time.ParseDuration(gconv.String(lifecycle["pre_stop_delay"]))
	if err != nil {
		t.Fatalf("lifecycle.pre_stop_delay 非法: %v", err)
	}
	if preStop != DefaultPreStopDelay {
		t.Fatalf("lifecycle.pre_stop_delay = %s，期望 %s（preStop 契约）", preStop, DefaultPreStopDelay)
	}
	grace, err := time.ParseDuration(gconv.String(lifecycle["termination_grace_period"]))
	if err != nil {
		t.Fatalf("lifecycle.termination_grace_period 非法: %v", err)
	}
	if grace != DefaultTerminationGracePeriod {
		t.Fatalf("lifecycle.termination_grace_period = %s，期望 %s", grace, DefaultTerminationGracePeriod)
	}

	budget := Budget{
		PreStopDelay:           preStop,
		HTTPDrainTimeout:       time.Duration(gracefulSeconds) * time.Second,
		RuntimeShutdownTimeout: shutdown,
		TerminationGracePeriod: grace,
	}
	if err := budget.Validate(); err != nil {
		t.Fatalf("配置文件预算组合不满足契约: %v", err)
	}
	if got := budget.SafetyMargin(); got != DefaultSafetyMargin {
		t.Fatalf("配置计算出的 safety_margin = %s，期望 %s", got, DefaultSafetyMargin)
	}
}

// TestBudgetContractMatchesCompose 断言 docker-compose.yml 的 app.stop_grace_period
// 等于 lifecycle.termination_grace_period，且未污染其他服务的生命周期。
func TestBudgetContractMatchesCompose(t *testing.T) {
	root := yamlRoot(t, filepath.Join(repoRoot(t), "docker-compose.yml"))
	services := yamlSection(t, root, "services")

	appRaw, ok := services["app"]
	if !ok {
		t.Fatal("docker-compose.yml 缺少 app 服务")
	}
	app := gconv.Map(appRaw)
	stopGraceText := gconv.String(app["stop_grace_period"])
	if stopGraceText == "" {
		t.Fatal("docker-compose.yml 的 app 服务缺少 stop_grace_period（Compose 默认 10s 会提前 SIGKILL）")
	}
	stopGrace, err := time.ParseDuration(stopGraceText)
	if err != nil {
		t.Fatalf("app.stop_grace_period 非法: %v", err)
	}
	if stopGrace != DefaultTerminationGracePeriod {
		t.Fatalf("app.stop_grace_period = %s，期望 %s（与 terminationGracePeriodSeconds 同源）",
			stopGrace, DefaultTerminationGracePeriod)
	}

	for name, raw := range services {
		if name == "app" {
			continue
		}
		if _, exists := gconv.Map(raw)["stop_grace_period"]; exists {
			t.Fatalf("服务 %q 不应声明 stop_grace_period（本任务只调整应用容器的终止预算）", name)
		}
	}
}
