# 工作流路线选择（Workflow Route Selection）

## 1. 目的与范围

本规范定义 TaskBuilder 如何为当前任务选择 `NORMAL` / `COMPLEX` / `ARCHITECT` 三条工作流之一，以及该决策如何被记录、如何被 Engine 校验。

它修复 V3.1.0 的三个缺口：

1. **决策权缺口**：V3.1.0 规定「`ARCHITECT` 只在 Owner 明确指定时使用，TaskBuilder 不自行判定」，导致 Owner 未指定时无法进入架构路线；
2. **记录缺口**：路线只以 `task.md` 的 `Complexity` 散文形式存在，`COMPLEX` 与 `ARCHITECT` 在 `state.yaml` 中完全相同（都是 `contract.status: PENDING`），机器不可区分；
3. **约束缺口**：没有任何机械检查阻止 `ARCHITECT` 以 `NOT_REQUIRED` 直接交 Coder，也没有检查阻止路线改变后复用旧审批 / 旧 Review 证据。

本规范**不重新设计**三条工作流：角色序列、五个 Gate、Evidence Snapshot / Neutral Tail、Registry 与 ARCHITECT 的结构验证阶段都保持 V3.1.0 语义不变。

## 2. 路线定义

| 路线 | 目标 | 交付物 | `contract.status` | 角色序列 |
| --- | --- | --- | --- | --- |
| `NORMAL` | 目标清楚、边界有限，不需要独立 Contract | 可直接实现的业务结果 | `NOT_REQUIRED` | TaskBuilder → Coder → Cleaner → OwnerGate → Deliverer → Owner |
| `COMPLEX` | 需要独立的需求、设计或 Contract 审批 | 业务实现 + 已批准 Contract | `PENDING` → `APPROVED` | TaskBuilder → Analyst → Coder → Cleaner → OwnerGate → Deliverer → Owner |
| `ARCHITECT` | 目标是独立的架构或结构交付，而不是已完成的业务系统 | 结构交付（骨架、依赖方向、结构测试）+ 已批准结构 Contract | `PENDING` → `APPROVED` | TaskBuilder → Analyst → Architect → OwnerGate（V1 结构验证 → V2 决策）→ Deliverer → Owner |

`ARCHITECT` 不新增 Gate、不新增角色数量上限、不跳过 Owner 审批；它只是把 Cleaner 的独立审查位置换成 OwnerGate 的 V1 结构验证阶段（入口仍为 `cleaner-start`）。这些语义由 `AgentCollaborationSpecification.md` §5/§6 与 `Five-AgentResponsibilityBoundary.md` 定义，本规范不复制、不修改。

## 3. 判定规则（TB-ROUTE-1）

判定依据是任务的实际目标、范围和交付物，**不是**任务名称、文件数量或实现工作量。

判定顺序：

1. **交付物是不是结构本身？** Owner 要的是「系统应该具有什么结构、结构是否成立」，且业务实现明确不在本次范围 → `ARCHITECT`。
2. **是否必须经独立 Contract 审批？** 存在下列任一情况 → `COMPLEX`：
   - 重要架构、数据模型或公开协议选择；
   - 事务、库存、并发或跨系统一致性；
   - MQ 可靠性、幂等、重试或补偿；
   - 权限模型或重要安全边界；
   - Bug 根因不明；
   - 多个现实方案会产生不同业务、可靠性或运维结果。
3. 两者都不成立 → `NORMAL`（默认路线）。

### 3.1 `ARCHITECT` 与 `COMPLEX` 的边界

沿用 V3.1.0 已确认的架构交付边界：只要需要完整业务功能、数据库 Migration、未获批准的 DB Schema 变更、新外部依赖或构建 / 部署模型变化，就不是 `ARCHITECT` 能交付的范围。

遇到这种情况只有两种合法处理：

- 在 `Out of Scope` 明确排除这些内容，并把 Goal / AC 限定为结构交付 → 仍可 `ARCHITECT`；
- 按 `COMPLEX` 处理，并记录判定理由。

不得把业务实现写进「结构 Contract」蒙混过关，也不得为了保留 `ARCHITECT` 而缩小 Owner 已确认的验收范围。

### 3.2 路线选择不得扩大 Scope

如果选定 `COMPLEX` / `ARCHITECT` 会让 Scope 超出 Owner 已确认范围，TaskBuilder 必须按停止条件交 Owner 决定，而不是自行降级为 `NORMAL`。路线是执行方式，不是扩大交付范围的授权。

## 4. 决策权与审批权

| 事项 | 决策者 | 说明 |
| --- | --- | --- |
| 交付范围、技术限制、路线要求 | Owner | 上层约束，TaskBuilder 必须遵守 |
| 三条路线之一的判定 | **TaskBuilder** | Owner 未指定时独立判定；**包括独立识别 `ARCHITECT`** |
| Contract 是否批准 | Owner | 路线选择不免除 Contract 审批 |
| 结构交付是否成立 | OwnerGate（V1，独立 Session）+ Owner | Engine 不认证 Session 身份 |

关键结论：

- **不存在「`ARCHITECT` 只能由 Owner 指定」的限制。** Owner 未指定路线时，TaskBuilder 必须能够独立识别 `ARCHITECT`，不得以「Owner 没有说 ARCHITECT」为由默认降级；
- Owner 明确指定路线时，该指定是硬约束，TaskBuilder 不得改判；
- 路线选择不等于免除审批：`COMPLEX` / `ARCHITECT` 的 Contract、范围与关键业务决定仍由 Owner 批准；TaskBuilder 不批准 Contract，也不替 Analyst 设计方案；
- 只有 Owner 可以要求改变已记录的路线（见 §6）。

集成状态：`AgentCollaborationSpecification.md` §5 已在 V3.2.0 第二阶段统一为「TaskBuilder 判定路线（TB-ROUTE-1），Owner 指定的路线是硬约束」。两份规范在路线决策权上不再有分歧；角色序列、Gate 与证据语义未变。

## 5. 路线事实（Route Authority）

### 5.1 唯一机器权威源

`state.yaml` 的顶层可选字段 `route` 是路线事实的唯一机器权威源：

```yaml
schema_version: 3
route: NORMAL            # NORMAL | COMPLEX | ARCHITECT，逐字大写
task_id: <task-slug>
```

- **写入者**：TaskBuilder（初始化与 Owner 授权的路线变更）。Engine 只读，不写 `state.yaml`；
- **可读性**：任何 Role 与 Engine 都从该字段读取路线，不再从 `Complexity` 散文或聊天记录推断；
- **`task.md` 的 `## Complexity`** 保留为供人阅读的同一事实，必须与 `route` 一致。

### 5.2 为什么不升级 schema_version

- `schema_version: 3` 的历史任务已大量存在，且它们没有 `route`。若把 `route` 设为 v3 必填，会直接破坏历史任务读取，违反 Legacy Read Compatibility；
- `route` 是「Git 无法推导的工作流决策事实」，与 `contract` / `review` / `owner` / `delivery` 同级，不改变资源或 Evidence 语义；
- 因此本轮**保持 `schema_version: 3`**，`route` 为兼容性可选字段。升级 schema 需要独立的迁移与 Adoption 设计，不属于本任务范围。

### 5.3 不一致时的处理

`route` 与 `task.md` 的 `## Complexity` 不一致、`route` 取值非法、或 `## Complexity` 没有唯一声明（含保留模板 `NORMAL / COMPLEX / ARCHITECT`）时，Engine **fail-closed**：

- `workflow-check route` 返回 `ROUTE_FAIL`（exit 1）；
- 五个 Gate 都按同一不变量拒绝，不因 Gate 不同而放宽。

## 6. 路线变更（TB-ROUTE-2）

| 要求 | 内容 |
| --- | --- |
| 授权 | 只有 Owner 明确要求才能改变路线；任何 Role 不得按 Prompt 自行把 `ARCHITECT` 降为 `COMPLEX` / `NORMAL`，也不得自行升级 |
| 记录 | 同步改写 `state.yaml.route` 与 `task.md` 的 `## Complexity`，写明旧路线、新路线、Owner 指令与受影响证据 |
| 证据失效 | 旧 Contract Approval、旧 Review / `CLEAN`、旧 Owner Acceptance、旧 Delivery PASS 都属于旧路线，不得复用；必须重新形成 A1 / A2 审批 |
| 结构证据 | `ARCHITECT` 的结构 `CLEAN` 不是 `COMPLEX` 的 Cleaner 结论；`COMPLEX` 的实现 `CLEAN` 也不是结构验证结论 |
| 出口 | 变更后按新路线执行：`NORMAL` 需 `coder-start` `PASS`；`COMPLEX` / `ARCHITECT` 交 Analyst 且不运行 `coder-start` |

Engine 可验证的部分（见 §7 的 R-4）：

- Contract Approval 所在 commit 记录的路线必须仍是当前路线。只改 `route` 字段而不重新审批，会使 `contract.target` 指向旧路线证据，Engine 直接拒绝；
- `ARCHITECT` 的 `review.target` 必须包含 `architecture.md`；`COMPLEX` / `NORMAL` 的 `review.target` 不得是**自身改写或引入** `architecture.md` 的结构交付快照。仅仅继承了历史 `architecture.md` 的实现快照是合法的：路线变更后不会自动删除该文件，Engine 不能把「文件存在」误判为「快照就是结构证据」。

Engine 无法验证的部分见 §9。

## 7. Engine 强制能力

Engine（`.agent/engine`，`workflow-check`）是只读校验器。V3.2.0 新增的路线不变量：

| 编号 | 不变量 | 检查内容 | 失败形态 |
| --- | --- | --- | --- |
| R-1 | 枚举与存在性 | `route` ∈ {`NORMAL`,`COMPLEX`,`ARCHITECT`}；`workflow-check route` 默认要求存在 | `route enum` / `Route Authority (R-1)` |
| R-2 | 路线 ↔ Contract 状态 | `NORMAL` 不得携带 `PENDING` / `APPROVED` / `REJECTED`；`COMPLEX` / `ARCHITECT` 不得 `NOT_REQUIRED` | `Route Contract (R-2)` |
| R-3 | 路线 ↔ `task.md` | `state.route` 必须等于 `## Complexity` 的唯一声明 | `Route Task Document (R-3)` |
| R-4a | 路线 ↔ Contract Approval 证据 | `contract.target` 的 `state.yaml` 记录的 `route` 必须等于当前 `route`；无记录即拒绝 | `Route Contract Evidence (R-4a)` |
| R-4b | 路线 ↔ Review 证据形态 | `ARCHITECT` 的 `review.target` 必须含 `architecture.md`；其他路线的 `review.target` **自身不得改写/引入** `architecture.md` | `Route Review Evidence (R-4b)` |

生效点：

- 五个 Gate（`coder-start`、`cleaner-start`、`owner-gate-start`、`delivery-start`、`merge-ready`）在 `blocked.active` 之后、其他检查之前统一执行 R-2 ~ R-4；`route` 缺省的历史任务跳过这些检查；
- `workflow-check route <task>` 执行 R-1 ~ R-4，供 TaskBuilder 出站自检。它**不是 Gate**：不产生 Handoff 授权，不改变 Gate 语义，也不替代 `coder-start`；
- exit code 与其他命令一致：`0` = 通过，`1` = 不变量不满足，`2` = schema / 输入 / 运行级错误。

由此可机械阻止的错误交接：

- `NORMAL` 被错误初始化为「需要独立审批但尚未完成」的任务（R-2）；
- `COMPLEX` / `ARCHITECT` 用 `NOT_REQUIRED` 跳过 Contract 审批（R-2）；
- `ARCHITECT` 以 `NOT_REQUIRED` 直接交 Coder（R-2，`coder-start` 同时被 `contractValid` 拒绝）；
- 非法 `route` 值、`route` 与 `task.md` 不一致（R-1/R-3）；
- 路线改变后复用旧 Contract Approval 或跨路线复用 Review 证据（R-4a/R-4b）。

## 8. Legacy 兼容

- `route` 缺省 = 历史任务未记录路线。Engine **不推断**历史任务「一定是」哪条路线，也不因此放宽任何与路线相关的检查；
- 历史任务（含 V3.1.0 的 `ARCHITECT` 任务）继续按原 Contract / Review / Owner / Delivery 语义通过 Gate。`workflow-check route` 默认 strict，历史任务需要显式 `--allow-legacy` 才能通过；
- 不得为兼容历史任务而放宽非法 `route`、非法 Evidence 或旧路线审批复用；
- 若 Owner 要求给历史任务补记路线，必须按 `owner_update` 处理：补写 `route` 与 `## Complexity`，并由 Analyst 重新形成审批证据（R-4a 会要求 `contract.target` 的路线与当前路线一致）。

## 9. SPEC_ONLY 边界

以下保护无法由当前 Engine 真实强制，明确标注为 **SPEC_ONLY**，由 Role Prompt、Session 隔离与 Owner 判断承担：

| 项 | 为什么 Engine 不能强制 |
| --- | --- |
| `ARCHITECT` 路线 V1 必须由新 Session 执行 | Engine 无法识别 Agent 身份、Session 或 Git 作者 |
| 「Owner 指定路线」的真实性与唯一性 | Engine 只读 Artifact，无法区分 Owner 指令与其他角色自述 |
| 路线判定质量（是否真的该走 `ARCHITECT`） | 属于语义判断，Engine 不做业务推断 |
| 非授权角色「意图」降级路线 | Engine 只能检查降级后证据是否自洽；无法判断动机与授权来源 |
| `task.md` Complexity 与 Scope 的语义一致性 | 文本语义不可机械判定 |
| 新任务遗漏 `route` 与历史任务无 `route` 的区分 | 两者在 `state.yaml` 中形态相同；只能通过 `workflow-check route` 默认 strict + Prompt 要求区分 |
| Contract 内容是否真的限定为结构交付 | 文本语义不可机械判定；Engine 只检查 `architecture.md` 存在性与 Approval 路线绑定 |

Engine 不声称「通过 Gate 就机器认证了 Architect Session」或「Approval 一定由 Owner 本人批准」。它只保证**能真实读取的路线与状态事实**自洽。

## 10. 测试映射

`.agent/engine` 中的真实测试（非文档）：

| 要求 | 测试 |
| --- | --- |
| `NORMAL` 初始化与交接 | `TestRouteNormalInitAndHandoff` |
| `COMPLEX` 初始化与 Contract Gate | `TestRouteComplexInitAndContractGate`、`TestRouteComplexNotRequiredRejected` |
| `ARCHITECT` 初始化与 Contract Gate | `TestRouteArchitectInitAndContractGate` |
| TaskBuilder 自主选择 `ARCHITECT` | `TestRouteArchitectSelectedByTaskBuilder` |
| Owner 约束与路线选择不冲突 | `TestRouteOwnerConstraintsCompatible` |
| `ARCHITECT` + `NOT_REQUIRED` 拒绝 | `TestRouteArchitectNotRequiredRejected` |
| `route` 与 `task.md` 不一致拒绝 | `TestRouteTaskDocumentMismatchRejected`、`TestParseComplexity` |
| 非法路线 / 未授权降级 | `TestRouteIllegalValueRejected`、`TestRouteDowngradeCannotReuseApproval`、`TestRouteStructuralSnapshotNotReusable`、`TestRouteArchitectReviewRequiresStructuralSnapshot` |
| 合法路线变更后重新审批 | `TestRouteChangeWithReapprovalPasses` |
| 仅继承历史 `architecture.md` 的实现快照不被误拒绝 | `TestRouteComplexSnapshotInheritingArchitectureAllowed`、`TestRouteComplexSnapshotTouchingArchitectureRejected` |
| 历史 State 兼容 | `TestRouteLegacyStateCompatible` |
| 原有 Gate 回归 | `TestRouteArchitectFullGateSequence` 与既有 NORMAL / COMPLEX / ARCHITECT 测试全集 |
| CLI 自检语义 | `TestRunRoutePass`、`TestRunRouteContractMismatch`、`TestRunRouteDocumentMismatch`、`TestRunRouteIllegalValue`、`TestRunRouteLegacy`、`TestRunRouteUsageErrors`、`TestRunRouteNonV2State` |

## 11. 不做的事

- 不新增 Gate，不修改五个 Gate 的语义；
- 不新增第八个角色、不新增第四条路线、不升级 `state.yaml` schema 版本；
- 不声称 Engine 具备身份认证能力。

集成状态：本规范在 V3.2.0 第二阶段被接入 `.agent/specs/WorkflowAgentDirective.md` §4（必读清单第 4 项）、七个角色 Prompt、`docs/design/agent-workflow.md` 与 `.agent/README.md`；`AgentCollaborationSpecification.md` 的路线决策权措辞已统一。本规范是当前路线事实、判定规则与 Engine 能力的权威说明。
