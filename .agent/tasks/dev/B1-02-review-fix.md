# B1-02-review-fix 开发知识文档

> 定位：交接与个人技术学习用的知识文档，记录"为什么这样做"和"改动后不能轻易破坏什么"。
> 正式交付结论见 `docs/reports/B1-02-review-fix.md`；本文不复制报告内容。
> 所有描述均以本次最终代码为准（分支 `feat/p2-backend`）。

---

## 1. B1-02 原始设计与本次整改背景

B1-02 解决的问题是：**让 OpenAPI 契约成为可回归的工程资产，而不是一份会漂移的静态文档。**

原始设计（仍然有效）：

```
api/*/v1/*.go            字段事实源（DTO 的 json/dc/v/in 标签）
internal/codes/codes.go  错误码事实源（常量 + codeTable 映射）
internal/cmd/routes_*.go 路由/认证/权限码事实源
        │
        └── tools/openapi-gen ──► openapi/surgecart.openapi.yaml（唯一入口）
                                 openapi/components/*.yaml（按类型拆分）
                                 openapi/paths/*.yaml（分域评审视图）
                                 docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md
```

生成器同时承担门禁：`-check` 用**同一套解析器**重新推导，再与磁盘上的 YAML 深比较，防止"生成逻辑"和"检查逻辑"各写一份而互相漂移。

GPT-6 第一轮审查确认了 5 类问题：D1 的 13 条错误响应需要逐条核实（R1）、D5 遗留的
`IdempotencyKeyHeader` 组件（R2）、Spectral 关闭 `oas3-unused-component` 后缺少替代检查（R3）、
主文件与分域 Paths 只有数量级校验且注释把 Path Item `$ref` 说错（R4）、跨包调用导致错误码可能遗漏（R5）。

本次整改的边界很硬：**不改生产业务代码，不改已批准的 D1～D5 决策，不重写生成器架构。**
落点集中在 `tools/openapi-gen` 与 `openapi/`（生成物），外加门禁脚本与文档。

---

## 2. OpenAPI 3.1 Components 引用机制

三个关键点：

1. **组件是"地址"，不是"内联"**。`components.schemas.Product` 只是一个可被 `$ref` 指向的节点，
   节点名（`Product`）由业务语义决定，与 Go 类型名解耦（`product.Product` → `Product`）。
2. **`$ref` 不产生运行时语义**。它只表达"这里引用那个结构"，工具负责解析。
   因此"被引用"= 可达，而不是"被复制"。
3. **JSON Pointer 是 `#` 之后的片段**（RFC 6901）。`#/components/schemas/Product` 与
   `schemas.yaml#/Product` 指向同一业务组件，只是书写上下文不同。

本项目在主文件里用的是**"占位 + 转指"**手法：

```yaml
components:
  schemas:
    Product:
      $ref: ./components/schemas.yaml#/Product
```

这样主文件仍然是唯一入口（工具只读主文件即可发现全部组件），同时组件定义按类型拆到独立文件，
避免单文件上万行、评审 diff 噪声爆炸。**代价是引用关系被打散成两层**：

- operation → `#/components/schemas/X`（主文件内）
- `components.schemas.X` → `./components/schemas.yaml#/X`（跨文件）
- `schemas.yaml` 内部嵌入结构 → `#/Y`（文件内本地引用）

做可达性分析时必须把这三层**归一化到同一个节点键**（`type/name`），否则会得出"全部未引用"或
"全部已引用"的错误结论。`components_reach.go` 里的 `parseRef` 就是干这件事的：

| 写法 | 解析结果 |
|---|---|
| `#/components/schemas/X` | `{schemas, X}` |
| `#/Y`（在 schemas.yaml 内） | 由 owner 所在文件反推类型 → `{schemas, Y}` |
| `./components/responses.yaml#/BadRequest` | 由文件→类型映射 → `{responses, BadRequest}` |
| `./components/error-codes.yaml#/codes` | 非组件类型，忽略（它是 `x-error-codes` 的数据源） |

---

## 3. `$ref` 的递归引用与组件可达性分析

组件之间可以嵌套（`OrderCreateResponse` → `Order` → `OrderItem`），也可能自引用（树结构）。
所以不能用"扫一遍 `$ref` 字符串，出现即算引用"的朴素做法，必须做**图遍历**。

实现（`tools/openapi-gen/components_reach.go`）：

1. **声明集**：遍历 `components.<type>.<name>`，得到所有节点；同时从占位 `$ref` 反推"文件 → 组件类型"映射。
2. **定义解析**：把占位 `$ref` 解析成真实定义（读 `openapi/components/*.yaml`），并记住定义所在文件，
   供本地 `#/Y` 引用定位类型。
3. **边**：对每个定义递归收集 `$ref`，转成节点边。
4. **根**：`paths` 全量（含 planned 占位）+ 主文件除 `components` 外的顶层字段（如 `x-error-codes`）。
5. **BFS**：从根出发沿边遍历，得到可达集；`声明集 - 可达集 = 未引用组件`。

两个容易踩的坑：

- **Security Requirement 不是 `$ref`**。`security: [{bearerAuth: []}]` 的 `bearerAuth` 是名称引用，
  在 OpenAPI 里解析到 `components.securitySchemes.bearerAuth`。只扫 `$ref` 会把认证方案误判成未引用。
  因此遍历时对 `security` 键单独处理。
- **不可达 ≠ 无意义**。见下一节。

---

## 4. 为什么不能简单删除全部未引用 Schema

本仓库当前 323 个 Schema，其中 **154 个未被任何 `$ref` 引用**：142 个 `*Request` + 12 个
null-data `*Response`。它们不是垃圾，而是**审计资产**：

- operation 的 `requestBody` 只包含 body 字段，query/path 字段进了 `parameters`，所以
  `requestBody.schema` 是"完整请求 DTO 的一个子集"，不能直接 `$ref` 完整 Req Schema；
- handler 显式返回 `nil` 的写接口，实际响应 `data: null`，而对应 `Res` DTO 是空结构体，
  契约里把 `data` 声明为 `type: null` 才是真话，也不能 `$ref` 空对象 Schema；
- 但这些完整 DTO Schema 是"DTO 全量覆盖"的证据：`components/schemas.yaml` 必须与
  `api/*/v1` 的每个 DTO 一一对应，`-check` 第 8 步会做双向比对。删掉它们等于放弃覆盖承诺。

所以正确做法不是删，而是**"允许保留 + 逐条登记理由 + 拒绝未知项"**：

- 白名单在 `tools/openapi-gen/unused-components-allowlist.yaml`，按"类型 + 理由"分组，组内逐个列名；
- 禁止通配（名称里出现 `*`、`?`、`/` 直接拒绝）；
- 白名单条目若已不存在 → 报"过期记录"；若实际已被引用 → 报"应从清单移除"；
- 未在白名单里的未引用组件 → 检查失败。

这样"新增未引用组件"会立刻失败，而保留项始终有人负责解释。

---

## 5. OpenAPI 主文件与分域视图如何保持一致

架构保持不变：**同一份 `[]*API` 数据，两次渲染**。

- 主文件：`paths` 内联（Path Item 直接写在主文件）；
- 分域文件：按业务域切成 `openapi/paths/<domain>.yaml`，只用于评审。

为什么内联而不是 Path Item `$ref`？**OpenAPI 3.1 的 Path Item 是允许 `$ref` 的**——B1-02 头注释里
"3.1 不支持"的说法是错的，本次已修正。真实原因是工程取舍：

1. 主文件要能被 Spectral / Redocly 等工具**单独**解析，不依赖分域文件的存在与路径；
2. 门禁要能**定位差异**：内联后可以直接对同一条 path 的两个 PathItem 做结构深比较，
   而跨文件 `$ref` 一旦解析失败，报错往往只告诉你"引用坏了"，不告诉你"哪条 operation 变了"；
3. 分域视图只承担评审职责，不需要被任何工具解析。

一致性检查（`tools/openapi-gen/paths_check.go`）刻意**不比较数量**：

- 每条非 planned 路径必须两边都存在（缺失/多余都报）；
- 同一路径不得出现在两个分域文件（会拆散 PathItem）；
- 同一条路径的 PathItem 做 JSON 归一化后深比较，报出首个差异键路径（如 `get.responses.200...`）。

注意 planned 路径（`x-status: planned`）只存在于主文件，比对时必须排除，否则永远失败。
判断 planned 要**看 operation 层**，不是 PathItem 层——这是本次实现时踩到的第一个坑。

---

## 6. Go AST 错误码分析的能力边界

`tools/openapi-gen/analyze_logic.go` 做的是**有限范围的静态可达性分析**：

- 解析 `internal/logic/<domain>/*.go` 的每个函数体，收集 `codes.CodeXxx` 引用；
- 收集"同包调用"：`foo()` 与 `x.foo()` 都只按**方法名**记录；
- 从 controller 的 `service.X().Method(...)` 拿到入口方法名，在**同域** logic 包内按名字做 BFS（深度 ≤ 6）。

它有三个明确的盲区，本次已核实并写进契约说明：

1. **跨包**：logic 里再调 `service.Inventory().DeductInTx(...)`，对方域的 `6001` 不会被收集。
2. **按名匹配、无接收者类型**：`funcCodes[domain][name]` 把同名函数（不同接收者/不同文件）合并，
   既会漏（名不匹配）也会多（把 A 类型的 `GetByID` 当成 B 类型的）。本次排查中
   `Product.GetByID` 就因为同名合并被误判为"返回 4001"，实际它返回 `nil, nil`。
3. **动态分派**：接口实现、map 分派、回调都无法静态确定。

因此契约里 `x-error-codes` 的 `scope` 从 `call-chain` 改成 **`static-call-chain`**，note 明确写了
"静态上界、不表示必然触发、不覆盖动态分派"。**不要把它读成"运行时可达性证明"。**

---

## 7. 业务错误码与 HTTP 状态码的区别

这是本项目最容易混淆的一层：

| 维度 | 业务错误码 `code` | HTTP 状态码 |
|---|---|---|
| 语义 | 客户端分支判断的**唯一稳定契约** | 传输层语义，给网关/日志/监控看 |
| 取值来源 | `internal/codes/codes.go` 常量 | `codeTable` 映射，未知码兜底 500 |
| 是否可枚举 | 是（93 个，含 0） | 否（只看类别） |
| 是否可复用 | 一个 HTTP 状态对应多个 code | 一个 code 只对应一个 HTTP 状态 |

链路：

```
DTO v:"required" ──gvalid失败──► gcode 51 ──gerror.Code──► codes.FromError ──► 51
                                                                               │
                                              codeTable 未命中 ──► HTTPStatus = 500
                                                                               │
                                                     Message(51) 未命中 ──► "内部错误"
```

所以 **51 是框架码穿透，不是业务码**：它既不在常量表里，也不在码表里，落进了兜底分支。
这解释了为什么"参数错误"最终表现为 `500 + 51 + 内部错误"`。

D1 的目标规范是把 gvalid 归一成 `1001 → 400 → 参数错误`，但**本次禁止改生产代码**，
所以契约只能"如实记录双轨"：operation 同时声明 `400`（service 显式校验路径）与 `500`（gvalid 路径），
并用 `x-known-issue` 标注实际行为。**契约不能为了让测试好过而美化。**

---

## 8. D1 的实际处理过程

步骤与证据：

1. 从 `opMetaByKey` 取出带 `B1-02-D1` 的 operation（原 13 条），核对 method/path 与路由注册一致
   （`assembleAPIs` 会用 DTO 的 `g.Meta` 反查，路径写错会直接失败）。
2. 反向核对"所有 DTO"：扫描 142 条 API 的 Req DTO，列出所有**非 path** 的 `v:"required"` 字段。
   GoFrame 在 controller 执行前完成绑定 + gvalid，因此这些字段缺失必然走 51 → 500，
   与 service 是否显式校验无关。
3. 结果：命中 14 条，比 D1 表多一条 **`POST /admin/roles/:id/permissions`**（`body.permission_id` 必填，
   `internal/controller/admin/admin.go` 是标准 controller，无手工兜底）。
   这是 B1-01/D1 的事实清单漏项，本次在契约中补标，并在交付报告里明确告知 Owner。
4. 运行时验证：用真实 gvalid + 真实 `middleware.Response` 起一个临时 `ghttp` 服务，POST `{}` 到
   带 `v:"required"` 的 DTO，实测 `500 {"code":51,"message":"内部错误","data":null}`，
   与静态推断一致（探针代码用完即删，不进入交付）。
5. 逐接口的运行时验证（需要 MySQL/Redis）→ 标记 **NOT_VERIFIED**，不写成 PASS。

补标后新增了一条**自动化守卫**：`TestD1KnownIssuesCoverAllRequiredFields` 断言
"D1 标注集合 == 非 path required 字段集合"，两边任一漂移都会失败。这样以后谁加一个必填字段，
契约必须同步标注，否则 CI 红。

---

## 9. 具体修改的生成器文件与关键函数

| 文件 | 关键函数 / 位置 | 改动 |
|---|---|---|
| `tools/openapi-gen/components.go` | `buildParametersComponent` | 删除 `IdempotencyKeyHeader` 定义，留注释说明 D5 已否决（K-1） |
| `tools/openapi-gen/gen.go` | `parametersComponentNames` | 移除 `IdempotencyKeyHeader` 登记 |
| `tools/openapi-gen/gen.go` | `writeAll` / `buildMainDocument` | 修正 Path Item `$ref` 注释（3.1 允许，但选择内联）；`d1AffectedCount` 让 D1 条数由代码推导 |
| `tools/openapi-gen/gen_paths.go` | `errorCodesFor` / `buildResponses` | 并入跨包补充登记；`scope` 改为 `static-call-chain`，note 说明静态上界 |
| `tools/openapi-gen/opmeta.go` | `opMetaByKey` | 新增第 14 条 D1 标注 |
| `tools/openapi-gen/analyze_logic.go` | 新增 `reachableFuncs` | 暴露"从入口可达的同包函数集"，供补充登记自校验 |
| `tools/openapi-gen/components_reach.go`（新增） | `buildComponentGraph` / `graph.reachable` / `validateComponentUsage` / `loadUnusedAllowlist` | 组件可达性 + 白名单治理 |
| `tools/openapi-gen/paths_check.go`（新增） | `verifyPathViews` / `checkPathViews` / `isPlannedPathItem` | 主/分域 Paths 结构化比对 |
| `tools/openapi-gen/errcodes_crosspkg.go`（新增） | `crossPackageErrorCodes` / `loadCrossPackageEvidence` / `validateCrossPackageRegistry` | 跨包错误码补充登记 + 代码事实自校验 |
| `tools/openapi-gen/check.go` | 第 10～12 步 | 接入未引用组件、Paths 一致性、补充登记自校验 |
| `tools/openapi-gen/review_fix_test.go`（新增） | 9 个用例 | R1/R3/R4/R5 的自动化覆盖 |
| `scripts/contract-check.sh` | 步骤 2 | 输出新增检查项，避免"脚本没跑"与"跑了但没查"混淆 |

---

## 10. 遇到的问题、根因与解决方案

**问题 1：Paths 一致性检查把 planned 路径判成"分域缺失"。**
根因：`x-status: planned` 写在 **operation** 上（`buildPlannedPathItem` 里 `op.Set("x-status", ...)`），
而检查最初读的是 PathItem 层。
解决：`isPlannedPathItem` 遍历 PathItem 下的 method，任一 operation 标记 planned 即视为 planned。

**问题 2：跨包遗漏分析一上来就把 11 条接口全标成"缺错误码"，其中大半是假阳性。**
根因：`analyzeLogic` 的 `calls` 表按**方法名**记录同包调用，且不区分接收者类型；
`order.List → toOrder → ... → Detail → cancelExpired` 这种链路是靠"名字撞上"串起来的，
`Product.GetByID` 也被同名函数污染。
解决：不用这套图去"自动发现遗漏"，改为**人工读代码确认调用链与错误传播**，再逐条登记；
补充登记表由 `validateCrossPackageRegistry` 反向校验（常量存在、目标函数确实引用、
调用链里确实有 `service.X().M()`），把"人肉结论"变成可回归的断言。

**问题 3：`x-error-codes` 不在 operation 顶层，而在 `responses` 里。**
根因：`buildResponses` 直接在 `responses` 对象上 `Set("x-error-codes", ...)`（B1-02 既有位置）。
解决：不改位置（避免无谓契约变更），测试按实际位置读取；在文档里标注这个"反直觉位置"，
后续若要挪到 operation 级，应作为独立契约变更并同步前端。

**问题 4：白名单一下子要列 160 条，容易变成"随手加"。**
根因：142 个 Request Schema 天然不会被 `$ref`（请求按字段位置内联）。
解决：清单按"类型 + 理由"分组、组内逐个列名（不是通配），并让检查同时校验
"过期/已引用/缺理由/重复"，把白名单从"放行名单"变成"需要解释的债务清单"。

---

## 11. 测试验证与未验证事项

已执行（真实输出见交付报告）：

- `gofmt -w tools/openapi-gen/`、`go build ./...`、`go vet ./...`、`make test-unit`；
- `scripts/contract-check.sh`：生成无漂移、`-check` 通过、路由双向、Spectral、生成器单测；
- 新增单测：组件可达性（含嵌套、security）、白名单四类失败、Paths 四类漂移、D1 集合一致性、
  补充登记真伪校验、契约中确实出现补充码。

未验证 / 不声称：

- 14 条 D1 接口的**逐接口运行时**响应：需要 MySQL/Redis 启动完整应用，本次未启动；
- 只验证了机制层面（gvalid → 51 → 500）可复现，不能推断每条接口的其它业务分支；
- `x-error-codes` 的完整性仍是"静态上界 + 人工补充"，不是运行时穷举。

---

## 12. 后续维护不能轻易破坏的设计

1. **单一事实源**：DTO / codes.go / routes_*.go 仍然是唯一源头，`openapi/` 全部生成。
   手工改 YAML 会被 `contract-check.sh` 步骤 1 的指纹比对抓住。
2. **`-check` 与生成共用解析器**：新增检查时不要另写一套解析，否则两边会漂移。
3. **白名单必须逐条列名**：不要为了提高便利性引入前缀/后缀通配；那是把治理退化成放行。
4. **D1 集合由测试守卫**：给 DTO 增加 `v:"required"` 必须同步 opMeta，否则单测失败。
5. **`x-error-codes` 的语义是"静态上界"**：不要在前端文档里写成"只会返回这些码"；
   也不要为了"看起来干净"删掉补充登记里的跨包码。
6. **Paths 内联是刻意选择**：`paths/*.yaml` 不是主文件的引用目标，改回 Path Item `$ref`
   会让 Spectral/Redocly 的解析行为依赖文件布局，需要重新评估门禁。
7. **补充登记表是人工核实结果**：新增条目必须走"读代码 → 确认错误传播 → 登记 → 由 check 反验"的流程，
   不能凭错误码名称猜测。
