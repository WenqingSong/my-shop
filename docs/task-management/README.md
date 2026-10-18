# Task 管理入口

本目录是项目历史 Task 的**人类可读导航**，用于回答“有哪些任务、哪些还在处理、哪些已完成、版本如何演进、如何安全继续”。

## 1. 权威边界（先读）

| 信息 | 权威来源 | 说明 |
|---|---|---|
| Task 当前状态与决策 | `.agent/tasks/<id>/state.yaml` + Git Evidence | 唯一机器权威；本目录不复制、不覆盖 |
| 官方索引 | `.agent/bin/workflow-status index` | DERIVED，只读当前工作树与本地 Git，不是审批证据 |
| 人类可读分类与关系 | 本目录 | 人工维护的导航层，明确标注 `UNKNOWN`，不推断状态 |
| 审计事实与逐项证据 | [2026-10-18 治理报告](../reports/2026-10-18-agent-task-governance.md) | 只读盘点结果；与 State 冲突时以 State + Git 为准 |

**不要**把本目录当成第二套状态机：它不写 State、不产生批准、不替代 Gate，也不自动修改历史 Task。

## 2. 快速查询

首次使用先构建 CLI（产物被 Git 忽略）：

```bash
for cli in workflow-check workflow-git workflow-status workflow-event workflow-program workflow-deliver; do
  GOWORK=off go -C .agent/engine build -o "../bin/$cli" "./cmd/$cli" || exit
done
```

常用查询：

```bash
# 全部 Task（TSV：含 schema / contract / review / owner / delivery / blocked / flags）
.agent/bin/workflow-status index

# 已完成（delivery=PASS）：注意跳过 1 行注释 + 1 行表头
.agent/bin/workflow-status index | awk -F'\t' 'NR>2 && $10=="PASS"'

# 被 blocked 的任务（blocked 列）
.agent/bin/workflow-status index | awk -F'\t' 'NR>2 && $14=="true"'

# 索引标记的异常（corrupt / ghost / orphan / no-branch）
.agent/bin/workflow-status index | awk -F'\t' 'NR>2 && $20 ~ /corrupt|ghost|orphan|no-branch/'

# 单任务诊断与恢复可行性（只读）
.agent/bin/workflow-status inspect <task-id>
.agent/bin/workflow-status recovery-check <task-id>
```

索引生成物位于 `.agent/index/`（被 `.agent/index/.gitignore` 忽略）：

```bash
.agent/bin/workflow-status index > .agent/index/tasks.tsv
.agent/bin/workflow-status index --format programs > .agent/index/programs.md
.agent/bin/workflow-status index --format meta > .agent/index/index-meta.yaml
```

**活动任务没有单一的 CLI 过滤器**：请查 [task-catalog.md](task-catalog.md) 的“活动 / 待处理”表，再用 `inspect` 核对真实 State。`blocked` 列只表示 State 中的 `blocked.active`，不等于“需要 Owner 决策”。

## 3. 状态词汇（本目录专用）

| 状态 | 含义 | 可机器核验 |
|---|---|---|
| `DONE/STATE` | State `delivery=PASS`，且 `review/owner/delivery` 目标一致、`feature_head` 在 `develop` 祖先链 | 是 |
| `DONE/DOC` | 无 State（或 State 为 schema 0），但 `delivery.md` 有明确 `Result=PASS` 与构建/测试证据，且实现已进入 `develop` | 否，仅人工证据 |
| `ACTIVE` | 仍有未完成工作，下一步是 Owner 决策或发布阶段动作 | 部分（State 可见） |
| `INCONSISTENT` | State 与交付文档矛盾，或交付证据滞留在未合并分支 | 否 |
| `UNKNOWN` | 无 State 且无交付记录；不能认定为完成 | 否 |

规则：

- 目录名、版本后缀、文件修改时间**都不能**推断状态。
- State 缺失或不可解析时，不标 `DONE`；标 `DONE/DOC` 时必须能在 `delivery.md` 找到 PASS 证据，否则标 `UNKNOWN`。
- `DONE` 不等于“当前 V4 流程可继续”：51 个 Task 全部是历史格式，继续需按下节 T1。

## 4. 当前快照（2026-10-18）

| 分类 | 数量 |
|---|---:|
| Task 总数 | 51 |
| 活动（`ACTIVE`） | 2 |
| 已完成（`DONE/STATE` + `DONE/DOC`） | 40 |
| 待关闭 / 不一致（`INCONSISTENT`） | 4 |
| 异常 / 无法核验（`UNKNOWN`） | 5 |
| 历史兼容（全部 Task） | 51 |

细节：

- 19 个 Task 无 `state.yaml`；3 个为 schema 0；**0 个 schema 4**。
- 官方索引当前输出 `# tasks=51 corrupt=22`（缺失 State 19 + schema 0 3）。
- 仓库没有 `.agent/programs`、`.agent/intake`、`events.md`，因此没有 Program/Intake/Event Log 可导航。

完整 51 项见 [task-catalog.md](task-catalog.md)。

## 5. Task 关系与版本

关系图与证据见 [task-relationships.md](task-relationships.md)。

- `flash-sale-v1 → v2 → v3 → v4 → v5`：Goal 逐版显式引用前一版，属于**已证实版本演进**。
- `iam-v2 → v3 → v4 → v5`：V4 明确引用 V3 与现有会话模型，属于**已证实版本演进**。
- 其他关系只标记“前置依赖”或“业务关联”，没有证据的不画箭头。
- 版本演进**不表示**旧版本已归档，也**不表示**新版本已正式替代旧版本；每个版本的交付状态独立判断。

## 6. 历史兼容与安全恢复

当前项目为 Agent Workflow `4.3.0-dev`，Task Schema 4；历史兼容规则以 `.agent/specs/adoption/WorkflowAdoption.md` 为准：

| 路径 | 允许 | 不允许 |
|---|---|---|
| **T0 只读** | `inspect` schema 2/3；阅读 `task.md` / `delivery.md` / Git | 调用 V4 handoff / taskbuilder-init / design Gate；写回 State |
| **T1 新任务承继** | Owner 逐任务批准后，新建 Intake + schema 4 Task，引用旧路径与 Evidence SHA | 继承旧 CLEAN/ACCEPTED/PASS；替换旧 SHA |
| **T2 原地升级** | 当前版本不提供 | 改 `schema_version`、补 Intake/Handoff/ACK/批准 |

安全继续步骤：

1. 查 [task-catalog.md](task-catalog.md) 确认分类与证据位置。
2. 跑 `inspect` / `recovery-check`，核对 `blocked`、Gate 与当前分支。
3. 若 Task 为 schema 2/3 或无 State：**停止**，向 Owner 提出 T1 申请（新范围、类型、父分支、Base）。
4. 若 Task 为 schema 4：按 `task_type` 从 `.agent/roles/` 取当前角色，先 `handoff-entry` 再 ACK。
5. 任何情况下都不通过清字段、补批准或降低 Gate 来“恢复”。

## 7. 新建 Task（正式流程）

1. 新需求先由公共 [DiscoveryAgent](../../.agent/roles/DiscoveryAgent.md) 调查，输出 Intake 并推荐类型；不预先选择 Workflow。
2. Owner 确认 Task ID、NORMAL/COMPLEX/DESIGN、父分支与完整 Base。
3. 公共 [TaskBuilderAgent](../../.agent/roles/TaskBuilderAgent.md) 按已批准类型初始化 schema 4 Task、分支/Worktree 与初始 Handoff。
4. 按 `.agent/specs/workflows/` 对应流程执行。

创建前先查本目录与 `workflow-status index`，避免为同一目标重复建 Task；同一业务的新版本必须新建 Task 并在 `task.md` 引用旧任务路径与 Evidence SHA。

## 8. 文档维护与验证

- 本目录为**人工维护**：业务领域、版本关系、是否需要处理需要判断，不能从索引自动推导。
- 新增/关闭 Task 后，更新 `task-catalog.md`，并运行：

```bash
make check-task-docs
```

该目标调用 `scripts/check-task-docs.sh`（只读），校验：

- 目录、官方索引与 catalog 的 Task ID 集合一致（51/51，无 Ghost、无重复）；
- catalog 状态词汇合法，`UNKNOWN` 不被写成 `DONE`；
- 本目录 Markdown 相对链接存在；
- Mermaid 代码块成对出现。

索引仍以官方 CLI 为准；脚本不生成第二套状态数据。

## 9. 目录

| 文件 | 用途 |
|---|---|
| [task-catalog.md](task-catalog.md) | 51 个 Task 的领域、状态、关系、交付与证据 |
| [task-relationships.md](task-relationships.md) | 版本演进 / 前置依赖 / 业务关联图与证据 |
| [owner-decisions.md](owner-decisions.md) | 需要 Owner 决策的 6 项及可安全执行动作 |
| [../reports/2026-10-18-agent-task-governance.md](../reports/2026-10-18-agent-task-governance.md) | 全量审计报告（事实与命令输出） |

维护约定：本目录只描述**已发生的事实**与**待决策事项**；不预写“计划完成”的状态，不复制 `.agent/tasks/` 内容。
