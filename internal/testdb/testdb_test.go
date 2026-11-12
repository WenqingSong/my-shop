package testdb_test

import (
	"os"
	"regexp"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/internal/testdb"
)

// TestIsAllowed 固化安全边界：CI 库与本地隔离库放行，其余一律拒绝。
// 特别断言「任意 *_test 后缀」不会被放行，防止把白名单退化为宽松前缀匹配。
func TestIsAllowed(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"my_shop_ci_test", true},
		{"my_shop_b104_test", true},
		{"my_shop_b104_integration", true},
		{"my_shop", false},
		{"my_shop_staging", false},
		{"my_shop_b10304_verify", false},
		{"", false},
		{"  ", false},
		{"my_shop_random_test", false},
		{"my_shop_ci_test_evil", false},
		{"mysql", false},
		{"information_schema", false},
	}
	for _, tc := range cases {
		if got := testdb.IsAllowed(tc.name); got != tc.want {
			t.Errorf("IsAllowed(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestCIConfiguredDatabasesAreAllowed 防止 CI 配置与 Go 守卫再次漂移：
// .cnb.yml 中所有 MYSQL_DATABASE / DATABASE_DEFAULT_NAME 必须是白名单库。
func TestCIConfiguredDatabasesAreAllowed(t *testing.T) {
	const path = "../../.cnb.yml"
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	re := regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:DATABASE_DEFAULT_NAME|MYSQL_DATABASE)[=:]\s*"?([A-Za-z0-9_]+)"?\s*$`)
	matches := re.FindAllStringSubmatch(string(content), -1)
	if len(matches) == 0 {
		t.Fatalf("%s 中未解析到 DATABASE_DEFAULT_NAME / MYSQL_DATABASE 配置", path)
	}
	for _, m := range matches {
		if !testdb.IsAllowed(m[1]) {
			t.Errorf("CI 配置的数据库 %q 不在 Go 测试白名单 %v 中：Shell 放行但 Go 守卫会拒绝", m[1], testdb.Allowed())
		}
	}
}
