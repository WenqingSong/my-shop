# Workflow V4 DESIGN 工作流规范（Design Workflow Spec）

本文件定义 `task_type = DESIGN` 的纯设计工作流、两个 Owner Checkpoint、State 字段绑定与设计 Evidence 规则。它是 DESIGN 专属的 Cleaner / OwnerGate 适配规范：不改写共享 `CleanerAgent.md` / `OwnerGateAgent.md` 的通用内容，只在此表达 DESIGN 侧差异（集中集成时以本文件为准叠加，避免覆盖其他工作包的通用改动）。

## 1. 定位

DESIGN 是纯设计工作流，目标是产出经 Owner 批准的架构与正式工程设计规范，不要求 Coder 生产业务代码。**不得因为没有 Coder 业务实现就直接判定 FAIL**。

固定流程（角色级，不引入自动调度）：

```text
Discovery → Owner 决策 → TaskBuilder → Architect → Owner 架构评审（Checkpoint 1）
→ DocsSpecWriter → Cleaner（独立设计审查）→ OwnerGate → Owner 最终批准（Checkpoint 2）→ Merger
```

Discovery / TaskBuilder / Handoff 由工作包 A 提供；Merger 由工作包 B 提供；本规范只负责 DESIGN 专属 Gate、角色与设计 Evidence 检查。

## 2. State 字段绑定（复用 Schema V4，不新增字段）

### 公共 TaskBuilder 初始化

从 `.agent/roles/DiscoveryAgent.md` 调查，经 Owner 批准 DESIGN 后交 `.agent/roles/TaskBuilderAgent.md`，见 `.agent/specs/workflows/EntryWorkflow.md`。schema 4 的 task_type=DESIGN、contract.status=NOT_REQUIRED、design.status=PENDING；approved_version/final_version=0，各 approved_target/review_target/final_target 为空，不能复制旧批准。review=NOT_REQUESTED、owner=PENDING、delivery=NOT_RUN、blocked.active=false、resources.reservations={}，intake/git 绑定原批准。初始 Handoff next_role=Architect、workflow=DESIGN，交 design/ArchitectAgent.md；AC 面向设计成果，不默认要求生产代码。完整字段以 State 模板和现有解析器为准。

| 字段 | 含义 | 写入者 | 时机 |
| --- | --- | --- | --- |
| `design.status` | 架构审批状态 | Architect（File Writer） | Owner 批准后置 `APPROVED` |
| `design.approved_version` | 已批准架构版本号 | Architect | 同上 |
| `design.approved_target` | Owner 批准架构的 Evidence Commit SHA | Architect | 同上 |
| `review.status` / `review.target` | 设计审查结论 / 设计交付 Evidence SHA | DocsSpecWriter（PENDING）、Cleaner（CLEAN） | 审查发起 / 审查通过 |
| `design.review_target` | Cleaner 实际审查的设计交付 SHA（= `review.target`） | Cleaner | CLEAN 时 |
| `owner.status` / `owner.review_target` | Owner 最终决定（ACCEPTED 绑定 review.target） | OwnerGate | 最终批准时 |
| `design.final_version` / `design.final_target` | 最终设计批准版本号 / 交付 SHA（= `review.target`） | OwnerGate | 最终批准时 |

- `approved_target`：经 Owner 批准的架构 Evidence SHA。
- `review_target`（`design.review_target`）：Cleaner 实际审查的设计交付 SHA。
- `final_target`：Owner 最终批准的设计交付 SHA。

三个 Target 互不混同，均为不可变 Evidence Commit SHA。

## 3. 两个 Owner Checkpoint

### Checkpoint 1：架构批准

Architect 产出 `architecture.md` 后请求 Owner 决策（`WAITING_FOR_OWNER_DECISION`，不是 Handoff）。Owner `ACCEPT` 后 Architect 机械持久化：

1. commit `architecture.md`（Evidence Commit A1）；
2. commit `state.yaml`：`design.status = APPROVED`、`design.approved_version = N`、`design.approved_target = A1`。

校验入口：`GateDesignArchitectureReview`。

### Checkpoint 2：最终设计批准

DocsSpecWriter 产出 `design.md` 后发起审查，Cleaner 审查通过（CLEAN），OwnerGate 向 Owner 解释后请求最终决策。Owner `ACCEPT` 后 OwnerGate 机械持久化：

- `owner.status = ACCEPTED`、`owner.review_target = review.target`；
- `design.final_version = N`、`design.final_target = review.target`。

校验入口：`GateDesignFinalApproval`。

**最终批准不等于 Cleaner PASS**：`final_target` 必须绑定 Cleaner 已审查的 `review.target`，且必须存在 Owner `ACCEPT`；只有 CLEAN 或只有 SHA 都不构成最终批准。

## 4. Evidence 规则

每个 SHA 绑定都做三重机器校验：

1. Git 真实性：`CommitExists(SHA)`；
2. 祖先关系：`IsAncestor(SHA, HEAD)`；
3. 文件内容一致性：`approved_target` 对应的 `architecture.md`、`final_target` 对应的 `design.md` 内容与当前工作区一致（漂移即旧批准/审查失效）。

设计变更规则：

- 已批准架构发生实质修改 → 旧批准失效，`GateDesignDocsSpecWriterStart` / `GateDesignArchitectureReview` 等后续 Gate FAIL；不允许 DocsSpecWriter 静默接受新架构，需返回 Owner 对变更重新决定。
- 设计规范（`design.md`）在 `review.target` 之后发生实质修改 → 旧审查/批准失效（`review.target` 之后的 Neutral Tail 检查，设计文件属 substantive，default-deny）。
- 审查过的旧快照不能自动继承最终批准：`final_target` 必须等于当前 `review.target`。

## 5. DESIGN Cleaner 审查范围

DESIGN Cleaner 复用共享 `CleanerAgent.md` 的审查方法，但审查对象是设计交付而非生产代码实现。审查目标：

- Task 目标与 AC；
- 已批准架构（`approved_target` 对应的 `architecture.md`）；
- 工程设计规范（`design.md`）；
- 接口契约、业务不变量；
- 风险与测试要求；
- 设计交付 Evidence（`review.target` 与工作区一致性）。

CLEAN 后写入 `review.status = CLEAN`、`review.target = D1`、`design.review_target = D1`，并完成 Cleaner-owned 出站收尾。DESIGN Cleaner 不因缺少 Coder 实现而判 FAIL。

## 6. 设计文档 Neutral Tail 边界

`architecture.md`、`design.md`、`docs/design/*`、`contract.md` 均属 substantive（default-deny），**不进入** review-neutral / delivery-neutral 白名单。设计交付 `review.target` 之后的 tail 只允许当前 task 的 review-neutral metadata（`findings.md` / `core-logic.md` / `owner-decision.md` / `delivery.md` / `state.yaml` / `events.md`）。

## 7. 身份边界

仅存在 SHA 不证明 Owner 身份。State 只记录 SHA 绑定事实；Owner 身份由 Git 侧（Evidence Commit 提交者 + shared branch protection）在集成侧提供并事后审计。本引擎的 Gate 只机械校验 SHA 真实性、祖先关系与内容一致，**不伪造身份认证能力**。

## 8. 最终交付与基线

最终设计批准后仍绑定初始化父分支：`final_target` 必须是 `git.base_commit` 的后代（`GateDesignFinalApproval` 的 `Design Baseline` 检查）。

## 9. Gate 清单（`design_gate.go`，可直接调用）

| Gate（Go 方法） | CLI gate 名 | 用途 |
| --- | --- | --- |
| `GateDesignArchitectStart` | `design-architect-start` | Architect 入口 |
| `GateDesignArchitectureReview` | `design-architecture-review` | Checkpoint 1：架构批准 |
| `GateDesignDocsSpecWriterStart` | `design-docsspecwriter-start` | DocsSpecWriter 入口 |
| `GateDesignCleanerStart` | `design-cleaner-start` | DESIGN Cleaner 入口 |
| `GateDesignOwnerGateStart` | `design-owner-gate-start` | DESIGN OwnerGate 入口 |
| `GateDesignFinalApproval` | `design-final-approval` | Checkpoint 2：最终设计批准 |

以上 Gate 均在 `internal/workflow/design_gate.go` 以 `Validator` 方法实现，并已接入全局 Gate CLI
（`workflow-check gate <cli-gate-name>`）与 BLOCKED recovery 候选 Gate 派生（`candidateDesignGates`）。

> DESIGN 最终交付接线（已核验）：`design-final-approval` 通过后，最终交付由 Merger 专属适配层
> `workflow-deliver plan/execute design` 消费该 Gate，在显式 Owner 授权后受控调用 Merger，
> 不因 Gate PASS 自动推送。

## 10. 实际操作链与禁止替代

| 阶段 | 执行角色 | Gate | 失败下一步 |
|---|---|---|---|
| 架构开始 | Architect | design-architect-start | TaskBuilder 修正任务/基线 |
| Owner Checkpoint 1 持久化后 | Architect | design-architecture-review | Architect 补批准记录或重请 Owner |
| 文档开始 | DocsSpecWriter | design-docsspecwriter-start | Architect 修正架构证据 |
| 设计审查开始 | Cleaner | design-cleaner-start | DocsSpecWriter 固定 Evidence 并补 PENDING |
| 审查证据完成后 | OwnerGate | design-owner-gate-start | Cleaner 修正有效性 |
| Owner Checkpoint 2 持久化后 | OwnerGate | design-final-approval | OwnerGate 修正绑定；批准只能来自 Owner |
| 交付 | Merger | 消费 design-final-approval 的 workflow-deliver plan/execute design | Merger 停止，回责任角色复验 |

OD-2：architecture-review 与 docsspecwriter-start 校验重叠，保留双边界，各由交出与接手角色执行，不合并、不设 alias。

禁止使用以下通用 Gate 替代 DESIGN 步骤，即使引擎可能误通过：

| 禁止使用 | 原因 |
|---|---|
| owner-gate-start | 不校验 DESIGN 专属目标 |
| delivery-start | 不代表最终设计批准 |
| merge-ready | 要求通用 delivery PASS，不能用于 DESIGN 出口 |

Task Handoff 按 `.agent/specs/protocols/HandoffProtocol.md` 执行，完整字段见 `.agent/specs/protocols/GateCatalogue.md`。workflow-deliver plan 只读；execute 必须显式绑定 source-sha、parent-branch 以及人类交付授权。

## 已批准架构返工
旧 Task 已有 approved_target 时，不清空批准、不在原 Task 强行运行 design-architect-start。发现架构 Finding，先冻结该旧任务的后续交付，将问题、旧 Task 路径、A1/D1/CLEAN/Owner 的完整 SHA 交 Owner 决定修订范围。

Owner 明确授权后，用公共 Discovery 和 TaskBuilder 创建新的 DESIGN 修订 Task、独立分支/Worktree 与新 Intake。在新 task.md 记录前驱 Task、旧批准 SHA、Finding、修订范围和为何需要重新批准；旧 Task、Contract、Review、批准与 Git 历史保持原样。新 Task 从其自身 PENDING、review=NOT_REQUESTED、owner=PENDING、delivery=NOT_RUN 开始，不复制旧批准/CLEAN/final 字段。Architect 只在新任务经过批准 Intake、taskbuilder-init 及正式 Handoff/ACK 后进入 design-architect-start；新架构重新请求 Owner 批准，再走 DocsSpecWriter、独立 Cleaner、最终 OwnerGate。纯规范表述问题且架构不变时返回原 DocsSpecWriter 重新发起审查，不必另建架构任务。Owner 未授权则 BLOCKED。旧版本的版本号属于旧 Task，新 Task 的批准版本按自身从正整数开始。
