//go:build serve_lifecycle

// 本文件只在 `-tags serve_lifecycle` 构建时编译，生产二进制永远不会包含它。
package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/os/gctx"
	qstorage "github.com/qiniu/go-sdk/v7/storage"

	"cnb.cool/go-cloud-devops/my-shop/internal/cmd"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
)

// run 装配与生产 main.go 一致，仅在显式提供测试 endpoint 时改写七牛 UC 解析地址。
//
// 为什么只改 UC 地址：upload.ValidateConfig 的真实校验逻辑（结构 → 必填 → GetBucketInfo）
// 完全保留；SDK 仍会按真实协议签名并请求 /v4/query 与 /v2/bucketInfo，只是这两步指向
// 本地 Mock。生产二进制不读取该环境变量，也不存在任何跳过校验的开关。
const ucHostEnv = "SERVE_LIFECYCLE_QINIU_UC_HOST"

func run() {
	ucHost, err := ucHostOverride()
	if err != nil {
		fmt.Fprintf(os.Stderr, "非法的 %s: %v\n", ucHostEnv, err)
		os.Exit(2)
	}
	if ucHost != "" {
		qstorage.SetUcHosts(ucHost)
		fmt.Fprintf(os.Stderr, "serve_lifecycle: 七牛 UC endpoint 指向本地 Mock %s（真实校验逻辑不变）\n", ucHost)
	}
	cmd.Main.Run(gctx.GetInitCtx())
}

// ucHostOverride 读取并校验测试 endpoint：只允许 http/https 的 loopback 地址，
// 防止该测试构建被误用为「指向外部地址」的校验绕过。
func ucHostOverride() (string, error) {
	raw := strings.TrimSpace(os.Getenv(ucHostEnv))
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("endpoint 必须使用 http/https，实际 %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("endpoint 缺少 host: %q", raw)
	}
	if host := u.Hostname(); host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", fmt.Errorf("endpoint 必须是 loopback 地址，实际 host %q", host)
		}
	}
	return raw, nil
}
