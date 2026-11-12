// Package testdb 定义破坏性数据库测试允许使用的隔离库白名单。
//
// 背景：address / region / profile / regiondata / freight 等测试会 DELETE 或
// DROP 业务表，必须 fail-closed 地确认目标库是隔离测试库。此前各包各自维护
// 硬编码列表，导致 CI（my_shop_ci_test）与本地（my_shop_b104_test）不一致：
// Shell 层放行、进入 Go 测试后被拒绝。
//
// 本包是唯一可信来源：
//   - 白名单逐库显式列名，绝不接受任意以 _test 结尾的库名；
//   - 新增测试库必须显式登记，并在 internal/testdb/testdb_test.go 覆盖；
//   - 生产/开发库（my_shop / my_shop_staging / my_shop_b10304_verify）永不登记。
package testdb

import "strings"

// allowed 是允许执行破坏性测试的隔离库（逐库显式列名，顺序仅用于错误信息展示）。
var allowed = []string{
	"my_shop_b104_test",        // 本地/B1-04 集成测试库
	"my_shop_b104_integration", // B1-04 集成备用库
	"my_shop_ci_test",          // CI（.cnb.yml）专用隔离库
}

// Allowed 返回白名单副本，供测试错误信息展示；调用方不得修改内部状态。
func Allowed() []string {
	out := make([]string, len(allowed))
	copy(out, allowed)
	return out
}

// IsAllowed 判断库名是否在白名单内。
// 空值、生产/开发库、未登记的任何库名（包括任意 *_test）一律返回 false。
func IsAllowed(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, item := range allowed {
		if name == item {
			return true
		}
	}
	return false
}
