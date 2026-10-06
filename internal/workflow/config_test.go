package workflow

import (
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
