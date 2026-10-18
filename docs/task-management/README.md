# Task 管理入口

本目录是项目历史 Task 的**人类可读导航**，用于回答“有哪些任务、哪些还在处理、哪些已完成、版本如何演进、如何安全继续”。
Task 已按状态迁入 `.agent/tasks/{active,completed,blocked,archive}/` 分类目录（项目本地扩展）。

## 1. 权威边界（先读）

| 信息 | 权威来源 | 说明 |
|---|---|---|
| Task 当前状态与决策 | `.agent/tasks/<category>/<id>/state.yaml` + Git Evidence | 唯一机器权威；本目录不复制、不覆盖 |
| Task 分类与物理位置 | `.agent/tasks/layout.yaml` + `.agent/bin/workflow-status layout check` | 导航分类；**不是**审批证据，不参与 Gate |
| 官方索引 | `.agent/bin/workflow-status index` | DERIVED，只读当前工作树与本地 Git，不是审批证据 |
| 人类可读分类与关系 | 本目录 | 人工维护的导航层，明确标注 `UNKNOWN`，不推断状态 |
| 审计事实与逐项证据 | [2026-10-18 治理报告](../reports/2026-10-18-agent-task-governance.md) | 只读盘点结果；与 State 冲突时以 State + Git 为准 |

**不要**把本目录或 `layout.yaml` 当成第二套状态机：它们不写 State、不产生批准、不替代 Gate，也不自动修改历史 Task。
分类规则、证据要求与迁移/回滚见 [.agent/specs/TaskLayoutExtension.md](../../.agent/specs/TaskLayoutExtension.md)。

## 2. 快速查询

首次使用先构建 CLI（产物被 Git 忽略）：

```bash
for cli in workflow-check workflow-git workflow-status workflow-event workflow-program workflow-deliver; do
  GOWORK=off go -C .agent/engine build -o "../bin/$cli" "./cmd/$cli" || exit
done
```

按分类查（推荐，先看目录再看 State）：

```bash
# 分类清单：task_id / category / basis / path / legacy
.agent/bin/workflow-status layout list

# 只看某个分类（例如仍待决策的任务）
.agent/bin/workflow-status layout list | awk -F'\t' 'NR>2 && $2=="blocked"'

# 校验清单与磁盘一致、分类依据可核验（0 一致 / 1 不一致 / 2 错误）
.agent/bin/workflow-status layout check

# Task ID 定位实际目录
.agent/bin/workflow-status layout resolve <task-id>
```

按 State 查（索引仍然是 DERIVED）：

```bash
# 全部 Task（TSV：含 schema / contract / review / owner / delivery / blocked / flags / category）
.agent/bin/workflow-status index

# 已完成（delivery=PASS）：注意跳过 1 行注释 + 1 行表头
.agent/bin/workflow-status index | awk -F'\t' 'NR>2 && $10=="PASS"'

# 被 blocked 的任务（blocked 列）
.agent/bin/workflow-status index | awk -F'\t' 'NR>2 && $14=="true"'

# 索引标记的异常（corrupt / ghost / orphan / no-branch / legacy-layout / unclassified）
.agent/bin/workflow-status index | awk -F'\t' 'NR>2 && $20 ~ /corrupt|ghost|orphan|no-branch|legacy-layout|unclassified/'

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

`blocked` 列只表示 State 中的 `blocked.active`，不等于 `blocked/` 目录：目录分类还包含“等待 Owner 决策”，以 `layout.yaml` 的 `reason`/`evidence` 为准。

## 3. 状态词汇与分类映射（本目录专用）

| 状态 | 含义 | 可机器核验 | 迁移后目录 |
|---|---|---|---|
| `DONE/STATE` | State `delivery=PASS`，且 `review/owner/delivery` 目标一致、`feature_head` 在 `develop` 祖先链 | 是 | `completed/` |
| `DONE/DOC` | 无 State（或 State 为 schema 0），但 `delivery.md` 有明确 `Result=PASS` 与构建/测试证据，且实现已进入 `develop` | 否，仅人工证据 | `completed/` |
| `ACTIVE` | 仍有未完成工作，下一步是 Owner 决策或发布阶段动作 | 部分（State 可见） | `blocked/`（等待决策/外部依赖时）或 `active/` |
| `INCONSISTENT` | State 与交付文档矛盾，或交付证据滞留在未合并分支 | 否 | `blocked/`（等待 Owner 决定） |
| `UNKNOWN` | 无 State 且无交付记录；不能认定为完成 | 否 | `archive/`（历史待核验） |

规则：

- 目录名、版本后缀、文件修改时间**都不能**推断状态。
- State 缺失或不可解析时，不标 `DONE`；标 `DONE/DOC` 时必须能在 `delivery.md` 找到 PASS 证据，否则标 `UNKNOWN`。
- **归档不等于完成**：`archive/` 只表示不再作为活动任务推进；`completed/` 必须有 State 或交付文档证据。
- `DONE` 不等于“当前 V4 流程可继续”：51 个 Task 目前全部是 schema 0/2/3 或无 State，继续需按下节 T1。

## 4. 当前快照（2026-10-18）

| 分类 | 数量 | 说明 |
|---|---:|---|
| Task 总数 | 51 | 无重复、无 Ghost，ID 唯一 |
| `active/` | 0 | 当前没有仍在推进的 schema 4 任务 |
| `completed/` | 40 | 25 项 State 证据 + 15 项交付文档证据 |
| `blocked/` | 6 | 1 项 `delivery=BLOCKED`；1 项 Owner 接受待决；1 项分支待合并；3 项历史不一致/无 State |
| `archive/` | 5 | 无 State 且无交付验收的历史任务，待核验 |

细节：

- 19 个 Task 无 `state.yaml`；3 个为 schema 0；**0 个 schema 4**。
- 官方索引当前输出 `# tasks=51 corrupt=22`（缺失 State 19 + schema 0 3），与迁移前一致。
- 仓库没有 `.agent/programs`、`.agent/intake`、`events.md`，因此没有 Program/Intake/Event Log 可导航。
- 迁移记录与逐文件 SHA-256：`.agent/tasks/layout-journal.yaml`。

完整 51 项见 [task-catalog.md](task-catalog.md)。

## 5. Task 关系与版本

关系图与证据见 [task-relationships.md](task-relationships.md)。

- `flash-sale-v1 → v2 → v3 → v4 → v5`：Goal 逐版显式引用前一版，属于**已证实版本演进**。
- `iam-v2 → v3 → v4 → v5`：V4 明确引用 V3 与现有会话模型，属于**已证实版本演进**。
- 其他关系只标记“前置依赖”或“业务关联”，没有证据的不画箭头。
- 版本演进**不表示**旧版本已归档，也**不表示**新版本已正式替代旧版本；每个版本的交付状态独立判断。

## 6. 历史兼容与安全恢复

当前项目为 Agent Workflow `4.3.0`（上游正式版），Task Schema 4；历史兼容规则以 `.agent/specs/adoption/WorkflowAdoption.md` 为准：

| 路径 | 允许 | 不允许 |
|---|---|---|
| **T0 只读** | `inspect` schema 2/3/0；阅读 `task.md` / `delivery.md` / Git | 调用 V4 handoff / taskbuilder-init / design Gate；写回 State |
| **T1 新任务承继** | Owner 逐任务批准后，新建 Intake + schema 4 Task，引用旧路径与 Evidence SHA | 继承旧 CLEAN/ACCEPTED/PASS；替换旧 SHA |
| **T2 原地升级** | 当前版本不提供 | 改 `schema_version`、补 Intake/Handoff/ACK/批准 |

安全继续步骤：

1. 用 `layout resolve` 定位目录，查 [task-catalog.md](task-catalog.md) 确认分类与证据位置。
2. 跑 `inspect` / `recovery-check`，核对 `blocked`、Gate 与当前分支。
3. 若 Task 为 schema 2/3/0 或无 State：**停止**，向 Owner 提出 T1 申请（新范围、类型、父分支、Base）。
4. 若 Task 为 schema 4：按 `task_type` 从 `.agent/roles/` 取当前角色，先 `handoff-entry` 再 ACK。
5. 任何情况下都不通过清字段、补批准或降低 Gate 来“恢复”。

## 7. 新建 Task（正式流程）

1. 新需求先由公共 [DiscoveryAgent](../../.agent/roles/DiscoveryAgent.md) 调查，输出 Intake 并推荐类型；不预先选择 Workflow。
2. Owner 确认 Task ID、NORMAL/COMPLEX/DESIGN、父分支与完整 Base。
3. 公共 [TaskBuilderAgent](../../.agent/roles/TaskBuilderAgent.md) 按已批准类型在 `.agent/tasks/active/<task-id>/` 初始化 schema 4 Task、分支/Worktree 与初始 Handoff。
4. 按 `.agent/specs/workflows/` 对应流程执行。

创建前先查本目录与 `workflow-status layout list`，避免为同一目标重复建 Task；同一业务的新版本必须新建 Task 并在 `task.md` 引用旧任务路径与 Evidence SHA。

任务关闭或状态变化后：

```bash
# 1. 只改分类清单（写明 basis/reason/evidence），2. 预检，3. 迁移
.agent/bin/workflow-status layout plan
.agent/bin/workflow-status layout migrate --apply
.agent/bin/workflow-status layout check
```

## 8. 文档维护与验证

- 本目录为**人工维护**：业务领域、版本关系、是否需要处理需要判断，不能从索引自动推导。
- 新增/关闭 Task 后，更新 `task-catalog.md`，并运行：

```bash
make check-task-docs
```

该目标调用 `scripts/check-task-docs.sh`（只读），校验：

- Task ID 集合一致（目录/分类清单/官方索引/catalog 四方 51 项，无 Ghost、无重复）；
- catalog 状态词汇合法，`UNKNOWN` 不被写成 `DONE`，状态与 `layout.yaml` 分类映射一致；
- 旧平铺目录已清空（`legacy-layout` 为 0）；
- 本目录 Markdown 相对链接存在；
- Mermaid 代码块成对出现。

索引与布局仍以官方 CLI 为准；脚本不生成第二套状态数据。

## 9. 目录

| 文件 | 用途 |
|---|---|
| [task-catalog.md](task-catalog.md) | 51 个 Task 的领域、状态、关系、交付、证据与分类目录 |
| [task-relationships.md](task-relationships.md) | 版本演进 / 前置依赖 / 业务关联图与证据 |
| [owner-decisions.md](owner-decisions.md) | 需要 Owner 决策的事项及可安全执行动作 |
| [../reports/2026-10-18-agent-task-governance.md](../reports/2026-10-18-agent-task-governance.md) | 全量审计报告（事实与命令输出） |
| [../../.agent/specs/TaskLayoutExtension.md](../../.agent/specs/TaskLayoutExtension.md) | 分类目录、解析、迁移与回滚规范（项目本地扩展） |

维护约定：本目录只描述**已发生的事实**与**待决策事项**；不预写“计划完成”的状态，不复制 `.agent/tasks/` 内容。
