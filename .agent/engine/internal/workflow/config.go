package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigSchemaV1 是 .agent/workflow.yaml 当前唯一支持的 schema_version。
const ConfigSchemaV1 = 1

// DefaultConfigPath 是 Workflow Engine runtime machine configuration 的固定路径（相对仓库根）。
const DefaultConfigPath = ".agent/workflow.yaml"

// registryRoot 是 Registry 文件必须严格位于其下的目录（相对仓库根）。
// Analyst 的 Registry-only shared-develop mutation authority 仅限该目录内的文件。
const registryRoot = ".agent/registry"

// Config 是 Workflow Engine 的机器配置（Machine Configuration）。
// 它只保存「跨项目会变化、且 Workflow Engine runtime 必须知道的最小参数」。
// 它不是 PROJECT_ADAPTATION.md、Task Artifact、业务配置。
type Config struct {
	SchemaVersion int                       `yaml:"schema_version"`
	Git           GitConfig                 `yaml:"git"`
	Resources     map[string]ResourceConfig `yaml:"resources"`
}

// GitConfig 记录 Git 集成模型的最小参数。
type GitConfig struct {
	IntegrationBranch string `yaml:"integration_branch"`
}

// ResourceConfig 记录单个 resource kind 的 Registry 文件路径（相对仓库根）。
//
// P2 起 resources 是 arbitrary map：resource kind → ResourceConfig，
// Workflow Core 不预置任何固定 kind，migration_version / error_code_domain
// 只是当前宿主项目 Project Config 声明的 kind。
type ResourceConfig struct {
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
	// KnownFields(true)：拒绝 machine config 中未定义的结构字段（typo / 未知字段），
	// fail closed 而不是静默忽略。resources.<kind> 是动态 map key，不受 KnownFields 限制，
	// kind 与 registry 仍由 validateConfig 校验。
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
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

	// registry path -> kind，用于检测两个不同 kind 声明完全相同的 Registry 文件。
	// 共享同一无 kind discriminator 的 Registry 会造成解析歧义，故视为 Config ERROR。
	seen := map[string]string{}
	for _, kind := range sortedKeys(c.Resources) {
		if !ValidResourceKind(kind) {
			return fmt.Errorf("非法 resource kind %q（应为 ^[a-z][a-z0-9_]*$）", kind)
		}
		rc := c.Resources[kind]
		if rc.Registry == "" {
			return fmt.Errorf("resources.%s.registry 不得为空", kind)
		}
		if err := validateRegistryPath(rc.Registry); err != nil {
			return fmt.Errorf("resources.%s: %w", kind, err)
		}
		if prev, ok := seen[rc.Registry]; ok {
			return fmt.Errorf("resources.%s 与 resources.%s 声明了相同的 registry 路径 %q", kind, prev, rc.Registry)
		}
		seen[rc.Registry] = kind
	}
	return nil
}

// validateRegistryPath 校验 registry 路径必须是仓库相对路径，且规范化后严格位于 .agent/registry/ 下。
// 空串由 validateConfig 在调用前显式拒绝，本函数只做路径安全校验。
func validateRegistryPath(p string) error {
	if p == "" {
		return nil
	}
	if filepath.IsAbs(p) {
		return fmt.Errorf("registry 路径必须是仓库相对路径，不能是绝对路径: %q", p)
	}
	cleaned := filepath.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("registry 路径不得穿越出 .agent/registry/: %q", p)
	}
	prefix := registryRoot + string(filepath.Separator)
	if !strings.HasPrefix(cleaned, prefix) {
		return fmt.Errorf("registry 路径必须严格位于 %s/ 下: %q", registryRoot, p)
	}
	return nil
}
