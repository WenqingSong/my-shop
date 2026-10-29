# Task 管理入口

本目录是项目历史 Task 的**人类可读导航**，用于回答“有哪些任务、哪些还在处理、哪些已完成、版本如何演进、如何安全继续”。

**当前框架：Agent Workflow `3.2.0`（上游发布线 `v3.2.0`）。** Task 物理位置已回归上游约定：`.agent/tasks/<task-id>/` 平铺，**不再有分类目录**；v4 分类机制移除时无法被 3.2.0 引擎校验的 22 个历史 Task 已隔离到 `.agent/tasks/_archive/<task-id>/`。

## 1. 权威边界（先读）

| 信息 | 权威来源 | 说明 |
|---|---|---|
| Task 当前状态与决策 | `.agent/tasks/<task-id>/state.yaml` + Git Evidence | 唯一机器权威；本目录不复制、不覆盖 |
| 归档任务 | `.agent/tasks/_archive/<task-id>/` | 无 state.yaml 或 schema 不可解析，**不得**调用 Gate |
| 人类可读分类与关系 | 本目录 | 人工维护的导航层，明确标注 `UNKNOWN`，不推断状态 |
| 审计事实与逐项证据 | [2026-10-18 治理报告](../reports/2026-10-18-agent-task-governance.md) | 只读盘点结果（快照于 4.3.0）；与 State 冲突时以 State + Git 为准 |

`3.2.0` 不提供分类清单、派生索引、单任务诊断等 CLI（`workflow-status` 已随 v4 移除），查询以文件系统 + `workflow-check` 为准。本目录不写 State、不产生批准、不替代 Gate，也不自动修改历史 Task。

## 2. 快速查询

构建校验器（产物被 Git 忽略）：

```bash
GOWORK=off go -C .agent/engine build -o ../bin/workflow-check ./cmd/workflow-check
```

```bash
# 路线事实自检（历史任务缺 route，需显式 --allow-legacy）
.agent/bin/workflow-check route .agent/tasks/<task-id> --allow-legacy

# 五个 Gate：coder-start / cleaner-start / owner-gate-start / delivery-start / merge-ready
.agent/bin/workflow-check gate merge-ready .agent/tasks/<task-id>

# 列出在用任务 / 归档任务
ls .agent/tasks | grep -v _archive
ls .agent/tasks/_archive

# 直接读 State 的机器事实
grep -E '^(schema_version|task_id|route):' .agent/tasks/<task-id>/state.yaml
```

退出码：`0` PASS、`1` 不变量不满足、`2` 输入/State/Git 运行级错误。

## 3. 状态词汇（本目录专用）

| 状态 | 含义 | 可机器核验 | 当前位置 |
|---|---|---|---|
| `DONE/STATE` | State `delivery=PASS`，且 `review/owner/delivery` 目标一致、`feature_head` 在 `develop` 祖先链 | 是 | `.agent/tasks/` |
| `DONE/DOC` | 无 State（或 State 为 schema 0），但 `delivery.md` 有明确 `Result=PASS` 与构建/测试证据，且实现已进入 `develop` | 否，仅人工证据 | `_archive/` |
| `ACTIVE` | 仍有未完成工作，下一步是 Owner 决策或发布阶段动作 | 部分（State 可见） | `.agent/tasks/` |
| `INCONSISTENT` | State 与交付文档矛盾，或交付证据滞留在未合并分支 | 否 | 视 State 是否存在，分别在 `.agent/tasks/` 或 `_archive/` |
| `UNKNOWN` | 无 State 且无交付记录；不能认定为完成 | 否 | `_archive/` |

规则：

- 目录名、版本后缀、文件修改时间**都不能**推断状态。
- State 缺失或不可解析时，不标 `DONE`；标 `DONE/DOC` 时必须能在 `delivery.md` 找到 PASS 证据，否则标 `UNKNOWN`。
- **归档不等于完成**：`_archive/` 只表示该 Task 在当前框架下不可机器校验；`DONE/STATE` 必须有 State 证据。

## 4. 当前快照（2026-10-29）

| 位置 | 数量 | 说明 |
|---|---:|---|
| Task 总数 | 51 | 无重复、无 Ghost，ID 唯一 |
| `.agent/tasks/`（在用） | 29 | 25 项 schema 3 + 4 项 schema 2，均无 `route` 事实 |
| `.agent/tasks/_archive/` | 22 | 19 项无 `state.yaml`；3 项 `schema_version=0`（`flash-sale-v1`、`product-favorite-v1`、`product-review-v1`） |

细节：

- 29 个在用 Task 全部有 `task.md`，`workflow-check route --allow-legacy` 结果为 PASS=29 FAIL=0 ERROR=0。
- 22 个归档 Task 的 `workflow-check` 调用会返回 ERROR（exit 2），这是预期行为，不代表任务失败。
- 分类迁移历史（原 `active/completed/blocked/archive` 归属）保留在 `.agent/tasks/_archive/_layout/layout.yaml` 与 `layout-journal.yaml`，仅供参考，不参与任何校验。
- 仓库没有 `.agent/programs`、`.agent/intake`，3.2.0 也不提供 Program/Intake/Event Log 机制。

完整 51 项见 [task-catalog.md](task-catalog.md)（快照于 2026-10-18，分类列已失效）。

## 5. Task 关系与版本

关系图与证据见 [task-relationships.md](task-relationships.md)。

- `flash-sale-v1 → v2 → v3 → v4 → v5`：Goal 逐版显式引用前一版，属于**已证实版本演进**。
- `iam-v2 → v3 → v4 → v5`：V4 明确引用 V3 与现有会话模型，属于**已证实版本演进**。
- 其他关系只标记“前置依赖”或“业务关联”，没有证据的不画箭头。
- 版本演进**不表示**旧版本已归档，也**不表示**新版本已正式替代旧版本；每个版本的交付状态独立判断。

## 6. 历史兼容与安全恢复

当前项目为 Agent Workflow `3.2.0`，state schema 支持范围由引擎决定：

| State 形态 | 3.2.0 行为 | 允许的操作 |
|---|---|---|
| `schema_version: 2` / `3` | 严格解析，可跑 Gate 与 `route` | 只读复核；不写回、不改 schema |
| `schema_version: 0` 或缺失 | 拒绝解析（exit 2） | 只读阅读 `task.md` / `delivery.md` / Git；不得调用 Gate |
| 无 `state.yaml` | 视为不存在该 Task | 仅作文档证据；不得补造 State |

重要事实：框架替换提交改动 `.agent/README.md` 等文件，落在所有历史任务的 `review.target` 之后，按 INV-4（Review Tail）判定为实质变化。因此 **29 个历史 Task 的 `owner-gate-start` / `delivery-start` / `merge-ready` 复核已全部失效**（实测 merge-ready：PASS=0，Review Tail FAIL=28，Contract FAIL=1）。这不是可修复的配置问题，而是“CLEAN 快照绑定具体 SHA”的机制结果；历史任务只作为冻结事实保留。

安全继续步骤：

1. 确认 Task 在 `.agent/tasks/`（在用）还是 `.agent/tasks/_archive/`（归档）。
2. 在用任务：`route --allow-legacy` 可自查；不要为通过校验补写 `route` 或 `## Complexity`。
3. 归档任务：**停止**，向 Owner 提出新 Task 申请（新范围、路线、父分支、Base），不继承旧 Evidence。
4. 任何情况下都不通过清字段、补批准或降低 Gate 来“恢复”。

## 7. 新建 Task（正式流程）

1. Owner 明确需求、Task slug、Feature Branch（名称 + base 分支 + base SHA）与授权原话。
2. 由 [TaskBuilderPrompt](../../.agent/roles/TaskBuilderPrompt.md) 以 `mode: create` 初始化 `.agent/tasks/<task-slug>/task.md`，并记录路线决策。
3. 路线 `NORMAL` / `COMPLEX` / `ARCHITECT` 的判定规则见 [WorkflowRouteSelection.md](../../.agent/specs/WorkflowRouteSelection.md)；`state.yaml.route` 是唯一机器权威，`task.md` 的 `## Complexity` 供人阅读且必须一致。
4. 按角色 Prompt 逐步执行，每个交接点跑对应 Gate：

```bash
.agent/bin/workflow-check gate coder-start .agent/tasks/<task-slug>
.agent/bin/workflow-check gate cleaner-start .agent/tasks/<task-slug>
.agent/bin/workflow-check gate owner-gate-start .agent/tasks/<task-slug>
.agent/bin/workflow-check gate delivery-start .agent/tasks/<task-slug>
.agent/bin/workflow-check gate merge-ready .agent/tasks/<task-slug>
```

## 8. 文档维护

- 本目录为**人工维护**：业务领域、版本关系、是否需要处理需要判断，不能从机器事实自动推导。
- 新增/关闭 Task 后手工更新 [task-catalog.md](task-catalog.md)；3.2.0 没有导航一致性校验脚本，`make check-task-docs` 已随 v4 机制移除。
- 本目录只描述**已发生的事实**与**待决策事项**；不预写“计划完成”的状态，不复制 `.agent/tasks/` 内容。

## 9. 目录

| 文件 | 用途 |
|---|---|
| [task-catalog.md](task-catalog.md) | 51 个 Task 的领域、状态、交付与证据（快照 2026-10-18） |
| [task-relationships.md](task-relationships.md) | 版本演进 / 前置依赖 / 业务关联图与证据 |
| [owner-decisions.md](owner-decisions.md) | 需要 Owner 决策的事项及可安全执行动作 |
| [../reports/2026-10-18-agent-task-governance.md](../reports/2026-10-18-agent-task-governance.md) | 全量审计报告（事实与命令输出，快照于 4.3.0） |
| [../../.agent/tasks/_archive/_layout/layout.yaml](../../.agent/tasks/_archive/_layout/layout.yaml) | 历史分类清单（已失效，仅归档记录） |
