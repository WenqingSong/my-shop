# B1-02 独立审查交接（GPT-6）

| 项目 | 内容 |
|---|---|
| 交接对象 | GPT-6 独立审查 |
| 审查对象 | `feat/p2-backend` 上 B1-02 交付提交（SHA 见交付报告 §2 与提交记录） |
| 契约基线 | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2` |
| 任务范围 | B1-02-01 ～ B1-02-04 的交付物 |
| 当前状态 | 执行者自检 PASS；Owner 决策 APPROVED；独立审查 `PENDING_GPT6_REVIEW` |
| 审查清单 | `B1-02_CONTRACT_REPORT.md` §8 的 15 项 |

本文件只提供"审什么、去哪看、怎么复现"，不替代独立判断。

---

## 1. 五项最终决策（Owner 已批准，2026-11-10）

| 决策 | 最终结论 | 依据/影响 |
|---|---|---|
| **D1 参数校验错误映射** | 目标标准为 `HTTP 400 + code 1001`；**当前**部分接口实际返回 `HTTP 500 + code 51`，必须在契约中如实标注，本任务不改错误码映射代码 | gvalid 错误在 `codes.HTTPStatus` 前未归一；13 条接口受影响 |
| **D2 枚举类型** | 新增 API 的业务状态目标使用 **string**；已有 API 的整数枚举**保留实际 JSON 类型**并记录数值含义；禁止把整数响应伪装成 string | `Order.status` 为 string，`Category.status` 为 int 0/1，两者并存 |
| **D3 分页** | 新 API 统一 `page`（默认 1）/`size`（默认 20，上限 100），非法值 `400 + 1001`；响应 `items/total/page/size`。已有接口按**实际**三态记录：静默归一 / 严格校验 / 无分页 | 形态 A（clamp）9 条、形态 B（strict）2 条，其余 none |
| **D4 时间格式** | 新 API 用 RFC3339（含时区偏移）；已有 `gtime.Time` 响应实际为 `YYYY-MM-DD HH:MM:SS` **无偏移**，不得写成 RFC3339 | 响应 59 处 `x-format: gtime-local`，请求 8 处 `gtime-lenient` |
| **D5 幂等键** | **继续使用 JSON 请求体字段 `idempotency_key`；不采用 `Idempotency-Key` HTTP Header 方案**（Owner 修订） | 3 条接口：`POST /orders`（required）、`POST /flash-sales/:id/orders`（可选）、`GET /flash-sales/:id/orders/result`（query） |

决策全文与签字记录：`docs/phase2/B1-02_CONTRACT_DECISIONS.md`（§8.1 五栏均为 APPROVED）。

> 审查提示：契约中仍存在 `components/parameters.yaml#/IdempotencyKeyHeader` 组件，它是 D5 评审前的草案，**未被任何 operation 引用**，不构成规范。因 Owner 指令"本次不重新生成 OpenAPI"，未在收尾阶段移除，登记为已知问题 **K-1**。

---

## 2. 142 条路由覆盖证据

| 证据 | 位置 / 命令 | 期望 |
|---|---|---|
| 契约声明数 | `openapi/surgecart.openapi.yaml` → `x-api-count` | `total: 142`（frontend 67 / admin 73 / health 2） |
| operation 数 | `grep -c '      operationId:' openapi/surgecart.openapi.yaml` | `146` = 142 常驻 + 4 planned |
| 真实路由表 | `go test ./internal/cmd -run TestRouteTable -count=1` | 142 条双向严格比对（缺失与多余都失败） |
| 路由 ↔ 契约 | `go test ./internal/cmd -run TestRouteTableMatchesContract -count=1` | 双向零差异 |
| 分布锁定 | `go test ./internal/cmd -run TestRouteCountByScope -count=1` | 67 / 73 / 2 |
| 完整清单 | `internal/cmd/routes_test.go` 的 `fullRouteTable()` | 142 条逐条列出 |

路由测试取的是**真实 GoFrame 路由表**（`g.Server().Start()` 后 `GetRoutes()`），不是读取源码字符串；已排除框架自动注册的 `ALL /*` 兜底路由。

不计入 142 条的项：

- `GET /metrics`：仅 `flash_sale.metrics.enabled=true` 时注册，Prometheus `text/plain`，注册在响应中间件之外 → 记录在 `x-conditional-routes`。
- `/storage/banners/*`：本地图片静态目录 → 记录在 `x-static-routes`。

---

## 3. 323 个 Schema 的生成与核对

| 组成 | 数量 | 来源 |
|---|---|---|
| Request | 142 | `api/*/v1/*.go` 中以 `Req` 结尾的结构体 |
| Response | 141 | 以 `Res` 结尾的结构体（`upload.TokenRes` 被两个 Req 共用） |
| 业务实体 | 38 | 显式登记在 `tools/openapi-gen/model.go` 的 `entitySchemaNames` |
| 通用包装 | 2 | `Success`、`Error`（契约基础设施，非 DTO） |
| **合计** | **323** | `grep -c '^[A-Za-z]' openapi/components/schemas.yaml` |

生成方式（`tools/openapi-gen`）：

1. `parse_dto.go` 用 Go AST 解析全部 `api/*/v1/*.go`；
2. 类型解析：基础类型 / `*T` / `[]T` / 跨包 `domain.Type` / `json.Number` / `gtime.Time` / `time.Time` / `g.Meta`；
3. 命名：Req/Res 用 `<Prefix><Name>Request|Response` 规则，实体用显式表；未登记类型、未知包别名、`map`、`any` 一律**报错失败**，不猜测；
4. `required`：请求按 `v:"required"`，响应按"非 `omitempty` 即必出现"（Go 会输出 null）；
5. 输出保持声明顺序，重复运行字节级一致（`contract-check.sh` 用内容指纹验证）。

核对方式：

| 核对 | 命令/实现 |
|---|---|
| 生成器可重复性 | `scripts/contract-check.sh` 步骤 1（生成前后指纹一致） |
| Schema ↔ DTO 深比较 | `go run ./tools/openapi-gen -check` 步骤 8（重新解析 DTO 生成后与 `schemas.yaml` 比较） |
| 名称唯一/非空/无包路径 | `tools/openapi-gen/main_test.go` 的 `TestParseDTOsCoversAllAPIPackages` |
| 字段级语义 | `TestBuildSchemasStructure`（可空、金额、枚举、嵌入 allOf） |
| 全量清单 | `docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md`（每个 Schema 的字段数、金额/枚举/时间/可空字段） |

---

## 4. 认证与 RBAC 的描述依据

`x-auth` / `x-auth-mode` / `x-permission` 全部来自 `internal/cmd/routes_frontend.go`、`routes_admin.go` 的**路由组中间件**，不按接口名猜测：

| 路由注册形态 | 契约表达 |
|---|---|
| `root.XXX` | `x-auth: public`，`security: []` |
| `user.Middleware(middleware.Auth)` 组 | `x-auth: user` |
| `user.Middleware(middleware.AuthSignatureOnly)` 组 | `x-auth: user` + `x-auth-mode: signature_only` |
| `admin.Middleware(middleware.AdminAuth)` 组 | `x-auth: admin` |
| `admin.Middleware(middleware.AdminAuthSignatureOnly)` 组 | `x-auth: admin` + `x-auth-mode: signature_only` |
| `require("<code>").XXX` | `x-auth: admin` + `x-permission: <code>` |

实现要点：`parse_routes.go` 按 AST 作用域维护"组变量 → 中间件"映射（同名变量 `user` 在不同 `Group` 闭包中语义不同，曾导致 `/admin/logout` 被误判为 user，已修正为 admin + signature_only）。

分布（与 B1-01 基线一致）：

| 类型 | 数量 |
|---|---|
| public | 17 |
| user（含会话校验） | 52 |
| user + signature_only | 1 |
| admin（无权限码） | 10 |
| admin + permission | 61 |
| admin + signature_only | 1 |

文档化规则写在 `openapi/components/security.yaml` 与每个 operation 的 `description`：用户/管理员身份隔离、Redis 会话校验、`auth_epoch`、超管（`is_super=1`）绕过权限码。

---

## 5. 错误码与权限码核对

**错误码（93 条）**

- 唯一来源：`internal/codes/codes.go` 的常量 + `codeTable`。
- `parse_codes.go` 逐项提取 `code / constant / http_status / message / domain`，常量缺码表映射即**报错**（禁止补全）。
- `error-codes.yaml` 与重新解析结果深比较（`-check` 步骤 7）。
- operation 级 `x-error-codes` 是 **controller→service→logic 调用链**可达的码 + 通用码（1000/1001，认证类 1002/1003），`scope: call-chain` 明示不是逐路径穷举。
- 跨域复用（23 条码）在注册表中以 `reused_by_domains` 标注。

**权限码（49 个）**

- 路由侧：从 `require("<code>")` 提取。
- seed 侧：解析 `internal/boot/seed.go` 的 `seedPermissionList`。
- 契约侧：operation 的 `x-permission`。
- 三集合**双向**比对（`-check` 步骤 6），任一侧多/少都失败。

---

## 6. planned 接口处理

| 占位 | 缺失项 | 路径（proposed） |
|---|---|---|
| 秒杀活动公开列表 | M1 | `GET /flash-sales` |
| 秒杀活动公开详情 | M1 | `GET /flash-sales/{id}` |
| 可领取优惠券列表 | M2 | `GET /coupons` |
| 订单可用优惠券查询 | M3 | `GET /orders/{id}/coupons` |

标注：`x-status: planned`、`x-proposed: true`、`x-implemented: false`、`x-missing-ref`。
响应只给 `200`（无 schema，说明结构未冻结）与 `501`（说明实际会命中 404 text/plain），**不虚构响应体**。
隔离：`TestContractPlannedNotRouted` 断言这 4 条不在真实路由表中；`-check` 断言 planned 数量为 4 且不与实际路由冲突。
索引：主文件 `x-planned-apis`。

---

## 7. 生成器与契约门禁实现

```
tools/openapi-gen/
├── main.go          CLI：-repo / -check
├── model.go         类型中间表示、域前缀、Schema 命名表
├── parse_dto.go     Go AST 解析 DTO（json/dc/v/in/d）
├── parse_routes.go  路由 AST + 中间件作用域 + controller 签名
├── parse_codes.go   codes.go → 错误码注册表
├── analyze_logic.go 函数级错误码引用与同包调用图
├── metadata.go      枚举取值、金额字段白名单
├── build_schema.go  DTO → JSON Schema（3.1）
├── components.go    Success/Error、通用响应、参数、securityScheme
├── gen_paths.go     operation 构建（参数、body、响应、扩展字段）
├── gen.go           主文档装配、统计、分域视图
├── emit.go          YAML 输出（保序）、DTO 清单
├── check.go         结构/$ref/一致性/权限/错误码 校验
├── yamlutil.go      保序 OMap、JSON Pointer
└── main_test.go     单测
```

门禁 `scripts/contract-check.sh`：

| 步骤 | 内容 | 失败后果 |
|---|---|---|
| 1 | 生成前后内容指纹比对 | 契约漂移 |
| 2 | `openapi-gen -check`（3.1 结构、`$ref`、operationId、DTO↔Schema、错误码、权限码、planned） | 契约错误 |
| 3 | 路由测试（双向锁定、契约比对、planned 隔离） | 路由漂移 |
| 4 | Spectral lint（`openapi/.spectral.yaml`） | 规范问题 |
| 5 | 生成器单测 | 工具回归 |

CI：`.cnb.yml` 的 `pr-check` 与 `main-ci` 均新增 `contract` stage，失败即阻断。

---

## 8. 测试结果与未验证事项

实际执行（本机，Go 1.26.0 / Spectral 6.17.1）：

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `make test-unit` | PASS |
| `scripts/contract-check.sh` | PASS（NOT RUN 0） |

未验证（NOT RUN，不得视为通过）：

- `make test` 完整门禁（含集成测试，需 MySQL/Redis/Kafka/kubectl）。
- `make test-integration`、`test-race`、`test-kafka-e2e`、`test-storage`。
- 真实 HTTP 响应冒烟（未启动完整 app）。

---

## 9. 主文件与分域文件的关系

- `openapi/surgecart.openapi.yaml` 是**唯一契约入口**，`paths` 内联全部 142 条 operation + 4 条 planned。
- `openapi/paths/*.yaml`（21 个）是同一生成器产出的**分域视图**，用于按域评审；由门禁的指纹比对保证与主文件同源一致，不构成第二份契约。
- `openapi/components/*.yaml` 被主文件通过 `$ref` 真实引用（逐项映射到 `components.schemas/responses/parameters/securitySchemes`）。
- OpenAPI 3.1.0 **支持**路径级 `$ref`（Path Item `$ref` 字段 + `components.pathItems`）。本契约未使用，原因是 Spectral 6.17.1 对该形态误报 `unevaluated properties`，会导致门禁失败；详见交付报告 §9.1 的 I-1 更正。属工程选择，不是规范限制。

---

## 10. 相关文件路径

| 类别 | 路径 |
|---|---|
| 契约入口 | `openapi/surgecart.openapi.yaml` |
| 组件 | `openapi/components/{schemas,error-codes,responses,parameters,security}.yaml` |
| 分域视图 | `openapi/paths/*.yaml` |
| Spectral 规则 | `openapi/.spectral.yaml` |
| 生成器 | `tools/openapi-gen/*.go` |
| 门禁 | `scripts/contract-check.sh`、`scripts/test-unit.sh` |
| 路由测试 | `internal/cmd/routes_test.go` |
| CI | `.cnb.yml` |
| 决策 | `docs/phase2/B1-02_CONTRACT_DECISIONS.md` |
| 覆盖清单 | `docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md` |
| 交付报告 | `docs/phase2/B1-02_CONTRACT_REPORT.md` |
| 开发知识 | `.agent/tasks/dev/openapi-contract.md` |
| 上游事实源 | `api/*/v1/*.go`、`internal/codes/codes.go`、`internal/cmd/routes_*.go`、`internal/boot/seed.go` |

---

## 11. 建议的审查顺序

1. 跑 `bash scripts/contract-check.sh`，确认全绿且 NOT RUN 为 0。
2. 抽查 5 条代表性 operation：`POST /orders`（D1+D5）、`GET /products`（clamp 分页）、`GET /admin/orders`（strict + 权限码）、`DELETE /sessions/{sid}`（null data + path 参数）、`GET /flash-sales`（planned）。
3. 抽查 3 个 Schema：`Order`（string 枚举 + 金额 + gtime 可空）、`Product`（嵌入/金额）、`Category`（int 枚举 + 递归 children）。
4. 核对 `x-error-codes` 与 `internal/codes/codes.go`、`x-permission` 与 `seed.go`。
5. 对 §8 的 15 项逐条下结论；对 K-1、I-1、I-2、I-3 判定是否必须整改。
