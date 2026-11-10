# B1-02-review-fix 交付报告

| 项目 | 内容 |
|---|---|
| 任务编号 | B1-02-review-fix |
| 阶段 | Phase 2.0（B1-02 有限整改） |
| 所属分支 | `feat/p2-backend` |
| 整改基线提交 | `74d5c17376942512dc44893d6042ab45240ec607` |
| 代码/契约提交 | `482672a` |
| 执行时间 | 2026-11-10 |
| 最终结论 | **PASS**（含 1 项需 Owner 确认的事实补正，见 §3.1） |

---

## 1. 任务目标

在不扩大 B1-02 范围、不修改生产业务代码的前提下，完成 GPT-6 第一轮独立审查发现的 5 类问题整改
（R1～R5），使 OpenAPI 契约与自动化门禁达到可靠交付标准，为 B1-03 数据模型与前后端联调提供稳定基线。

---

## 2. 实际修改范围

**修改（全部为契约生成器、生成物、门禁脚本与文档）：**

| 类别 | 文件 |
|---|---|
| 生成器 | `tools/openapi-gen/{components,gen,gen_paths,opmeta,analyze_logic,check}.go` |
| 生成器新增 | `tools/openapi-gen/components_reach.go`、`paths_check.go`、`errcodes_crosspkg.go`、`review_fix_test.go`、`unused-components-allowlist.yaml` |
| 生成物 | `openapi/surgecart.openapi.yaml`、`openapi/components/parameters.yaml`、`openapi/paths/*.yaml`（21 个）、`openapi/.spectral.yaml` |
| 门禁 | `scripts/contract-check.sh` |
| 文档 | `.agent/tasks/dev/B1-02-review-fix.md`（新增）、`.agent/tasks/dev/openapi-contract.md`（已知限制更新）、`docs/phase2/B1-02_CONTRACT_REPORT.md`（增补整改指引与 K-1 关闭说明）、本报告 |

**未修改（已核对 `git status`，零改动）：** `api/**`、`internal/**`（含认证、商品/SKU/库存、订单状态机、
支付退款、Redis、Kafka、Outbox、Migration）、`manifest/**`、`docker-compose.yml`、`Dockerfile`、`nginx/**`、
`prometheus/**`、前端相关目录、`docs/phase2/B1-02_CONTRACT_DECISIONS.md`（D1～D5 决策正文保持原样）。

生成物差异的构成：① 22 个分域视图文件仅头注释修正（R4）；② `parameters.yaml` 删除 1 个组件（R2）；
③ 主文件新增补充错误码与 `scope` 说明（R5）、新增 1 条 D1 标注（R1）、删除 K-1 组件引用（R2）。

---

## 3. R1～R5 完成情况

| 编号 | 事项 | 结论 | 说明 |
|---|---|---|---|
| R1 | 核实 D1 的错误响应 | **完成（发现 1 条漏项并补标）** | 全量反查 DTO 必填字段，确认 14 条（原 13 条）；运行时探针复现 500/51；契约如实声明 400+500 双状态 |
| R2 | 处理 K-1 `IdempotencyKeyHeader` | **完成** | 生成器定义与登记同时删除后重新生成，主文件/组件文件均为 0 次出现 |
| R3 | 未引用组件自动化治理 | **完成** | 可达性分析 + 逐条列名白名单 + 6 类失败场景单测，已接入 `-check` 与门禁脚本 |
| R4 | Paths 一致性检查 | **完成** | 结构化逐字段比对 + 重复/缺失/多余检测；修正 Path Item `$ref` 的错误注释 |
| R5 | 跨包错误码分析能力核实 | **完成（方案 B）** | 确认 2 条接口存在真实遗漏，采用补充登记 + 代码事实自校验；不重写 AST 分析器 |

### 3.1 R1 逐接口映射表

判定链路：DTO `v:"required"` → GoFrame 绑定阶段 gvalid 失败 → `gcode.CodeValidationFailed`(51)
→ `codes.FromError` 返回 51 → `codes.HTTPStatus(51)` 查表未命中 → 兜底 **HTTP 500**
→ `codes.Message(51)` 未命中 → `"内部错误"`。13 条原有接口 + R1 复核新增的 1 条，共 14 条。

| # | Method | Path | 触发字段（非 path 必填） | 实际错误码 | 实际 HTTP | OpenAPI HTTP | 结论 |
|---|---|---|---|---|---|---|---|
| 1 | POST | `/admin/login` | username, password | 51 | 500 | 400 + 500 | 契约已如实标注 `x-known-issue` |
| 2 | POST | `/admin/admins` | username, password | 51 | 500 | 400 + 500 | 同上 |
| 3 | PUT | `/admin/admins/:id/status` | status | 51 | 500 | 400 + 500 | 同上 |
| 4 | POST | `/admin/admins/:id/roles` | role_id | 51 | 500 | 400 + 500 | 同上（`data` 为 null） |
| 5 | POST | `/admin/roles` | name | 51 | 500 | 400 + 500 | 同上 |
| 6 | POST | `/admin/permissions` | code, name | 51 | 500 | 400 + 500 | 同上 |
| 7 | PUT | `/admin/users/:id/status` | status, reason | 51 | 500 | 400 + 500 | 同上 |
| 8 | POST | `/categories` | name | 51 | 500 | 400 + 500 | 同上 |
| 9 | POST | `/admin/skus` | product_id, name, price | 51 | 500 | 400 + 500 | 同上 |
| 10 | POST | `/admin/refunds/:id/review` | result, reason | 51 | 500 | 400 + 500 | 同上 |
| 11 | POST | `/admin/refunds/:id/execute` | result, reason | 51 | 500 | 400 + 500 | 同上 |
| 12 | POST | `/admin/refunds/:id/close` | reason | 51 | 500 | 400 + 500 | 同上 |
| 13 | POST | `/orders` | source, address_id, idempotency_key | 51 | 500 | 400 + 500 | 同上 |
| 14 | POST | `/admin/roles/:id/permissions` | permission_id | 51 | 500 | 400 + 500 | **R1 新发现**：B1-01/D1 表漏项，契约补标 |

核实方法与证据：

1. **Controller 参数绑定**：14 条均为标准 `ControllerV1` 方法（`internal/controller/**`），签名
   `func(ctx, *v1.XxxReq) (*v1.XxxRes, error)`，由框架在 handler 执行前完成绑定 + gvalid，无手工兜底。
2. **参数校验**：扫描全部 142 条 API 的 Req DTO，只有上述 14 条含"非 path 的 `v:"required"` 字段"
   （其余 required 全在 `in:"path"` 上，路径段缺失时由路由匹配直接 404，不进入 gvalid）。
3. **错误码 51 的产生路径**：`internal/codes/codes.go` 的常量表与 `codeTable` 均无 51 → `HTTPStatus`/`Message`
   落兜底分支（`internal/middleware/response.go`）。
4. **业务码 → HTTP 映射**：`error-codes.yaml` 由 `codes.go` 的 `codeTable` 解析生成（93 个码），
   `-check` 与源码深比较，本次未改动映射。
5. **运行时验证**：使用真实 `gvalid` + 真实 `middleware.Response` 起临时 `ghttp` 服务，
   POST `{}` 到带 `v:"required"` 的 DTO，实测 `500 {"code":51,"message":"内部错误","data":null}`，
   与静态推断一致。临时探针用完即删，未进入交付。
6. **偏差处理**：第 14 条接口属 D1 事实表漏项。D1 的**规范决策**（目标 400/1001、本次不改代码、
   双轨记录 + 已知问题标注）未被改动，仅按 D1.4 的标注规则补标实际行为。此项需 Owner 知晓并在
   下次修订 D1 时把条数改为 14（本任务禁止改决策正文）。

### 3.2 R2

- 删除 `components.go::buildParametersComponent` 中 `IdempotencyKeyHeader` 定义；
- 删除 `gen.go::parametersComponentNames` 中对应登记；
- 重新生成后 `openapi/components/parameters.yaml` 与 `openapi/surgecart.openapi.yaml` 均 0 次出现
  `Idempotency-Key` / `IdempotencyKeyHeader`；
- JSON body 的 `idempotency_key` 与 `x-idempotency`（mode=body/query）保持不变；
- `docs/phase2/B1-02_CONTRACT_DECISIONS.md` §D5 决策历史未改动。

### 3.3 R3

- 可达性口径：根 = `paths`（含 planned）+ 主文件顶层扩展（含 `x-error-codes` 的引用）；
  边 = 组件定义内 `$ref`（含 `#/X` 本地引用与 `./components/*.yaml#/X` 跨文件引用）；
  另对 `security` requirement 单独解析到 `securitySchemes`。
- 覆盖类型：`schemas`、`responses`、`parameters`、`securitySchemes`（脚本按 `components` 下实际声明的
  所有类型遍历，不硬编码类型白名单）。
- 实测未引用组件 160 个：142 个完整 Request Schema（DTO 覆盖审计）、12 个 null-data Response Schema、
  2 个通用响应模板、4 个分页语义参数。逐条列名于
  `tools/openapi-gen/unused-components-allowlist.yaml`（按"类型 + 理由"分组，组内逐个列名，禁止通配）。
- 检查失败条件：未登记的未引用组件、白名单条目已不存在、白名单条目实际已被引用、条目缺理由、
  重复登记、名称为通配。已接入 `-check` 第 10 步与 `contract-check.sh` 步骤 2。
- Spectral `oas3-unused-component` 仍为 `off`，注释已改为指向替代检查。

### 3.4 R4

- `verifyPathViews` 结构化比对：主文件非 planned 路径与 21 个分域文件的并集逐条对齐，
  比较 PathItem 全量内容（JSON 归一化后深比较，差异定位到首个不同键）；
- 检测：主文件有而分域缺失、分域有而主文件缺失、同一路径落入多个分域文件、PathItem 内容漂移；
- 实测：主文件 116 条路径（含 4 条 planned）与 112 条分域路径逐字段一致；
- 注释修正：Path Item `$ref` 在 OpenAPI 3.1 中**是允许的**，原注释"3.1 不支持"不准确；
  已改为说明"允许但刻意内联"的三条工程理由（工具兼容性、门禁差异可定位、分域视图不参与解析）。
  涉及 `gen.go`（主文件与写分域视图处）、`gen_paths.go`（`buildPathItem`）与 21 个分域文件头。

### 3.5 R5

核实结果：`analyzeLogic` 只覆盖 `internal/logic/<domain>` 同包调用，而 logic 层确实存在
`service.<Other>()` 跨包调用（订单/退款/结算链路）。确认存在**少量真实遗漏**，选择**方案 B**：

| Method | Path | 补充错误码 | 代码依据 |
|---|---|---|---|
| POST | `/orders` | 7001、19003、19007、6001 | `address.Detail` / `coupon.GetDiscount` / `coupon.LockInTx` / `inventory.DeductInTx`，错误均直接 `return`，事务整体回滚 |
| POST | `/admin/refunds/:id/execute` | 9002 | `order.CompleteRefundInTx` 订单非 paid(20) → 0 行更新，退款整体回滚 |

未纳入的静态可达码及理由（记录在案，避免被误认为遗漏）：
- `Inventory.IncreaseInTx` 的 6002（qty<1）与 5001（SKU 不存在）：调用方传入的是订单项持久化数量（≥1），
  且存在库存记录的 SKU 无法删除（5005），数据不变量使其不可达；
- `Product.GetByID` 的 4001：同名函数合并导致的假阳性，该实现未命中时返回 `nil, nil`。

机制与保障：
- 登记表 `crossPackageErrorCodes` 记录 `Service/Method/Code/Why`；
- `validateCrossPackageRegistry`（`-check` 第 12 步）校验：接口真实存在、常量在 `codes.go` 中真实存在且非 0、
  目标 logic 函数确实引用该常量、该 API 调用链中确实存在对应 `service.X().M()` 调用、不得重复登记、
  不得登记已被同包分析覆盖的码、必须有触发条件说明；
- 契约中 `x-error-codes.scope` 由 `call-chain` 改为 **`static-call-chain`**，note 明确"静态上界、
  不表示必然触发、不覆盖动态分派"，避免把有限分析伪装成运行时穷举。

---

## 4. 关键文件清单

| 文件 | 作用 |
|---|---|
| `tools/openapi-gen/components_reach.go` | 组件可达性分析 + 白名单加载/校验 |
| `tools/openapi-gen/unused-components-allowlist.yaml` | 160 个允许保留组件的逐条登记（类型 + 理由） |
| `tools/openapi-gen/paths_check.go` | 主文件与分域 Paths 结构化一致性检查 |
| `tools/openapi-gen/errcodes_crosspkg.go` | 跨包错误码补充登记与代码事实自校验 |
| `tools/openapi-gen/review_fix_test.go` | R1/R3/R4/R5 的 10 组新增自动化用例 |
| `tools/openapi-gen/{components,gen,gen_paths,opmeta,analyze_logic,check}.go` | K-1 移除、注释修正、D1 补标、可达函数集、门禁接线 |
| `scripts/contract-check.sh` | 门禁输出项更新（未引用组件 / Paths 一致性 / 跨包登记） |

---

## 5. 测试命令与真实结果

| # | 命令 | 退出码 | 结果 |
|---|---|---|---|
| 1 | `gofmt -l tools/openapi-gen/` | 0 | 无输出（格式干净） |
| 2 | `go build ./...` | 0 | PASS |
| 3 | `go vet ./...` | 0 | PASS（无告警） |
| 4 | `make test-unit` | 0 | PASS（含 `internal/cmd` 路由契约测试、`tools/openapi-gen` 17 个用例） |
| 5 | `go run ./tools/openapi-gen -repo . -check` | 0 | `142 条常驻 API + 4 条 planned 占位，323 个 Schema，160 个未引用组件（均已在白名单登记）`；`主文件 116 条路径（planned 4 条）与 112 条分域路径逐字段一致` |
| 6 | `bash scripts/contract-check.sh` | 0 | PASS，`NOT RUN 0 项` |

逐项覆盖（与任务书要求对应）：

| 要求 | 覆盖方式 | 结果 |
|---|---|---|
| Go AST 生成器单测 | `go test ./tools/openapi-gen/`（17 用例） | PASS |
| OpenAPI 生成一致性 | 脚本步骤 1（生成前后内容指纹） | PASS（无漂移） |
| 142 条路由与契约双向核对 | 脚本步骤 3（真实 GoFrame 路由表） | PASS |
| DTO 与 Schema 一致性 | `-check` 第 8 步 + 脚本步骤 2 | PASS |
| 93 个错误码与定义一致 | `-check` 第 7 步（`error-codes.yaml` 深比较） | PASS |
| 49 个权限码 ↔ 路由 ↔ seed | `-check` 第 6 步 | PASS |
| planned API 隔离 | `-check` 第 4 步 + 脚本步骤 3 | PASS |
| Spectral lint | 脚本步骤 4（`@stoplight/spectral-cli@6`） | PASS（无 error） |
| 未引用组件检查 | `-check` 第 10 步 + 6 类失败单测 | PASS |
| 主/分域 Paths 结构化一致 | `-check` 第 11 步 + 4 类漂移单测 | PASS |
| K-1 Header 不再生成 | 重新生成后全文检索为 0 | PASS |
| D1 错误响应映射 | 14 条逐接口表 + 集合一致性单测 + 机制级运行时探针 | PASS（逐接口运行时见 §6） |

`scripts/contract-check.sh` 的写盘副作用：步骤 1 会运行生成器并重写 `openapi/` 与
`docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md`。执行前后 `git status` 文件数一致（38 → 38，无新增改动），
证明生成结果与提交内容一致，无漂移。

---

## 6. NOT RUN / NOT VERIFIED

| 项 | 状态 | 原因 |
|---|---|---|
| 14 条 D1 接口的逐接口运行时响应 | **NOT VERIFIED** | 需要 MySQL/Redis 启动完整应用；本任务未启动依赖，仅验证了机制链路（gvalid → 51 → 500）可复现 |
| `make test` 全量门禁（集成/race/Kafka E2E） | **NOT RUN** | 无 MySQL/Redis/Kafka 环境，任务书允许不为此启动依赖 |
| `x-error-codes` 的运行时穷举 | **NOT VERIFIED** | 契约声明为静态上界，未做运行时埋点验证 |
| 前端对 400/500 双状态的实际处理 | **NOT VERIFIED** | 超出本次后端契约范围 |

---

## 7. Git

| 项 | 内容 |
|---|---|
| 当前分支 | `feat/p2-backend` |
| 整改前 HEAD | `74d5c17376942512dc44893d6042ab45240ec607` |
| 代码/契约提交 | `482672a`（`fix(openapi): B1-02 契约独立审查整改（R1-R5）`） |
| 报告提交 | 本次提交（本文件），推送后为最终 HEAD |
| 推送 | 推送至 `origin/feat/p2-backend`（fast-forward，无 force push） |
| 工作区 | 提交后干净 |
| 禁止范围 | 未修改 `api/**`、`internal/**`、manifest、Docker/K8s、前端、D1～D5 决策正文 |

---

## 8. 已知限制

1. **D1 条数与决策文档不一致（需 Owner 确认）**：D1.2 表述 13 条，实际 14 条。契约已按实际行为补标，
   但决策正文按"不改已批准内容"原则未动，建议 Owner 在下一次决策修订时同步条数。
2. **静态分析仍是上界**：同包按名匹配存在误报/漏报，跨包靠人工登记；`x-error-codes` 不应被当作
   "运行时只会返回这些码"的依据。
3. **白名单维护成本**：新增 API 会新增一个未引用 Request Schema，需在白名单追加一行，
   这是刻意的"必须解释"摩擦。
4. **`x-error-codes` 位置**：由 B1-02 放在 `responses` 对象内（非 operation 顶层），本次未迁移；
   若后续迁移属于契约变更，需同步前端。
5. **未引用组件检查不覆盖**：Spectral 规则本身仍关闭；若将来引入 `requestBodies`/`headers` 等新组件类型，
   检查会自动覆盖，但白名单需要相应新增理由。

---

## 9. 最终交付结论

**PASS。** R1～R5 全部完成并通过自动化门禁；未修改任何生产业务代码与已批准决策正文；
所有未执行/未验证事项均如实标注，无伪造 PASS。

交付物：

- 开发知识文档：`.agent/tasks/dev/B1-02-review-fix.md`
- 本报告：`docs/reports/B1-02-review-fix.md`

等待 Owner 与 GPT-6 复审。
