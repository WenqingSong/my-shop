# Task: SurgeCart 秒杀链路工程基线审计（surgecart-baseline-audit）

## Goal

在集成分支当前真实代码上重建 SurgeCart 秒杀链路的工程基线：每一条结论都必须能落到具体源码、SQL、测试或真实执行输出，使后续 Agent 不再依赖 README、聊天记录或历史验收报告判断现状，并让 T02、T03 可凭本任务证据直接开展专项审查。

## Scope

- **版本锚定**：核实仓库路径、当前分支、HEAD Commit、工作区状态（暂存 / 未暂存 / 未跟踪）与未提交修改，把审查对应的精确代码版本写入报告首页。
- **调用链测绘**：从秒杀 HTTP 入口一路追到最终订单结果，覆盖路由注册、参数校验、限流、Redis Lua 准入、MySQL 请求记录写入、后台消费者领取、订单创建、库存扣减、请求状态查询、对账任务，逐跳给出文件与函数。
- **数据层定位**：关键表与字段、状态取值、事务边界、唯一约束、索引、条件更新（`RowsAffected`）判定、migration 文件，全部定位到具体文件或 SQL，不停留在模块名。
- **请求表职责判定**：按代码回答 MySQL 请求表当前承担排队、持久化、幂等、状态查询、恢复中的哪几项，并说明消费者实际如何领取与处理请求。
- **工程配套核实**：Go 测试、质量门禁、`Dockerfile`、CNB 流水线、Prometheus 相关代码的真实状态，区分「已核实」与「尚未核实」。
- **测试基线执行**：在当前环境运行可执行的测试，保存命令、退出码与必要错误输出；不可执行项标记未执行。
- **证据分区**：把结果分成已验证事实、推断、未知事项、潜在风险四类，风险只登记不修复。

## Out of Scope

- 不修改秒杀业务实现，不改 `internal/`、`api/`、`test/` 下的生产代码与测试代码。
- 不新增 Kafka Producer、Consumer、Outbox，不引入任何外部 MQ。
- 不修改数据库 Schema，不新增或修改 Migration，不占用 `migration_version`。
- 不修复审计中发现的疑似缺陷、竞态或数据不一致；只记录证据、影响面与复现条件。
- 不定义 T02、T03 的任务内容、路线或结论，只交付它们可用的审查输入。
- 不产出压测、容量规划、性能达标结论，也不做生产部署验证。
- 不修改 Agent Workflow 本身（`.agent/specs/`、`.agent/roles/`、`.agent/templates/`、`.agent/VERSION`）。

## Constraints

- 只读审计：除 `docs/reports/surgecart-baseline-audit/` 与本任务目录外，不产生仓库改动；确需改动其他文件，须先由 Lead 向 Owner 申请授权。
- 证据优先顺序：源码 / Migration / 测试 / 真实命令输出 > 设计文档 > README。设计文档与 README 只作为「宣称」对照项。
- `docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md` 是旧基线的历史交付，只能作为对照对象；引用其中任何结论都必须在本次代码上重新验证。
- 每条已验证事实至少附带一种锚点：`path:line`、SQL 原文、命令 + 输出、commit SHA；缺锚点的结论只能进「推断」或「未知事项」。
- 环境观测（任务建立时，非审计结论）：`go1.24.1 linux/amd64`、`Docker 29.9.2`、`mysql` 客户端可用、`redis-cli` 不在 PATH。
- 依赖不可用或未执行的测试必须记 `NOT_EXECUTED` 并写明原因，不得计入通过。
- 交付物位置：`docs/reports/surgecart-baseline-audit/`（本任务唯一报告目录）。
- 交付前 `git status --short` 只能包含本任务预期改动，不得夹带任务目录外的既有修改。

## Deliverables

| # | 交付物 | 文件 |
| --- | --- | --- |
| D1 | 核心调用链与源码入口地图 | `docs/reports/surgecart-baseline-audit/call-chain-map.md` |
| D2 | 关键表、状态字段、事务边界与索引定位清单 | `docs/reports/surgecart-baseline-audit/data-layer-map.md` |
| D3 | 测试基线记录（通过 / 失败 / 未执行） | `docs/reports/surgecart-baseline-audit/test-baseline.md` |
| D4 | 已验证事实、推断、未知事项与潜在风险清单 | `docs/reports/surgecart-baseline-audit/facts-and-risks.md` |
| D5 | 提供给 T02、T03 的审查输入 | `docs/reports/surgecart-baseline-audit/review-input-t02-t03.md` |

## Acceptance Criteria

- [ ] AC-1: 报告首页登记仓库路径、分支名、完整 40 位 HEAD SHA、工作区状态，并与执行时 `git rev-parse HEAD`、`git status --short` 一致，同时声明结论只对该版本成立。
  - 验证：`git rev-parse HEAD` 与首页 SHA 逐字符比对；`git status --short` 与首页描述比对。
- [ ] AC-2: D1 给出从秒杀 HTTP 入口到最终订单结果的完整调用链，逐跳标注文件路径与函数 / 方法名（含行号更佳），至少覆盖路由注册、参数校验、限流、Redis Lua 准入、请求记录写入、消费者领取、订单创建、库存扣减、请求状态查询、对账任务十类环节。
  - 验证：随机抽 3 跳（必须含 Redis Lua 准入与消费者领取），在标注文件中能 grep 到同名函数及调用点。
- [ ] AC-3: D2 列出关键表与字段、状态取值、事务边界、唯一约束、索引、条件更新语句与 Migration 文件名，并逐项说明该约束守护的不变量（库存不为负、成功订单不超卖、一人一单、失败不建单、重复请求不重复扣减）。
  - 验证：条目可在 `internal/migrations/sql/*.up.sql` 与逻辑层 SQL 中逐项找到，且与代码判断分支一致。
- [ ] AC-4: D2 明确回答「当前 MySQL 请求表承担哪些职责」，并用具体 SQL 与代码位置说明消费者如何领取、并发保护、失败处理与超时 / 崩溃恢复；与 `docs/design/` 的宣称不一致处逐条列出，无对应设计时写明核对范围。
  - 验证：引用的领取 SQL、条件更新、恢复逻辑可在源码中逐一找到；`ls docs/design/` 中的秒杀相关文档逐条对照。
- [ ] AC-5: D3 以表格记录实际执行的检查：命令、退出码、`PASS` / `FAIL` / `NOT_EXECUTED`、关键输出摘要，至少覆盖 `go build ./...`、`go vet ./...`、`make test-unit`、`make test-prometheus`、`make test-container`，以及依赖 MySQL / Redis 的集成测试（`make test-integration` 或 `make test`）。
  - 验证：按 D3 记录的同一命令在同类环境重跑，退出码与状态一致；`NOT_EXECUTED` 项原因可复核（依赖缺失、环境不可用）。
- [ ] AC-6: D4 把结论分为「已验证事实（附锚点）」「推断（附依据与缺口）」「未知事项（附核验方式）」「潜在风险（附影响面）」四类；README 或设计文档的意图性表述不出现在已验证事实中。
  - 验证：抽 3 条已验证事实均有可复核锚点；抽 2 条推断确认未被写成事实；检查「潜在风险」条目均无修复动作。
- [ ] AC-7: 本轮变更范围只包含 `docs/reports/surgecart-baseline-audit/` 与 `.agent/tasks/surgecart-baseline-audit/`；`git diff --stat <AC-1 基线 SHA>..HEAD` 中不出现 `internal/`、`api/`、`test/`、`go.mod`、`go.sum`、`internal/migrations/`、`Dockerfile`、`.cnb.yml`、`prometheus/`、`.agent/specs/` 的改动，也没有新增 Kafka / Outbox 代码。
  - 验证：`git diff --stat <sha>..HEAD`、`git status --short`、`git log --oneline` 三项核对。
- [ ] AC-8: D5 给出可按路径、行号、SQL、命令、SHA 复核的证据清单，以及仍需专项核查的问题清单；只列问题，不下 T02 / T03 的结论，并注明两个编号来自 Owner 后续任务、其范围不在本任务内。
  - 验证：从 D5 任选 3 条锚点，可在源码、SQL 或命令输出中定位；问题清单中不出现「已确认缺陷」式结论。
- [ ] AC-9: D1 ~ D5 五个文件在 `docs/reports/surgecart-baseline-audit/` 下齐备且非空，报告路径登记在 `delivery.md`。
  - 验证：`ls -l docs/reports/surgecart-baseline-audit/` 与 `wc -l` 检查五个文件存在且非空。

## Verification

| AC | 验证方式 |
| --- | --- |
| AC-1 | `git rev-parse HEAD`、`git status --short` 与报告首页逐项比对 |
| AC-2 | 抽查 3 跳（含 Lua 准入、消费者领取）用 `grep -n` 在标注文件定位 |
| AC-3 | Migration 与逻辑层 SQL 双向核对；逐项对照不变量 |
| AC-4 | 领取 / 恢复 SQL 回源码核对；`docs/design/` 差异逐条对照 |
| AC-5 | 按 D3 命令重跑并比对退出码与状态 |
| AC-6 | 四类清单抽检；确认无「推断当事实」「风险带修复」 |
| AC-7 | `git diff --stat <baseline>..HEAD` 白名单核对 |
| AC-8 | 抽 3 条锚点回源码 / 命令核对 |
| AC-9 | `ls -l` 与 `wc -l` 检查五个交付文件 |

## Review Baseline

- Repo: `/root/projects/my-shop`
- Remote: `origin` = `https://cnb.cool/framework-station/my-shop`
- Base branch: `develop`（本地与 `origin/develop` 同为 `473d6f5253e3abcb98ece9d79a5a8059563a3e8d`）
- Base Commit: `473d6f5253e3abcb98ece9d79a5a8059563a3e8d`
- 工作区: clean（`git status --short` 输出为空，无暂存、未暂存、未跟踪文件）
- Task branch: `task/surgecart-baseline-audit`（从 Base Commit 创建，未使用任何远端分支作为起点）
- 基线选择依据（Git 事实）：
  - `develop` 最新提交 `473d6f5`（2026-11-05），`main` 最新提交 `be93060`（2026-10-14）；`git rev-list --count origin/main..origin/develop` = 29，`origin/develop..origin/main` = 14。
  - 任务分支历史合并进入 `develop`：`a01a626 合并 task/surgecart-baseline-audit-v1`、`f7b7a8d`、`49430ab` 等；`main` 的头部提交为「合并来自 develop 的合并请求」。
  - `main` 相对 `develop` 独有的非合并提交只有 `d96bd28 docs(workflow): 创建后台订单查询管理 V1 任务定义`（仅 `.agent/tasks/admin-order-management/` 文档），不含业务代码；`develop` 含 `main` 未有的 `95344f5 feat(inventory): 库存流水查询支持分页并补充边界测试`。
  - 结论：`develop` 是当前集成分支，`main` 为下游稳定线；本任务以 `develop` 的 `473d6f5` 为基线。
- 说明: 交付时与该基线的差异必须全部属于本任务。

## Owner Authorization

- Owner: SurgeCart Owner（通过本次重新初始化请求下达）
- 授权范围:
  - 撤销上一轮 `surgecart-baseline-audit-v2` 的初始化产物（本地分支、远端分支、任务目录）；
  - 创建 `task/surgecart-baseline-audit` 分支；
  - 提交并推送该分支上的任务记录与后续审计报告；
  - 只读审计当前仓库代码与配置。
- 需要额外授权: 修改业务代码、构建脚本或 CI；改 Schema 或 Migration；引入 Kafka / Outbox；合并到 `develop` / `main`；`push --force`；删除本任务与其他既有分支。

## Open Questions

- 无。以下为已记录假设，不需要 Owner 补充输入：
  - `T02`、`T03` 为 Owner 后续任务编号，其范围与验收标准未提供，本任务只交付审查输入，不推断其内容。
  - 报告目录 `docs/reports/surgecart-baseline-audit/` 由本任务固定，不另设版本后缀。
  - 集成测试能否执行取决于执行环境的 MySQL / Redis 可用性；不可执行时按 AC-5 记 `NOT_EXECUTED`，不阻塞任务。
