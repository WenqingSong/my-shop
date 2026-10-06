package workflow

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// 本文件实现 state.yaml 的版本化解析：schema v3（generic）与 schema v2（legacy）。
//
// Generic Core 只认识 resources.reservations.<kind> = []opaque-value；
// schema v2 的 resources.migrations / resources.error_code_domains 是历史输入，
// 在此兼容层被 normalize 为 generic reservations，不写回磁盘（历史 Task Artifact 保持原样）。

// stateFile 是 state.yaml 的磁盘表示。
// resources 用 raw node 映射捕获，以便精确区分「字段存在但为空」与「字段不存在」，
// 从而在 v2/v3 之间严格拒绝混用 legacy 与 generic 资源字段（避免半迁移 State）。
type stateFile struct {
	SchemaVersion int                  `yaml:"schema_version"`
	TaskID        string               `yaml:"task_id"`
	Contract      Contract             `yaml:"contract"`
	Resources     map[string]yaml.Node `yaml:"resources"`
	Review        Review               `yaml:"review"`
	Owner         Owner                `yaml:"owner"`
	Delivery      Delivery             `yaml:"delivery"`
	Blocked       Blocked              `yaml:"blocked"`
}

// parseState 解析 state.yaml 并 normalize 为 generic State。
// 返回 error 属于 schema / 输入 ERROR（exit 2），不属于业务 Gate FAIL。
func parseState(raw []byte) (State, error) {
	var f stateFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return State{}, fmt.Errorf("解析 state.yaml: %w", err)
	}
	s := State{
		SchemaVersion: f.SchemaVersion,
		TaskID:        f.TaskID,
		Contract:      f.Contract,
		Review:        f.Review,
		Owner:         f.Owner,
		Delivery:      f.Delivery,
		Blocked:       f.Blocked,
	}
	res, err := normalizeResources(f.SchemaVersion, f.Resources)
	if err != nil {
		return State{}, err
	}
	s.Resources = res
	if err := validateReservations(s.Resources.Reservations); err != nil {
		return State{}, err
	}
	return s, nil
}

// normalizeResources 按 schema_version 归一化资源字段为 generic Reservations。
func normalizeResources(schemaVersion int, raw map[string]yaml.Node) (Resources, error) {
	_, hasLegacyMigrations := raw["migrations"]
	_, hasLegacyDomains := raw["error_code_domains"]
	_, hasReservations := raw["reservations"]

	switch schemaVersion {
	case SchemaV3:
		if hasLegacyMigrations || hasLegacyDomains {
			return Resources{}, fmt.Errorf("schema_version=3 不允许 legacy 资源字段 resources.migrations / resources.error_code_domains，请使用 resources.reservations")
		}
		if !hasReservations {
			return Resources{Reservations: map[string][]string{}}, nil
		}
		var reservations map[string][]string
		if err := decodeNode(raw, "reservations", &reservations); err != nil {
			return Resources{}, err
		}
		return Resources{Reservations: reservations}, nil

	case SchemaV2:
		if hasReservations {
			return Resources{}, fmt.Errorf("schema_version=2 不允许 generic 字段 resources.reservations，请使用 legacy migrations / error_code_domains")
		}
		res := map[string][]string{}
		if hasLegacyMigrations {
			var m []string
			if err := decodeNode(raw, "migrations", &m); err != nil {
				return Resources{}, err
			}
			if len(m) > 0 {
				res["migration_version"] = m
			}
		}
		if hasLegacyDomains {
			var d []string
			if err := decodeNode(raw, "error_code_domains", &d); err != nil {
				return Resources{}, err
			}
			if len(d) > 0 {
				res["error_code_domain"] = d
			}
		}
		return Resources{Reservations: res}, nil

	default:
		return Resources{}, fmt.Errorf("不支持的 state schema_version=%d（期望 %d 或 %d）", schemaVersion, SchemaV2, SchemaV3)
	}
}

// decodeNode 从 raw resources 映射中取出 key 对应的 node 并解码到 out。
// yaml.Node.Decode 是指针方法，map 索引结果不可寻址，故先赋局部变量。
func decodeNode(raw map[string]yaml.Node, key string, out interface{}) error {
	node := raw[key]
	if err := node.Decode(out); err != nil {
		return fmt.Errorf("解析 resources.%s: %w", key, err)
	}
	return nil
}

// validateReservations 校验 generic reservations 的 kind 名与 value 非空。
// 非法 kind / 空 value 属于 schema ERROR（exit 2）。
func validateReservations(reservations map[string][]string) error {
	for _, kind := range sortedKeys(reservations) {
		if !ValidResourceKind(kind) {
			return fmt.Errorf("非法 resource kind %q（应为 ^[a-z][a-z0-9_]*$）", kind)
		}
		for _, v := range reservations[kind] {
			if v == "" {
				return fmt.Errorf("resource kind %q 的 reservation value 不得为空", kind)
			}
		}
	}
	return nil
}

// sortedKeys 返回 map 的 key 升序排序结果，保证验证与错误输出顺序确定。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
