# Agent Workflow 历史 Task 盘点与治理报告

- 报告日期：2026-10-18
- 仓库：`my-shop`（`/root/projects/my-shop`）
- 执行基线：分支 `chore/update-workflow-to-v4.3.0-dev`，HEAD `acebfcf6b23f73f233a1a18696fb5c7ad86f2953`，开始前工作区 clean
- 数据范围：`.agent/tasks/` 下全部 51 个真实 Task 目录；`.agent/programs`、`.agent/intake` 不存在
- 结论性质：只读盘点 + 官方派生索引生成 + 本报告；未修改任何 Task State、Contract、Handoff、ACK、Evidence、Event Log 或 Git 历史
- 后续实施（导航、关系图、Owner 决策清单）见 [Task 管理入口](../task-management/README.md)

## 0. 结论摘要

1. 当前安装版本为 **4.3.0-dev**（开发版本，非正式 V4.3.0），Task 当前 schema 为 **4**；但仓库内 **没有任何 schema 4 Task**，51 个 Task 全部属于历史格式。
2. 51 个 Task 中：**40 个已完成**（有可核验交付证据）、**2 个活动**、**4 个待关闭/状态不一致**、**5 个异常/无法核验**。
3. 22 个 Task 的 State 不可用于机器核验：**19 个完全没有 `state.yaml`**（早期 V2 文档式 Task），**3 个 `state.yaml` 无 `schema_version`（schema 0）**，当前 CLI 报 `STATE_UNPARSEABLE` / `exit 2`。
4. 发现 1 个已验证的 Evidence Binding 破坏：`recommendation-v1` 的 `review.target` / `delivery.feature_head` 因历史重写已不在 `develop` 历史中。
5. 发现 1 个交付证据滞留在未合并分支：`flash-sale-v3` 的 `delivery=PASS` 只存在于 `origin/feat/flash-sale-v3`（`a905e1c`），`develop` 上仍是 `NOT_RUN`。
6. 未发现真正的重复 Task。`flash-sale-v1..v5`、`iam-v2..v5` 等是明确的增量版本演进，按规范**不合并、不删除、不移动**。
7. 未执行任何 State Schema 迁移（T2 在当前版本中不提供），未补造任何批准、交付或关闭记录。

---

## 1. 当前 Workflow 规范核验

### 1.1 版本与来源

| 事实 | 值 | 来源 |
|---|---|---|
| 产品版本 | `4.3.0-dev` | `.agent/VERSION` |
| 上游仓库 | `https://cnb.cool/framework-station/agent-workflow` | `.agent/upstream.yaml` |
| 上游 Commit | `0c62d1c07bc9a98120c7a11b9cd3bbeae104a2f0` | 同上 |
| 上一基线 | `4.2.1` / `1fc5c6dc…`，更早 `3.0.0` / `7727e0a6…` | 同上 |
| Engine | 独立 module `workflow-v2-engine`，位于 `.agent/engine/` | `.agent/AGENTS.md` |
| CLI | 6 个：`workflow-check` / `workflow-git` / `workflow-status` / `workflow-event` / `workflow-program` / `workflow-deliver` | `.agent/specs/protocols/CLI.md` |

本次按 `.agent/specs/protocols/CLI.md` 从已提交源码重新构建六 CLI 至 `.agent/bin/`（产物被 `.agent/bin/.gitignore` 忽略），构建 exit 0。

### 1.2 Task 生命周期与状态模型（实际支持）

- 当前 schema 为 **4**；`state.yaml` 是“无法由 Git 推导的决策事实”的唯一机器权威，**不保存** `phase`/`DONE`/`branch`/`HEAD`/`NEXT_ROLE`（`.agent/specs/Architecture.md`、`AgentCollaborationSpecification.md` §4）。
- 决策字段：`contract`（APPROVED/NOT_REQUIRED）、`review`（PENDING/CLEAN/CHANGES_REQUIRED）、`owner`（PENDING/ACCEPTED/REJECTED）、`delivery`（NOT_RUN/PASS/BLOCKED）、`blocked`。
- 没有全局 `phase`、持久 `DONE` 或 `STALE`；失效由 Evidence 与 neutral tail 机械推导（INV-3/4/6/7）。
- 交付证据必须是真实 commit：`review.target`、`owner.review_target`、`delivery.feature_head`、`delivery.develop_base` 三者相互绑定；schema 4 进一步要求完整 SHA。

### 1.3 Task 创建与初始化

- 唯一入口：公共 `Discovery` 调查 → Owner 确认 Task ID / 类型 / 父分支 / Base → 公共 `TaskBuilder` 按已批准类型加载初始化规范（`.agent/specs/workflows/EntryWorkflow.md`）。
- 新 Task 通过 `taskbuilder-init` 建立 schema 4 State 与初始 Handoff；**不允许为历史 Task 补跑该 Gate**。
- NORMAL/COMPLEX/DESIGN/COORDINATED 四种流程；Program 不生成第四种 Task 类型。

### 1.4 Gate、Handoff 与 ACK

- 共 **20 个 Gate**（`.agent/specs/protocols/GateCatalogue.md`）；`workflow-check` 覆盖 17 个 Task/Intake Gate，`workflow-program` 覆盖 3 个 Program Gate。
- `handoff-prepared` / `handoff-entry` **仅 schema 4 Task** 强制；schema 2/3 禁止调用。
- ACK 由接手方自行写入，Gate 不生成 ACK、不调度下一角色、不代表 Owner 审批。
- 历史 Handoff 中的 SHA/路径不因 Prompt 迁移而重写（`.agent/specs/protocols/HandoffProtocol.md`）。

### 1.5 历史 State 兼容策略（本任务的核心约束）

来源：`.agent/specs/adoption/WorkflowAdoption.md` §“支持范围与操作顺序”。

| 路径 | 当前版本支持 | 实际含义 |
|---|---|---|
| **T0 只读** | 支持 | `inspect` 查看 schema 2/3；不调用 V4 专属 handoff / taskbuilder-init / design Gate；不写回 |
| **T1 新任务承继** | 支持，推荐 | Owner 逐任务批准新范围/类型/父分支/Base，创建新 Discovery Intake + schema 4 Task + 独立分支/Worktree，引用旧路径与准确 Evidence SHA；重新履行 Contract/Cleaner/Owner/交付 |
| **T2 原地升级** | **不提供** | 不修改 `schema_version`，不补造 Intake/Handoff/批准/Revision/SHA |

七条红线（同文件）：不伪造 Owner ACCEPT / Cleaner CLEAN / 资源 RESERVED；不回填旧 target；不覆盖 Task/Program/Registry；不追溯补做未发生的 Discovery；承继只追加；Owner 逐任务授权；无法无损衔接时明确阻断。

### 1.6 索引与事件日志

- 官方索引机制存在：`workflow-status index [--format tasks/programs/meta]`，输出到 `.agent/index/`，为 **DERIVED**，不得作为审批证据；生成物被 `.agent/index/.gitignore` 忽略（`git check-ignore` 已核验）。
- 索引字段包含 `schema/task_type/contract/review/owner/delivery/blocked/branch/base/flags`；无法解析的 State 仍输出一行并标 `corrupt`。
- Event Log 协议存在（`.agent/specs/protocols/EventLogProtocol.md`），但仓库内 **`events.md` 数量为 0**，历史 Task 没有事件记录；事件日志不参与 Gate 判定。

### 1.7 当前版本的边界（不得从其他版本补造）

- 当前版本**不提供** Program/Intake 历史目录（仓库中确实不存在 `.agent/programs`、`.agent/intake`），因此本次无法盘点 Program 规划、子 Task 登记或 Integrate Registry。
- 当前版本**不提供**历史 Task 原地升级（T2）、批量重置、归档目录搬迁或自动关闭机制。
- `workflow-status recovery-check` 是只读诊断，但内部会派生评估候选 Gate；其结果不能直接当作历史任务“可继续”的证明。

---

## 2. 总体统计

### 2.1 互斥主分类（合计 = 51，不重复计算）

| 分类 | 数量 | 判定口径 |
|---|---:|---|
| **A. 活动任务** | 2 | 仍有明确未完成工作，且下一步是 Owner 决策或发布阶段动作 |
| **B. 已完成任务** | 40 | 有可追溯交付证据（State `delivery=PASS` 或 `delivery.md` Result PASS + 合并事实） |
| **C. 待关闭 / 状态不一致** | 4 | 有完成迹象，但 State 与交付证据矛盾、交付记录缺失或证据不可复现 |
| **E. 异常 / 无法核验（主分类）** | 5 | 无 State 且交付文档为占位，无法证明已交付 |
| **合计** | **51** | — |

### 2.2 交叉标签（可重叠，单独计数，不参与上面的 51 求和）

| 标签 | 数量 | 说明 |
|---|---:|---|
| **D. 历史兼容** | 51 | 全部 Task 均为历史格式：schema 3 = 25、schema 2 = 4、schema 0 = 3、无 `state.yaml` = 19；无 schema 4 |
| **E. 异常（任一缺陷）** | 25 | 19 缺 State + 3 State 不可解析 + `recommendation-v1` SHA 不可达 + `flash-sale-v3` 证据滞留分支 + `test-quality-gate-v1` 交付占位 |
| **F. 疑似重复 / 版本演进** | 16 | 均为“演进/关联”，**无真正重复**；明细见 §4.5 |

### 2.3 证据口径

| 指标 | 数量 |
|---|---:|
| Task 目录总数 | 51 |
| `state.yaml` 存在 | 32 |
| `state.yaml` 缺失 | 19 |
| State 可被当前 CLI 解析（schema 2/3） | 29 |
| State 不可解析（schema 0） | 3 |
| schema 4 Task | 0 |
| State `delivery=PASS` | 27 |
| 仅 `delivery.md` 有 PASS 证据（无 State） | 14 |
| 有任一 PASS 交付证据的任务（去重） | 41 |
| `delivery=NOT_RUN/NONE/BLOCKED` 或占位 | 10 |
| `events.md` | 0 |
| Program 目录 / Program Registry | 0 / 0 |
| Intake 目录 | 0 |

> 说明：41 个有 PASS 证据的任务中，`admin-identity-rbac` 的 PASS 建立在“未提交工作区”上（后续由 `f5d481a` 补齐），因此主分类为 C，故 B = 40。

---

## 3. 全量任务清单

字段说明：`s2/s3/s0` = State schema；`C/R/O/D` = contract/review/owner/delivery 状态；“无 state.yaml” = 早期 V2 文档式 Task。

| Task ID | State 摘要 | 主分类 | 异常 / 标签 | 建议动作 |
|---|---|---|---|---|
| `admin-identity-rbac` | 无 state.yaml（V2 文档） | C 待关闭/不一致 | 无 State；交付基于未提交工作区；后由 `f5d481a` 补齐 | 只读保留；Owner 决定是否补建 T1；不得补写 State/Evidence |
| `admin-order-management` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `agent-workflow-owner-confirmation` | 无 state.yaml | E 异常/无法核验 | 无 State；delivery 占位 | 只读保留；Owner 决定归档为历史未交付或新建 T1 追溯 |
| `agent-workflow-state-machine-v1` | 无 state.yaml | E 异常/无法核验 | 无 State；delivery 占位 | 只读保留；Owner 决定归档为历史未交付或新建 T1 追溯 |
| `article-cms-v1` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `backend-config-hardening-v1` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `backend-container-v1` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `banner-v1` | s2 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `cart-v1` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` 有 PASS 证据 | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `categories-v1` | 无 state.yaml | E 异常/无法核验 | 无 State；delivery 占位；无 contract | 只读保留；Owner 决定归档或新建 T1 追溯 |
| `ci-image-publish-v1` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=BLOCKED | A 活动 | `delivery=BLOCKED`，4 项 AC `NOT_EXECUTED` | Owner 决定是否在 main 集成后启动发布验收；V4 继续需 T1 |
| `container-runtime-hardening-v1` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `coupon-v1` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `dashboard-v1` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `db-migration` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS；合并提交 `c1598e3` | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `design-debt-backfill` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `design-doc-governance` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `dev-environment-setup` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `error-code-namespace-expansion` | 无 state.yaml | E 异常/无法核验 | 无 State；delivery 占位 | 只读保留；Owner 决定归档或新建 T1 追溯 |
| `flash-sale-v1` | V2 state（phase=READY_FOR_REVIEW, delivery=NONE） | C 待关闭/不一致 | schema 0；State delivery=NONE 与 `delivery.md` PASS 矛盾 | 只读保留；不得补写 State；Owner 决定归档或 T1 |
| `flash-sale-v2` | s2 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `flash-sale-v3` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=NOT_RUN | C 待关闭/不一致 | `delivery=PASS` 仅在未合并分支 `a905e1c` | Owner 决定是否受控合并该 metadata commit；不得在 develop 改写 |
| `flash-sale-v4` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `flash-sale-v5` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `flashsale-p95-p99-query` | s3 C=NOT_REQUIRED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `flashsale-performance-baseline` | s3 C=NOT_REQUIRED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `flashsale-v5-loadtest` | s3 C=NOT_REQUIRED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `global-resource-reservation` | 无 state.yaml | E 异常/无法核验 | 无 State；delivery 占位 | 只读保留；Owner 决定归档或新建 T1 追溯 |
| `iam-v2` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `iam-v3` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS；合并提交 `d0c4bb2` | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `iam-v4` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS；合并提交 `9836f5e` | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `iam-v5` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `identity-domain-isolation` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `inventory-v1` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `legacy-data-migration` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `nginx-reverse-proxy-v1` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `object-storage-upload` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `order-refund-v1` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `order-v1` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `product-favorite-v1` | V2 state（phase=APPROVED, delivery=PASS） | B 已完成 | schema 0；无 `review.target`（V2 格式） | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `product-like-v1` | s2 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `product-review-v1` | V2 state（phase=WAITING_FOR_OWNER_ACCEPTANCE, delivery=NONE） | C 待关闭/不一致 | schema 0；State delivery=NONE 与 `delivery.md` PASS 矛盾 | 只读保留；不得补写 State；Owner 决定归档或 T1 |
| `product-spu-v1` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `product-view-count-v1` | s2 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `project-baseline-cleanup-v1` | s3 C=NOT_REQUIRED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `prometheus-integration` | s3 C=NOT_REQUIRED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `recommendation-v1` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | **Evidence SHA 被重写后不可达** | 保持原样；不得改写 State；后续变更新建 Task |
| `shipping-address-v1` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `sku-v1` | 无 state.yaml | B 已完成 | 无 State；`delivery.md` PASS | 保持原样；后续变更新建 Task 并引用本任务证据 |
| `test-quality-gate-v1` | s3 C=NOT_REQUIRED R=CLEAN O=PENDING D=NOT_RUN | A 活动 | owner=PENDING；delivery 占位；实现已合并 `4e6dfa0` | 等 Owner 明确接受/拒绝；Agent 不得置 ACCEPTED；V4 继续需 T1 |
| `verify-metrics-histogram` | s3 C=APPROVED R=CLEAN O=ACCEPTED D=PASS | B 已完成 | - | 保持原样；后续变更新建 Task 并引用本任务证据 |

---

## 4. 分类详情

### 4.1 A. 活动任务（2）

**`ci-image-publish-v1`**（schema 3）

- State：`contract=APPROVED`、`review=CLEAN`、`owner=ACCEPTED`、`delivery=BLOCKED`、`blocked.active=false`。
- 交付文档 `Result = BLOCKED`，原因：`Owner-controlled integration dependency`；AC-002/003/004/005/006/007/009 为 `NOT_EXECUTED`，需要真实 `main` push 与授权制品库。
- Git 事实：`origin/feat/ci-image-publish-v1` 相对 `develop` 领先 0 个提交（代码已并入），但发布验收未完成。
- 下一步：Owner 决定是否在 main 集成后启动发布验收。schema 3 不能原地进入 V4 流程，需 T1 新 Task 或回到原 V3 环境。

**`test-quality-gate-v1`**（schema 3）

- State：`contract=NOT_REQUIRED`、`review=CLEAN`（target `24cfd01`）、`owner=PENDING`、`delivery=NOT_RUN`。
- `owner-decision.md` 仍为占位“等待 OwnerGate 决策后填写”；`delivery.md` 为“当前尚未进入交付验收”。
- 实现已并入 `develop`（`4e6dfa0`）；`origin/test/test-quality-gate-v1` 领先的 4 个提交经 `git cherry` 校验均为已上游的等价补丁（历史重写），不构成额外未交付实现。
- 下一步：Owner 明确接受或拒绝；任何 Agent 不得把 `PENDING` 改为 `ACCEPTED`。若要继续走 V4，需 T1 新 Task。

### 4.2 C. 待关闭 / 状态不一致（4）

**`flash-sale-v1`**（schema 0）

- `state.yaml` 无 `schema_version`，`inspect` 输出 `STATE_UNPARSEABLE`，`recovery-check` exit 2。
- State：`phase=READY_FOR_REVIEW`、`review=CLEAN`、`owner_verification=ACCEPTED`、`delivery=NONE`；`delivery.md` 却记录 `Result=PASS`。
- 结论：State 与交付文档互相矛盾，且 State 无法被当前版本解析。缺失的正式步骤是“可解析的交付记录”，但按 T0/T2 规则**不得补写**。

**`flash-sale-v3`**（schema 3）

- `develop` 上的 State：`owner=ACCEPTED`、`review=CLEAN`（`8f0db11`）、`delivery=NOT_RUN`；`delivery.md` 为占位。
- `origin/feat/flash-sale-v3` tip `a905e1c` 含 `delivery=PASS`（`feature_head=fa7626f`、`develop_base=91a67d5`），`git cherry` 显示该提交尚未进入 `develop`。
- 迁移 `20261001000015` 已在 `develop` 存在，Registry 标记 ACTIVE。
- 缺失的正式步骤：交付 metadata 未合并到共享分支。是否合并需 Owner 授权，本次未执行。

**`product-review-v1`**（schema 0）

- State：`phase=WAITING_FOR_OWNER_ACCEPTANCE`、`review=CLEAN`、`owner_verification=ACCEPTED`、`delivery=NONE`；`delivery.md` 为 `Result=PASS`。
- `phase` 与 `owner_verification=ACCEPTED` 语义冲突，且无 `review.target`（V2 使用 `target_base` + `target_paths`）。
- 结论：State 不可解析且自相矛盾；只读保留。

**`admin-identity-rbac`**（无 State）

- `delivery.md` 记录 `Result=PASS`，但 Delivery Target 是“HEAD `e8f727e` + 未提交工作区改动（10 个文件）”，并注明合并前需由 Coder 提交。
- 后续提交 `f5d481a`（`fix: 恢复路由路径以对齐已批准契约`）表明修复最终已提交，但当日交付快照本身不可复现。
- 无 `state.yaml`，`inspect` / `recovery-check` 均 exit 2；缺失的正式步骤是“可复核的已提交交付快照 + State”，不得补造。

### 4.3 E. 异常 / 无法核验（主分类 5）

| Task | 具体异常 | 影响 |
|---|---|---|
| `agent-workflow-owner-confirmation` | 无 State；`delivery.md` 仅“当前尚未进入交付验收” | 无法证明交付；不能继续 V4 流程 |
| `agent-workflow-state-machine-v1` | 同上 | 同上；`develop` 有 `906637f docs(workflow): 引入任务状态机与 Validator 契约`，但无交付验收 |
| `categories-v1` | 无 State；delivery 占位；无 `contract.md` | `internal/logic/categories` 已存在，提示可能已实际落地，但无交付证据，不能据此认定完成 |
| `error-code-namespace-expansion` | 无 State；delivery 占位 | 错误码域扩展是否落地无法从本任务证据确认 |
| `global-resource-reservation` | 无 State；delivery 占位 | `.agent/registry/*` 机制已存在，但本任务无交付记录 |

### 4.4 B. 已完成（40）

- 其中 27 个有 State `delivery=PASS`，且 `review.target == owner.review_target == delivery.review_target`、`delivery.feature_head` 均可在 `develop` 历史中找到（`recommendation-v1` 除外，见 §7）。
- 另外 13 个无 State，但 `delivery.md` 有明确 `Result=PASS` 与构建/测试/启动证据，且相关实现已进入 `develop`（如 `cart-v1`、`db-migration`、`iam-v2/v3/v4`、`inventory-v1`、`order-v1`、`sku-v1`）。
- 已完成不等于“当前 V4 可继续”：这些任务全部是 schema 2/3/0 或无 State，后续任何变更都按 §5 走新 Task。

### 4.5 F. 版本演进与关联（16，均非重复）

| 组 | Task | 判定依据 |
|---|---|---|
| 秒杀演进链 | `flash-sale-v1` → `v2` → `v3` → `v4` → `v5` | Goal 逐版增量：单体 MySQL 闭环 → Redis+Lua 闸门 → 异步下单 → 故障恢复 → 容量保护/可观测/压测；后一版显式引用前一版已证明的不变量 |
| IAM 演进链 | `iam-v2` → `v3` → `v4` → `v5` | 有状态 Redis session → 多设备会话管理 → Refresh Token 轮换 → 用户禁用/启用与审计；均为独立增量 |
| 秒杀性能/观测 | `flashsale-performance-baseline`、`flashsale-p95-p99-query`、`flashsale-v5-loadtest` | 同一 V5 性能主题的三个独立验收面：基线保存、PromQL 查询、分级压测；不是互相替代 |
| 设计治理 | `design-debt-backfill`、`design-doc-governance` | 前者补历史模块 Design，后者建立长期 Design 治理机制；互补 |
| 容器演进 | `backend-container-v1` → `container-runtime-hardening-v1` | 先容器化接入 Compose，再补 K8s 非 root / liveness / readiness / graceful shutdown |
| 其他业务相邻 | `admin-identity-rbac` / `admin-order-management`；`product-like-v1` / `product-favorite-v1` / `product-view-count-v1`；`identity-domain-isolation` 与 IAM 链 | 同域但目标不同，仅标记“关联”，不标记“重复” |

处理原则：**不合并目录、不删除旧版本、不改写旧证据**；新版本一律新建 Task 并在 `task.md` 引用旧任务路径与准确 Evidence SHA。

---

## 5. 索引核验结果

### 5.1 生成与覆盖

| 命令 | 退出码 | 结果 |
|---|---:|---|
| `.agent/bin/workflow-status index > .agent/index/tasks.tsv` | 0 | 51 条数据行 + 1 表头；末尾 `# tasks=51 corrupt=22` |
| `.agent/bin/workflow-status index --format programs > .agent/index/programs.md` | 0 | 0 个 Program（仓库无 `.agent/programs`） |
| `.agent/bin/workflow-status index --format meta > .agent/index/index-meta.yaml` | 0 | `source_sha=acebfcf6…`，`generated_at=2026-10-18T13:08:04Z` |

### 5.2 一致性

- 目录数 51 = 索引数据行 51。
- `corrupt=22` = 缺失 State 19 + schema 0 3，与文件系统核验一致。
- 所有 `state.yaml` 的 `task_id` 与目录名一致，无 ghost。
- `programs.md` 为空表，与“无 Program 目录”一致。
- 索引生成物均被 `.agent/index/.gitignore` 忽略，不进入 Git，不改变任何 State/CLEAN/Neutral Tail。

### 5.3 局限

- 索引是 DERIVED，只读本地 State 与本地 Git，不证明远端新鲜度、审批有效性或 Gate PASS。
- 索引把 schema 0 与缺失 State 都标为 `corrupt`，二者原因不同，需结合本报告区分。

---

## 6. 实际修改

### 6.1 新增

| 文件 | 性质 |
|---|---|
| `docs/reports/2026-10-18-agent-task-governance.md` | 本报告；独立治理文档，不在 `.agent/tasks/` 下，不会被误认为 Task/State/Evidence |

### 6.2 生成（被 Git 忽略，不属于交付证据）

| 文件 | 说明 |
|---|---|
| `.agent/index/tasks.tsv` | 官方派生索引 |
| `.agent/index/programs.md` | 官方派生索引（0 Program） |
| `.agent/index/index-meta.yaml` | 官方派生索引元数据 |

### 6.3 未修改

- 未新增/修改/移动/删除任何 `.agent/tasks/*` 目录或文件。
- 未修改任何 `state.yaml`、`contract.md`、`findings.md`、`core-logic.md`、`owner-decision.md`、`delivery.md`。
- 未修改 Gate 实现、Role Prompt、Spec、Registry、`workflow.yaml`、根 `AGENTS.md`。
- 未创建 Intake、Task、Program、Handoff、ACK 或 Event Log。
- 未执行 `git clean`、`reset`、`rebase`、`amend`、`push` 或分支操作。

> 工作区唯一持久变化是本报告；其余变化仅为被忽略的索引/CLI 生成物。

---

## 7. 风险与阻塞

### 7.1 已验证问题（有命令/文件证据）

1. **22 个 Task 的 State 不可机器核验**
   - 19 个无 `state.yaml`：`admin-identity-rbac`、`agent-workflow-owner-confirmation`、`agent-workflow-state-machine-v1`、`cart-v1`、`categories-v1`、`db-migration`、`design-debt-backfill`、`design-doc-governance`、`error-code-namespace-expansion`、`global-resource-reservation`、`iam-v2`、`iam-v3`、`iam-v4`、`identity-domain-isolation`、`inventory-v1`、`order-v1`、`product-spu-v1`、`shipping-address-v1`、`sku-v1`。
   - 3 个 schema 0：`flash-sale-v1`、`product-favorite-v1`、`product-review-v1`。
   - 影响：`inspect` 对缺失 State exit 2；对 schema 0 输出 `STATE_UNPARSEABLE`，`recovery-check` exit 2。

2. **`recommendation-v1` Evidence Binding 已破坏**
   - `review.target=7772129…` 与 `delivery.feature_head=72d531e…` 均存在于对象库，但不在 `develop` 祖先链；`recovery-check` 报 `Contract (INV-2) … contract.target 不在当前 feature 历史中`（exit 1）。
   - `git cherry` 显示分支上 18 个提交的补丁均已等价存在于 `develop`（历史重写/重放），说明内容已交付，但**被审与被验的精确 SHA 不可追溯**。
   - 影响：按 INV-3/INV-6，旧 CLEAN/PASS 不能直接用于当前 V4 交付；不得改写 State 或重建证据。

3. **`flash-sale-v3` 交付证据滞留未合并分支**
   - `origin/feat/flash-sale-v3` 的 `a905e1c` 含 `delivery=PASS` 与完整 `state.yaml`，`git cherry` 标记为未上游。
   - `develop` 上同一 State 仍为 `NOT_RUN`，`delivery.md` 为占位。
   - 影响：`develop` 视图下任务“未交付”；真实交付记录存在但未被共享分支接纳。

4. **`test-quality-gate-v1` 缺 Owner 接受**
   - `owner=PENDING`、`owner-decision.md` 占位、`delivery=NOT_RUN`；实现已合并。
   - 影响：不能认定为完成；任何 Agent 不得补写 ACCEPTED。

5. **`ci-image-publish-v1` 发布验收未完成**
   - `delivery=BLOCKED`，4 项 AC `NOT_EXECUTED`；代码分支已并入 `develop`，但 `main` 集成与制品库发布未验证。

6. **`admin-identity-rbac` 交付快照不可复现**
   - 交付文档明确基于“未提交工作区”；后续 `f5d481a` 才提交修复；无 State。

7. **仓库没有任何 schema 4 Task、Program、Intake 或 Event Log**
   - 影响：当前所有历史 Task 都不满足 V4 的 schema 4 Handoff/ACK 语义；`workflow-status recovery-check` 对 schema 3 的 Gate 结果不能直接用于“可继续”判断。

8. **在当前分支运行 `recovery-check` 的语义限制**
   - 对 `iam-v5`：`merge-ready` FAIL，原因 `.agent/.gitignore` 在 `review.target` 后变化（框架升级引入）。
   - 对 `test-quality-gate-v1`：`owner-gate-start` FAIL，同一原因。
   - 这是“诊断运行在错误分支/当前 HEAD 上”的派生结果，不等于历史任务当时审查无效；不得据此修改旧 State 或降低 Gate。

### 7.2 仅有嫌疑、需 Owner 判断

1. `categories-v1`、`error-code-namespace-expansion`、`global-resource-reservation` 的代码/机制在 `develop` 中已存在，但本任务无交付证据。**不能据此认定完成**，只能作为“可能已实际落地”的线索。
2. `agent-workflow-owner-confirmation`、`agent-workflow-state-machine-v1` 的产出可能已被后续框架文档吸收（`docs/design/agent-workflow.md` 为 V3 归档），但没有可核验的交付记录。
3. 多个 schema 3 State 使用 7 位短 SHA（`iam-v5`、`ci-image-publish-v1`、`container-runtime-hardening-v1` 等）。这是 V3 历史格式，不代表无效，但无法满足 schema 4 的完整 SHA 要求；是否需要追溯由 Owner 决定，**不得回填**。

### 7.3 明确阻塞（本次不做）

| 事项 | 阻塞原因 |
|---|---|
| 将 schema 2/3 升级为 schema 4 | 当前版本不提供 T2；必须 Owner 逐任务授权 T1 新 Task |
| 关闭/重开任何 Task | 无该能力的正式 CLI；不得伪造 Owner/交付决定 |
| 合并 `flash-sale-v3` 的 `a905e1c` | 需 Owner 的受控交付授权；不属于本次盘点 |
| 为 `test-quality-gate-v1` 记录 Owner 接受 | 只能由 OwnerGate 按明确人类决定机械记录 |
| 归档或移动 `.agent/tasks/*` | 路径参与证据回读，规范禁止搬迁 |
| 补齐缺失 State / 重建 Handoff / ACK | T2 禁止；会改写历史事实 |

---

## 8. 验证结果

### 8.1 实际执行的命令

| 命令 | 退出码 | 结论 |
|---|---:|---|
| `go version` | 0 | `go1.24.1 linux/amd64` |
| 构建 6 个 CLI（`GOWORK=off go -C .agent/engine build …`） | 0 | 6 个二进制生成于 `.agent/bin/` |
| `.agent/bin/workflow-check config` | 0 | `PASS config: workflow.yaml schema/registry paths valid` |
| `.agent/bin/workflow-status index` | 0 | 51 Task，22 corrupt |
| `.agent/bin/workflow-status index --format programs` | 0 | 0 Program |
| `.agent/bin/workflow-status index --format meta` | 0 | source_sha = 当前 HEAD |
| `.agent/bin/workflow-status inspect iam-v5` | 0 | schema 3；candidate `merge-ready` |
| `.agent/bin/workflow-status recovery-check iam-v5` | 1 | `merge-ready` FAIL（Review Tail：`.agent/.gitignore`） |
| `.agent/bin/workflow-status inspect flash-sale-v1` | 0 | `STATE_UNPARSEABLE`（schema 0） |
| `.agent/bin/workflow-status recovery-check flash-sale-v1` | 2 | 读取 State 失败 |
| `.agent/bin/workflow-status inspect cart-v1` | 2 | `state.yaml` 不存在 |
| `.agent/bin/workflow-status recovery-check cart-v1` | 2 | `state.yaml` 不存在 |
| `.agent/bin/workflow-status inspect test-quality-gate-v1` | 0 | `owner=PENDING`、`delivery=NOT_RUN` |
| `.agent/bin/workflow-status recovery-check test-quality-gate-v1` | 1 | `owner-gate-start` FAIL（Review Tail） |
| `.agent/bin/workflow-status inspect recommendation-v1` | 0 | schema 3；状态字段齐全 |
| `.agent/bin/workflow-status recovery-check recommendation-v1` | 1 | `Contract (INV-2)` FAIL：target 不在当前 feature 历史 |
| `git cat-file -t 7772129…` | 0 | 对象存在，但不在 develop 祖先链 |
| `git cherry -v develop origin/<branch>` | 0 | 用于区分“未合并证据”与“重写后的等价补丁” |
| `git check-ignore -v .agent/index/tasks.tsv` | 0 | 由 `.agent/index/.gitignore` 忽略 |

### 8.2 未执行（不标记 PASS）

| 检查 | 状态 | 原因 |
|---|---|---|
| Engine 单元测试 | NOT_EXECUTED | 本次未修改 Engine 源码 |
| 业务 `go test ./...` | NOT_EXECUTED | 本次未修改业务代码 |
| schema 2/3 的 V4 Task Gate | NOT_EXECUTED（按规范禁止） | T0 不允许调用 V4 专属 Gate |
| `handoff-prepared` / `handoff-entry` | NOT_EXECUTED（不适用） | 仓库无 schema 4 Task |
| Program Gate / `workflow-program` 写操作 | NOT_EXECUTED | 无 Program |
| 远端新鲜度核验（fetch） | NOT_EXECUTED | 只读盘点，未联网/未 fetch |

### 8.3 Git 状态

- 开始：分支 `chore/update-workflow-to-v4.3.0-dev`，HEAD `acebfcf6…`，`git status --short` 为空。
- 结束：仅新增 `docs/reports/2026-10-18-agent-task-governance.md`（未跟踪）；`.agent/index/*` 与 `.agent/bin/*` 为忽略产物。
- 未提交、未推送、未切换分支。

---

## 9. 后续 Task 治理标准（基于当前规范，不新增 Engine 协议）

### 9.1 什么时候创建新 Task

- 出现**新的 Goal/AC/范围**，或旧 Task 无法安全继续且 Owner 需要独立批准时。
- 新 Task 一律从公共 `Discovery` 开始，产出 Intake；Owner 确认 Task ID、task_type、父分支、完整 Base 后，由公共 `TaskBuilder` 初始化 schema 4 Task。
- 不为“整理目录”“补文档”创建虚假 Task；治理报告放在 `docs/reports/`，不进入 `.agent/tasks/`。

### 9.2 什么时候继续已有 Task

- 已有 schema 4 Task：先 `workflow-status inspect <id>`，再 `recovery-check`；确认 `next_role`、Handoff prepared、ACK 与 clean/local==remote，然后按 `task_type` 进入对应角色。
- schema 2/3 或无 State 的历史 Task：**不能按 V4 继续**。按 T0 只读；需要继续时由 Owner 逐任务批准 T1，新建 Intake + schema 4 Task。
- `blocked.active=true`、Gate FAIL/ERROR、证据 missing 时不得清字段强行继续。

### 9.3 什么时候需要 Contract Revision 或重新 Discovery

- 接口、数据模型、不变量、并发/失败语义、新增全局资源：COMPLEX，回 `Analyst` 形成 Contract Revision，Owner 批准新 revision/target；不得由 Coder 偷改。
- 超出原 Scope、需要改变已批准架构或跨多个独立交付面：重新 `Discovery`，必要时拆分为多个 Task 或 Program。
- DESIGN 的已批准架构变更：新 DESIGN 修订 Task，不清空旧批准。

### 9.4 如何处理同一业务的多个版本

- 视为**版本演进**：每个版本保留独立 Task 目录、独立分支与独立证据；新版本 `task.md` 显式引用旧 Task 路径与准确 Evidence SHA。
- 不合并、不重命名、不删除旧版本；不把旧 `CLEAN`/`ACCEPTED`/`PASS` 继承给新版本。
- 若某个版本只是“同一目标的重复创建”，由 Owner 决定保留哪一个；Agent 不得自行裁决。

### 9.5 如何判断任务是否正式完成

必须同时满足（按当前版本语义）：

1. `review.status=CLEAN`，且 `review.target` 是真实 commit；
2. `owner.status=ACCEPTED` 且 `owner.review_target == review.target`；
3. `delivery.status=PASS`，`delivery.review_target == review.target`，`delivery.feature_head` / `develop_base` 可解析；
4. 交付验证的 `feature_head` 与父基线关系可核验（父分支前进会使旧验证失效）；
5. 交付 metadata 已进入共享分支，或按当时流程有明确交付记录；
6. 不存在 substantive 的 neutral-tail 违规。

仅凭 `delivery.md` 中的 `PASS` 字样、`phase` 字段或目录名，不构成正式完成。

### 9.6 如何识别异常或无法解析的历史 Task

- 先跑 `workflow-status index`，关注 `corrupt`、`v3`、`no-branch`、`orphan`、`stale`。
- 再对可疑项跑 `inspect`：出现 `STATE_UNPARSEABLE`、`state_error`、exit 2 即为不可机器核验。
- 检查 `state.yaml` 是否缺失、`schema_version` 是否为 0/2/3、`review/owner/delivery` 的 SHA 是否可解析、是否为 develop 祖先。
- 检查 `delivery.md` 是否为“当前尚未进入交付验收”占位。
- 记录异常，不修改原文件。

### 9.7 如何处理历史任务与当前 Schema 的兼容问题

- 默认 T0：只读、只报告。
- 需要继续/变更：Owner 逐任务授权 T1，新建 Task，引用旧证据 SHA，重新履行 Contract/Cleaner/Owner/交付。
- 禁止 T2 原地升级、批量重置、回填 target、补造 Handoff/ACK/批准。
- 无法无损衔接时明确 BLOCKED，不以降低 Gate 换取“通过”。

### 9.8 如何避免创建重复 Task

- 创建前先跑 `workflow-status index` + 对候选 Task `inspect`/`recovery-check`，并阅读相关 `task.md` 的 Goal/Scope。
- Discovery 必须先识别“同一目标是否已有活动 Task”；有则给恢复入口，不重复创建。
- 名称相似不等于重复；以 Goal、Scope、Contract、Evidence 和 Git 历史判断。
- 版本演进用新 Task 并在 `task.md` 中建立引用链，不用同名目录覆盖。

### 9.9 如何通过正式索引查询活动与历史任务

```bash
.agent/bin/workflow-status index                    # 全部 Task（TSV）
.agent/bin/workflow-status index --format programs  # Program（当前为 0）
.agent/bin/workflow-status index --format meta      # 生成时刻与 source_sha
.agent/bin/workflow-status inspect <task-id>        # 单任务诊断
.agent/bin/workflow-status recovery-check <task-id> # 恢复可行性（只读诊断）
```

索引是派生缓存，不是审批证据；以 `state.yaml` + Git Evidence 为准。

### 9.10 规范缺口与建议（需 Owner 决策，本次不实施）

1. 当前版本没有“历史任务归档标记”机制；无 State 的 V2 任务只能靠报告区分。若需要机器可读的“已归档/已废弃”标记，应由 Owner 决定是否扩展规范，不在本任务自行发明。
2. 当前版本不提供 T2，历史 Task 的“仅补一个 delivery 记录”也无法原地完成；若这类需求增多，应由 Owner 评估是否需要正式的承继模板。
3. `workflow-status recovery-check` 在非任务分支上运行会产生“Review Tail”误报；建议使用说明中明确“必须在任务自身 Worktree/分支运行”，是否需要写入 CLI 文档由 Owner 决定。

---

## 10. 待 Owner 决策清单

| # | 事项 | 可选方向 | 本次已做 |
|---|---|---|---|
| 1 | `test-quality-gate-v1` 是否接受 | 接受并记录 / 拒绝并归档 / T1 继续 | 仅盘点，未改 State |
| 2 | `ci-image-publish-v1` 发布验收 | 在 main 集成后启动 / 暂缓 / T1 | 仅盘点 |
| 3 | `flash-sale-v3` 的 `a905e1c` | 受控合并交付 metadata / 保持未合并并归档 | 仅盘点 |
| 4 | `flash-sale-v1`、`product-review-v1` State 矛盾 | 只读归档 / T1 追溯 | 仅盘点 |
| 5 | `admin-identity-rbac` 交付快照 | 只读归档 / T1 追溯 | 仅盘点 |
| 6 | 5 个无交付证据的早期 Task | 归档 / T1 追溯 | 仅盘点 |
| 7 | `recommendation-v1` SHA 不可达 | 接受“内容已交付、SHA 不可追溯” / T1 追溯 | 仅盘点 |
| 8 | 是否需要“历史任务归档标记”规范 | 维持现状 / 由 Owner 发起规范变更 | 未实施 |

---

## 附录 A：事实与证据索引

- 版本：`.agent/VERSION`、`.agent/upstream.yaml`
- 规范：`.agent/specs/Architecture.md`、`AgentCollaborationSpecification.md`、`AgentResponsibilityBoundary.md`
- 流程：`.agent/specs/workflows/EntryWorkflow.md` 等
- 协议：`.agent/specs/protocols/GateCatalogue.md`、`HandoffProtocol.md`、`EventLogProtocol.md`、`CLI.md`
- 历史兼容：`.agent/specs/adoption/WorkflowAdoption.md`
- 索引：`.agent/index/README.md`
- 任务数据：`.agent/tasks/*`
- 非 Engine Task 的历史文档（不计入 51）：`docs/tasks/iam-v1`、`docs/tasks/make-lifecycle`、`docs/tasks/project-init`
