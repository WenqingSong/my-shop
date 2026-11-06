package config

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// strictEndpoint 描述「Kubernetes 严格配置模式」下必须由环境变量显式提供的连接项。
type strictEndpoint struct {
	// env 是环境变量名（与契约注册表一致）。
	env string
	// label 是错误信息中的中文说明。
	label string
	// hostPort 为 true 表示该值是 host:port 形式，需要拆出 host 判断回环。
	hostPort bool
}

// strictEndpoints 是严格模式要求显式注入的关键连接参数。
//
// 只覆盖「漏配后会静默连本机」的三项：DB Host、DB Name、Redis Address。
// DB 密码/Redis 密码不在此列：它们本就有缺失即启动失败的独立校验。
var strictEndpoints = []strictEndpoint{
	{env: MustSpecOf(KeyDatabaseDefaultHost).Env, label: "数据库主机", hostPort: false},
	{env: MustSpecOf(KeyDatabaseDefaultName).Env, label: "数据库名", hostPort: false},
	{env: MustSpecOf(KeyRedisDefaultAddress).Env, label: "Redis 地址", hostPort: true},
}

// RequireExternalEndpoints 返回部署期严格开关（deployment.require_external_endpoints）。
func (l *Loader) RequireExternalEndpoints(ctx context.Context) (bool, error) {
	return l.Bool(ctx, KeyDeploymentRequireExternalEndpoints)
}

// RequireExternalEndpoints 使用进程级默认读取器返回严格开关。
func RequireExternalEndpoints(ctx context.Context) (bool, error) {
	return Default().RequireExternalEndpoints(ctx)
}

// ValidateExternalEndpoints 在严格模式下校验关键连接参数。
//
// 严格模式（DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true）的语义不是「能读到一个值」，
// 而是「必须由环境变量显式提供」：即使 config.yaml 里写了 127.0.0.1，也不能通过。
// 这样 Kubernetes ConfigMap 漏配时，进程会带着明确的环境变量名启动失败，而不是
// 静默连接容器内的 localhost（连不上时表现为难以定位的重试超时）。
//
// 开关关闭（默认）时该方法直接返回 nil，本地开发仍可使用 config.yaml 的默认值。
// 该方法必须在建立任何数据库/Redis 连接之前调用。
func (l *Loader) ValidateExternalEndpoints(ctx context.Context) error {
	required, err := l.RequireExternalEndpoints(ctx)
	if err != nil {
		return err
	}
	if !required {
		return nil
	}

	problems := make([]string, 0, len(strictEndpoints))
	for _, endpoint := range strictEndpoints {
		raw, exists := l.lookupEnv(endpoint.env)
		if !exists {
			problems = append(problems, fmt.Sprintf(
				"%s 必须由环境变量 %s 显式提供（严格模式 DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true，不接受配置文件或代码默认值）",
				endpoint.label, endpoint.env,
			))
			continue
		}
		value := strings.TrimSpace(raw)
		if value == "" {
			problems = append(problems, fmt.Sprintf("%s 的环境变量 %s 不得为空", endpoint.label, endpoint.env))
			continue
		}
		host := value
		if endpoint.hostPort {
			host = hostOnly(value)
		}
		if isLoopbackHost(host) {
			problems = append(problems, fmt.Sprintf(
				"环境变量 %s=%q 指向本机回环地址，严格模式禁止回退本机；请指向集群内或外部可达地址",
				endpoint.env, value,
			))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf(
		"Kubernetes 严格配置模式校验失败（%d 项）：\n  - %s",
		len(problems), strings.Join(problems, "\n  - "),
	)
}

// ValidateExternalEndpoints 使用进程级默认读取器执行严格模式校验。
func ValidateExternalEndpoints(ctx context.Context) error {
	return Default().ValidateExternalEndpoints(ctx)
}

// hostOnly 从 host:port 中取出 host；不是 host:port 形式时按纯主机名处理。
func hostOnly(address string) string {
	if host, _, err := net.SplitHostPort(address); err == nil {
		return host
	}
	return strings.Trim(address, "[]")
}

// isLoopbackHost 判断主机名是否为本机回环：localhost、*.localhost、IPv4/IPv6 回环地址。
func isLoopbackHost(host string) bool {
	trimmed := strings.TrimSpace(strings.Trim(host, "[]"))
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	if ip := net.ParseIP(trimmed); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
