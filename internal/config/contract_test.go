package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// configDocPath 是配置契约文档（相对 internal/config 包目录）。
const configDocPath = "../../docs/development/configuration.md"

// TestRegisteredPathsExistInRepositoryConfig 断言每个登记路径都真实存在于
// manifest/config/config.yaml，防止代码登记了配置但 YAML 从未提供（或键名拼写漂移）。
func TestRegisteredPathsExistInRepositoryConfig(t *testing.T) {
	loader := newRepositoryLoader(t)
	data, err := loader.cfg.Data(context.Background())
	if err != nil {
		t.Fatalf("读取仓库 config.yaml: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("仓库 config.yaml 解析结果为空")
	}
	for _, spec := range Specs() {
		if _, ok := lookupConfigPath(data, spec.Path()); !ok {
			t.Errorf("配置项 %s 未出现在 manifest/config/config.yaml", spec.Path())
		}
	}
}

// TestEnvironmentNamesAreUniqueAndCanonical 断言环境变量名唯一，且符合
// 「点分路径大写蛇形」的显式映射规则（防止手写映射出现拼写错误）。
func TestEnvironmentNamesAreUniqueAndCanonical(t *testing.T) {
	seen := make(map[string]Key, len(Specs()))
	for _, spec := range Specs() {
		if spec.Env == "" {
			t.Errorf("配置项 %s 缺少环境变量名", spec.Path())
			continue
		}
		if previous, duplicated := seen[spec.Env]; duplicated {
			t.Errorf("环境变量 %s 被 %s 与 %s 重复使用", spec.Env, previous, spec.Path())
		}
		seen[spec.Env] = spec.Key

		canonical := strings.ToUpper(strings.ReplaceAll(spec.Path(), ".", "_"))
		if spec.Env != canonical {
			t.Errorf("配置项 %s 的环境变量 %s 与约定 %s 不一致", spec.Path(), spec.Env, canonical)
		}
	}
}

// TestEverySpecHasExplicitTypeAndDefault 断言每个配置项都有明确类型与默认值，
// 且默认值的动态类型与声明类型一致（避免 int/int64 混用导致解析分支取错）。
func TestEverySpecHasExplicitTypeAndDefault(t *testing.T) {
	for _, spec := range Specs() {
		if spec.Consumer == "" {
			t.Errorf("配置项 %s 缺少生效路径说明", spec.Path())
		}
		if spec.Description == "" {
			t.Errorf("配置项 %s 缺少用途说明", spec.Path())
		}
		if spec.Default == nil {
			t.Errorf("配置项 %s 缺少默认值（空字符串请显式写 \"\"）", spec.Path())
			continue
		}
		var typeOK bool
		switch spec.Kind {
		case KindString:
			_, typeOK = spec.Default.(string)
		case KindBool:
			_, typeOK = spec.Default.(bool)
		case KindInt:
			_, typeOK = spec.Default.(int)
		case KindInt64:
			_, typeOK = spec.Default.(int64)
		case KindDuration:
			_, typeOK = spec.Default.(time.Duration)
		case KindStringList:
			_, typeOK = spec.Default.([]string)
		default:
			t.Errorf("配置项 %s 的类型 %q 未在测试中覆盖", spec.Path(), spec.Kind)
			continue
		}
		if !typeOK {
			t.Errorf("配置项 %s 的默认值类型与声明类型 %s 不匹配", spec.Path(), spec.Kind)
		}
	}
}

// TestSensitiveClassificationMatchesWhitelist 断言敏感配置的分类与白名单完全一致，
// 且敏感项没有内置默认值（凭据绝不写进代码）。
func TestSensitiveClassificationMatchesWhitelist(t *testing.T) {
	wantSensitive := map[Key]bool{
		KeyDatabaseDefaultPass: true,
		KeyRedisDefaultPass:    true,
		KeyAuthJWTSecret:       true,
		KeyAdminSuperPassword:  true,
		KeyQiniuAccessKey:      true,
		KeyQiniuSecretKey:      true,
	}

	gotSensitive := make(map[Key]bool)
	for _, spec := range Specs() {
		if spec.Sensitive {
			gotSensitive[spec.Key] = true
			if text, ok := spec.Default.(string); ok && text != "" {
				t.Errorf("敏感配置 %s 不得内置默认值", spec.Path())
			}
		}
	}
	for key := range wantSensitive {
		if !gotSensitive[key] {
			t.Errorf("配置项 %s 必须标记为敏感", key)
		}
	}
	for key := range gotSensitive {
		if !wantSensitive[key] {
			t.Errorf("配置项 %s 被标记为敏感，但不在凭据白名单内（请确认是否真的含凭据）", key)
		}
	}
}

// TestDocumentationMatchesContract 断言 docs/development/configuration.md 的映射表
// 与注册表逐行一致（路径/环境变量/类型/默认值/敏感级别/生效路径）。
func TestDocumentationMatchesContract(t *testing.T) {
	documented := readDocumentedRows(t)
	expected := renderContractRows()

	if len(documented) != len(expected) {
		t.Fatalf("文档映射表行数 %d 与注册表条目数 %d 不一致", len(documented), len(expected))
	}
	for index := range expected {
		if documented[index] != expected[index] {
			t.Errorf("文档第 %d 行与契约不一致：\n  文档: %s\n  契约: %s", index+1, documented[index], expected[index])
		}
	}
}

// TestDeploymentStrictSwitchIsDocumentedAndRegistered 断言 T2-01 的严格模式开关
// 同时存在于契约、仓库配置与文档中。
func TestDeploymentStrictSwitchIsDocumentedAndRegistered(t *testing.T) {
	spec, ok := SpecOf(KeyDeploymentRequireExternalEndpoints)
	if !ok {
		t.Fatal("deployment.require_external_endpoints 未登记到契约")
	}
	if spec.Env != "DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS" {
		t.Fatalf("严格模式环境变量名错误: %s", spec.Env)
	}
	if spec.Kind != KindBool {
		t.Fatalf("严格模式开关类型必须为 bool，实际 %s", spec.Kind)
	}
	if value, ok := spec.Default.(bool); !ok || value {
		t.Fatalf("严格模式开关默认值必须为 false，实际 %v", spec.Default)
	}
}

// lookupConfigPath 按点分路径在配置数据中查找值，不做任何键归一化。
func lookupConfigPath(data map[string]any, path string) (any, bool) {
	var current any = data
	for _, segment := range strings.Split(path, ".") {
		node, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, exists := node[segment]
		if !exists {
			return nil, false
		}
		current = value
	}
	return current, true
}

// renderContractRows 按文档表格格式渲染注册表，生成期望行。
func renderContractRows() []string {
	rows := make([]string, 0, len(Specs()))
	for _, spec := range Specs() {
		sensitive := "非敏感"
		if spec.Sensitive {
			sensitive = "敏感"
		}
		rows = append(rows, fmt.Sprintf(
			"| `%s` | `%s` | %s | %s | %s | %s |",
			spec.Path(), spec.Env, spec.Kind, spec.DefaultText(), sensitive, spec.Consumer,
		))
	}
	return rows
}

// readDocumentedRows 只解析文档「## 8. 完整配置映射表」章节内的表格数据行。
func readDocumentedRows(t *testing.T) []string {
	t.Helper()
	absolute, err := filepath.Abs(configDocPath)
	if err != nil {
		t.Fatalf("解析文档路径: %v", err)
	}
	content, err := os.ReadFile(absolute)
	if err != nil {
		t.Fatalf("读取配置文档 %s: %v", absolute, err)
	}

	var (
		rows      []string
		inSection bool
	)
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trimmed, "## ") {
			inSection = strings.HasPrefix(trimmed, "## 8.")
			continue
		}
		if !inSection || !strings.HasPrefix(trimmed, "| `") {
			continue
		}
		rows = append(rows, trimmed)
	}
	if len(rows) == 0 {
		t.Fatal("文档中未找到「## 8. 完整配置映射表」的表格行")
	}
	return rows
}
