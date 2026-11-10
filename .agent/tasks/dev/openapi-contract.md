# 开发知识：OpenAPI 3.1 契约（B1-02）

> 适用：修改任一 HTTP API、错误码、权限码，或调整契约工具链时。
> 原则：**契约是生成物，不是手写文档。** 手改 `openapi/` 会被门禁判定为漂移。

## 1. 唯一入口与事实源

| 角色 | 位置 |
|---|---|
| 契约唯一入口 | `openapi/surgecart.openapi.yaml`（OpenAPI 3.1.0） |
| 字段事实源 | `api/*/v1/*.go` 的 `json` / `dc` / `v` / `in` / `d` 标签 |
| 路由与权限事实源 | `internal/cmd/routes_frontend.go`、`routes_admin.go` |
| 错误码事实源 | `internal/codes/codes.go` |
| 权限码 seed | `internal/boot/seed.go` 的 `seedPermissionList` |
| 运行时行为事实源 | `internal/controller/**` 的方法签名与返回值 |
| 生成器 | `tools/openapi-gen/` |
| 门禁 | `scripts/contract-check.sh` |

## 2. 文件职责

```text
openapi/
├── surgecart.openapi.yaml      # 唯一入口，paths 内联 + components 逐项 $ref
├── .spectral.yaml              # 契约 lint 规则（关闭 oas3-unused-component，原因见文件内注释）
├── components/
│   ├── schemas.yaml            # 323 个 Schema（321 DTO + Success + Error）
│   ├── error-codes.yaml        # 93 条错误码注册表
│   ├── responses.yaml          # 统一响应与状态码响应组件
│   ├── parameters.yaml         # 严格/归一两种分页参数、幂等 Header 草案（K-1）
│   └── security.yaml           # bearerAuth（JWT HS256）
└── paths/*.yaml                # 21 个分域视图（与主文件同源生成，供按域评审）
```

`openapi/paths/*.yaml` **不是**第二份契约：OpenAPI 3.1 支持路径级 `$ref`，但 Spectral 6.17.1 对该形态误报
`unevaluated properties`，会导致门禁失败，因此主文件内联 paths，分域文件仅作视图并参与指纹校验。

## 3. 常用命令

```bash
# 重新生成契约（改完 DTO / 路由 / codes.go 后必做）
go run ./tools/openapi-gen -repo .

# 只校验，不写文件
go run ./tools/openapi-gen -repo . -check

# 完整门禁（生成 → 结构/$ref/一致性 → 路由比对 → Spectral → 单测）
bash scripts/contract-check.sh

# 路由表锁定与契约比对
go test ./internal/cmd -run 'TestRoute|TestContractPlannedNotRouted' -count=1

# 生成器单测
go test ./tools/openapi-gen -count=1
```

## 4. 新增或修改 API 的标准流程

1. 在 `api/<domain>/v1/*.go` 定义 `XxxReq`（含 `g.Meta` 的 `path`/`method`/`tags`/`summary`）与 `XxxRes`；
   - 字段必须有 `json` 标签；
   - 请求校验用 `v:"required"`、`in:"path"` / `in:"query"`、`d:"默认值"`；
   - 说明写 `dc:"..."`，会进入 Schema 的 `description`。
2. 在 `internal/controller/<domain>/` 实现方法，签名为 `func (c *ControllerV1) Method(ctx, req *v1.XxxReq) (res *v1.XxxRes, err error)`。
3. 在 `internal/cmd/routes_{frontend,admin}.go` 注册路由，挂到正确的组（`root` / `user` / `admin` / `require("<code>")`）。
4. 若引入新的写入接口且 `XxxRes` 为空结构：`handler` 返回 `nil` 时 `data` 为 `null`，返回 `&v1.XxxRes{}` 时 `data` 为 `{}`；
   该事实无法从 DTO 推断，需要在 `tools/openapi-gen/opmeta.go` 的 `opMetaByKey` 登记（`NullData: true`）。
5. 运行 `go run ./tools/openapi-gen -repo .` 重新生成。
6. 更新 `internal/cmd/routes_test.go` 的 `fullRouteTable()`（**必须同步**，否则 `TestRouteTable` 失败）。
7. 运行 `bash scripts/contract-check.sh`。

> 新增路由若未登记进测试清单，`TestRouteTable` 会以"出现未登记路由"失败——这是有意设计。

## 5. 新增错误码 / 权限码

**错误码**：只在 `internal/codes/codes.go` 增加常量 + `codeTable` 条目（HTTP 状态与 message 必须齐全）。
生成器缺映射会直接报错，不会补全。随后重新生成。

**权限码**：在 `internal/boot/seed.go` 的 `seedPermissionList` 增加条目，并在路由上用 `require("<code>")` 引用。
`-check` 会做"路由 ↔ seed ↔ 契约"三向比对，任一不一致即失败。

## 6. 契约扩展字段约定

| 字段 | 出现位置 | 含义 |
|---|---|---|
| `x-auth` | operation | `public` / `user` / `admin` |
| `x-auth-mode` | operation | `signature_only`：只验签、不查 Redis 会话（登出） |
| `x-permission` | operation | 实际 `require()` 的权限码，超管绕过 |
| `x-pagination-mode` | operation + 分页参数 | `clamp` / `strict` / `none` |
| `x-pagination-contract` | operation | 分页行为的文字契约 |
| `x-idempotency` | operation | `mode: body/query/none`，`header_supported` 恒为 `false` |
| `x-error-codes` | operation | 调用链可达的业务码 + 通用码，`scope: call-chain` |
| `x-known-issue` | operation / 字段 / 顶层 | 现状与目标规范的差异 |
| `x-unit` | 字段 | 金额 `cent`（整数分） |
| `x-format` | 字段 | `gtime-local`（响应，无偏移）/ `gtime-lenient`（请求）/ `rfc3339` |
| `x-enum-labels` | 字段 | 整数枚举的数值含义 |
| `x-status` | operation | `planned`：proposed 路径，未实现，不计入 142 |
| `x-api-count` / `x-conditional-routes` / `x-static-routes` | 顶层 | 统计口径与非 JSON 路由 |

## 7. 禁止事项

- 禁止手工编辑 `openapi/` 下任何生成文件（会被 `contract-check.sh` 步骤 1 判定漂移）。
- 禁止把整数枚举写成 string、把 `gtime.Time` 写成 `format: date-time`——必须与 DTO 实际类型一致。
- 禁止给未实现的能力补 `x-*` 之外的"看起来完整"的响应。
- 禁止为任何接口声明 `Idempotency-Key` Header（D5 已否决，见决策文档）。
- 禁止把 planned 接口计入 142 条，或让它出现在真实路由中。

## 8. 已知限制（与最终代码一致）

| 编号 | 内容 | 影响 |
|---|---|---|
| D1 | DTO `gvalid` 校验失败实际返回 `HTTP 500 + code 51` | 13 条接口在契约中以 `x-known-issue` 标注，运行时未修复 |
| D2 | `Order.status` 的 `received` 是事务内中间态，API 查询不可观测 | 枚举保留，字段级 `x-known-issue` |
| K-1 | `components/parameters.yaml#/IdempotencyKeyHeader` 是 D5 前的草案，未被引用 | 下次契约生成时应移除 |
| L-2 | `x-error-codes` 为调用链级，不追踪跨包 service 调用 | `scope: call-chain` 已声明，不声称逐路径穷举 |
| L-3 | 未匹配路由返回 404 `text/plain`，不经过统一 JSON 包装 | 顶层 `x-known-issues` 记录，需单独立项修 |

## 9. 排障

| 现象 | 原因 | 处理 |
|---|---|---|
| `openapi-gen 失败: ... 未登记稳定 Schema 名` | 新增了非 Req/Res 结构体 | 在 `model.go` 的 `entitySchemaNames` 登记稳定名称 |
| `未登记的 API 业务域` | 新增了 `api/<new>/v1` | 在 `model.go` 的 `domains` 加前缀与 camel 名 |
| `controller 中找不到方法` | 路由注册的 handler 名与方法名不一致 | 对齐路由与 controller 方法 |
| `DTO g.Meta 与路由注册不一致` | `path`/`method` 写成两套 | 以路由注册为准修正 DTO |
| `请求 DTO ... 已定义但未在 routes_*.go 注册` | 定义未使用 | 补注册或删除 |
| `scope: domain` 或错误码明显过多 | logic 入口失配触发兜底 | 检查 controller 是否 `service.X().Method(...)` 形式调用 |
| `出现未登记路由`（测试失败） | 新增路由未更新清单 | 更新 `fullRouteTable()` 并重新生成契约 |

## 10. 相关文档

- 决策：`docs/phase2/B1-02_CONTRACT_DECISIONS.md`
- 覆盖清单：`docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md`
- 交付报告：`docs/phase2/B1-02_CONTRACT_REPORT.md`
- 独立审查交接：`docs/phase2/B1-02_GPT6_REVIEW_HANDOFF.md`
- 上游事实源：`docs/phase2/B1-01_API_INVENTORY.md`
