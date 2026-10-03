# Cleaner Findings

## Review Target

- 任务基线（Base commit）：`9e11a2d`（`develop`）。
- 审查对象（HEAD）：`aa030d7`「docs: 明确错误码域按域序分配的规则」；工作区干净，与 HEAD 一致。
- 本任务相关提交：`1f4b0dd`（task.md）、`c7802b3`（contract.md + `docs/design/error-codes.md`）、`aa030d7`（3 个治理文件）。
- 无关提交（属 `global-resource-reservation`，非本任务，未计入）：`1021e79`、`f874067`。
- 本任务改动文件：
  - `.agent/registry/error-codes.md`「分配规则」（8 个 ACTIVE 域条目不变）
  - `docs/agent/AgentCollaborationSpecification.md` §11.3
  - `docs/agent/models/AnalystAgent.md`「全局资源预留」
  - `docs/design/error-codes.md`（新增，Design Impact = NEW）
  - `.agent/tasks/error-code-namespace-expansion/*`（task / contract / findings / core-logic / delivery）
- 任务前已有修改区分方式：`git diff --stat 9e11a2d..HEAD` 中 `global-resource-reservation` 两个文件的改动来自提交 `1021e79`/`f874067`（另一任务），不属本任务；本任务未触碰 `internal/`、`api/`、`scripts/`、`internal/migrations/sql/*`、`.agent/registry/migrations.md`、历史 `.agent/tasks/*`。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（Domain 统计完整） | PASS | task.md Relevant Context 利用率矩阵（8 域 / Used Count / Max Code / Utilization）与 `internal/codes/codes.go` const 块逐条一致：Common 6(1005)、IAM 11(2011)、Category 5(3005)、Product 7(4007)、SKU 5(5005)、Inventory 2(6002)、Address 2(7002)、Cart 3(8003)，合计 41、0.5%；明确给出「仅剩 9000-9999」结论。 |
| AC-002（兼容性边界明确） | PASS | task.md「Compatibility Findings」列出 Backend 测试 / Frontend / Docs-Design / Registry 四类依赖对象；immutable 成立条件与例外（仅纯 RESERVED 可 RELEASE 复用）见 contract.md §2 与 design §6。 |
| AC-003（四位数限制结论明确） | PASS | task.md「Four-digit Assumption Audit」+ contract VERIFIED L17 结论「技术层面无四位数硬限制」；Cleaner 独立 grep 复核 code/1000、%1000、%04d、<10000、<=9999、>9999、length===4 均无命中。 |
| AC-004（`<10000`/四位长度假设审计） | PASS | 逐项命中/未命中表完整（task.md L104-119）；Frontend 已存在五位数比较 `request.js:63 === 50008/50012/50014` 证明无解析障碍。 |
| AC-005（扩展规则统一且可派生） | PASS | 域序制公式 `domain_seq = code/1000`、`next = max(已记录域序)+1` 落于 Registry + `docs/agent/*` + `docs/design/error-codes.md` 三处，措辞一致，可由 Analyst 从 Registry 机械派生。 |
| AC-006（不 renumber 已有域） | PASS | INV-002 + design §5 + contract §2；方案中无任何 renumber 动作，`7001`/`8001` 原值不变。 |
| AC-007（可容纳多个未来 Domain） | PASS | 域序为正整数、不受四位宽度限制；`9000-9999`（序 9）→ `10000-10999`（序 10）→ `11000-11999`（序 11）可连续派生。 |
| AC-008（Registry 兼容） | PASS | Registry「分配规则」已改为域序制公式；Cleaner 临时追加 `10000-10999 Payment RESERVED` 后 `check-registry.sh` 无重叠报错，证明可表达五位数条目。 |
| AC-009（Coder 只能用已分配域） | PASS | 规则保留于 design §7 与 AgentCollaborationSpecification §11.6「禁止自行 max+1 占号」；本任务未新增任何 RESERVED 条目。 |
| AC-010（Validator 支持扩展 Range） | PASS | Cleaner 独立实测：五位数 RESERVED 无误报（exit 0）；`10000-10999` 与 `10500-11499` 重叠被正确报 FAIL（exit 1）。 |
| AC-011（错误码复用规则明确） | PASS | design §6 + contract §2：码级 immutable 永不复用；域级仅纯 RESERVED 可 RELEASE 复用，与 Registry ACTIVE/RESERVED/RELEASED 一致。 |
| AC-012（不改变业务语义） | PASS | 无生产代码改动；`internal/codes/codes.go` const 取值、codeTable、HTTPStatus/Message 语义不变（diff 无 internal/ 改动）。 |
| AC-013（不改变 HTTP/Business 分层） | PASS | design §1 与 contract 明确「业务码与 HTTP 状态两层语义」不变；`response.go` 现状复核一致。 |
| AC-014（长期设计/治理一致） | PASS | 四者一致：Task ↔ APPROVED Contract ↔ `docs/design/error-codes.md` ↔ 实现，域序公式三处一致、无双事实源冲突（design §7 显式声明 Registry 为分配状态唯一权威）。 |
| AC-015（不依赖人工算号） | PASS | design §3/§7 明确 `next` 由规则派生、不维护显式 next 指针、不依赖 Owner 手工算号。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| Registry ↔ 实现机械校验（基线） | PASS | `bash scripts/check-registry.sh` → exit 0「校验通过：未发现 Reservation 重复或 Registry ↔ 实现明显不一致」 |
| 五位数 RESERVED 无 ≤9999 误报 | PASS | 临时追加 `10000-10999 Payment RESERVED` 后运行，exit 0 |
| 五位数重叠检测 | PASS | 追加 `10500-11499` 与 `10000-10999` 重叠后运行，报 `[FAIL] 错误码域区间 10500-11499（RESERVED）与 10000-10999（RESERVED）重叠`，exit 1；已还原 |
| 四位假设审计 | PASS | `rg code/1000|%1000|%04d|<10000|<=9999|>9999|length===4`（排除 dist）无命中 |
| Frontend 五位数既有 | PASS | `frotend_web`/`frotend_manage` 的 `request.js:63` 均判 `=== 50008/50012/50014` |
| 无生产代码/测试改动 | PASS | `git diff --stat 9e11a2d..HEAD` 仅 docs/.agent，无 `internal/`、`api/`、`scripts/`、`internal/migrations/sql/*` |
| 四者一致（域序公式） | PASS | Registry、`docs/agent/AgentCollaborationSpecification.md` §11.3、`docs/agent/models/AnalystAgent.md`、`docs/design/error-codes.md` 四处域序公式措辞一致 |

## Findings

### CLEAN-001：task.md 的 Design Impact 字段仍为 UPDATE，与 APPROVED Contract 的 NEW 不一致

- Severity：P3
- Status：OPEN
- Location：`.agent/tasks/error-code-namespace-expansion/task.md:44`（`Design Impact: UPDATE`）
- AC / Invariant：四者一致（Task ↔ Contract ↔ Design ↔ Implementation），对应 AC-014
- Trigger：任何人单独阅读 task.md 的 Design Impact 字段
- Actual：task.md 写 `Design Impact: UPDATE`；而 APPROVED Contract（`contract.md:146/148`）与 `docs/design/error-codes.md` 均为 `NEW`
- Expected：task.md 的 Design Impact 字段同步为 `NEW`
- Impact：未来读者仅看 task.md 会误判 Design Impact 取值；不影响当前实现正确性——Contract 已显式记录校正（`contract.md:148`「task.md 的 Design Impact 字段需 Task Builder/Owner 同步为 NEW（非阻塞）」）且 Owner 已批准
- Evidence：`task.md:44` vs `contract.md:146/148`（已注明校正与同步责任人）
- Required Fix Boundary：由 Task Builder/Owner 将 task.md 的 Design Impact 字段更新为 `NEW`；非 Coder 修复项，不阻塞 CLEAN
