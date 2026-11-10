// Command openapi-gen 从 Go DTO 生成并校验 OpenAPI 3.1 契约。
//
// 设计约束：
//   - api/*/v1/*.go 是字段的唯一事实源，禁止在 YAML 中手工补字段；
//   - internal/codes/codes.go 是错误码的唯一事实源；
//   - internal/cmd/routes_*.go 是认证与权限码的唯一事实源；
//   - 生成结果必须确定性：同一输入重复运行产生完全相同的文件。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	repo := flag.String("repo", ".", "仓库根目录")
	doCheck := flag.Bool("check", false, "执行契约一致性校验（不写文件）")
	flag.Parse()

	abs, err := filepath.Abs(*repo)
	if err != nil {
		fail(err)
	}
	if *doCheck {
		if err := check(abs); err != nil {
			fail(err)
		}
		return
	}
	if err := run(abs); err != nil {
		fail(err)
	}
	fmt.Println("openapi 契约生成完成：openapi/surgecart.openapi.yaml")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "openapi-gen 失败:", err)
	os.Exit(1)
}
