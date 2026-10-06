package workflow

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ConfigSchemaV1 是 .agent/workflow.yaml 当前唯一支持的 schema_version。
const ConfigSchemaV1 = 1

// DefaultConfigPath 是 Workflow Engine runtime machine configuration 的固定路径（相对仓库根）。
const DefaultConfigPath = ".agent/workflow.yaml"

// Config 是 Workflow Engine 的机器配置（Machine Configuration）。
// 它只保存「跨项目会变化、且 Workflow Engine runtime 必须知道的最小参数」。
// 它不是 PROJECT_ADAPTATION.md、Task Artifact、业务配置。
type Config struct {
	SchemaVersion int            `yaml:"schema_version"`
	Git           GitConfig      `yaml:"git"`
	Resources     ResourceConfig `yaml:"resources"`
}

// GitConfig 记录 Git 集成模型的最小参数。
type GitConfig struct {
	IntegrationBranch string `yaml:"integration_branch"`
}

// ResourceConfig 记录资源 kind → registry 文件的映射。
// 本轮只有 migration_version 与 error_code_domain 两个固定 kind，
// 与 state.resources 的现有 schema（migrations / error_code_domains）保持兼容桥接；
// P2 才泛化为通用 resource model，本轮不扩展。
type ResourceConfig struct {
	MigrationVersion ResourceRegistry `yaml:"migration_version"`
	ErrorCodeDomain  ResourceRegistry `yaml:"error_code_domain"`
}

// ResourceRegistry 记录单个资源 kind 的 Registry 文件路径（相对仓库根）。
type ResourceRegistry struct {
	Registry string `yaml:"registry"`
}

// LoadConfig 从 root 读取并校验 .agent/workflow.yaml。
func LoadConfig(root string) (*Config, error) {
	raw, err := os.ReadFile(filepath.Join(root, DefaultConfigPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("读取机器配置 %s: 文件不存在", DefaultConfigPath)
		}
		return nil, fmt.Errorf("读取机器配置 %s: %w", DefaultConfigPath, err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("解析机器配置 %s: %w", DefaultConfigPath, err)
	}
	if err := validateConfig(c); err != nil {
		return nil, err
	}
	return &c, nil
}

func validateConfig(c Config) error {
	if c.SchemaVersion != ConfigSchemaV1 {
		return fmt.Errorf("不支持的 schema_version=%d（期望 %d）", c.SchemaVersion, ConfigSchemaV1)
	}
	if c.Git.IntegrationBranch == "" {
		return fmt.Errorf("缺少必填 git.integration_branch")
	}
	return nil
}
