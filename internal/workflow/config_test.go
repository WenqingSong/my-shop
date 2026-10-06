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
	if cfg.Resources.MigrationVersion.Registry != ".agent/registry/migrations.md" {
		t.Fatalf("migration registry=%q", cfg.Resources.MigrationVersion.Registry)
	}
	if cfg.Resources.ErrorCodeDomain.Registry != ".agent/registry/error-codes.md" {
		t.Fatalf("error-code registry=%q", cfg.Resources.ErrorCodeDomain.Registry)
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

// registryConfig 构造一份 v1 机器配置，用于指定两个 registry 路径。
func registryConfig(migrationPath, errorCodePath string) string {
	return fmt.Sprintf("schema_version: 1\ngit:\n  integration_branch: develop\nresources:\n  migration_version:\n    registry: %q\n  error_code_domain:\n    registry: %q\n", migrationPath, errorCodePath)
}

func TestLoadConfigRegistryPathValid(t *testing.T) {
	for _, p := range []string{
		".agent/registry/migrations.md",
		".agent/registry/error-codes.md",
		".agent/registry/im-migrations.md",
	} {
		t.Run(p, func(t *testing.T) {
			_, err := LoadConfig(writeConfigFile(t, registryConfig(p, ".agent/registry/error-codes.md")))
			if err != nil {
				t.Fatalf("合法 registry 路径 %q 不应报错: %v", p, err)
			}
		})
	}
}

func TestLoadConfigRegistryPathInvalid(t *testing.T) {
	valid := ".agent/registry/error-codes.md"
	illegal := []string{
		"docs/design/foo.md",
		"../foo.md",
		".agent/registry/../../README.md",
		"/tmp/foo.md",
	}
	for _, p := range illegal {
		t.Run("migration/"+p, func(t *testing.T) {
			_, err := LoadConfig(writeConfigFile(t, registryConfig(p, valid)))
			if err == nil {
				t.Fatalf("非法 migration registry 路径 %q 应报错", p)
			}
			if !strings.Contains(err.Error(), "registry") {
				t.Fatalf("期望错误涉及 registry，实际 %v", err)
			}
		})
		t.Run("error-code/"+p, func(t *testing.T) {
			_, err := LoadConfig(writeConfigFile(t, registryConfig(valid, p)))
			if err == nil {
				t.Fatalf("非法 error-code registry 路径 %q 应报错", p)
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
