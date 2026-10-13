# Task: 项目基线清理（project-baseline-cleanup-v1）

## Goal

消除工程配置、文档与全局资源登记之间的漂移，使代码、配置、Registry（migration version / 错误码域）、README 与 `docs/design/*` 相互一致，并形成一份与真实实现相符的项目能力基线与已知限制清单。

## Scope

- 核对 `.agent/registry/migrations.md` 与 `.agent/registry/error-codes.md` 中每个条目的状态与实际合并情况是否一致，将「已正式合并进 `develop` 且实际使用」的资源由 `RESERVED` 更新为 `ACTIVE`，未合并的保持 `RESERVED`。
- 核对并修正 `docs/design/migration.md` 的「当前迁移清单」，使其完整、逐条对齐 `internal/migrations/sql/*` 与 `migrations.md` Registry。
- 核对 README 的「当前实现」「能力边界」「目录结构」「快速开始」「环境变量」「容器化部署模型」「指标与 Prometheus」等章节与实际实现一致。
- 核对 Docker / CI/CD（`.cnb.yml`）/ Prometheus（`prometheus/prometheus.yml`）相关文档（`docs/design/deployment.md` 及 README 对应章节）是否滞后。
- 检查并处理废弃目录、无效配置、历史遗留脚本：仅在确认「当前代码与文档均无引用、且不再承担运行时或历史证据职责」时移除，其余保留。
- 明确记录 Mock 支付、秒杀独立库存/独立下单流程、MySQL 出队表（无外部 MQ）等设计边界，并形成当前项目能力基线与已知限制清单。
- 运行 Registry / 文档相关只读检查与构建、静态检查，确认无回归。

## Out of Scope

- 不新增任何业务功能、数据模型、状态机、公开 API、错误码或 migration。
- 不重新设计或改动 Mock 支付、秒杀库存、出队表等既有业务语义；只确保其边界被准确记录。
- 不执行 `RESERVED → RELEASED` 的资源释放，不新增/复用任何错误码域或 migration version（本任务不占用新的全局资源）。
- 不删除仍被引用或承担历史证据职责的资源；判断不清的一律保留。
- 不进行 Kubernetes / Ingress / HTTPS / 多实例部署等尚未形成结论的工作，只记录其「未形成已验证结论」的现状。
- 不修改 `.agent/workflow.yaml`、`.agent/specs/*`、`.agent/roles/*` 与 Workflow Engine。

## Acceptance Criteria

- [ ] AC-001：`migrations.md` Registry 中每个条目的状态与其真实合并状态一致——已合并进 `develop` 且 `internal/migrations/sql/` 存在对应 `.up.sql` 的条目标记为 `ACTIVE`；尚未合并的保持 `RESERVED`；不删除、不复用任何 version。
- [ ] AC-002：`error-codes.md` Registry 中每个域区间的状态与其真实合并状态一致——已合并进 `develop` 的域标为 `ACTIVE`，未合并的保持 `RESERVED`；任意两个 ACTIVE/RESERVED 域区间不重叠。
- [ ] AC-003：`docs/design/migration.md` 的「当前迁移清单」逐条完整覆盖 `internal/migrations/sql/` 下全部 23 个 migration（version + title + 内容），与 `.up.sql` 文件名及 `migrations.md` Registry 完全一致，无遗漏、无多余、无 title 漂移。
- [ ] AC-004：`./scripts/check-registry.sh` 以退出码 0 通过（无重复、无区间重叠、无「迁移文件未登记」、无「ACTIVE version 无对应文件」、无「错误码不在任何 ACTIVE/RESERVED 域内」等 FAIL）。
- [ ] AC-005：README 的「当前实现」表与「能力边界」准确反映真实代码，至少明确记录：普通订单使用 Mock 支付（未接真实支付机构）、秒杀订单为独立下单流程（不走普通支付/取消）、秒杀异步队列为 MySQL 出队表（无外部 MQ）、仓库未发布稳定性能报告且未形成 K8s 部署结论。
- [ ] AC-006：`docs/design/deployment.md`、README 部署章节、`.cnb.yml`、`prometheus/prometheus.yml` 对 Go 版本、Compose 服务清单、健康检查端点、`/metrics` 开关（`FLASH_SALE_METRICS_ENABLED`）语义的描述与实际 `Dockerfile`、`docker-compose.yml`、`.cnb.yml`、`prometheus/prometheus.yml` 一致，无滞后引用（已改动的服务名/端口/镜像）。
- [ ] AC-007：对疑似废弃目录/脚本（如 `docs/tasks/`、`scripts/import-legacy.sh` 等）逐一核验：仅当「当前代码与文档均无引用、且不再承担运行时或历史证据职责」时移除；仍被使用的历史资源（如 `docs/学习/` 的 Project Mastery 学习产物）必须保留；无法确认用途的一律保留并记录原因，不盲目删除。每项移除需留下引用核验证据。
- [ ] AC-008：形成当前项目能力基线与已知限制清单（落在 README「当前实现」+「能力边界」或等价的明确位置），内容与真实代码、`docs/design/*`、Registry 一致，覆盖已实现模块、两条核心交易链路与未覆盖边界。
- [ ] AC-009：本轮清理后 `go build ./...` 与 `go vet ./...` 通过；若移除文件，确认无编译/测试引用残留；`./scripts/check-registry.sh` 通过。

## Relevant Context

- 已核实事实：
  - 当前分支 `chore/project-baseline-cleanup-v1`，HEAD `359b8cc`，working tree clean。
  - `migrations.md` 登记 00001–00023：00001–00008 为 `ACTIVE`，00009–00023 为 `RESERVED`。
  - `error-codes.md` 登记 1000–8999（域序 1–8）为 `ACTIVE`，10000–19999（域序 10–19）为 `RESERVED`。
  - `internal/migrations/sql/` 实际存在 23 个 `.up.sql`（00001–00023 齐全）。
  - `docs/design/migration.md` §2.1「当前迁移清单」仅列 00001–00008 与 00019，缺 00009–00018、00020–00023（已滞后）。
  - `docs/design/error-codes.md` §4 明确是「长期设计快照」并声明以 Registry 为唯一权威，不属于本次需强行补全的漂移。
  - `git log` 合并历史显示大量 feature 已合并进 `develop`（coupon-v1、order-refund-v1、iam-user-status、object-storage-upload、flash-sale-v3/v5、cms、banner、like、goods-view-count、product-favorite 等），与上述 `RESERVED` 状态普遍冲突。
  - `docs/tasks/`（iam-v1 / make-lifecycle / project-init）为 Workflow V3 迁移前的旧任务产物目录；`docs/学习/` 为 Project Mastery 独立 workflow 的学习产物（其 README 明确声明用途）。
  - CI 为 `.cnb.yml`（golang:1.24 + docker compose + `make test` + build + GitHub 镜像同步）。
  - `scripts/check-registry.sh` 是只读机械校验器，只校验重复/重叠/Registry↔实现漂移，**不校验** `RESERVED → ACTIVE` 的语义正确性。
- Assumption：00009–00023 与 10000–19999 中的多数资源其 feature 已合并进 `develop`，因此应转 `ACTIVE`；但每个条目的最终状态由 Coder 按 git 合并历史与代码实际使用逐条核实，本任务不预设具体哪些条目必转 `ACTIVE`。
- OPEN QUESTION：无阻塞项。「废弃目录/脚本是否删除」为逐项引用核实的判断，规则已在 AC-007 明确，无法确认时一律保留。

## Verification

- AC-001 / AC-002 → 以 `git log --merges`（`develop` 合并历史）与 `internal/migrations/sql/*`、`internal/codes/codes.go` 逐条对照 Registry 条目，记录「条目 → 依据 commit → 判定」的核对表；需真实 Git 历史，不依赖本地未合并分支状态。
- AC-003 → 逐行对照 `docs/design/migration.md` 清单、`.up.sql` 文件名、`migrations.md` 三个来源，三者 version/title 一致。
- AC-004 / AC-009 → 在仓库根执行 `./scripts/check-registry.sh`、`go build ./...`、`go vet ./...`，退出码均为 0。
- AC-005 / AC-008 → 对照 `docs/design/order.md`、`flash-sale.md`、`deployment.md` 与 `internal/` 真实实现核验 README 表述。
- AC-006 → 对照实际 `Dockerfile`、`docker-compose.yml`、`.cnb.yml`、`prometheus/prometheus.yml` 核验文档描述。
- AC-007 → 对每个移除项，记录「引用检查（`grep`/`search`）证据 + 移除或保留结论」。

## Complexity

NORMAL

原因：本任务是配置、文档与资源登记的**一致性核对与收敛**，不涉及新的架构/数据模型/协议选择，不涉及事务、并发、MQ、权限或跨系统一致性，无根因不明的 Bug，也无多个会产生不同业务结果的现实方案。资源状态（`RESERVED`/`ACTIVE`）由 git 合并历史与代码实际使用这一客观事实决定，不需要 Analyst 做方案设计。

## Review Baseline

- Base commit：`359b8cc`（`chore/project-baseline-cleanup-v1` 当前 HEAD，等于 `develop` 最新合并点）。
- 任务开始时已有修改：working tree clean（`git status --short` 为空），无暂存、未暂存或新增文件。
- 重叠修改的区分方式：本任务全部变更均应属于基线之后的清理内容；若实施中遇到来源不明的既有改动，应停止并上报，不擅自覆盖。

## Initial Route

交 Coder（NORMAL）。
