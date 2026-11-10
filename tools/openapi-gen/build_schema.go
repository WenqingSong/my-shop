package main

import (
	"fmt"
	"strings"
)

// schemaLocalRefPrefix 用于 schemas.yaml 文件内部的相互引用。
const schemaLocalRefPrefix = "#/"

// schemaMainRefPrefix 用于主文档 paths 中对 components.schemas 的引用。
const schemaMainRefPrefix = "#/components/schemas/"

// buildSchemas 生成全部组件 Schema（按 DTO 声明顺序，结果确定性）。
func buildSchemas(defs map[string]*StructDef, order []string) (*OMap, error) {
	out := NewOMap()
	seen := map[string]string{}
	for _, key := range order {
		def, ok := defs[key]
		if !ok {
			return nil, fmt.Errorf("内部错误：%s 缺失结构体定义", key)
		}
		if prev, ok := seen[def.SchemaName]; ok {
			return nil, fmt.Errorf("Schema 名冲突：%s 与 %s 均映射为 %q", prev, key, def.SchemaName)
		}
		seen[def.SchemaName] = key

		schema, err := buildStructSchema(def)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		out.Set(def.SchemaName, schema)
	}
	return out, nil
}

// buildStructSchema 把一个 DTO 转成 JSON Schema（OpenAPI 3.1 Dialect）。
func buildStructSchema(def *StructDef) (*OMap, error) {
	s := NewOMap().Set("type", "object")
	if def.Doc != "" {
		s.Set("description", def.Doc)
	}

	var required []any
	props := NewOMap()
	var allOf []any

	for _, f := range def.Fields {
		if f.Embedded {
			allOf = append(allOf, NewOMap().Set("$ref", schemaLocalRefPrefix+f.Type.Ref))
			continue
		}
		fs, err := buildFieldSchema(def, f, schemaLocalRefPrefix)
		if err != nil {
			return nil, fmt.Errorf("字段 %s: %w", f.JSONName, err)
		}
		props.Set(f.JSONName, fs)
		if isFieldRequired(def.Shape, f) {
			required = append(required, f.JSONName)
		}
	}

	if len(allOf) > 0 {
		s.Set("allOf", allOf)
	}
	if len(required) > 0 {
		s.Set("required", required)
	}
	if props.Len() > 0 {
		s.Set("properties", props)
	} else if len(allOf) == 0 {
		// 空 DTO（如 LogoutRes）在 Go 中序列化为 {}。
		s.Set("properties", NewOMap())
		s.Set("additionalProperties", false)
	}
	return s, nil
}

// isFieldRequired 按 Go 实际序列化语义决定 required：
//   - 请求 DTO：仅 v:"required" 字段必填；
//   - 响应 DTO：非 omitempty 字段一定出现在 JSON 中（值为 null 也算出现）。
func isFieldRequired(shape string, f *Field) bool {
	if shape == "request" {
		return f.Required()
	}
	return !f.OmitEmpty
}

// buildFieldSchema 构造字段级 Schema。
func buildFieldSchema(def *StructDef, f *Field, refPrefix string) (*OMap, error) {
	s := typeToSchema(f.Type, refPrefix)

	desc := f.Dc
	if isMoneyField(f) {
		s.Set("x-unit", "cent")
		if !strings.Contains(desc, "分") {
			desc = strings.TrimSpace(desc) + "；单位：分（整数）"
		}
	}

	if f.Type.Kind == KindScalar {
		switch f.Type.Scalar {
		case "gtime":
			if def.Shape == "request" {
				s.Set("x-format", "gtime-lenient")
				desc = appendDesc(desc, "请求侧为宽松解析：支持 YYYY-MM-DD HH:MM:SS、YYYY-MM-DD、RFC3339 等格式，无偏移值按服务端时区解释")
			} else {
				s.Set("pattern", `^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)
				s.Set("example", "2026-11-10 11:27:26")
				s.Set("x-format", "gtime-local")
				desc = appendDesc(desc, "格式 YYYY-MM-DD HH:MM:SS，不含时区偏移（当前实现限制，非 RFC3339）")
			}
		case "time":
			s.Set("format", "date-time")
			s.Set("x-format", "rfc3339")
			desc = appendDesc(desc, "RFC3339，必须包含时区偏移")
		case "jsonnumber":
			s.Set("x-go-type", "json.Number")
			desc = appendDesc(desc, "接受 JSON 整数或数字字符串（GoFrame 绑定行为）")
		}
	}

	// 金额统一声明为整数分；指针金额同样标注单位。
	if f.Type.Pointer && f.Type.Kind == KindScalar {
		if isIntScalar(f.Type.Scalar) && isMoneyName(f.JSONName) {
			s.Set("x-unit", "cent")
		}
	}

	if e, ok := enumRegistry[def.SchemaName+"."+f.JSONName]; ok && len(e.Values) > 0 {
		values := make(FlowList, 0, len(e.Values)+1)
		values = append(values, e.Values...)
		if f.Type.Pointer {
			values = append(values, nil)
		}
		s.Set("enum", values)
		if len(e.Labels) > 0 {
			labels := NewOMap()
			for _, kv := range e.Labels {
				labels.Set(kv[0], kv[1])
			}
			s.Set("x-enum-labels", labels)
		}
		if e.Note != "" {
			desc = appendDesc(desc, e.Note)
		}
	}

	// 参数位置与默认值（仅请求 DTO 有意义）。
	if f.In != "" {
		s.Set("x-in", f.In)
	}
	if f.Default != "" {
		s.Set("default", coerceDefault(f.Default, f.Type))
	}
	if f.Embedded {
		s.Set("x-embedded", true)
	}
	if f.OmitEmpty {
		s.Set("x-json-omitempty", true)
	}
	if len(f.V) > 0 {
		s.Set("x-go-validate", strings.Join(f.V, "|"))
	}

	if def.SchemaName == "Order" && f.JSONName == "status" {
		s.Set("x-known-issue", []any{
			NewOMap().
				Set("id", "B1-02-D2").
				Set("summary", "received(40) 为事务内中间态，API 查询实际不可观测；枚举仍保留以匹配 DTO 与 DB"),
		})
	}

	if desc != "" {
		s.Set("description", desc)
	}
	return s, nil
}

func appendDesc(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	switch {
	case base == "":
		return extra
	case strings.Contains(base, extra):
		return base
	default:
		return base + "；" + extra
	}
}

func isIntScalar(s string) bool {
	switch s {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

func isMoneyName(name string) bool {
	return moneyExactFields[name] ||
		strings.HasSuffix(name, "_amount") ||
		strings.HasSuffix(name, "_price")
}

// typeToSchema 把 TypeRef 转为 JSON Schema 片段。
func typeToSchema(t *TypeRef, refPrefix string) *OMap {
	s := NewOMap()
	switch t.Kind {
	case KindRef:
		ref := NewOMap().Set("$ref", refPrefix+t.Ref)
		if t.Pointer {
			return NewOMap().Set("oneOf", FlowList{
				ref,
				NewOMap().Set("type", "null"),
			})
		}
		return ref
	case KindArray:
		elem := typeToSchema(t.Elem, refPrefix)
		if t.Pointer {
			s.Set("type", FlowList{"array", "null"})
		} else {
			s.Set("type", "array")
		}
		s.Set("items", elem)
		return s
	case KindScalar:
		switch t.Scalar {
		case "string":
			setType(s, "string", t.Pointer)
		case "bool":
			setType(s, "boolean", t.Pointer)
		case "float32", "float64":
			setType(s, "number", t.Pointer)
			s.Set("format", "double")
			s.Set("x-go-type", t.Scalar)
		case "gtime":
			setType(s, "string", true) // *gtime.Time 在 DTO 中始终为指针语义（可 null）
			s.Set("x-go-type", "gtime.Time")
			if !t.Pointer {
				s.Set("type", "string")
			}
		case "time":
			setType(s, "string", true)
			s.Set("x-go-type", "time.Time")
			if !t.Pointer {
				s.Set("type", "string")
			}
		case "jsonnumber":
			// 请求侧可为整数或数字字符串；响应侧不会出现该类型。
			return NewOMap().
				Set("oneOf", FlowList{
					NewOMap().Set("type", "integer").Set("format", "int64"),
					NewOMap().Set("type", "string").Set("pattern", `^-?[0-9]+$`),
				})
		default:
			if isIntScalar(t.Scalar) {
				setType(s, "integer", t.Pointer)
				s.Set("format", "int64")
				s.Set("x-go-type", t.Scalar)
			}
		}
		return s
	case KindAny:
		return NewOMap()
	}
	return s
}

func setType(s *OMap, typ string, nullable bool) {
	if nullable {
		s.Set("type", FlowList{typ, "null"})
		return
	}
	s.Set("type", typ)
}

// coerceDefault 把 GoFrame d:"..." 默认值转换为目标 JSON 类型。
func coerceDefault(raw string, t *TypeRef) any {
	if t == nil {
		return raw
	}
	switch t.Kind {
	case KindScalar:
		switch {
		case isIntScalar(t.Scalar):
			var n int64
			if _, err := fmt.Sscanf(raw, "%d", &n); err == nil {
				return n
			}
		case t.Scalar == "bool":
			return raw == "true" || raw == "1"
		}
	}
	return raw
}
