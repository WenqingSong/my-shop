package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// FlowList 以 YAML 流式（[a, b]）输出数组，用于 type/enum 等短列表。
type FlowList []any

// OMap 是保持声明顺序的 YAML 映射，用于生成确定性、可读的契约文件。
type OMap struct {
	keys []string
	vals map[string]any
}

// NewOMap 创建有序映射。
func NewOMap() *OMap {
	return &OMap{vals: map[string]any{}}
}

// Set 追加或覆盖一个键；覆盖时保持首次插入位置。
func (m *OMap) Set(k string, v any) *OMap {
	if _, ok := m.vals[k]; !ok {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
	return m
}

// SetIf 仅在条件成立时写入。
func (m *OMap) SetIf(cond bool, k string, v any) *OMap {
	if cond {
		m.Set(k, v)
	}
	return m
}

// Get 读取一个键。
func (m *OMap) Get(k string) (any, bool) {
	v, ok := m.vals[k]
	return v, ok
}

// Len 返回键数量。
func (m *OMap) Len() int { return len(m.keys) }

// Keys 返回有序键。
func (m *OMap) Keys() []string { return m.keys }

// MarshalJSON 用于契约一致性深比较（键顺序无关）。
func (m *OMap) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.vals)
}

// toYAMLNode 递归构造 yaml.Node，保证键顺序与字符串安全引用。
func toYAMLNode(v any) (*yaml.Node, error) {
	switch t := v.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	case *OMap:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, k := range t.keys {
			kn := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
			vn, err := toYAMLNode(t.vals[k])
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, kn, vn)
		}
		return n, nil
	case map[string]any:
		om := NewOMap()
		for _, k := range sortedKeys(t) {
			om.Set(k, t[k])
		}
		return toYAMLNode(om)
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range t {
			in, err := toYAMLNode(item)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, in)
		}
		return n, nil
	case FlowList:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, item := range t {
			in, err := toYAMLNode(item)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, in)
		}
		return n, nil
	case []string:
		items := make([]any, 0, len(t))
		for _, s := range t {
			items = append(items, s)
		}
		return toYAMLNode(items)
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprintf("%t", t)}, nil
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", t)}, nil
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", t)}, nil
	case float64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: fmt.Sprintf("%v", t)}, nil
	default:
		return nil, fmt.Errorf("不支持的 YAML 值类型 %T", v)
	}
}

// writeYAML 将有序映射写入文件（已存在则覆盖），并保证目录存在。
func writeYAML(path string, m *OMap) error {
	node, err := toYAMLNode(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return f.Close()
}

// readYAML 读取 YAML 文件为通用结构。
func readYAML(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out any
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}
