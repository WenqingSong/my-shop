//go:build serve_lifecycle

package cmd

import "github.com/gogf/gf/v2/net/ghttp"

// ServeLifecycleHook 仅存在于 serve_lifecycle 测试构建：进程级生命周期测试用它
// 注入「阻塞超过 drain 预算」的测试路由。生产构建不包含该符号，也不存在任何
// 改变终止预算或跳过关闭步骤的入口。
var ServeLifecycleHook func(*ghttp.Server)

// applyServeLifecycleHook 在测试构建中调用注入的 hook；未注入时为空操作。
func applyServeLifecycleHook(s *ghttp.Server) {
	if ServeLifecycleHook != nil {
		ServeLifecycleHook(s)
	}
}
