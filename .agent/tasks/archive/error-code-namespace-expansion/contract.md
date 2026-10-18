# Technical Contract

## Decision Status
APPROVED

## Problem

错误码 Namespace 被建模为「4 位整数 `1000-9999`、千位 = Domain、每域固定 1000」。该模型把「Domain 身份编码」与「4 位整数宽度」耦合：域数被硬性限制为 9 个（1000~8000 已用 8 域，仅剩 `9000-9999`）。真正的瓶颈是「4 位 → 至多 9 域」，而非域内编号耗尽（8 域合计 41 个编号，利用率 0.5%）。Order 一旦占用 `9000-9999` 即触顶，后续 Payment 等模块无域可用。

本任务定义一套 **Namespace 扩展规则**：`1000-8999` 全部保持原值、不 renumber，未来域不受现有四位数宽度限制、可按正整数域序继续扩展（`9000-9999` → `10000-10999` → `11000-11999` …），并保持对 Backend / Frontend / Tests / Docs / Registry / Validator 的兼容。本质是「精确化治理层分配规则的语义与边界」，**不改任何生产代码、不分配新域、不改任何已落地错误码取值**。

## Verified Current Behavior

- VERIFIED：`internal/codes/codes.go` 中 `type Code = int`，单一 `const` 块定义全部业务码（`CodeOK = 0`），`codeTable map[Code]codeInfo` 提供 `HTTPStatus`/`Message`，`HTTPStatus`/`Message`/`FromError` 均按 `map[Code]` 查表，对任意宽度整数无差别。
- VERIFIED：统一响应协议 `ResponseBody{Code int; Message string; Data any}`，成功 `code:0`，失败 `code:业务码` 且 `data:null`，HTTP 状态由 `HTTPStatus(code)` 派生（两层语义）。`internal/middleware/response.go`。
- VERIFIED：8 个 ACTIVE 域 `1000-1999` 通用 / `2000-2999` IAM / `3000-3999` category / `4000-4999` product / `5000-5999` SKU / `6000-6999` inventory / `7000-7999` address / `8000-8999` cart，仅剩 `9000-9999`。利用率矩阵与 `codes.go` const 块逐条一致（41 个编号，合计利用率 0.5%）。
- VERIFIED：**无四位数技术限制**。全仓库（排除 `dist/` 构建产物）无 `code/1000`、`% 1000`、`%04d`/`%4d`、`< 10000`、`<= 9999`、`> 9999`、`String(code).length===4` 命中；错误码为编译期常量 + 内存 map，不落库，无 DB 字段长度限制；无按 code 数值范围归类 Domain 的日志/监控逻辑。
- VERIFIED：Frontend（`frotend_web`/`frotend_manage`）`src/utils/request.js` 仅精确等值比较 `res.code !== 0`、`res.code === 0`、`res.code === 50008 || 50012 || 50014`（已存在五位数比较，证明五位数无前端解析障碍）。
- VERIFIED：Backend 测试 `internal/controller/*/*_test.go` 硬编码数字业务码（`7001`/`1002`/`1001` 等）做精确等值断言，位数变宽不失效，但用字面量而非 `codes.Code*` 常量（见 Open Risks）。
- VERIFIED：`scripts/check-registry.sh` 用 `^[0-9]+-[0-9]+$` 解析 Range、整数比较做重叠检测与 `codes.go` 归属检测，**无 `<=9999` 上限**，机械上已支持任意宽度 Range，无需改动。
- VERIFIED：「千位 = Domain、固定 1000、`next = max(已记录域上限) + 1000`」这一分配规则是**工程治理层正式规则**，落点在 `docs/agent/AgentCollaborationSpecification.md` §11.3、`docs/agent/models/AnalystAgent.md`（「全局资源预留」）、`.agent/registry/error-codes.md`「分配规则」三处；**不在 `docs/design/*`**。
- VERIFIED：`docs/design/<module>.md`（address/cart/product/sku/rbac/category/inventory/iam）各「错误码域」章节只记录模块具体码与一行归属声明（如 address §6 `7000-7999 归地址域`），**不陈述全局「千位划分/固定 1000/9999 上限」分配规则**，因此全局规则扩展不要求改写这些模块 doc。
- VERIFIED：Global Resource Reservation 机制（`.agent/registry/*` 为分配状态权威事实源；三态 `RESERVED/ACTIVE/RELEASED`；错误码域仅纯 `RESERVED` 阶段可 RELEASE 后复用）已由 `global-resource-reservation` Contract `APPROVED` 确立，本任务在其上定义「如何扩展」，不复制机制。
- UNKNOWN：无阻塞性 UNKNOWN。未来模块清单（Payment/Promotion/Coupon/Refund/After-sales/Delivery）在仓库内无设计文档，仅 Owner 口头计划，本任务只保证「可容纳 N 个未来域」的形式化容量，不虚构具体清单。

## Recommendation

RECOMMENDATION：采用**方案 A——域序制扩展**，即把「千位 = Domain」精确化为「**域序 = `code / 1000`，域区间 = `[域序×1000, 域序×1000+999]`，域序为任意正整数**」，并据此重写分配规则的派生公式。同时**新增独立 `docs/design/error-codes.md`** 承载完整 Namespace 模型（Design Impact = NEW），并在代码级正式声明「已落地错误码 immutable、永不复用」。

### 1. 扩展模型与精确派生公式（Q1、Q2）

- 域序 = `code / 1000`（整数除法）；`CodeOK = 0` 无域序，不属于任何域。
- 域区间 = `[域序×1000, 域序×1000+999]`，固定大小 1000。
- 下一个空闲域：`域序_next = max(已记录域序) + 1`；区间 `[域序_next×1000, 域序_next×1000+999]`。
- 现有 8 域即域序 1~8；`9000-9999` = 序 9，`10000-10999` = 序 10，`11000-11999` = 序 11，…… 不受现有四位数宽度限制、可按正整数域序继续扩展，不 BLOCKED 于 `9999`。

**消除 off-by-one**：旧措辞 `next = max(已记录域上限) + 1000` 中 `next` 实为「新域上限」（`max=8999 → 9999`，即 `9000-9999` 的上界），而非「新域起点」。新公式直接以「域序」表达，`8999 → 序 9 → 9000-9999`，与旧规则在 4 位区间内行为完全等价，且天然推广到五位数，无歧义。

**为什么不是 B/C/D**：
- B（`Domain ID × N + Local` 显式编码）：`code/1000` 已干净分离域序，B 只是更复杂的形式化，无实际收益，违反「不为看起来架构化引入复杂编码」。
- C（缩小区块，如每域 100）：打破历史 1000-block，引入非均匀 block size，且整数空间本不稀缺，节省空间无意义，反而更复杂。
- D（取消数字范围表达 Domain、全局连续 allocator）：丢失 `code/1000` 的即时可读性与排障效率，改变既有体系，代价大于收益。

关键取舍：方案 A 保留「域 = 1000 对齐整数块」的人类可读性，改动最小、与 Registry/Validator 兼容度最高；代价是「域序」不再是单一「千位」数字（五位域时域序跨入十位/百位），但 `code/1000` 表达完全消除这一认知负担。

### 2. 错误码复用与 immutable 政策（Q3、AC-011）

- **代码级**：任何已 `ACTIVE`（合并进 `develop`、进入 `internal/codes/codes.go`）的业务错误码取值**不可变、永不复用**。renumber、改语义、删除后复用均禁止；确需废弃时编号 tombstone、不复用。
- **域级**：沿用既有 Registry 生命周期——仅纯 `RESERVED` 阶段（未 APPROVED Contract 落地、未实现、未 merge）可 `RELEASE` 后复用；已实现/已合并即不可复用。本任务不新增、不改变生命周期语义。
- 由此形成两层一致的复用边界：域级（生命周期态）+ 码级（immutable），均与 `.agent/registry/*` 的 `ACTIVE/RESERVED/RELEASED` 一致。

### 3. Design Impact 落点（Q4，需 Owner 决定）

RECOMMENDATION：**Design Impact = NEW**，新增 `docs/design/error-codes.md` 作为「错误码 Namespace 公开协议」的单一长期设计事实源，承载：编码模型（域序制）、完整域表（8 个 ACTIVE + 保留扩展规则）、复用/immutable 政策、兼容性边界（哪些对象依赖已落地码为稳定事实）、与 Registry 的职责分工。

理由：全局 Namespace 模型（分配规则/复用政策/兼容边界）当前在 `docs/design/*` 无归属，仅散落在治理层三处；`docs/design/<module>.md` 的「错误码域」章节只记录模块具体码，无需改写。AC-014 要求「长期设计事实（docs/design/*）与工程治理规则最终一致、无双事实源」，独立 design doc 是最小且正确的收敛方式。

**此决定改变 task.md 声明的 `Design Impact = UPDATE` → `NEW`，按校正条件须回 Owner / Task Builder。**

### 4. `9000-9999` 归属与未来模块预留（Q6、Q7）

RECOMMENDATION：**不预留、不预分配** `9000-9999` 给任何具体模块。分配只经 `域序_next = max(已记录域序)+1` 规则，由下一个申请者（最临近且有仓库内设计依据的是 Order）在其 Task 走标准 Reservation 流程取得。扩展规则只承诺「可容纳 N 个未来域」的形式化容量，不维护易腐的「已知未来模块 → 域」映射表。

### 5. 测试字面量迁移（Q5）

RECOMMENDATION：**本任务不迁移**，记录为 Follow-up（降低 renumber 风险、提升可维护性，独立任务处理）。不在本任务改动 `internal/controller/*/*_test.go`。

### 6. 本任务不分配新域

本任务属治理/设计任务，不新增错误码域，因此**不产生 `.agent/registry/error-codes.md` 的 RESERVED 条目**。Coder 实现范围仅为治理/设计文档改写（见 Allowed / Forbidden Changes）。

## Selected Design

经 Owner 确认（2026-10-04），采用方案 A（域序制扩展），关键选择固化如下：

1. **编码模型**：每域固定 1000 个连续整数，`domain_seq = code / 1000`，域区间 `[domain_seq×1000, domain_seq×1000+999]`；新域按 Registry 已记录最大 `domain_seq + 1` 分配。域序不受现有四位数宽度限制，可按正整数域序继续扩展（序 9=`9000-9999`、序 10=`10000-10999`、序 11=`11000-11999`…）。
2. **Design Impact = NEW**：新增 `docs/design/error-codes.md` 承载项目级长期 Error Code Namespace 设计（编码模型、域分配规则、已落地码兼容性、immutable/reuse 政策、与 Registry 的关系、新域 Reservation 规则）；各模块 Design 继续只记录本模块具体错误码，不重复全局 Namespace 规则。
3. **已落地错误码为稳定 API 契约**：具体 code 值 immutable；不允许无独立迁移方案的 renumber；已落地错误码即使废弃也不重新赋予其它语义；域级生命周期沿用 Global Resource Reservation 规则（仅纯 RESERVED、尚未 APPROVED Contract / 实现 / merge 的域可 RELEASE 后重新分配）。
4. **`9000-9999` 不预留**：不预留给任何假定未来模块，由下一个真实申请并完成 Reservation 的 Task 获得，不建立 Order/Payment 预分配映射表。
5. **测试数字字面量治理**：记录为独立 Follow-up，本任务不处理、不扩大 Scope。
6. **本任务不新增域**：不产生 `.agent/registry/error-codes.md` 的 RESERVED 条目，不改任何生产代码、`internal/codes/codes.go` 取值或 `scripts/check-registry.sh`。

## Interfaces and Data

### 需要修改的治理/设计文档（唯一改动面）

1. `.agent/registry/error-codes.md`「分配规则」：将「域按千位划分、大小固定 1000；下一个空闲域 = `max(已记录域上限) + 1000`」改为域序制公式（见 Recommendation §1）。8 个 ACTIVE 域条目不变。
2. `docs/agent/AgentCollaborationSpecification.md` §11.3：同步域序制公式。
3. `docs/agent/models/AnalystAgent.md`「全局资源预留」：同步域序制公式。
4. `docs/design/error-codes.md`（**若 Owner 确认 Design Impact = NEW**）：新增，承载完整 Namespace 模型。

### 需要保持的既有接口/事实

- `internal/codes/codes.go` 全部现有 const 取值、`codeTable`、`HTTPStatus`/`Message`/`New`/`Wrap`/`FromError` 签名与语义不变。
- 响应协议 `{code,message,data}` 与 `code:0` 成功约定不变。
- `scripts/check-registry.sh` 不改动（已宽度无关）；仅以「临时写入 `10000-10999 RESERVED` 后运行、再还原」方式做验证。
- `docs/design/<module>.md` 各「错误码域」章节不改动（内容仍正确）。
- `.agent/registry/migrations.md`、`docs/design/migration.md`、历史 `.agent/tasks/*`（含 `cart-v1`/`shipping-address-v1`）不改动。

## Business Invariants

- INV-001（域独占）：任一错误码域序在 `.agent/registry/error-codes.md` 中至多被一个 `RESERVED`/`ACTIVE` 条目持有。
- INV-002（已落地码 immutable）：任何已 `ACTIVE` 的业务错误码取值在扩展前后保持原值；方案中不存在 renumber 动作；`CodeAddressNotFound = 7001`、`CodeCartItemNotFound = 8001` 等扩展前后原值不变。
- INV-003（域序派生确定性）：下一空闲域 = `max(已记录域序)+1`，区间 `[域序×1000, 域序×1000+999]`，可由 Analyst 从 Registry 机械派生，不依赖 Owner 手工算号。
- INV-004（无四位上限）：域序为任意正整数，域区间不受现有四位数宽度限制、可按正整数域序继续扩展；Order（序 9 `9000-9999`）之后 Payment（序 10 `10000-10999`）可连续分配，不 BLOCKED 于 `9999`。
- INV-005（复用边界一致）：错误码复用政策与 Registry `ACTIVE/RESERVED/RELEASED` 语义一致——纯 `RESERVED` 域可 RELEASE 后复用，已落地码永不复用。

## Failure and Consistency Semantics

本任务为纯治理/设计规则改动，无 Redis、无 MQ、无运行时组件、无多存储一致性，不新增并发或错误语义。一致性体现为四边一致：Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ `.agent/registry/*`，由 Cleaner 审查（`Design Impact = NEW` 时四者须一致）。既有 Reservation 生命周期与并发竞争防护（Git 线性历史 + Validator 机械检查）完全沿用 `global-resource-reservation` 已 APPROVED 的机制，本任务不复制、不改变。

## Allowed / Forbidden Changes

允许：
- 修改 `.agent/registry/error-codes.md` 的「分配规则」章节（8 个 ACTIVE 域条目不动）。
- 修改 `docs/agent/AgentCollaborationSpecification.md` §11.3、`docs/agent/models/AnalystAgent.md` 的域派生公式措辞。
- 若 Owner 确认 Design Impact = NEW：新增 `docs/design/error-codes.md`。
- 维护本任务 `.agent/tasks/error-code-namespace-expansion/*`。

禁止：
- 修改 `api/`、`internal/` 任何业务代码；修改 `internal/codes/codes.go` 现有取值；修改 `internal/migrations/sql/*`。
- 修改 `scripts/check-registry.sh`。
- 修改 `.agent/registry/migrations.md`、`docs/design/migration.md`。
- 修改 `docs/design/<module>.md` 各「错误码域」章节。
- 修改历史任务 `.agent/tasks/cart-v1/*`、`.agent/tasks/shipping-address-v1/*`、`.agent/tasks/global-resource-reservation/*`（仅读取作证据）。
- 新增任何错误码域 RESERVED 条目（本任务不分配域）。
- 为 `9000-9999` 预分配给具体模块；引入「已知未来模块 → 域」映射表。

## Verification Requirements

- INV-001/INV-003 → 场景推演 + Registry 检查：两并行未来 Task 经 `域序_next = max+1` 得不同域；`bash scripts/check-registry.sh` 无重叠报错。
- INV-002 → Scenario A/E：`7001`/`8001` 扩展前后原值不变；把 `8001 → 10001` 的「整理」判为兼容性破坏。
- INV-004 → Scenario B：Order 占 `9000-9999` 后新增 Payment，断言派生 `10000-10999`，不 BLOCKED 于 `9999`。
- AC-008/AC-010 → Scenario C/D + Validator 实测：临时追加 `| 10000-10999 | Payment | RESERVED | 验证 |` 到 Registry，运行 `bash scripts/check-registry.sh`，断言五位数 Range 无 `<=9999` 误报、重叠检测正常；再 `git checkout` 还原。
- AC-014 → 阅读 `docs/design/error-codes.md`（若 NEW）与 `docs/agent/*`、`.agent/registry/*`：断言域序制公式三处一致，无双事实源冲突。
- 本任务不触及生产代码，无需 `go build`/`go test`。

## Open Risks

- Frontend `request.js:56` 读 `res.msg` 而 Backend 返回 `message`（模板遗留不一致）——Follow-up，非本任务。
- 测试数字业务码字面量未迁移为 `codes.Code*` 常量——Follow-up（若未来 renumber 会静默失效；本任务已声明 immutable 使该风险进一步降低）。
- 域序制下「千位」不再是字面意义上的单一数字位（五位域跨入十位），依赖 `code/1000` 的排障直觉需在 Design 中明确写出，避免后继者回到「4 位上限」认知。

## Design Impact

Design Impact：NEW。新增 `docs/design/error-codes.md` 承载错误码 Namespace 的长期公开协议（编码模型、域分配规则、已落地码兼容性、immutable/reuse 政策、与 Global Resource Registry 的关系、新域 Reservation 规则）。

注：task.md 原声明 `Design Impact = UPDATE`，经 Owner 确认校正为 `NEW`（校正条件已回 Owner 并获批）；task.md 的 Design Impact 字段需 Task Builder/Owner 同步为 `NEW`（非阻塞，以本 APPROVED Contract 为准）。

## Owner Decision Record

Owner 于 2026-10-04 确认以下决定（均与 Task Goal/Scope/AC 兼容）：

1. **方案 A（域序制扩展）**：每域固定 1000 连续整数，`domain_seq = code / 1000`，域区间 `[domain_seq×1000, domain_seq×1000+999]`，新域按 `max(已记录 domain_seq)+1` 分配；不受现有四位数宽度限制，可按正整数域序继续扩展（要求避免「无限」绝对表述）。
2. **Design Impact = NEW**：新增 `docs/design/error-codes.md` 承载长期 Namespace 设计；各模块 Design 不重复全局规则。
3. **已落地码 immutable**：具体 code 值 immutable；禁止无独立迁移方案的 renumber；废弃码不重新赋予语义；域级生命周期沿用 Reservation 规则（纯 RESERVED 可 RELEASE 复用）。
4. **`9000-9999` 不预留**：由下一个真实申请并完成 Reservation 的 Task 获得，不建预分配映射表。
5. **测试字面量治理为 Follow-up**：本任务不处理、不扩大 Scope。

Design Impact = NEW，Contract 转 `APPROVED`。
