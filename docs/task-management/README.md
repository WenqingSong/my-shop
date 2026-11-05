# Task 管理入口

本目录是项目历史 Task 的**人类可读导航**，用于回答“有哪些任务、哪些还在处理、哪些已完成、版本如何演进、如何安全继续”。

**当前框架：Agent Workflow `4.0.0`（上游发布线 `v4.0.0`，2026-11-05 由 `3.4.0` 升级）。** V4 删除了 Workflow Engine、`state.yaml` 权威、五个 Gate 与 Route 选择，任务记录是普通 Markdown，由 Lead Agent 组织、由 Git 事实佐证。Task 物理位置为 `.agent/tasks/<task-id>/` 平铺，**没有分类目录**；无法被 V3 引擎校验的 22 个历史 Task 已隔离到 `.agent/tasks/_archive/<task-id>/`。

## 1. 权威边界（先读）

| 信息 | 权威来源 | 说明 |
|---|---|---|
| Task 当前事实 | `.agent/tasks/<task-id>/` 下的 Markdown 记录（`task.md` / `plan.md` / `handoff.md` / `findings.md` / `delivery.md` / `decisions.md`）+ Git | V4 没有 State 文件；本目录不复制、不覆盖 |
| V3 历史任务的 `state.yaml` | `.agent/tasks/<task-id>/state.yaml` | 冻结证据，V4 不解析、不续用、不得补写 |
| 归档任务 | `.agent/tasks/_archive/<task-id>/` | 无 state.yaml 或 schema 不可解析，仅作文档证据 |
| 人类可读分类与关系 | 本目录 | 人工维护的导航层，明确标注 `UNKNOWN`，不推断状态 |
| 审计事实与逐项证据 | [2026-10-18 治理报告](../reports/2026-10-18-agent-task-governance.md) | 只读盘点结果（快照于 4.3.0）；与记录冲突时以任务目录 + Git 为准 |

V4 不提供分类清单、派生索引、单任务诊断等 CLI，也没有 Gate 命令；查询以文件系统 + `.agent/tools/git-facts` 为准。本目录不写任务记录、不产生批准，也不自动修改历史 Task。

## 2. 快速查询

```bash
# 框架自身校验（V4 唯一的机器校验：受管文件与哈希）
.agent/adoption/workflow-adoption verify --target .

# 只读 Git 事实：分支 / HEAD / 上游 / 领先落后 / 工作区是否干净
.agent/tools/git-facts --json

# 列出在用任务 / 归档任务
ls .agent/tasks | grep -v _archive
ls .agent/tasks/_archive

# 读 V3 历史任务的冻结事实（只读，不参与任何校验）
grep -E '^(schema_version|task_id|route):' .agent/tasks/<task-id>/state.yaml
```

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

- 29 个在用 Task 全部有 `task.md`；V3 时代（3.4.0 引擎）`workflow-check route --allow-legacy` 曾为 PASS=29 FAIL=0 ERROR=0，该工具已随 V4 删除，此结论降级为历史事实。
- 22 个归档 Task 在 V3 时代 `workflow-check` 返回 ERROR（exit 2），属预期行为，不代表任务失败；V4 已无此命令。
- 分类迁移历史（原 `active/completed/blocked/archive` 归属）保留在 `.agent/tasks/_archive/_layout/layout.yaml` 与 `layout-journal.yaml`，仅供参考，不参与任何校验。
- 仓库没有 `.agent/programs`、`.agent/intake`；V4 也不提供 Program / Intake / Event Log 机制。

完整 51 项见 [task-catalog.md](task-catalog.md)（快照于 2026-10-18，分类列已失效）。

## 5. Task 关系与版本

关系图与证据见 [task-relationships.md](task-relationships.md)。

- `flash-sale-v1 → v2 → v3 → v4 → v5`：Goal 逐版显式引用前一版，属于**已证实版本演进**。
- `iam-v2 → v3 → v4 → v5`：V4 明确引用 V3 与现有会话模型，属于**已证实版本演进**。
- 其他关系只标记“前置依赖”或“业务关联”，没有证据的不画箭头。
- 版本演进**不表示**旧版本已归档，也**不表示**新版本已正式替代旧版本；每个版本的交付状态独立判断。

## 6. 历史兼容与安全恢复

当前项目为 Agent Workflow `4.0.0`，V4 **没有引擎**，不解析 `state.yaml`、不跑 Gate、不做 Stage 推导：

| State 形态 | V4 行为 | 允许的操作 |
|---|---|---|
| `schema_version: 2` / `3` | 不解析；仅作为 V3 冻结证据 | 只读阅读 `task.md` / `delivery.md` / Git |
| `schema_version: 0` 或缺失 | 同上 | 只读阅读；不得补造字段 |
| 无 `state.yaml` | 与本目录记录一致（归档证据） | 仅作文档证据；不得补造 State |

重要事实：V3 时代框架替换提交改动 `.agent/README.md` 等文件，落在所有历史任务的 `review.target` 之后，按 INV-4（Review Tail）判定为实质变化，**29 个历史 Task 的 `owner-gate-start` / `delivery-start` / `merge-ready` 复核已全部失效**（实测 merge-ready：PASS=0，Review Tail FAIL=28，Contract FAIL=1）。V4 里这套机制整体不存在，历史任务只作为冻结事实保留。

安全继续步骤：

1. 确认 Task 在 `.agent/tasks/`（在用）还是 `.agent/tasks/_archive/`（归档）。
2. 在用任务：直接读六份记录（V3 任务只有 `task.md` / `delivery.md` 等旧记录）与 Git；不要为通过已不存在的校验补写 `route` / `state.yaml` 字段。
3. 归档任务：**停止**，向 Owner 提出新 Task 申请（新范围、分支、Base），不继承旧 Evidence。
4. 任何情况下都不通过清字段、补批准来“恢复”。

## 7. 新建 Task（正式流程）

1. Owner 明确需求、Task slug、分支与授权原话。
2. 由 [TaskBuilderPrompt](../../.agent/roles/TaskBuilderPrompt.md) 建档到 `.agent/tasks/<task-slug>/`，按 [templates/task/](../../.agent/templates/task/) 生成六份记录：`task.md`、`plan.md`、`handoff.md`、`findings.md`、`delivery.md`、`decisions.md`。
3. Lead 按任务需要调用角色（Analyst / Architect / Coder / Cleaner / OwnerGate / Deliverer，可跳过、可并行、可多轮）；角色是能力边界，不是固定流水线。
4. 每次交接落盘并以四字段交接块收尾（`RESULT` / `NEXT` / `TASK` / `ACTION`，可选 `EVIDENCE`），规则见 [WorkflowHandoffContract.md](../../.agent/specs/WorkflowHandoffContract.md)。

## 8. 文档维护

- 本目录为**人工维护**：业务领域、版本关系、是否需要处理需要判断，不能从机器事实自动推导。
- 新增/关闭 Task 后手工更新 [task-catalog.md](task-catalog.md)；V4 没有导航一致性校验脚本（原 `make check-task-docs` 已随 V3 机制移除）。
- 本目录只描述**已发生的事实**与**待决策事项**；不预写“计划完成”的状态，不复制 `.agent/tasks/` 内容。

## 9. 目录

| 文件 | 用途 |
|---|---|
| [task-catalog.md](task-catalog.md) | 51 个 Task 的领域、状态、交付与证据（快照 2026-10-18） |
| [task-relationships.md](task-relationships.md) | 版本演进 / 前置依赖 / 业务关联图与证据 |
| [owner-decisions.md](owner-decisions.md) | 需要 Owner 决策的事项及可安全执行动作 |
| [../reports/2026-10-18-agent-task-governance.md](../reports/2026-10-18-agent-task-governance.md) | 全量审计报告（事实与命令输出，快照于 4.3.0） |
| [../../.agent/tasks/_archive/_layout/layout.yaml](../../.agent/tasks/_archive/_layout/layout.yaml) | 历史分类清单（已失效，仅归档记录） |
