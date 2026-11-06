//go:build !serve_lifecycle

package cmd

import "github.com/gogf/gf/v2/net/ghttp"

// applyServeLifecycleHook 生产构建为空实现：不注册额外路由，也不覆盖终止预算。
func applyServeLifecycleHook(*ghttp.Server) {}
