//go:build !serve_lifecycle

package main

import (
	"fmt"
	"os"
)

// run 在未启用 serve_lifecycle 构建标签时给出明确提示。
//
// 保留一个可编译的占位实现，是为了让 `go build ./...` / `go vet ./...` 不会因为
// 「目录下所有文件都被 build tag 排除」而失败；该占位二进制不会启动任何服务。
func run() {
	fmt.Fprintln(os.Stderr, "test/serveproc 仅用于 B4.1 进程级生命周期测试，请使用 -tags serve_lifecycle 构建")
	os.Exit(2)
}
