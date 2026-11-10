package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// writeYAMLWithHeader 写入带注释头的 YAML 文件（已存在则覆盖）。
func writeYAMLWithHeader(path string, header []string, m *OMap) error {
	node, err := toYAMLNode(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, line := range header {
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// writeInventory 生成 DTO → Schema 覆盖清单。
func writeInventory(repoRoot string, defs map[string]*StructDef, order []string, apis []*API) error {
	reqCount, resCount := 0, 0
	var moneyRows, enumRows, timeRows, nullableRows []string
	var rows []string

	for _, key := range order {
		def := defs[key]
		kind := "Entity"
		switch {
		case strings.HasSuffix(def.Name, "Req"):
			kind = "Request"
			reqCount++
		case strings.HasSuffix(def.Name, "Res"):
			kind = "Response"
			resCount++
		}

		var money, enums, times, nullables []string
		for _, f := range def.Fields {
			if f.Embedded {
				continue
			}
			if isMoneyField(f) {
				money = append(money, f.JSONName)
				moneyRows = append(moneyRows, fmt.Sprintf("| `%s` | `%s` | %s |", def.SchemaName, f.JSONName, goTypeOf(f.Type)))
			}
			if _, ok := enumRegistry[def.SchemaName+"."+f.JSONName]; ok {
				enums = append(enums, f.JSONName)
				enumRows = append(enumRows, fmt.Sprintf("| `%s` | `%s` | %s |", def.SchemaName, f.JSONName, goTypeOf(f.Type)))
			}
			if f.Type != nil && f.Type.Kind == KindScalar && (f.Type.Scalar == "gtime" || f.Type.Scalar == "time") {
				times = append(times, f.JSONName)
				timeRows = append(timeRows, fmt.Sprintf("| `%s` | `%s` | %s | %s |",
					def.SchemaName, f.JSONName, goTypeOf(f.Type), def.Shape))
			}
			if f.Type != nil && f.Type.Pointer {
				nullables = append(nullables, f.JSONName)
				nullableRows = append(nullableRows, fmt.Sprintf("| `%s` | `%s` |", def.SchemaName, f.JSONName))
			}
		}
		rows = append(rows, fmt.Sprintf("| `%s` | `%s` | %s | %d | %s | %s | %s | %s |",
			def.SchemaName, def.Domain+"."+def.Name, kind, len(def.Fields),
			joinOrDash(money), joinOrDash(enums), joinOrDash(times), joinOrDash(nullables)))
	}

	var b strings.Builder
	b.WriteString("# B1-02-02 DTO → OpenAPI Schema 覆盖清单\n\n")
	b.WriteString("| 项目 | 内容 |\n|---|---|\n")
	b.WriteString("| 任务 | B1-02-02 Go DTO Schema 自动生成 |\n")
	b.WriteString("| 契约基线 | `" + contractBaseline + "` |\n")
	b.WriteString("| 生成器 | `tools/openapi-gen`（Go AST，确定性输出） |\n")
	b.WriteString("| 字段事实源 | `api/*/v1/*.go` 的 `json` / `dc` / `v` / `in` / `d` 标签 |\n")
	b.WriteString("| 生成命令 | `go run ./tools/openapi-gen -repo .` |\n")
	b.WriteString("| 输出 | `openapi/components/schemas.yaml`、`openapi/components/error-codes.yaml`、`openapi/surgecart.openapi.yaml` |\n\n")

	b.WriteString("## 1. 覆盖统计\n\n")
	b.WriteString("| 指标 | 数值 |\n|---|---|\n")
	fmt.Fprintf(&b, "| DTO 结构体总数 | %d |\n", len(defs))
	fmt.Fprintf(&b, "| Request 结构体 | %d |\n", reqCount)
	fmt.Fprintf(&b, "| Response 结构体 | %d |\n", resCount)
	fmt.Fprintf(&b, "| 业务实体结构体 | %d |\n", len(defs)-reqCount-resCount)
	fmt.Fprintf(&b, "| 已登记 API | %d |\n", len(apis))
	fmt.Fprintf(&b, "| OperationId 唯一性 | 通过（生成期强制校验） |\n")
	fmt.Fprintf(&b, "| 无法确认的字段 | 0（遇到未登记类型直接失败，不猜测） |\n\n")

	b.WriteString("## 2. 命名规则\n\n")
	b.WriteString("- `<Domain><Name>Req`  → `<Prefix><Name>Request`（如 `order.CreateReq` → `OrderCreateRequest`）\n")
	b.WriteString("- `<Domain><Name>Res`  → `<Prefix><Name>Response`（如 `order.ListRes` → `OrderListResponse`）\n")
	b.WriteString("- 业务实体使用显式登记表（`entitySchemaNames`），禁止使用 Go 包路径名作为 Schema 名\n")
	b.WriteString("- 未登记的类型会让生成器直接失败并提示补登记，避免静默猜测\n")
	b.WriteString("- Schema 名冲突由生成期检测并失败\n\n")

	b.WriteString("## 3. 类型映射规则\n\n")
	b.WriteString("| Go 类型 | OpenAPI 表达 | 说明 |\n|---|---|---|\n")
	b.WriteString("| `string` | `type: string` | |\n")
	b.WriteString("| `bool` | `type: boolean` | |\n")
	b.WriteString("| `int`/`int64`/... | `type: integer, format: int64` + `x-go-type` | 统一 int64，避免 32/64 平台差异 |\n")
	b.WriteString("| `float64` | `type: number, format: double` | 仅 `avg_rating` 使用 |\n")
	b.WriteString("| `json.Number` | `oneOf: [integer(int64), string(数字字符串)]` | GoFrame 绑定接受两种输入 |\n")
	b.WriteString("| `*gtime.Time`（响应） | `type: [string, \"null\"]` + pattern + `x-format: gtime-local` | 实际格式 `YYYY-MM-DD HH:MM:SS`，无时区偏移 |\n")
	b.WriteString("| `*gtime.Time`（请求） | `type: [string, \"null\"]` + `x-format: gtime-lenient` | 服务端宽松解析 |\n")
	b.WriteString("| `time.Time` | `type: [string, \"null\"], format: date-time` | 当前 DTO 未使用 |\n")
	b.WriteString("| `*T` | `oneOf: [$ref, {type: null}]` 或 `type: [x, \"null\"]` | 可空语义 |\n")
	b.WriteString("| `[]T` | `type: array, items: <T>` | 元素指针同样可空 |\n")
	b.WriteString("| 匿名嵌入 `T` | `allOf: [$ref T]` | 字段提升 |\n\n")
	b.WriteString("`required` 规则：请求 DTO 仅 `v:\"required\"` 字段必填；响应 DTO 的非 `omitempty` 字段一律视为必出现" +
		"（Go 会输出 null，键仍存在）。\n\n")

	b.WriteString("## 4. 全量 Schema 清单\n\n")
	b.WriteString("| Schema | Go 类型 | 类型 | 字段数 | 金额字段 | 枚举字段 | 时间字段 | 可空字段 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	b.WriteString(strings.Join(rows, "\n"))
	b.WriteString("\n\n")

	b.WriteString("## 5. 金额字段（整数分，`x-unit: cent`）\n\n")
	b.WriteString("| Schema | 字段 | Go 类型 |\n|---|---|---|\n")
	b.WriteString(strings.Join(moneyRows, "\n"))
	b.WriteString("\n\n")

	b.WriteString("## 6. 枚举字段\n\n")
	b.WriteString("| Schema | 字段 | Go 类型 |\n|---|---|---|\n")
	b.WriteString(strings.Join(enumRows, "\n"))
	b.WriteString("\n\n")
	b.WriteString("string 枚举直接声明 `enum`；整数枚举同时声明 `x-enum-labels` 给出数值与业务含义的对照。\n\n")

	b.WriteString("## 7. 时间字段\n\n")
	b.WriteString("| Schema | 字段 | Go 类型 | 形态 |\n|---|---|---|---|\n")
	b.WriteString(strings.Join(timeRows, "\n"))
	b.WriteString("\n\n")

	b.WriteString("## 8. 可空字段\n\n")
	b.WriteString("| Schema | 字段 |\n|---|---|\n")
	b.WriteString(strings.Join(nullableRows, "\n"))
	b.WriteString("\n\n")

	b.WriteString("## 9. 已知限制与不猜测声明\n\n")
	b.WriteString("1. `openapi/surgecart.openapi.yaml` 内联全部 paths：OpenAPI 3.1 的 Path Item Object 不支持 `$ref`，" +
		"逐 path 引用会产出非法契约。`openapi/paths/*.yaml` 是同源生成的**分域视图**，由 `contract-check.sh` 校验与主文件一致。\n")
	b.WriteString("2. 请求 DTO 中的 `json.Number` 同时接受整数与数字字符串，这是 GoFrame 绑定的实际行为，契约用 `oneOf` 表达。\n")
	b.WriteString("3. `dc` 描述中的业务约束（如长度、取值范围）未全部映射为 Schema 约束，因为 DTO 未使用 `v:\"min/max/length\"`，" +
		"这些约束由 service 层显式校验；契约只在 `description` 中保留原文。\n")
	b.WriteString("4. 生成器无法从 DTO 判断成功响应的 `data` 是 `null` 还是 `{}`；该事实由 `tools/openapi-gen/opmeta.go` 的 `NullData` 显式登记，" +
		"依据是 controller 的实际 `return nil, nil`。未登记项一律按实际结构体输出。\n")
	b.WriteString("5. 错误码不落到单接口粒度：每个 operation 的 `x-error-codes` 是「该业务域在 `internal/logic` 中实际引用的错误码集合」+ 通用码，" +
		"并以 `scope: domain` 明确声明，不声称是逐路径穷举。\n")

	return os.WriteFile(
		filepath.Join(repoRoot, "docs", "phase2", "B1-02_DTO_SCHEMA_INVENTORY.md"),
		[]byte(b.String()), 0o644)
}

func joinOrDash(in []string) string {
	if len(in) == 0 {
		return "-"
	}
	return "`" + strings.Join(in, "`<br>`") + "`"
}

// goTypeOf 返回字段的 Go 类型描述（用于清单审计）。
func goTypeOf(t *TypeRef) string {
	if t == nil {
		return "-"
	}
	prefix := ""
	if t.Pointer {
		prefix = "*"
	}
	switch t.Kind {
	case KindRef:
		return prefix + t.Ref
	case KindArray:
		return prefix + "[]" + goTypeOf(t.Elem)
	case KindAny:
		return "any"
	}
	return prefix + t.Scalar
}
