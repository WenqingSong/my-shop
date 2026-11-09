// Package termination 冻结 SurgeCart 的容器终止预算契约（T2-03）。
//
// 预算由四段组成，且 Kubernetes preStop 的耗时同样计入 terminationGracePeriodSeconds
// （preStop 不是「额外的 5 秒」）：
//
//	termination_grace_period
//	  >= pre_stop_delay + http_drain_timeout + runtime_shutdown_timeout + safety_margin
//
// 冻结值：5s + 10s + 30s + 15s = 60s。
//
// 同一组数值是以下三处的唯一事实源：
//   - manifest/config/config.yaml 的 lifecycle.* 与 server.graceful_timeout / flash_sale.shutdown_timeout；
//   - docker-compose.yml 的 app.stop_grace_period；
//   - 后续 T2-06 Kubernetes 清单的 terminationGracePeriodSeconds 与 preStop。
//
// budget_contract_test.go 对这些来源做自动化一致性断言，避免清单与实现漂移。
package termination

import (
	"context"
	"fmt"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/logx"
)

const (
	// DefaultPreStopDelay 是 Kubernetes preStop 钩子的冻结预算（摘流窗口）。
	DefaultPreStopDelay = 5 * time.Second
	// DefaultHTTPDrainTimeout 是 HTTP Server 停止接收新连接后等待在途请求完成的预算。
	DefaultHTTPDrainTimeout = 10 * time.Second
	// DefaultRuntimeShutdownTimeout 是 Publisher → Consumer → Producer → Scanner 的统一关闭预算。
	DefaultRuntimeShutdownTimeout = 30 * time.Second
	// DefaultTerminationGracePeriod 是容器终止宽限期（Compose stop_grace_period /
	// K8s terminationGracePeriodSeconds）。
	DefaultTerminationGracePeriod = 60 * time.Second
	// DefaultSafetyMargin 是冻结契约要求的最小安全余量。
	DefaultSafetyMargin = 15 * time.Second
)

// Budget 是一次进程终止的完整时间预算。
type Budget struct {
	PreStopDelay           time.Duration
	HTTPDrainTimeout       time.Duration
	RuntimeShutdownTimeout time.Duration
	TerminationGracePeriod time.Duration
}

// Required 返回关闭流程实际需要的时间（不含安全余量）。
func (b Budget) Required() time.Duration {
	return b.PreStopDelay + b.HTTPDrainTimeout + b.RuntimeShutdownTimeout
}

// SafetyMargin 返回宽限期扣除必需预算后的余量；为负表示预算已超出宽限期。
func (b Budget) SafetyMargin() time.Duration {
	return b.TerminationGracePeriod - b.Required()
}

// Validate 校验预算契约。宽限期不足时返回错误，调用方必须 fail-fast：
// 静默接受会让进程在 Kubernetes 的 SIGKILL 时刻被强杀，关闭顺序随之失效。
func (b Budget) Validate() error {
	for _, item := range []struct {
		name  string
		value time.Duration
	}{
		{"pre_stop_delay", b.PreStopDelay},
		{"http_drain_timeout", b.HTTPDrainTimeout},
		{"runtime_shutdown_timeout", b.RuntimeShutdownTimeout},
		{"termination_grace_period", b.TerminationGracePeriod},
	} {
		if item.value <= 0 {
			return fmt.Errorf("终止预算 %s 必须大于 0，实际 %s", item.name, item.value)
		}
	}
	if b.SafetyMargin() < DefaultSafetyMargin {
		return fmt.Errorf(
			"终止预算不足：pre_stop_delay(%s) + http_drain_timeout(%s) + runtime_shutdown_timeout(%s) = %s，"+
				"加安全余量 %s 需要 termination_grace_period >= %s，当前为 %s。"+
				"请同步调整 lifecycle.termination_grace_period、docker-compose.yml 的 stop_grace_period "+
				"与后续 Kubernetes terminationGracePeriodSeconds；不得单独缩短 runtime 关闭预算",
			durationText(b.PreStopDelay), durationText(b.HTTPDrainTimeout),
			durationText(b.RuntimeShutdownTimeout), durationText(b.Required()),
			durationText(DefaultSafetyMargin),
			durationText(b.Required()+DefaultSafetyMargin),
			durationText(b.TerminationGracePeriod),
		)
	}
	return nil
}

// Fields 返回启动日志所需的预算字段（键名冻结，供自动化时序断言使用）。
func (b Budget) Fields() logx.Fields {
	return logx.Fields{
		"pre_stop_delay":              durationText(b.PreStopDelay),
		"http_drain_timeout":          durationText(b.HTTPDrainTimeout),
		"runtime_shutdown_timeout":    durationText(b.RuntimeShutdownTimeout),
		"required_termination_budget": durationText(b.Required()),
		"termination_grace_period":    durationText(b.TerminationGracePeriod),
		"safety_margin":               durationText(b.SafetyMargin()),
	}
}

// LogStartup 输出结构化终止预算日志（event=termination_budget）。
func (b Budget) LogStartup(ctx context.Context) {
	logx.Info(ctx, "termination_budget", "容器终止预算契约已生效", b.Fields())
}

// Load 组装运行期实测预算与部署期契约：
//   - httpDrainTimeout 来自 server.graceful_timeout（与实际 drain 定时器同源）；
//   - runtimeShutdownTimeout 来自 flash_sale.shutdown_timeout（与实际 Runtime 关闭预算同源）；
//   - pre_stop_delay / termination_grace_period 来自 lifecycle.* 配置（含环境变量覆盖）。
//
// 任一值非法或宽限期不足都会返回错误，由调用方 fail-fast。
func Load(ctx context.Context, httpDrainTimeout, runtimeShutdownTimeout time.Duration) (Budget, error) {
	preStop, err := config.Duration(ctx, config.KeyLifecyclePreStopDelay)
	if err != nil {
		return Budget{}, err
	}
	grace, err := config.Duration(ctx, config.KeyLifecycleTerminationGracePeriod)
	if err != nil {
		return Budget{}, err
	}
	budget := Budget{
		PreStopDelay:           preStop,
		HTTPDrainTimeout:       httpDrainTimeout,
		RuntimeShutdownTimeout: runtimeShutdownTimeout,
		TerminationGracePeriod: grace,
	}
	if err := budget.Validate(); err != nil {
		return budget, err
	}
	return budget, nil
}

// durationText 以秒为单位输出整秒预算（如 60s），非整秒保留原始写法（如 1500ms）。
// 日志字段保持稳定文本，便于自动化断言。
func durationText(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int64(d/time.Second))
	}
	return d.String()
}
