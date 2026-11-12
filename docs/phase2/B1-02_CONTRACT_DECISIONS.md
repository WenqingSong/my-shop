# B1-02-01 API 契约规范决策记录（ADR）

| 项目 | 内容 |
|---|---|
| 任务编号 | B1-02-01 |
| 阶段 | Phase 2.0 |
| 所属分支 | `feat/p2-backend` |
| 契约基线 | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2` |
| 事实来源 | `docs/phase2/B1-01_API_INVENTORY.md`（§10、§11、§15、§16、§17） |
| 文档状态 | **APPROVED（Owner 已批准，2026-11-10）** |
| 决策数量 | 5（D1～D5） |
| 最后更新 | 2026-11-10 |
| 勘误记录 | 2026-11-10 B1-02-DOC-FIX：D1 受影响接口数由 13 修正为 14（仅事实勘误，决策与批准状态不变） |

> 本文档只做契约层决策，不修改任何生产业务代码。
> 所有"当前实际行为"均来自 B1-01 的逐条核实证据，本文档不新增未经证据支持的事实。
> 审批门禁：D1～D5 已于 **2026-11-10 由 Owner 批准**，签字记录见 §8；B1-02-03 契约按本决策产出。
> 其中 D5 按 Owner 修订意见执行：**继续使用 JSON Body，不采用 `Idempotency-Key` Header 方案**。

---

## 0. 阅读前置：本决策解决什么问题

B1-01 的结论是：142 条常驻 API 的路由、DTO、错误码、权限码均已核实，可以冻结为契约事实；但**不能直接生成可交付的 OpenAPI 3.1**，因为存在五类结构性不一致：

| 编号 | 不一致 | B1-01 证据 |
|---|---|---|
| ① | 参数校验失败同时存在 `400/1001` 与 `500/51` | §15.1 |
| ② | 同一业务状态存在 string / int 两套 JSON 表示 | §15.2、§15.3 |
| ③ | 分页存在"静默归一 / 严格报错 / 无分页"三种形态 | §11.1、§15.4 |
| ④ | 时间存在"输出无偏移 / 输入要求偏移 / 宽松解析 / 纯日期"四种形态 | §11.3、§15.5 |
| ⑤ | 幂等键只存在于 JSON body，无 HTTP Header 标准 | §5.3、§5.6 |

本 ADR 的目标是：**在不修改现有实现的前提下，确定"如何把现状写进契约"，并明确"未来新接口按什么标准写"。**

原则：
1. **契约描述现状，不美化现状。** 未实现的行为不得写成已实现。
2. **历史兼容优先于一致性。** 已有接口保留实际行为，新接口才适用新规范。
3. **差异必须显式标注。** 不允许用"统一"字样掩盖双轨行为。

---

## D1 参数校验错误映射

### D1.1 背景

GoFrame 的 `gvalid` 校验失败返回 `gcode.CodeValidationFailed`（数值 **51**）。项目中间件链路为：

```
DTO `v:"required"` → gvalid 失败（gcode 51）
  → codes.FromError(err) 得到 51
  → codes.HTTPStatus(51) 查表未命中
  → 兜底 500 + message "内部错误"
```

证据：`internal/codes/codes.go:269-283`、`internal/middleware/response.go:31-37`、B1-01 §15.1。

项目已定义的语义标准是 `CodeInvalidArgument = 1001 → HTTP 400 → "参数错误"`（`internal/codes/codes.go:20`、`162`），但该映射**尚未接入 gvalid 错误链**。

### D1.2 当前实际行为（必须如实写入契约）

| 场景 | 实际响应 | 受影响接口（B1-01 §15.1 逐条核实） |
|---|---|---|
| 经 service 显式校验的参数错误 | `400` + `code 1001` | 购物车、订单、评价、文章、优惠券、秒杀等活动参数 |
| DTO 带 `v:"required"` 且未显式兜底 | `500` + `code 51` + `message "内部错误"` | `POST /admin/login`、`POST /admin/admins`、`PUT /admin/admins/:id/status`、`POST /admin/admins/:id/roles`、`POST /admin/roles`、`POST /admin/permissions`、`PUT /admin/users/:id/status`、`POST /categories`、`POST /orders`、`POST /admin/skus`、`POST /admin/refunds/:id/review`、`POST /admin/refunds/:id/execute`、`POST /admin/refunds/:id/close`、`POST /admin/roles/:id/permissions` |
| 路径参数缺失（如 `/admin/products/:id` 的 `id` 非数字） | 由框架路由匹配失败，返回 `404 text/plain "Not Found"`，不是统一 JSON | 所有含 `:id` 的接口 |

补充事实：
- 全部 DTO 的校验标签只有两种形态：`required`（87 处）与 `required|in:0,1`（2 处），不存在 `min/max/length` 等可映射为数值/长度约束的标签。
- `internal/logic/cart/cart.go:81-85` 有显式注释与规避代码，说明团队已知该缺陷。
- `code 51` 不在 `internal/codes/codes.go` 的常量与码表中，属于**框架码穿透**，不是业务码。
- **勘误（2026-11-10，B1-02-DOC-FIX）**：上表"`500 + 51`"接口清单原记为 **13 条**，经 B1-02-REVIEW-FIX
  对全部 DTO 的非 path 必填字段逐条复核后确认为 **14 条**，补入 `POST /admin/roles/:id/permissions`
  （body 字段 `permission_id` 带 `v:"required"`）。本次仅修正统计与清单，D1.3 规范内容、
  APPROVED 状态与审批记录时间均不变。

### D1.3 已批准规范（APPROVED 2026-11-10；目标标准，仅适用于新接口与新代码）

| 项 | 规范 |
|---|---|
| 参数校验失败 | HTTP **400** |
| 业务错误码 | **1001**（`CodeInvalidArgument`） |
| message | `参数错误`（码表固定文案，不拼接字段细节） |
| 框架码 51 | **禁止**作为对外业务错误码暴露 |
| 响应体 | `{"code":1001,"message":"参数错误","data":null}` |

### D1.4 契约表达方式

B1-02 采取"**双轨记录 + 已知问题标注**"：

1. **通用响应**：`components/responses.yaml` 中定义 `BadRequest`（400/1001），作为新规范的默认 400 响应。
2. **现状偏离**：对 D1.2 表中"`500 + 51`"的 14 条接口，在 operation 上标注：

```yaml
x-known-issue:
  - code: "B1-02-D1"
    summary: "DTO gvalid 校验失败实际返回 HTTP 500 + code 51，而非 400 + 1001"
    http_status: 500
    business_code: 51
    remediation: "B1-02 不修改错误码映射；需单独立项在 Response 中间件/错误码层接入 gvalid→1001"
```

3. **不虚构**：这些 operation 的 `responses` 同时声明 `'400'`（目标规范）与 `'500'`（实际行为），并在描述中说明 400 仅在 service 显式校验路径出现。
4. **不影响未受影响接口**：未列入 D1.2 的接口，其 `v:"required"` 字段由 service 层显式校验覆盖（已逐条核实），契约按 `400 + 1001` 描述。

### D1.5 兼容性分析

| 维度 | 结论 |
|---|---|
| 对现有客户端 | 无影响。契约只是把既有行为写清楚，不改变运行时。 |
| 对前端错误处理 | 前端必须同时处理 `51` 与 `1001`；本决策要求契约显式暴露该事实，避免前端只按 1001 写死。 |
| 对后续修复 | 修复时只需在 `codes.HTTPStatus`/`codes.Message` 前把 gvalid 错误归一为 1001，不需要改任何 DTO。 |
| 契约稳定性 | 修复后需删除 `x-known-issue` 并把 400 变为唯一声明；这是一次**有意的契约变更**，需版本化。 |

### D1.6 影响范围

- 契约：14 条 operation 的 `x-known-issue` 与 `responses`。
- 代码（本任务禁止）：`internal/middleware/response.go`、`internal/codes/codes.go`。
- 文档：`docs/phase2/B1-02_CONTRACT_REPORT.md` 已知问题章节。

### D1.7 后续代码改造建议（不在 B1-02 实施）

1. 在 `codes.FromError` 中识别 `gcode.CodeValidationFailed` 并归一为 `CodeInvalidArgument`。
2. 补一条单测：`POST /admin/login` 缺 `username` 必须返回 `400/1001`。
3. 为 `gcode.CodeValidationFailed` 增加显式映射，避免任何框架码落到 500 兜底分支。

---

## D2 枚举类型

### D2.1 背景

同一业务概念在 JSON 中存在 string 与 int 两套表示（B1-01 §15.2、§15.3）：

| 资源 | string 表示 | int 表示 |
|---|---|---|
| 订单 | `Order.status` | `dashboard.status_distribution[].status`（10/20/30/40/50/60/70） |
| 退款 | `Refund.status` | `RefundAudit.before_status/after_status` |
| 秒杀请求 | `GetOrderResultRes.status` | `RequestAudit.before_status/after_status` |
| 优惠券 | `UserCoupon.status` | `CouponTemplate.status`（0/1） |
| 分类/轮播图/推荐位 | — | `status`（0/1） |
| 用户/管理员 | — | `status`（0/1） |

### D2.2 当前实际行为

已逐字段核实（B1-01 §15.3）：

| 资源 | 出参类型 | 取值 |
|---|---|---|
| `Product.status` | string | `draft` / `on_shelf` / `off_shelf` |
| `Sku.status` | string | `enabled` / `disabled` |
| `Order.status` | string | `pending_payment` / `paid` / `shipped` / `received` / `completed` / `cancelled` / `refunded` |
| `Refund.status` | string | `applying` / `pending_refund` / `refund_failed` / `review_rejected` / `revoked` / `refund_succeeded` / `closed` |
| `UserCoupon.status` | string | `available` / `locked` / `used` |
| `FlashSaleActivity.status` | string | `enabled` / `disabled` |
| `FlashSaleRequest.status` | string | `queued` / `success` / `failed` / `dead` |
| `Review.status` | string | `published` / `taken_down` / `deleted` |
| `InventoryLog.change_type` | string | `increase` / `deduct` |
| `Category.status` / `Banner.status` / `RecommendPosition.status` | int | 0 / 1 |
| `CouponTemplate.status` | int | 0 / 1 |
| `UserStatus.status` / `AdminStatus.status` | int | 0 / 1 |
| `Order.cancel_reason` | int | 1 / 2（未取消为 null） |
| `RefundAudit.operator_type` | int | 1 / 2 |
| `RefundAudit.before_status/after_status` | int | 10/20/30/40/50/60/70，apply 时 before=0 |
| `RequestAudit.before_status/after_status` | int | 0/1/2/3 |
| `dashboard.status_distribution[].status` | int | 10/20/30/40/50/60/70 |

### D2.3 已批准规范（APPROVED 2026-11-10；目标标准）

1. **新增 API 的业务状态枚举统一使用 string**，取值为小写下划线或单词，语义自解释。
2. **已有 API 的整数枚举保留实际 JSON 类型**，禁止为了"统一"改成 string。
3. **整数枚举必须在 OpenAPI 中给出数值与业务含义的对照**，使用：
   - `enum: [0, 1]`
   - `x-enum-labels: {"0": "...", "1": "..."}`
4. **禁止类型伪装**：实际返回 `integer` 的字段，不得在 OpenAPI 中声明为 `string`；反之亦然。
5. string 枚举统一使用 `enum` 限定取值，禁止只写 `type: string`。

### D2.4 契约表达方式

| 场景 | OpenAPI 表达 |
|---|---|
| string 枚举 | `type: string` + `enum: [...]` + `description` 含 DB 数值（如"状态：pending_payment(10)/..."） |
| int 枚举（0/1） | `type: integer` + `enum: [0,1]` + `x-enum-labels` |
| int 枚举（10/20/...） | `type: integer` + `enum: [10,20,30,40,50,60,70]` + `x-enum-labels` |
| 可空 int 枚举 | `type: [integer, "null"]` + `enum: [1, 2, null]` + `x-enum-labels` |

注意：`Order.status` 的契约枚举包含 `received`，但 B1-01 §15.11 已证明 `received` 在 API 上**不可观测**（同一事务内 `shipped → received → completed`）。契约保留 `received`（因为 DTO 与 DB 均存在），并在字段描述中标注 `x-known-issue`。

### D2.5 兼容性分析

- 前端可继续按现有类型解析，无运行时影响。
- 若未来把 int 枚举改为 string，属于**破坏性变更**，需要新路径版本或新字段，不得原地修改。
- 整数枚举加 `enum` 可能让严格的 schema 校验器拒绝历史数据（例如 `cancel_reason` 出现 3）。当前代码只会写入 1/2，风险可接受；若后续扩展取值，必须同步更新契约。

### D2.6 影响范围

- 契约：全部含 `status` / `*_status` / `change_type` / `operator_type` / `cancel_reason` 的 Schema。
- 代码：无。
- 前端：无。

### D2.7 后续代码改造建议

1. 新模块的 API 状态一律用 string 常量（参考 `api/order/v1`、`api/refund/v1` 的既有写法）。
2. 若需给 int 枚举补充对外含义，优先新增只读字段（如 `status_text`），不改原有字段类型。
3. 为 `received` 中间态补一次产品确认：若确认永不对外可见，可在下一大版本从 DTO 枚举中移除。

---

## D3 分页

### D3.1 背景

现有分页存在三种形态（B1-01 §11.1、§15.4）：

| 形态 | 行为 | 接口示例 |
|---|---|---|
| A 静默归一 | `page<1→1`、`size<1→20`、`size>100→100` | 前台商品列表、后台商品列表、文章列表/我的文章/收藏、评价公开列表/我的评价、收藏列表、库存流水 |
| B 严格校验 | 非法分页参数返回 `400 + 1001` | 后台订单列表、后台退款列表 |
| C 无分页 | 仅返回 `items`，无 `total/page/size`，无上限 | 购物车、地址、会话、用户订单、用户退款、我的券、轮播图、分类树、角色、权限、退款审计、秒杀审计、后台券列表、后台轮播图、后台推荐位 |

补充事实：
- 分页响应字段统一为 `items/total/page/size`（形态 A、B），但 `total` 类型不一致：商品/文章为 `int`，订单统计为 `int64`。
- 无统一 `maxSize`/`defaultSize` 常量，各域重复定义（`product.maxSize`、`article.maxSize`、`review.maxSize` 等）。
- 不存在 `total_pages`、`has_next`、游标分页。

### D3.2 已批准规范（APPROVED 2026-11-10；目标标准，仅适用于新接口）

请求：

| 参数 | 类型 | 默认 | 约束 | 非法处理 |
|---|---|---|---|---|
| `page` | integer | 1 | `>= 1` | `400 + 1001` |
| `size` | integer | 20 | `1 <= size <= 100` | `400 + 1001` |

响应：

```json
{ "items": [], "total": 0, "page": 1, "size": 20 }
```

- `total`：满足条件的总记录数（`int64`）。
- `page`/`size`：**生效值**（服务端归一后的值），不是客户端原始输入。
- 禁止静默归一（不产生"传 0 得到 20"的隐式行为）。

### D3.3 契约表达方式

1. `components/parameters.yaml` 建立 **两个独立组件**，语义不同，不可混用：

| 组件 | 语义 | 用于 |
|---|---|---|
| `PageStrictParam` | 严格校验，越界 400 | 新接口；后台订单/退款列表 |
| `SizeStrictParam` | 严格校验，越界 400 | 同上 |
| `PageClampParam` | 静默归一（`<1→1`） | 形态 A 的历史接口 |
| `SizeClampParam` | 静默归一（`<1→20`、`>100→100`） | 形态 A 的历史接口 |

2. 形态 A 的参数描述必须写明：

```yaml
description: "页码；<1 时静默归一为 1（历史行为，不报错）"
x-pagination-mode: clamp
```

3. 形态 B 的参数描述：

```yaml
description: "页码；<1 或非整数返回 400 + 1001"
x-pagination-mode: strict
```

4. 形态 C 的 operation 不得声明任何分页参数；响应 Schema 只含 `items`。同时在 operation 描述中标注：

```yaml
x-pagination-mode: none
description: "该接口无分页，返回全部数据，无 total。"
```

5. 分页响应统一使用 `PagedItems<T>` 的 `allOf` 组合（`items/total/page/size`），但 `total` 类型按实际声明（`int64` 与 `int` 在 OpenAPI 中均为 `integer`，差异记录在 `x-go-type`）。

### D3.4 兼容性分析

- 形态 A 若改为严格校验，会让现网前端传 `size=0` 或 `size=1000` 的请求开始报错，属于**行为破坏**。因此 B1-02 **不改变** A 的行为，只在契约中如实标注 `clamp`。
- 形态 B 与目标规范一致，无需改动。
- 形态 C 若未来补分页，必须新增参数并保持旧响应字段兼容（`items` 保留，新增 `total/page/size` 为可选），否则破坏前端。
- `total` 的 `int`/`int64` 差异对 JSON 无影响，契约统一声明 `integer` + `format: int64`，并在 `x-go-type` 记录实际 Go 类型。

### D3.5 影响范围

- 契约：约 30 条列表类 operation 的 `parameters` 与响应 Schema。
- 代码：无。
- 前端：需要按 operation 的 `x-pagination-mode` 决定是否发送分页参数、是否需要处理 400。

### D3.6 后续代码改造建议

1. 抽取 `internal/pagination` 包，提供 `Clamp(page,size)` 与 `Strict(page,size)` 两套函数，逐步收敛重复实现。
2. 新接口统一使用 `Strict`。
3. 为形态 C 的高增长列表（`GET /orders`、`GET /refunds`）优先补分页，需产品确认兼容策略。

---

## D4 时间格式

### D4.1 背景

现有时间表示有四种形态（B1-01 §11.3、§15.5）：

| 场景 | 格式 | 证据 |
|---|---|---|
| 响应中的 `*gtime.Time` | `"YYYY-MM-DD HH:MM:SS"`，**无时区偏移**，进程本地时区 | `gtime.Time.MarshalJSON → String()` |
| 后台订单筛选 `start_time/end_time` | RFC3339，**必须含偏移**（`time.Parse(time.RFC3339)` 会拒绝无偏移值） | `internal/logic/order/order.go:1152-1181` |
| 大屏 `start_time/end_time` | RFC3339，含偏移 | `internal/logic/dashboard/*.go` |
| 秒杀/优惠券请求时间（`*gtime.Time`） | 宽松解析，接受多种格式 | `api/flashsale/v1`、`api/coupon/v1` |
| 大屏日期 `date` | `YYYY-MM-DD` | `api/dashboard/v1/dashboard.go:32` |
| 会话 `login_at`、上传 `expires_at` | unix 秒（int64） | `api/iam/v1/iam.go:69`、`api/upload/v1/upload.go:31` |

结论：**输出无偏移、输入要求偏移、部分输入宽松解析**，三种约定并存；另有纯日期与 unix 秒两种特例。

### D4.2 已批准规范（APPROVED 2026-11-10；目标标准，仅适用于新接口）

1. 新 API 的日期时间字段统一采用 **RFC3339**，且**必须包含时区偏移**：
   - `2026-11-10T11:27:26+08:00`
   - 小数秒可选，最多 3 位。
2. **Unix 秒字段继续使用 integer**（如 `login_at`、`expires_at`），不得改成字符串。
3. **纯日期字段继续使用 `YYYY-MM-DD` 字符串**（如大屏 `date`），不升级为 datetime。
4. 响应中的 `gtime.Time` **不得伪装成 RFC3339**：当前实现无偏移，契约必须按实际格式记录。

### D4.3 契约表达方式

| 场景 | OpenAPI 表达 |
|---|---|
| 响应 `*gtime.Time`（无偏移） | `type: [string, "null"]`<br>`pattern: '^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$'`<br>`example: "2026-11-10 11:27:26"`<br>`x-format: gtime-local`<br>description: "本地时间，格式 YYYY-MM-DD HH:MM:SS，**不含时区偏移**（当前实现限制，非 RFC3339）" |
| 请求 `*gtime.Time`（宽松解析） | `type: [string, "null"]`<br>`x-format: gtime-lenient`<br>description: "宽松解析：支持 `YYYY-MM-DD HH:MM:SS`、`YYYY-MM-DD`、RFC3339 等；服务端按进程时区解释无偏移值" |
| RFC3339 请求参数 | `type: string`<br>`format: date-time`<br>description: "RFC3339，必须含时区偏移，如 2026-11-10T11:27:26+08:00" |
| 纯日期 | `type: string`<br>`format: date`<br>`pattern: '^\d{4}-\d{2}-\d{2}$'` |
| Unix 秒 | `type: integer`<br>`format: int64`<br>`x-unit: unix-second` |

`x-format` 取值固定为：`gtime-local`、`gtime-lenient`、`rfc3339`、`date`、`unix-second`。

### D4.4 兼容性分析

- 若把响应改为 RFC3339（带偏移），会改变现有响应字符串，前端解析与数据库快照对比逻辑可能受影响，属于**行为变更**，需要独立立项 + 版本化。
- 因此 B1-02 **不改响应**，只在契约中标注 `x-format: gtime-local` 与 `x-known-issue`（"响应时间不含时区偏移，与目标 RFC3339 标准不一致"）。
- 请求侧不受影响：现有 RFC3339 参数继续可用。
- 宽松解析的请求字段（秒杀/优惠券）不能声明严格 `pattern`，否则契约会拒绝实际可接受的输入。

### D4.5 影响范围

- 契约：所有含 `created_at`/`updated_at`/`*_at`/`start_time`/`end_time`/`date`/`login_at`/`expires_at` 的 Schema 与参数。
- 代码：无。
- 前端：必须按 `x-format` 分支处理，不能假设所有时间都是 RFC3339。

### D4.6 后续代码改造建议

1. 新增统一的 `types.RFC3339Time` 包装类型，仅用于新接口。
2. 若要全局改为 RFC3339：需评估 `gtime.Time` 的 `MarshalJSON` 替换对历史数据与前端的影响，并配合 API 版本升级。
3. 在 `.env`/配置层固定 `dashboard.timezone`，避免"进程本地时区"随部署漂移（当前 DSN 已按配置设 `time_zone`，但 `gtime` 输出仍依赖进程时区）。

---

## D5 幂等键

### D5.1 背景

现有幂等键只存在于 JSON 请求体（B1-01 §5.3、§5.6）：

| 接口 | 字段 | 类型 | 语义 |
|---|---|---|---|
| `POST /orders` | `idempotency_key` | string | `v:"required"`；同键同内容幂等，同键不同内容 `9006` |
| `POST /flash-sales/:id/orders` | `idempotency_key` | string | 同键同内容返回既有请求状态，同键不同内容 `12005` |
| `GET /flash-sales/:id/orders/result` | `idempotency_key` | string（query） | 查询已受理请求的结果 |

不存在 `Idempotency-Key` HTTP Header 支持。除上述接口外，其余写接口**没有幂等键**。

### D5.2 已批准规范（APPROVED 2026-11-10）

Owner 决策：**幂等键继续使用 JSON 请求体字段，不采用"新 API 默认使用 `Idempotency-Key` HTTP Header"的提议。**

1. 所有需要幂等的接口统一在 JSON 请求体中声明 `idempotency_key`（string）。
2. 不新增、不启用 `Idempotency-Key` HTTP Header；服务端不读取该 Header，契约中不为任何 operation 声明该参数。
3. 现有三条接口保持实际行为，本次不作变更：
   - `POST /orders`：`v:"required"`；同键同内容幂等，同键不同内容返回 `9006`。
   - `POST /flash-sales/:id/orders`：可选；同键同内容返回既有请求状态，同键不同内容返回 `12005`。
   - `GET /flash-sales/:id/orders/result`：query 参数，用于查询已受理请求的结果。
4. 新增需要幂等的接口沿用同一字段名 `idempotency_key` 与同一语义，不引入第二套传输方式。
5. 幂等记录的存储键与 TTL 由实现文档约定，不在契约层约束。

### D5.3 契约表达方式

1. 三条现有接口按实际行为声明：

```yaml
x-idempotency:
  mode: body            # 秒杀结果查询为 query
  field: idempotency_key
  required: true        # POST /orders 为 true，其余为 false
  header_supported: false
```

2. 其余写接口一律 `x-idempotency: {mode: none}`，明确告知前端该接口不可通过幂等键重试。
3. 契约中**不得**出现 `Idempotency-Key` 参数声明。
4. `components/parameters.yaml` 中遗留的 `IdempotencyKeyHeader` 组件是 D5 评审前的草案产物，未被任何 operation 引用，**不构成规范**。按 Owner"本次不重新生成 OpenAPI"的指令，B1-02 收尾不改动该文件，在下次契约生成时移除；该事项登记为 B1-02 已知问题 **K-1**（见交付报告）。

### D5.4 兼容性分析

- 前端只需继续传 body 字段，无迁移成本。
- 不引入 Header，因此不存在"Header 与 body 同时存在时以谁为准"的歧义。
- 反向风险为零：契约不为任何接口声明 Header，不会出现"契约承诺但服务端未实现"。

### D5.5 影响范围

- 契约：3 条幂等相关 operation 的 `x-idempotency`；无 Header 参数。
- 代码：无。
- 前端：沿用 `idempotency_key` body 字段，重试时复用同一 key。

### D5.6 后续代码改造建议

1. 如需统一幂等存储，可抽 `internal/idempotency` 包，但入口仍是 body 字段。
2. 不实现 Header 解析；若未来要引入 Header，必须单独立项、定义与 body 的优先级规则，并同步契约。
3. 新增幂等接口必须复用 `idempotency_key` 字段名与 `9006` / `12005` 一类冲突码语义。

---

## 6. 五项决策汇总

| 编号 | 决策主题 | 现状 | 目标标准 | 本次是否改代码 | 契约标注方式 |
|---|---|---|---|---|---|
| D1 | 参数校验错误映射 | `400/1001` 与 `500/51` 并存 | 一律 `400/1001` | 否 | `x-known-issue` + 双状态声明 |
| D2 | 枚举类型 | string 与 int 并存 | 新接口 string；旧接口保留实际类型 | 否 | `enum` + `x-enum-labels` |
| D3 | 分页 | 归一 / 严格 / 无分页并存 | 新接口 `page/size` 严格校验 + `items/total/page/size` | 否 | `x-pagination-mode` |
| D4 | 时间格式 | 无偏移输出 / 偏移输入 / 宽松 / 纯日期 | 新接口 RFC3339 含偏移 | 否 | `x-format` |
| D5 | 幂等键 | 仅 body 字段 | **继续使用 body 字段 `idempotency_key`；不采用 Header** | 否 | `x-idempotency` |

---

## 7. 对 B1-02-03 的约束（契约生成必须遵守）

1. **不得**把 `received` 之外的订单状态改成 int，也**不得**把 `dashboard.status_distribution.status` 改成 string。
2. **不得**给形态 A 的分页参数声明 `minimum: 1`（因为实际是静默归一，不是拒绝）。
3. **不得**给 `*gtime.Time` 响应声明 `format: date-time`（会暗示 RFC3339）。
4. **不得在任何接口声明 `Idempotency-Key` 参数**（D5 已否决 Header 方案）。
5. **不得**把 §D1.2 表中 14 条接口的 `500/51` 隐藏，也不得把全部接口都写成 `500/51`。
6. 任何"目标规范"与"实际行为"不一致处，必须同时出现：
   - 实际的 `responses`/`schema`
   - 解释差异的 `description`
   - 可机器检索的 `x-known-issue` 或 `x-*` 标注

---

## 8. 审批记录

| 项 | 内容 |
|---|---|
| 审批人角色 | Owner（产品/技术负责人） |
| 审批对象 | D1～D5 全部五项决策 |
| 当前状态 | **APPROVED（2026-11-10 批准）** |
| 提交时间 | 2026-11-10 |
| 提交人 | TaskBuilder（DeepSeek 4.1） |

### 8.1 审批签字栏

| 决策 | 结论（APPROVED / REJECTED / DEFERRED） | 审批人 | 审批时间 | 备注 |
|---|---|---|---|---|
| D1 参数校验错误映射 | APPROVED | Owner | 2026-11-10 | 目标 `400/1001`；当前 14 条接口的 `500/51` 以 `x-known-issue` 记录，本次不改代码（条数于 2026-11-10 B1-02-DOC-FIX 勘误，原记 13 条） |
| D2 枚举类型 | APPROVED | Owner | 2026-11-10 | 新 API 目标 string；现有整数枚举按实际 JSON 类型记录，不得伪装 |
| D3 分页 | APPROVED | Owner | 2026-11-10 | 新 API `page/size` 严格校验 + `items/total/page/size`；现有按 clamp/strict/none 三态记录 |
| D4 时间格式 | APPROVED | Owner | 2026-11-10 | 新 API RFC3339 含偏移；现有 `gtime.Time` 如实记录为无偏移 |
| D5 幂等键 | APPROVED | Owner | 2026-11-10 | **按 Owner 修订**：继续使用 JSON Body，不采用 Header |

### 8.2 门禁说明

- 8.1 已全部填写 `APPROVED`（2026-11-10），审批门禁解除：
  - 本文档状态为 `APPROVED`；
  - B1-02-03 契约按本决策产出并归档于 `openapi/surgecart.openapi.yaml`；
  - D5 以 Owner 修订后的方案为准（JSON Body，不使用 Header）。
- B1-02-02（DTO Schema 生成器）不依赖本审批，可并行完成；其产物只包含由 Go DTO 机械推导的 Schema，不包含上述五项决策的业务语义。
- 审批后的任何方案变更必须新增决策条目并重新签字，不得在原条目上静默改写。

---

## 9. 附：本决策引用的证据索引

| 主题 | 证据位置 |
|---|---|
| 统一响应与错误映射 | `internal/middleware/response.go:12-45`、`internal/codes/codes.go:269-306` |
| gvalid 512 缺陷 | B1-01 §15.1；`internal/logic/cart/cart.go:81-85` |
| 字符串枚举定义 | `api/order/v1/order.go:8-17`、`api/refund/v1/refund.go:9-30`、`api/product/v1/product.go:12-17`、`api/sku/v1/sku.go:10-14`、`api/coupon/v1/coupon.go:10-21`、`api/flashsale/v1/flash_sale.go:12-24`、`api/review/v1/review.go:8-13` |
| 整数枚举定义 | `api/categories/v1/categories.go:14`、`api/banner/v1/banner.go:25`、`api/recommendation/v1/recommendation.go:11-14`、`api/dashboard/v1/dashboard.go:51`、`api/refund/v1/refund.go:140-144` |
| 分页实现 | B1-01 §11.1；`internal/logic/order/order.go:1110-1126`、`internal/logic/refund/refund.go:834-850` |
| 时间格式 | B1-01 §11.3；`internal/logic/order/order.go:1152-1181`、`internal/boot/boot.go:176-202` |
| 幂等键 | `api/order/v1/order.go:80`、`api/flashsale/v1/flash_sale.go:108`、`internal/codes/codes.go:89`、`internal/codes/codes.go:108` |
