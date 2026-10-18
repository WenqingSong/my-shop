# Owner 决策清单

- 快照日期：2026-10-18（同日完成 Workflow 4.3.0 升级与 Task 分类迁移；第 6 项已落地）
- 本清单只列**仍需 Owner 决定**的事项；文档、导航、索引核验等安全工作已直接完成。
- 任何 Agent 不得代替 Owner 记录 `ACCEPTED`、合并交付分支、改写历史 SHA 或补写 State。

## 0. 一览

| # | 事项 | 当前状态 | 推荐 | 不处理的影响 |
|---|---|---|---|---|
| 1 | `test-quality-gate-v1` 接受与交付 | `owner=PENDING`、`delivery=NOT_RUN` | 先接受或拒绝，再决定是否 T1 交付 | 任务长期悬空，实现已合并但无正式接受记录 |
| 2 | `ci-image-publish-v1` 发布验收 | `delivery=BLOCKED`，4 项 AC 未执行 | main 集成后单独启动发布验收 | 镜像发布链路未被真实证明 |
| 3 | `flash-sale-v3` 受控集成 | `develop=NOT_RUN`，分支 `a905e1c` 有 PASS | 受控合并该 metadata commit | `develop` 视图与真实证据不一致 |
| 4 | 历史 State 矛盾（`flash-sale-v1`、`product-review-v1`） | schema 0，State 与 `delivery.md` 冲突 | 只读归档，不做 T2 | 历史记录无法机器核验，但不影响现状 |
| 5 | `recommendation-v1` SHA 不可追溯 | `DONE/STATE`，旧 SHA 不在 `develop` 祖先链 | 接受“内容已交付、SHA 不可追溯”，或 T1 追溯 | 旧 CLEAN/PASS 不能直接复用 |
| 6 | ~~是否新增历史归档标记规范~~ | **已授权并实现**（2026-10-18 升级任务）：Task 目录分类扩展 + `layout` CLI | 维护 `layout.yaml` 并按需迁移 | 无（已落地） |

## 1. `test-quality-gate-v1` 的 Owner 接受与交付状态

**问题**：实现已完成并合并，但 Owner 从未接受，交付验收未运行。

**已确认事实**

- State：`contract=NOT_REQUIRED`、`review=CLEAN`（target `24cfd01`）、`owner=PENDING`、`delivery=NOT_RUN`。
- `owner-decision.md` 仍为“等待 OwnerGate 决策后填写”；`delivery.md` 为“当前尚未进入交付验收”。
- 实现已并入 `develop`（`4e6dfa0`）；分支 `origin/test/test-quality-gate-v1` 的领先提交经 `git cherry` 校验均为已上游的等价补丁（历史重写）。
- 当前版本对 schema 3 不提供原地继续；`recovery-check` 报 `owner-gate-start` FAIL（Review Tail：`.agent/.gitignore` 在 review target 后变化），这是诊断环境差异，不是历史审查无效。

**可安全执行**

- 阅读 State、`findings.md`、`core-logic.md` 与交付证据；在 `workflow-status inspect` 中复核。
- 用本目录导航记录现状（已完成）。

**必须授权**

- Owner 明确 `ACCEPTED` / `REJECTED`。
- Owner 决定是否新建 T1 任务完成 schema 4 的接受与交付。

**推荐方案**

1. Owner 先给出接受或拒绝的明确结论。
2. 若接受：按 T1 新建一个 schema 4 Task，引用本任务路径与 `review.target`，重新履行 OwnerGate / Merger（不能继承旧 CLEAN/PASS）。
3. 若拒绝：保留原 State，在 T1 新任务中记录拒绝原因与后续范围。

**不处理的影响**：任务永久停留在 `PENDING`；后续开发者无法判断该门禁是否被正式认可。

**依据**：`state.yaml`、`owner-decision.md`、`.agent/specs/workflows/NormalWorkflow.md`、`.agent/specs/protocols/GateCatalogue.md`。

## 2. `ci-image-publish-v1` 的发布验收

**问题**：CI 镜像发布能力已实现，但发布阶段的验收条件未执行。

**已确认事实**

- State：`owner=ACCEPTED`、`review=CLEAN`、`delivery=BLOCKED`；`blocked.active=false`（交付阻塞不等于任务 blocked）。
- `delivery.md` 的 `Result=BLOCKED`，原因：`Owner-controlled integration dependency`。
- 未执行：AC-002/003/004/005/006/007/009 —— 需要真实 `main` push、授权制品库与流水线触发。
- 代码分支 `origin/feat/ci-image-publish-v1` 已全部并入 `develop`（领先 0 提交）。

**可安全执行**

- 整理未执行 AC、所需环境/权限、当前阻塞原因与执行步骤（已完成，见 `delivery.md`）。
- 复核 `.cnb.yml` 与镜像构建的静态检查结果。

**必须授权**

- 向 `main` 推送或触发真实流水线；访问/推送授权制品库。
- Owner 决定发布验收的范围与时间窗口。

**推荐方案**：在 main 集成窗口单独启动发布验收；通过后按 T1 记录新的交付证据，不把本次 `BLOCKED` 改写为 PASS。

**不处理的影响**：镜像发布链路（双标签同源、digest、失败不更新 latest）只有本地证据，没有真实流水线证据。

**依据**：`state.yaml`、`delivery.md`、`.cnb.yml`。

## 3. `flash-sale-v3` 的受控集成

**问题**：真实交付记录存在，但只在一个未合并分支上。

**已确认事实**

- `develop` 的 State：`owner=ACCEPTED`、`review=CLEAN`（`8f0db11`）、`delivery=NOT_RUN`；`delivery.md` 为占位。
- `origin/feat/flash-sale-v3` tip `a905e1c` 含 `delivery=PASS`（`feature_head=fa7626f`、`develop_base=91a67d5`），`git cherry` 标记为未上游。
- 迁移 `20261001000015` 已在 `develop` 存在，Registry 标记 `ACTIVE`；V3 的代码内容已通过 `1543ecb` 合并。
- `develop..origin/feat/flash-sale-v3` 仅 1 个提交（交付 metadata）。

**可安全执行**

- 只读核对分支、提交、State、Registry 与合并历史（已完成）。
- 在文档中记录差异与恢复步骤（已完成）。

**必须授权**

- 合并 `a905e1c`（或等价地以受控流程把交付 metadata 带入 `develop`）。
- 若 Owner 选择不合并，明确把 V3 标记为“证据滞留分支、develop 未关闭”。

**推荐方案**：先确认 `a905e1c` 只改 `delivery.md` 与 `state.yaml`（已验证），再由 Owner 授权受控合并；不得直接 `git merge` 或改写 State。

**不处理的影响**：`develop` 上该任务永远显示 `NOT_RUN`，后续审计会重复发现同一差异。

**依据**：`state.yaml`、`origin/feat/flash-sale-v3:delivery.md`、`git cherry -v develop origin/feat/flash-sale-v3`。

## 4. 历史 State 矛盾（`flash-sale-v1`、`product-review-v1`）

**问题**：schema 0 State 无法被当前 CLI 解析，且与 `delivery.md` 的 PASS 结论矛盾。

**已确认事实**

- `flash-sale-v1`：State `phase=READY_FOR_REVIEW`、`review=CLEAN`、`owner_verification=ACCEPTED`、`delivery=NONE`；`delivery.md` 为 `Result=PASS`。
- `product-review-v1`：State `phase=WAITING_FOR_OWNER_ACCEPTANCE`、`review=CLEAN`、`owner_verification=ACCEPTED`、`delivery=NONE`；`delivery.md` 为 `Result=PASS`。
- 两者均无 `schema_version`，`inspect` 输出 `STATE_UNPARSEABLE`，`recovery-check` exit 2。
- 当前版本不提供 T2；不得补写 `schema_version`、target 或 delivery。

**可安全执行**

- 只读保留原文件；在目录中标注 `INCONSISTENT` 与证据位置（已完成）。
- 提供安全恢复说明与 T1 入口（已完成）。

**必须授权**

- 是否把两者作为“历史已交付、仅记录不一致”接受。
- 是否需要为新需求创建 T1 任务（新范围、新 Base、重新审查）。

**推荐方案**：接受为历史不一致，不做任何 State 写入；若未来要修改相关功能，新建 T1 任务并引用旧路径与 `delivery.md`，不继承旧批准。

**不处理的影响**：机器索引持续显示 `corrupt`；不影响当前业务运行。

**依据**：两份 `state.yaml`、`delivery.md`、`.agent/specs/adoption/WorkflowAdoption.md`（T0/T2）。

## 5. `recommendation-v1` 的 SHA 不可追溯

**问题**：State 指向的被审/被验 SHA 因历史重写已不在 `develop` 祖先链。

**已确认事实**

- `review.target=7772129…`、`owner.review_target=7772129…`、`delivery.feature_head=72d531e…`、`contract.target=70672be…`。
- 这些对象仍存在于 Git 对象库，但 `git merge-base --is-ancestor` 显示不在 `develop`；`recovery-check` 报 `Contract (INV-2) FAIL`。
- `git cherry` 显示 `origin/feat/recommendation-slot` 的 18 个提交补丁均已等价存在于 `develop`，`develop` 上也有等价的交付提交 `52f88e2`；代码与迁移 `20261001000016` 已在 `develop`。
- State 未被改写；本次也不改写。

**可安全执行**

- 记录“内容已交付、精确 SHA 不可追溯”的事实与影响范围（已完成）。
- 用现有 `delivery.md` 与 `develop` 代码作为业务完成证据。

**必须授权**

- Owner 接受该历史事实，或决定新建 T1 任务重新固定可追溯的 Base 与 Evidence。

**推荐方案**：接受历史事实，不改 State；后续涉及推荐位的变更一律新建 Task，从当前 `develop` 取 Base。

**不处理的影响**：该任务的旧 CLEAN/PASS 不能用于新的 V4 交付；不会影响现有功能。

**依据**：`state.yaml`、`delivery.md`、`git cat-file` / `git cherry` 输出（见治理报告 §7.1）。

## 6. 是否新增正式的历史归档标记规范

**状态：已由 2026-10-18 升级任务授权并实现（项目本地扩展）。**

**问题**：当前版本没有“已归档 / 已废弃”标记；无 State 的 V2 任务只能靠文档区分。

**已确认事实**

- `workflow-status` 只有 `corrupt` / `v3` / `no-branch` / `orphan` / `stale` 等提示，不表达业务归档。
- 规范未定义归档字段；向 `state.yaml` 写入自定义字段会违反 T2 与 State 语义。
- 本目录已用 `ACTIVE` / `DONE/STATE` / `DONE/DOC` / `INCONSISTENT` / `UNKNOWN` 表达导航状态，且不改 State。

**可安全执行**

- 使用本目录导航与 `workflow-status index` 的组合查询（已完成）。
- 在报告中提出规范缺口（已完成）。

**必须授权**

- 是否修改 Workflow 规范或 Engine，增加正式的归档标记/命令。
- 若修改，需要单独的设计（State 兼容、Gate 影响、历史数据只读策略）与 Owner 批准。

**已实施**：`.agent/specs/TaskLayoutExtension.md` + `workflow-status layout`（list/check/plan/migrate/rollback/resolve）。
分类清单为 `.agent/tasks/layout.yaml`，迁移日志为 `.agent/tasks/layout-journal.yaml`；分类不是审批证据，
`archive` 不等于完成，`completed` 必须有 State 或交付文档证据。

**仍由 Owner 决定**：把某个 `blocked` 任务改成 `archive` 还是新建 T1 任务；分类变化需先改清单并写明证据。

**不处理的影响**：已消除；布局与分类可由 `layout check` 机器校验。

**依据**：`.agent/specs/Architecture.md`、`.agent/specs/protocols/CLI.md`、`.agent/specs/adoption/WorkflowAdoption.md`。

## 7. 已直接完成、无需 Owner 决定的安全动作

| 动作 | 结果 |
|---|---|
| 全量 Task 盘点与证据核验 | 已完成，见治理报告 |
| 官方索引生成与覆盖核验 | 51/51，`corrupt=22`，无 Ghost |
| Task 导航入口 | `docs/task-management/README.md` + 根 README 入口 |
| 51 项人类可读目录 | `docs/task-management/task-catalog.md` |
| 版本/依赖/关联关系图 | `docs/task-management/task-relationships.md` |
| 历史兼容与恢复说明 | README §6 |
| 文档一致性校验 | `make check-task-docs`（只读） |
| 历史数据保护 | 未修改任何 Task/State/Evidence/Registry |
