# Task: 错误码 Namespace 扩展（Error Code Namespace Expansion）

## Goal

在不改动、不重新编号任何已落地错误码（`1000-8999`）的前提下，定义一套明确的 **错误码 Namespace 扩展规则**，使未来模块（至少 Order、Payment 等）拥有充足且可继续扩展的空间，同时保持既有 API 错误码与 `{code,message,data}` 响应协议对 Backend、Frontend、Tests、Docs、Registry、Validator 全部兼容。

用户可观察结果：当 Order 占用 `9000-9999` 之后，再新增 Payment 等模块时，新机制仍能正常分配出互不冲突、可被 Registry 预留、可被 Validator 校验、可被 Backend/Frontend/Tests 正确处理的错误码域，不会 BLOCKED 于 `9999`；且已存在的 `7001`/`8001` 等错误码取值在扩展前后保持原值。

## Problem Statement

当前错误码 Namespace 被建模为「4 位整数区间 `1000-9999`，千位 = Domain，每个 Domain 固定 1000 个编号」。这一模型把「Domain 身份编码」与「4 位整数」绑定在一起：`1000-1999` 到 `8000-8999` 已用 8 个域，仅剩 `9000-9999` 一个传统四位 Domain。后续仍计划建设的 Order / Payment / Promotion / Coupon / 可能的 Delivery / Refund / After-sales 等模块，会立即撞上「4 位上限 → 至多 9 个 Domain」的天花板。

关键事实（已核实，见 Relevant Context）：**瓶颈不在每个 Domain 内部编号耗尽**（8 个域实际只用了 2~11 个编号，总利用率 0.5%），而在于 **4 位上限把 Domain 总数限制在 9 个**。因此本任务的本质是「如何扩展未来 Namespace」，而不是「重排已有 Namespace」。

## Scope

- 完整统计当前 Error Code Domains 及实际使用数量（Utilization Matrix）。
- 确认现有错误码（`1000-8999`）的兼容性边界：哪些对象（Backend 测试 / Frontend / API Consumer / Contract / Design / 日志监控）依赖它们作为稳定事实。
- 明确是否存在四位数技术限制（类型、格式化、DB 字段、日志按范围识别、`code/1000` 域解析等）。
- 明确 Backend / Frontend / Tests / Docs 中是否存在 `<10000` 或四位长度假设。
- 形成未来 Namespace Expansion 的统一规则（域如何申请、如何预留、域内如何增长、删除是否复用）。
- 保证新规则与 Global Resource Reservation Registry（`.agent/registry/error-codes.md`）及 `scripts/check-registry.sh` Validator 兼容。
- 保证 Coder 仍只能使用已 Reservation / APPROVED 的 Domain，不自行推断编号。
- 保证 Validator 能支持扩展后的 Range（含 `10000+`）。

## Out of Scope

- 重编号现有 `1000-8000` Domain；为「整理得更漂亮」把 `8001` 改成 `10001` 等任何重新编号。
- 修改现有 API 业务行为、现有错误码取值与 `codeTable` 语义。
- 修改 HTTP Status 体系；把 business code 改成 HTTP Status 的替代品，或废弃业务错误码。
- 把业务错误码改为字符串 Enum。
- 国际化错误消息、Error Message 文案治理。
- 前端错误展示 UX 重构。
- Trace ID / Request ID、Observability Error Taxonomy、gRPC Status。
- Order / Payment 等模块本身的业务实现。
- 重新设计 Global Resource Reservation（该机制由 `global-resource-reservation` 任务负责）。
- 修改 Migration Version Registry（`.agent/registry/migrations.md`）。
- 修改历史 Task / Contract（`cart-v1`、`shipping-address-v1` 等仅作证据读取）。

调查中若发现上述问题，只记录为 Follow-up，不在本任务处理。

## Design Impact

Design Impact: UPDATE

Design Artifact: 错误码域 / Error Code Namespace（当前分散于 `docs/design/<module>.md` 各「错误码域」章节 + `docs/agent/*` 分配规则 + `.agent/registry/error-codes.md`；是否收敛为独立 `docs/design/error-codes.md` 由 Analyst 提案、Owner 决定）

说明：错误码是稳定公共 API 契约（`internal/codes/codes.go` 包注释「客户端依靠 code 判断错误类型」），且「错误码域」已被 `docs/design/*` 各模块的「错误码域」章节承载为项目级长期事实。本任务改变「千位划分、固定 1000、上限 9999」这一错误码域公开协议，属「修改长期公开协议」→ `UPDATE`。

**校正条件（交 Analyst，必要时回 Owner）**：若最终方案只改 `docs/agent/*` 与 `.agent/registry/error-codes.md` 的治理规则、完全不触碰 `docs/design/*` 已有内容，则 Design Impact 可能校正为 `NONE`；若最终决定新增独立 `docs/design/error-codes.md` 承载完整 Namespace 模型，则校正为 `NEW`。校正改变 Scope 时须回 Owner / Task Builder。

## Acceptance Criteria

- [ ] AC-001（Domain 统计完整）：产出覆盖全部当前 Error Code Domain 的利用率矩阵（Domain、Range、Used Count、Max Code、Utilization），与 `internal/codes/codes.go` 实际取值一致，且明确给出「仅剩 `9000-9999`」的容量结论。
- [ ] AC-002（兼容性边界明确）：明确列出所有依赖已有错误码为稳定事实的对象（Backend 测试、Frontend、API Consumer、Contract、Design、日志/监控），并断言「已落地错误码原则上 immutable」的成立条件与例外。
- [ ] AC-003（四位数限制结论明确）：明确判定 Backend / Frontend / Tests / Docs 中「是否存在四位数技术限制」，给出逐项证据（不得只以「Go `int` 能表示五位数」为由断言无兼容问题）。
- [ ] AC-004（`<10000`/四位长度假设审计）：明确判定是否存在 `<10000`、`String(code).length===4`、`code/1000`、`%04d` 等四位假设，逐项给出命中/未命中证据。
- [ ] AC-005（扩展规则统一且可派生）：形成统一 Namespace Expansion 规则，规则必须可由 Analyst 从 Registry 事实派生 next 域，不得依赖 Owner 人工计算编号。
- [ ] AC-006（不 renumber 已有域）：`1000-8999` 现有错误码取值在扩展方案下保持原值，方案中不存在「通过重新编号历史模块腾空间」的动作。
- [ ] AC-007（可容纳多个未来 Domain）：方案能继续容纳 Order、Payment 等多个新 Domain，而不是只解决下一个 `9000-9999` block。
- [ ] AC-008（Registry 兼容）：`.agent/registry/error-codes.md` 的分配规则（当前「按千位划分、固定 1000、`next = max(已记录域上限) + 1000`」）与扩展后的规则一致，Registry 能表达 `9000-9999 Order RESERVED`、`10000-10999 Payment RESERVED` 这类条目。
- [ ] AC-009（Coder 只能用已分配域）：扩展后 Coder 仍只能使用已 Reservation / APPROVED 的 Domain，禁止自行推断编号。
- [ ] AC-010（Validator 支持扩展 Range）：`scripts/check-registry.sh` 能对 `10000-10999` 等扩展 Range 正确执行重叠检测、`codes.go` 归属检测，不因数字变宽而误报或漏报。
- [ ] AC-011（错误码复用规则明确）：明确「历史错误码删除后是否允许复用」，并与 Registry 的 `ACTIVE` / `RESERVED` / `RELEASED` 语义一致。
- [ ] AC-012（不改变业务语义）：方案不改变任何现有错误码的 HTTP 状态映射、message 与业务含义。
- [ ] AC-013（不改变 HTTP/Business 分层）：保持「HTTP status + business code」两层语义现状，不把 business code 变成 HTTP status 替代品。
- [ ] AC-014（长期设计/治理一致）：错误码 Namespace 的长期设计事实（`docs/design/*`）与工程治理规则（`docs/agent/*`、`.agent/registry/*`）在扩展后最终一致，无双事实源冲突。
- [ ] AC-015（不依赖人工算号）：新规则不要求 Owner 手工查询、比对或编排编号。

## Relevant Context

### Current Namespace Model（已核实）

- 错误码类型：`internal/codes/codes.go` 中 `type Code = int`（对 `int` 的别名），单一 `const` 块定义全部业务码，配套 `codeTable map[Code]codeInfo{HTTPStatus, Message}` 提供 HTTP 状态与用户安全 message 映射。`CodeOK = 0`。
- 统一响应协议：`internal/middleware/response.go` 输出 `ResponseBody{Code int `json:"code"`; Message string `json:"message"`; Data any `json:"data"`}`；成功 `code:0`，失败 `code:业务码` 且 `data:null`，HTTP 状态由 `codes.HTTPStatus(code)` 派生。业务码与 HTTP 状态是两层语义（如 HTTP 404 + code `7001`、HTTP 409 + code `6001`）。
- Domain 编码：事实惯例为「千位 = Domain，域内递增」。8 个域已 `ACTIVE`：`1000-1999` Common、`2000-2999` IAM、`3000-3999` Category、`4000-4999` Product、`5000-5999` SKU、`6000-6999` Inventory、`7000-7999` Address、`8000-8999` Cart。仅剩 `9000-9999`。
- 正式规则落点：`docs/agent/AgentCollaborationSpecification.md` §11.3 与 `.agent/registry/error-codes.md`「分配规则」均写「按千位划分、大小固定 1000；下一个空闲域 = `max(已记录域上限) + 1000`」；`docs/agent/models/AnalystAgent.md` 同样写明该派生规则。**「千位 = Domain」是工程治理的正式规则，不是仅事实惯例**，但它记录在治理层（`docs/agent/*` + Registry），而非独立 `docs/design/*` 设计文档。

### Current Domain Utilization Matrix（已核实，来自 `internal/codes/codes.go`）

| Domain | Range | Used Count | Max Code | Utilization |
| --- | ---: | ---: | ---: | ---: |
| Common | 1000-1999 | 6 | 1005 | 0.6% |
| IAM | 2000-2999 | 11 | 2011 | 1.1% |
| Category | 3000-3999 | 5 | 3005 | 0.5% |
| Product | 4000-4999 | 7 | 4007 | 0.7% |
| SKU | 5000-5999 | 5 | 5005 | 0.5% |
| Inventory | 6000-6999 | 2 | 6002 | 0.2% |
| Address | 7000-7999 | 2 | 7002 | 0.2% |
| Cart | 8000-8999 | 3 | 8003 | 0.3% |
| **合计** | 1000-8999 | **41** | — | **0.5%** |

结论：每个域实际使用 2~11 个编号，**1000 个编号的 block 严重超配**；真正瓶颈是「4 位上限 → 至多 9 个域」，而非域内编号耗尽。这是「容量问题真实且近期」的关键证据：8000 已用，9000 是最后一个 4 位域，Order 一旦占用即触顶。

### Compatibility Findings（已核实）

- 已有错误码作为稳定事实被以下对象依赖：
  - **Backend 测试**：`internal/controller/*/*_test.go` 大量硬编码数字业务码断言（如 `res.Code != 7001`、`!= 1002`、`!= 1001`），且用**数字字面量**而非 `codes.Code*` 常量。
  - **Frontend**：`frotend_web` / `frotend_manage` 的 `src/utils/request.js` 判 `res.code !== 0`，视图层大量 `res.code === 0` 判成功；`request.js` 还判 `res.code === 50008 || 50012 || 50014`（模板遗留的五位码，见 Four-digit Assumption Audit）。
  - **Docs / Design**：`docs/design/<module>.md` 各「错误码域」章节硬编码 Range 与具体码（如 `address.md` §6 `7000-7999 归地址域`、`product.md` §7 `4001-4007`）。
  - **Registry**：`.agent/registry/error-codes.md` 8 个 ACTIVE 域。
- 已落地错误码「不可随意 renumber」的成立证据充分（测试 + 前端 + Design + Registry 四类事实源均锁定具体取值）。当前无任何文档明确承诺「错误码 immutable」，但兼容性事实已形成。

### Four-digit Assumption Audit（已核实）

| 检查项 | 结论 | 证据 |
| --- | --- | --- |
| Go 类型是否为 `int` | 是 | `type Code = int`（`codes.go:13`），响应 `Code int` |
| `code / 1000`、`% 1000` 隐式域解析 | 未命中 | 全仓库无 `code/1000`；仅前端时间换算 `/1000` 为误命中 |
| `%04d` / `%4d` 四位格式化 | 未命中 | 全仓库无 |
| `len(code)==4` / `strconv` 长度假设 | 未命中 | 无对 code 的字符串长度判断 |
| `< 10000` / `<= 9999` / `> 9999` 数值上限 | 未命中 | 全仓库无（仅前端 dist 构建产物含 `10000`/`1e5` 等无关常量） |
| DB 字段长度限制 | 不适用 | 错误码为编译期常量 + 内存 `map`，不落库；无存储字段限制 |
| 日志/监控按范围识别 Domain | 未命中 | 无按 `code` 数值范围归类 Domain 的逻辑 |
| Frontend 四位长度假设 | 未命中 | 仅精确等值判断 `=== 0`、`=== 50008`；无 `String(code).length===4` |
| Frontend 已有五位数 | 命中（模板遗留） | `request.js:63` 判 `res.code === 50008 || 50012 || 50014` |
| Validator Range 解析 | 支持任意宽度 | `check-registry.sh` 用 `^[0-9]+-[0-9]+$` 解析 Range、整数比较，无 `<=9999` 上限 |

结论：**技术层面不存在四位数硬限制**，`10000+` 可被 Backend、响应协议、Frontend、Tests、Validator 正确表示与比较。真正的「四位」约束是治理层的正式规则措辞「按千位划分、固定 1000」与「4 位上限 → 9 域」的认知模型，而非代码/存储限制。

### Backend / Frontend / Test Impact（已核实）

- **Backend**：`codes.go` 新增五位数 const 即可；`HTTPStatus` / `Message` / `FromError` 用 `map[Code]` 查表，对任意宽度整数无差别。无改动风险点。
- **Frontend**：`request.js` 与视图层均为精确等值比较，五位数不破坏；但 `request.js:56` 读 `res.msg` 而 Backend 返回 `message`（模板遗留不一致，属 Follow-up，非本任务）。已存在 `50008/50012/50014` 五位码比较，证明五位码对前端无解析障碍。
- **Tests**：`internal/controller/*/*_test.go` 硬编码数字业务码（`7001`/`1002`/`1001` 等）做精确等值断言，不因位数变宽而失效；但它们用字面量而非 `codes.Code*` 常量，若未来 renumber 会静默失效——这是兼容性「锁定」证据，也是可选重构点（见 Analyst Questions）。

### Global Registry Dependency（已核实）

- `.agent/registry/error-codes.md` 是错误码域分配的权威事实源（`develop` 上），`global-resource-reservation` 任务确立的机制：Reservation 生效 = 只改 Registry 的 commit 落在 `develop`；状态 `RESERVED/ACTIVE/RELEASED`；`next` 由规则派生。
- 当前分配规则「按千位划分、固定 1000、`next = max(已记录域上限) + 1000`」：若把 `next` 理解为「新域上限」，则 `8999 → 9999（9000-9999）→ 10999（10000-10999）` 可自然顺延到五位数；若把 `next` 理解为「新域起点」，则存在 off-by-one 歧义。**该规则措辞与五位数后的「千位/万位」语义需要 Analyst 精确化**。
- `scripts/check-registry.sh`（Validator）机械上已支持任意宽度 Range（重叠检测、`codes.go` 归属检测均用整数比较），无 `<=9999` 假设；但「next 派生」与「千位语义」属 Analyst/Cleaner 语义判断，不在 Validator 机械校验内。
- **依赖关系**：本任务不复制 Reservation 机制，只在其上定义「Namespace 如何扩展」。必须保证扩展后 Registry 能表达 `10000-10999` 等条目、`next` 派生规则同步更新、且不与 `RESERVED/ACTIVE/RELEASED` 生命周期冲突。

### Root Cause

> 错误码 Namespace 被建模为「4 位整数 `1000-9999`、千位 = Domain、每域固定 1000」。这把「Domain 身份编码」与「4 位整数宽度」耦合：域数被硬性限制为 9 个，而每域实际只用 2~11 个编号。当业务 Domain 数量继续增长（Order/Payment/…）时，触顶的是「4 位 → 9 域」这一编码宽度，而非编号消耗。现行分配规则 `next = max(已记录域上限) + 1000` 只在 4 位内成立，扩展后需重新精确化「千位/万位」语义与 off-by-one 边界。

### Candidate Solution Directions（供 Analyst 比较，Task Builder 不预设）

- **方案 A：直接扩展为五位数 Domain**——`1000-8999` 保持不动，`9000-9999` 可继续，之后 `10000-10999`、`11000-11999`… 延续现有认知模型，改动小，与 Registry 兼容度高。需验证「按千位划分」到五位数后是否退化为「按万位划分」。
- **方案 B：扩大 Domain 编码模型**——显式 `Domain ID × N + Local Code` 或更大区间。需评估是否真的比 A 有收益，禁止为「看起来更架构化」引入复杂编码。
- **方案 C：减小每域预留**——如每域 100 个。需回答历史 1000-block 如何兼容、新旧域 block size 是否可不同、Registry 如何识别、是否反而更复杂、在整数可继续扩大的前提下节省空间是否还有意义。
- **方案 D：取消数字范围表达 Domain**——全局连续 allocator，Domain 只存在于 Registry metadata。需评估可读性/排障效率损失，及是否值得改变现有体系。

（Task Builder 建议的初始设计原则，供 Analyst 作为输入而非最终决定：①已发布错误码不变；②优先扩展 Namespace 而非 renumber；③数字格式保持人类可读；④Error Domain 继续可被 Registry 预留；⑤Coder 不自分配 Domain；⑥Error Code 继续是稳定 API 契约；⑦不为省数字空间引入复杂编码。）

### Assumption

- 「错误码域按千位划分」的 Domain Range 模型本身可被保留或平滑扩展，本任务只解决「如何扩展」，不推翻既有 8 个域。
- 未来模块至少包含 Order（多个 `docs/design/*` 已明确引用「未来订单模块」）；Payment / Promotion / Coupon / Refund / After-sales / Delivery 由 Owner 描述为计划，但当前仓库无对应设计文档，本任务不虚构具体清单，只保证扩展规则可容纳它们。

### OPEN QUESTION（不阻塞任务创建）

- 前端 `request.js` 的 `res.msg` 与 Backend `message` 字段名不一致（模板遗留），是否另行治理——记录为 Follow-up。
- 是否把测试中的数字业务码字面量迁移为 `codes.Code*` 常量引用（降低 renumber 风险）——见 Analyst Questions。

## Verification

- AC-001 → 阅读任务产物中的利用率矩阵，逐条与 `internal/codes/codes.go` const 块比对（grep `= 数字`），确认 8 域、Used Count、Max Code 与 Utilization 正确。
- AC-002 / AC-003 / AC-004 → 阅读 Four-digit Assumption Audit 表与证据；用 `rg -n "code/1000|%04d|< 10000|<= 9999|length === 4"` 复核无命中（时间换算 `/1000` 与 dist 产物为误命中需说明）。
- AC-005 / AC-007 → **Scenario B 推演**：Order 占 `9000-9999` 后新增 Payment，断言按扩展规则能派生不冲突的 next 域（如 `10000-10999`），不 BLOCKED 于 `9999`。
- AC-006 → **Scenario A / Scenario E 推演**：`CodeAddressNotFound = 7001`、`CodeCartItemNotFound = 8001` 扩展后保持原值；试图把 `8001 → 10001` 的「整理」被判定为兼容性破坏，除非有独立 APPROVED Migration Plan。
- AC-008 / AC-009 / AC-010 → **Scenario C / Scenario D 推演 + Validator 实测**：将 `10000-10999` 作为 RESERVED 条目临时写入 `.agent/registry/error-codes.md`，运行 `bash scripts/check-registry.sh`，断言重叠检测与 `codes.go` 归属检测对五位数 Range 正常（无 `<=9999` 误报）；两个未来 Task（Order/Payment）并行时通过 Registry 取得不同域。
- AC-011 → 阅读最终规则中的 Error Code Reuse Policy，断言与 Registry `ACTIVE/RESERVED/RELEASED` 语义一致（纯 RESERVED 可复用、已落地不复用）。
- AC-012 / AC-013 → 阅读方案：断言现有错误码 HTTP 状态与 message 映射不变，HTTP status 与 business code 职责边界不变。
- AC-014 / AC-015 → 阅读最终治理/设计文档：断言 `docs/design/*` 与 `docs/agent/*`、`.agent/registry/*` 一致，且 next 由规则派生、不要求 Owner 手工算号。
- 若本任务最终不修改生产代码（仅治理规则 + 可能的 `docs/design/*`），无需 `go build`/`go test`；若最终方案改动了 `internal/codes/codes.go` 或 Validator，则按其说明运行 `go build ./...`、`go test ./...` 与 `bash scripts/check-registry.sh`。

## Complexity

COMPLEX

原因：涉及 API 长期兼容、Error Code Namespace（长期公开协议）、Global Resource Registry、Backend / Frontend / Tests、历史 Contract、以及未来所有业务 Domain；存在多个现实方案（A 直接五位扩展 / B 编码模型扩大 / C 缩小区块 / D 全局 allocator），不同方案会产生不同的可读性、排障效率与维护结果；且需与 `global-resource-reservation` 治理机制保持术语与生命周期一致。业务代码本身不复杂，协议与治理设计复杂度高。

## Analyst Questions

1. **扩展模型选择**：方案 A（五位延续）/ B（Domain ID × N + Local）/ C（缩小区块）/ D（全局 allocator）中选哪个？是否「直接五位扩展」已足够，无需更复杂编码？给出可读性、排障、Registry 兼容、维护成本对比。
2. **「千位划分」到五位数的精确语义**：五位数后 Domain 识别是「万位」还是「千位组」？现行 `next = max(已记录域上限) + 1000` 中的 `next` 究竟是新域**起点**还是**上限**（存在 off-by-one）？请给出 `9000-9999 → 10000-10999` 的精确派生公式，消除歧义。
3. **Error Code Reuse Policy**：历史错误码删除后是否允许复用？如何与 Registry `ACTIVE/RESERVED/RELEASED` 语义对齐（纯 RESERVED 可复用、已落地不复用）？已发布错误码是否正式声明 immutable？
4. **Namespace 模型的长期设计落点**：是否新增独立 `docs/design/error-codes.md` 承载完整 Namespace 模型（Design Impact → NEW），还是只改 `docs/agent/*` + `.agent/registry/*`（→ NONE），还是更新各 `docs/design/<module>.md`（→ UPDATE）？这决定 Design Impact 最终值。
5. **测试字面量迁移**：是否把 `internal/controller/*/*_test.go` 中的数字业务码字面量迁移为 `codes.Code*` 常量引用（降低未来 renumber 风险、提升可维护性）？若做，是否属本任务 Scope 还是独立 Follow-up？
6. **9000-9999 归属**：是否把 `9000-9999` 直接留给 Order（已知最临近模块），还是继续按 `next` 规则给下一个申请者？是否需要在扩展规则中预留「已知未来模块」的映射表？
7. **容量需求边界**：当前仅 Order 有仓库内设计依据（多个 `docs/design/*` 引用「未来订单模块」），Payment/Promotion/Coupon/Refund/After-sales/Delivery 无设计文档。扩展规则是否只需「可容纳 N 个新域」的形式化保证，而不需要现在逐一预留？

## Review Baseline

- Base commit：`9e11a2d0e19a09a39c002f65069c646b0f8dfc6e`（分支 `develop`）。
- 任务开始时已有修改：`.agent/tasks/global-resource-reservation/core-logic.md`、`.agent/tasks/global-resource-reservation/findings.md` 两个文件为 modified（`git status --short`）。二者归属于 `global-resource-reservation` 任务，**不属于本任务，不得触碰**。
- 重叠修改的区分方式：本任务新增产物仅为 `.agent/tasks/error-code-namespace-expansion/*`（以及 Analyst/Owner 后续确定的设计/治理落点）。审查时以 `git diff --stat 9e11a2d` 与 `git status --short` 核对：不得包含 `global-resource-reservation` 相关文件的改动，也不得包含 `internal/codes/codes.go`、`api/`、业务代码、前端代码、`internal/migrations/sql/*`、`.agent/registry/migrations.md`、历史 `.agent/tasks/*` 的改动。

## Initial Route

READY_FOR_ANALYST
