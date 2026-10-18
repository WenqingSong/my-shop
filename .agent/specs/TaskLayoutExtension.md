# Task 目录分类扩展（项目本地，非上游）

- 状态：项目本地扩展，已由 Owner 于 2026-10-18 在升级任务中明确授权。
- 上游基线：Agent Workflow `4.3.0`（commit `229edfd18e3b6e770f803db01e5010b215ba623f`）。
- 上游事实：V4.3.0 **没有** Task 目录分类、归档标记或路径解析机制，全部 Task 平铺在 `.agent/tasks/<task-id>/`。
- 本文不是上游规范的一部分；与上游冲突时以本文件为准（仅限本仓库），升级上游时必须保留本文描述的本地补丁。

## 1. 为什么需要

历史 Task 长期平铺在 `.agent/tasks/`，已增长到 51 个，无法按「进行中 / 已完成 / 阻塞 / 历史」快速定位。上游没有该能力，因此本仓库实现一个与现有 Workflow 协议兼容、可验证、可回滚的分类扩展。

## 2. 目录布局

```text
.agent/tasks/
├── layout.yaml              # 分类清单（权威分类记录，committed）
├── layout-journal.yaml      # 最近一次迁移的回滚日志（committed）
├── active/                  # 有证据表明仍在推进
├── completed/               # 有正式完成证据
├── blocked/                 # 真实阻塞 / 等待决策 / 无法继续
└── archive/                 # 不再作为活动任务推进的历史任务（不等于完成）
```

`<category>/<task-id>/` 内部文件与上游完全一致：`task.md`、`state.yaml`、`findings.md`、`core-logic.md`、`owner-decision.md`、`delivery.md`、`contract.md`、`architecture.md`、`design.md`、`events.md`。

## 3. 身份与解析规则

1. **Task ID 是唯一身份**，物理路径不是。所有 Gate、CLI、索引、事件、交付、Program 都通过 `internal/workflow/task_layout.go` 解析路径。
2. 解析候选顺序：清单登记的 `category/<id>` → 旧平铺 `.agent/tasks/<id>` → 其余分类目录。
3. 同一 ID 出现在多个目录属于**错误**（`duplicate`），解析直接失败，不静默选一个。
4. CLI 位置参数接受：实际目录、`.agent/tasks/<id>`（旧简写）、`.agent/tasks/<category>/<id>`、裸 `<id>`。旧简写只在 ID 唯一时解析到真实目录。
5. 未找到返回包装了 `os.ErrNotExist` 的错误，供 Program 快照回退区分；事件日志读取把「目录不存在」视为「无事件」。
6. review/delivery neutral 判定按**同一 Task 的全部候选目录**匹配，因此目录移动本身不会把 Artifact tail 误判为实质变化；其他 Task 的同名文件仍为 substantive。

## 4. 分类语义与证据要求

`layout.yaml` 的每条记录必须声明 `basis`：

| basis | 含义 | 机器校验 |
|---|---|---|
| `state` | 分类可由当前 `state.yaml` 机械核验 | 是，见下 |
| `document` | 分类来自人工交付文档证据（`delivery.md`、治理报告、Owner 决策） | 否，必须列出 `evidence` |
| `unknown` | 无法核实状态，只能作为历史待核验项 | 否，必须列出 `evidence` |

规则：

- `completed` + `basis=state`：要求 `delivery=PASS`、`review=CLEAN`、`owner=ACCEPTED`、`owner.review_target == review.target`、`delivery.feature_head` 位于 `origin/<integration_branch>` 历史中。
- `completed` + `basis=document`：只允许用于无 State / schema 0 的早期任务，或 State 事实存在但 SHA 已不可追溯的情况；必须给出 evidence。
- `blocked` + `basis=state`：要求 `blocked.active=true` 或 `delivery=BLOCKED`。
- `archive` 不得使用 `basis=state`：归档表示「不再作为活动任务推进」，不能用交付状态证明。
- **归档不等于完成，旧 Schema 不等于完成，目录名也不能代替 State 与 Evidence。**
- 分类清单不是 State：不写 CLEAN / ACCEPTED / PASS，不参与 Gate 计算，不能被用作审批证据。

## 5. 新任务创建

- TaskBuilder 新建 schema 4 Task 时，目录固定为 `.agent/tasks/active/<task-slug>/`。
- 首次 Gate（`taskbuilder-init`）仍传入 ID 或目录；CLI 会解析到 `active/<id>`。
- 任务完成后由 `layout migrate` 依据清单移动到 `completed/`；需要人工决定的事项移动前先改清单并说明证据。
- 生成器、模板与文档不得再创建新的平铺目录。

## 6. 迁移、预检与回滚

```bash
# 只读查询
.agent/bin/workflow-status layout list            # task_id/category/basis/path/legacy
.agent/bin/workflow-status layout list --format ids
.agent/bin/workflow-status layout resolve <id>    # 打印实际目录
.agent/bin/workflow-status layout check           # 0 一致 / 1 不一致 / 2 错误

# 迁移（默认 dry-run）
.agent/bin/workflow-status layout plan
.agent/bin/workflow-status layout migrate
.agent/bin/workflow-status layout migrate --apply

# 回滚（默认 dry-run；校验内容未变化）
.agent/bin/workflow-status layout rollback
.agent/bin/workflow-status layout rollback --apply
```

安全属性：

- 预检 fail closed：未登记分类、重复 ID、目标目录占用、清单非法、符号链接、特殊文件都拒绝执行，且不移动任何目录。
- 迁移只做目录移动，不读改写 `state.yaml`、`delivery.md`、`events.md`。
- 迁移前后对每个文件计算 SHA-256，写入 `layout-journal.yaml`。
- 中途失败自动把已移动目录移回（尽力而为，失败会显式报错）。
- 回滚先校验当前内容与日志一致，再移动；已回滚条目幂等；内容漂移时拒绝回滚。
- 迁移是实质变更：活动 Task 的 review/delivery tail 会因此包含路径变化，必须在 Cleaner/Owner 审查前完成，或重新审查。

## 7. 与现有能力的一致性

- 索引（`workflow-status index`）新增 `category` 列（追加在最后，保持既有列序号稳定）；`path` 为真实路径；`legacy-layout` / `unclassified` flag 提示未迁移或未分类。索引仍是 DERIVED，不是审批证据。
- Gate、Handoff、Event Log、Evidence 路径全部经统一解析，不复制第二套权威路径。
- Program 交付读取 Git 快照时按候选路径尝试，兼容协调者工作树与目标提交布局不一致。
- 未知状态任务只进入 `archive` 或 `blocked`，不得进入 `completed`。

## 8. 升级上游时

上游更新涉及以下文件时需要合并而非覆盖（本地补丁点）：

- `internal/workflow/task_layout.go`、`task_layout_migration.go`（新增）
- `internal/workflow/index.go`（category 列与发现逻辑）
- `internal/workflow/gates.go`、`paths.go`、`validator.go`、`contract_revision.go`、`design_gate.go`、`event_writer.go`、`recovery.go`、`task_delivery.go`、`delivery.go`、`program_state.go`、`program_operations.go`（路径解析）
- `cmd/workflow-check/main.go`、`cmd/workflow-status/main.go`、`cmd/workflow-status/layout.go`
- 本文与 `.agent/specs/protocols/CLI.md` 的 `layout` 小节

## 9. 已知限制

- 分类由人工清单维护，不自动推导；`layout check` 只核验已声明分类的证据，不替 Owner 决定归属。
- `document` / `unknown` 依据无法机器验证，只能追溯 evidence 路径。
- 迁移不做跨文件系统复制；`os.Rename` 失败会回滚并报错。
- 目录移动会改变 Git 路径，历史 `state.yaml` 中的 SHA 仍指向旧路径，这是历史事实，不回写。
