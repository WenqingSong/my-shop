// Package main 是 B4.1 进程级生命周期测试专用的 serve 入口（test-only）。
//
// 它与生产入口 main.go 使用完全相同的命令装配（internal/cmd → serve → boot.Bootstrap →
// service.Upload().ValidateConfig → worker.StartFlashSaleRuntime → ghttp Server.Run），
// 唯一差异是允许把七牛「UC 域名解析 endpoint」指向本地 Mock 服务，以便在没有真实七牛
// 凭据的 CI 环境里验证真实 serve 启动链路。生产二进制不包含本目录代码。
//
// 构建：go build -tags serve_lifecycle -o <bin> ./test/serveproc
package main

func main() {
	run()
}
