package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfigFile 在临时目录写入 .agent/workflow.yaml；content 为空表示不写文件。
func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if content != "" {
		if err := os.MkdirAll(filepath.Join(dir, ".agent"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, DefaultConfigPath), []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return dir
}

// configWithResources 构造一份 v1 机器配置，用任意 resource kind → registry 映射（确定性排序）。
func configWithResources(resources map[string]string) string {
	var b strings.Builder
	b.WriteString("schema_version: 1\ngit:\n  integration_branch: develop\nresources:\n")
	for _, kind := range sortedKeys(resources) {
		fmt.Fprintf(&b, "  %s:\n    registry: %q\n", kind, resources[kind])
	}
	return b.String()
}

func TestLoadConfigDefault(t *testing.T) {
	dir := writeConfigFile(t, `schema_version: 1
git:
  integration_branch: develop
resources:
  migration_version:
    registry: .agent/registry/migrations.md
  error_code_domain:
    registry: .agent/registry/error-codes.md
`)
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Git.IntegrationBranch != "develop" {
		t.Fatalf("integration_branch=%q，期望 develop", cfg.Git.IntegrationBranch)
	}
	if cfg.Resources["migration_version"].Registry != ".agent/registry/migrations.md" {
		t.Fatalf("migration registry=%q", cfg.Resources["migration_version"].Registry)
	}
	if cfg.Resources["error_code_domain"].Registry != ".agent/registry/error-codes.md" {
		t.Fatalf("error-code registry=%q", cfg.Resources["error_code_domain"].Registry)
	}
}

func TestLoadConfigNonDevelopBranch(t *testing.T) {
	dir := writeConfigFile(t, `schema_version: 1
git:
  integration_branch: main
`)
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Git.IntegrationBranch != "main" {
		t.Fatalf("integration_branch=%q，期望 main", cfg.Git.IntegrationBranch)
	}
}

// TestLoadConfigArbitraryKind 覆盖任意 resource kind 无需修改 Go 代码。
func TestLoadConfigArbitraryKind(t *testing.T) {
	dir := writeConfigFile(t, configWithResources(map[string]string{
		"im_migration_version": ".agent/registry/im-migrations.md",
	}))
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Resources["im_migration_version"].Registry != ".agent/registry/im-migrations.md" {
		t.Fatalf("im_migration_version registry=%q", cfg.Resources["im_migration_version"].Registry)
	}
}

// TestLoadConfigMultipleArbitraryKinds 覆盖多个任意 kind。
func TestLoadConfigMultipleArbitraryKinds(t *testing.T) {
	dir := writeConfigFile(t, configWithResources(map[string]string{
		"im_migration_version":  ".agent/registry/im-migrations.md",
		"idp_migration_version": ".agent/registry/idp-migrations.md",
		"nats_subject":          ".agent/registry/nats-subjects.md",
	}))
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Resources) != 3 {
		t.Fatalf("期望 3 个 kind，实际 %d", len(cfg.Resources))
	}
}

func TestLoadConfigInvalidKindName(t *testing.T) {
	for _, kind := range []string{"MigrationVersion", "migration-version", "foo.bar", "1foo"} {
		t.Run(kind, func(t *testing.T) {
			dir := writeConfigFile(t, configWithResources(map[string]string{
				kind: ".agent/registry/foo.md",
			}))
			_, err := LoadConfig(dir)
			if err == nil {
				t.Fatalf("非法 kind %q 应报错", kind)
			}
			if !strings.Contains(err.Error(), "resource kind") {
				t.Fatalf("期望错误涉及 resource kind，实际 %v", err)
			}
		})
	}
}

func TestLoadConfigMissingRegistryPath(t *testing.T) {
	dir := writeConfigFile(t, configWithResources(map[string]string{
		"migration_version": "",
	}))
	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("空 registry 路径应报错")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Fatalf("期望错误涉及 registry，实际 %v", err)
	}
}

func TestLoadConfigRegistryPathValid(t *testing.T) {
	for _, p := range []string{
		".agent/registry/migrations.md",
		".agent/registry/error-codes.md",
		".agent/registry/im-migrations.md",
	} {
		t.Run(p, func(t *testing.T) {
			_, err := LoadConfig(writeConfigFile(t, configWithResources(map[string]string{
				"migration_version": p,
			})))
			if err != nil {
				t.Fatalf("合法 registry 路径 %q 不应报错: %v", p, err)
			}
		})
	}
}

func TestLoadConfigRegistryPathInvalid(t *testing.T) {
	illegal := []string{
		"docs/design/foo.md",
		"../foo.md",
		".agent/registry/../../README.md",
		"/tmp/foo.md",
	}
	for _, p := range illegal {
		t.Run(p, func(t *testing.T) {
			_, err := LoadConfig(writeConfigFile(t, configWithResources(map[string]string{
				"migration_version": p,
			})))
			if err == nil {
				t.Fatalf("非法 registry 路径 %q 应报错", p)
			}
			if !strings.Contains(err.Error(), "registry") {
				t.Fatalf("期望错误涉及 registry，实际 %v", err)
			}
		})
	}
}

// TestLoadConfigDuplicateRegistryPath 覆盖两个不同 kind 声明相同 registry 路径 → ERROR。
func TestLoadConfigDuplicateRegistryPath(t *testing.T) {
	dir := writeConfigFile(t, configWithResources(map[string]string{
		"migration_version": ".agent/registry/shared.md",
		"error_code_domain": ".agent/registry/shared.md",
	}))
	_, err := LoadConfig(dir)
	if err == nil {
		t.Fatal("重复 registry 路径应报错")
	}
	if !strings.Contains(err.Error(), "相同") {
		t.Fatalf("期望错误涉及重复路径，实际 %v", err)
	}
}

// TestLoadConfigStrictUnknownField 覆盖 P2.1：git / resources / 顶层结构字段 typo → ERROR。
func TestLoadConfigStrictUnknownField(t *testing.T) {
	tests := []struct {
		name string
		yml  string
		want string
	}{
		{
			"git.integration_branhc typo",
			`schema_version: 1
git:
  integration_branhc: develop
`,
			"integration_branhc",
		},
		{
			"resources.registri typo",
			`schema_version: 1
git:
  integration_branch: develop
resources:
  migration_version:
    registri: .agent/registry/migrations.md
`,
			"registri",
		},
		{
			"top-level unknown field",
			`schema_version: 1
git:
  integration_branch: develop
resourcess:
  migration_version:
    registry: .agent/registry/migrations.md
`,
			"resourcess",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfig(writeConfigFile(t, tt.yml))
			if err == nil {
				t.Fatalf("期望错误含 %q，实际 nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("期望错误含 %q，实际 %v", tt.want, err)
			}
		})
	}
}

func TestLoadConfigErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string // 空串表示不写文件
		wantSub string
	}{
		{"config missing", "", "文件不存在"},
		{"malformed yaml", "schema_version: [\n", "解析机器配置"},
		{"unsupported schema", "schema_version: 2\ngit:\n  integration_branch: develop\n", "schema_version"},
		{"missing integration branch", "schema_version: 1\ngit:\n  integration_branch: \"\"\n", "integration_branch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfig(writeConfigFile(t, tt.content))
			if err == nil {
				t.Fatalf("期望错误含 %q，实际 nil", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("期望错误含 %q，实际 %v", tt.wantSub, err)
			}
		})
	}
}
