# Task: CI 镜像发布 V1 — CNB 自动测试、构建与制品发布

## Goal

`main` 分支更新后，CI 自动完成测试、Docker 构建与镜像发布，产出可追溯（短 SHA 标签）、可拉取、可运行的镜像；同时保留现有云开发环境、PR 检查与 Push 测试/构建/GitHub 镜像同步等流水线功能，且在测试或构建失败时不更新 `latest`。

## Scope

- 在现有 `.cnb.yml` 上扩展（保留 `$` 云开发环境、`main.pull_request` PR 检查、`main.push` 的测试/构建/GitHub 镜像同步），新增：PR 自动测试并与仓库保护规则对齐（测试失败禁止合并）；`main` Push 后自动运行测试 + Docker 构建；构建成功后推送后端镜像到 CNB 制品库；为同一次成功构建同时生成短 SHA 与 `latest` 标签；保证两标签同源、失败不更新 `latest`；使用 CNB 提供的安全凭据机制完成制品库认证，禁止硬编码密码。
- 更新 `docs/design/deployment.md`，记录镜像仓库地址、版本标识（短 SHA + `latest` 语义）与回滚方法。
- 验证推送后的镜像可通过指定短 SHA 标签拉取并运行（容器可启动、`/health` 正常）。

## Out of Scope

- 不接入生产部署（不自动部署到生产环境、Kubernetes/Ingress）。
- 不改动业务代码、`Dockerfile`/`docker-compose.yml`/`scripts/*` 的既有构建产物与 entrypoint 语义。
- 不新增数据库 migration 与错误码域。
- 不引入多实例部署、镜像签名/漏洞扫描等额外制品治理（除非 CNB 机制必需）。

## Milestone

Milestone: ci-image-publish-v1

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/deployment.md

## Acceptance Criteria

- [ ] AC-001：现有流水线功能保留——云开发环境（`$`/vscode）、`main.pull_request` PR 检查、`main.push` 的测试、构建与 GitHub 镜像同步在改造后仍存在且行为不变。
- [ ] AC-002：PR 自动测试——对 `main` 发起 PR 时自动运行测试；测试失败时 PR 检查状态为失败，并能被仓库保护规则消费为「不允许合并」（保护规则的平台侧启用为 Owner 动作，本任务保证 PR 测试 job 存在且失败信号正确）。
- [ ] AC-003：`main` Push 自动测试 + 构建——`main` 分支 Push 后自动运行测试（`make test`）；仅测试通过后才执行 Docker 构建，产出后端镜像。
- [ ] AC-004：镜像推送——构建成功后，镜像自动推送到 CNB 制品库。
- [ ] AC-005：双标签——同一次成功构建的镜像同时打短 SHA 标签与 `latest` 标签。
- [ ] AC-006：双标签同源——短 SHA 标签与 `latest` 标签指向同一镜像 digest（同一次成功构建的同一产物）。
- [ ] AC-007：失败不更新 `latest`——测试或构建任一失败时，不推送任何标签，尤其不更新 `latest`（`latest` 保持指向上一次成功构建）。
- [ ] AC-008：凭据安全——使用 CNB 提供的安全凭据机制完成制品库认证；流水线配置与运行日志中不出现硬编码的制品库密码。
- [ ] AC-009：SHA 标签可拉取可运行——推送完成后，能通过指定短 SHA 标签拉取镜像并运行，容器 `/health` 正常（或可完成 bootstrap 启动）。
- [ ] AC-010：文档落地——`docs/design/deployment.md` 记录镜像仓库地址、版本标识（短 SHA + `latest` 语义）与回滚方法（用 SHA 标签回滚到历史成功构建）。

## Relevant Context

已核实事实：

- 仓库 remote = `https://cnb.cool/framework-station/my-shop`；CNB 镜像域名 `docker.cnb.cool`（`.cnb.yml` 中 goland 镜像引用可见）。
- 当前 `.cnb.yml`：`$`（vscode 云开发环境）、`main.pull_request`（pr-check：start-dependencies + `make test`）、`main.push`（main-ci：start-dependencies + `make test` + `go build` + sync-to-github-mirror 条件同步）。**无** Docker 构建/推送、**无**制品库配置、**无**镜像标签。
- 后端 `Dockerfile` 为多阶段构建（golang:1.23-alpine → alpine:3.20），入口 `scripts/docker-entrypoint.sh`（`migrate up` → `exec serve`）；`.dockerignore` 已排除 `.env`/`bin`/`tmp`/`storage`。
- `make test` = `scripts/test.sh`（`go vet` + `go test`）。
- `docs/design/deployment.md` 已记录容器化部署模型（`backend-container-v1` + `nginx-reverse-proxy-v1`），但未涉及镜像发布、制品库、标签与回滚。
- Git 分支：CNB 默认分支为 `main`（`origin/HEAD -> origin/main`）；Workflow 集成分支为 `develop`；本任务在 `feat/ci-image-publish-v1`（当前 == `develop` HEAD）。
- 仓库内无硬编码制品库密码（现状安全良好）。

Assumption：

- CNB 制品库镜像地址格式与内置安全凭据机制的具体形态以平台实际能力为准，需在真实 CNB 环境核实（交由 Analyst）。
- 短 SHA 标签默认采用 `git rev-parse --short`（7 位）语义，最终格式由 Analyst/Owner 决定。

## Verification

- AC-001 → 比对改造前后 `.cnb.yml`，确认 `$`、`pull_request`、`push` 原有 job 与关键步骤未被删除/破坏，`sync-to-github-mirror` 的条件判断仍保留。
- AC-002 → 在 CNB 上发起一次含失败测试的 PR，观察 PR 检查为失败；仓库保护规则（required check）是否启用需 Owner 在平台确认（未启用则该项标 `NOT_VERIFIED` 并说明）。
- AC-003 → 向 `main` 推一次提交，观察 CI 依次执行 test → docker build，且 test 失败时 build 不执行。
- AC-004 → `main` push 成功后，在 CNB 制品库能查到新推送的镜像。
- AC-005 / AC-006 → 用镜像 digest 比对：`docker pull <repo>:<short-sha>` 与 `<repo>:latest` 的 `RepoDigests` 一致。
- AC-007 → 构造测试失败或构建失败的提交，确认 `latest` 未变化（仍指向旧 digest），制品库未新增 SHA 标签。
- AC-008 → 审查 `.cnb.yml` 与运行日志，无明文密码；确认使用 CNB 内置凭据/密钥机制。
- AC-009 → `docker pull <repo>:<short-sha>` 后 `docker run` 启动容器，`/health` 返回 ok（需真实 CNB 制品库与可运行环境，由 Deliverer 集成验证）。
- AC-010 → 人工核对 `docs/design/deployment.md` 与最终实现一致。
- 通用检查 → `make test` 通过（pipeline 自身测试）；本任务以 YAML/文档改动为主，不触碰 Go 业务代码。

## Complexity

COMPLEX

原因：本任务为 Design Impact = UPDATE（`docs/design/deployment.md` 需新增镜像发布/制品库/标签/回滚模型），Analyst 需草拟并落长期设计；「失败不更新 `latest`」与「双标签同源」存在多种现实流水线编排方案（单 stage 顺序执行 vs 多 stage 隔离），会产生不同可靠性/运维结果；短 SHA 标签格式与制品库仓库命名存在选择；且涉及制品库凭据这一安全边界，须采用 CNB 安全凭据机制、禁止硬编码；CNB 制品库具体能力（镜像地址格式、内置凭据注入、是否需预建仓库）需在真实环境核实。

## Analyst Questions

- CNB 制品库的镜像地址格式与仓库命名规范（`docker.cnb.cool/<org>/<repo>` 或其它；是否需要子路径如 `my-shop/app`），以及是否需要预先在平台创建/授权镜像仓库。
- CNB 提供的内置安全凭据机制具体形态（`docker login` 是否需显式执行；内置环境变量如 `CNB_TOKEN`/`CNB_REGISTRY`/`CNB_REPO_SLUG` 的可用性），如何在不硬编码密码的前提下完成制品库认证。
- 「失败不更新 `latest`」与「双标签同源」的流水线保证方式：单 stage 顺序执行（test → build → push 两标签，任一步失败即整体失败、不推送）是否足以满足，还是需要更细的阶段隔离；`latest` 更新是否应作为独立收尾步骤。
- 短 SHA 标签格式约定（`git rev-parse --short` 默认 7 位 vs 完整 SHA vs 平台约定）。
- 镜像目标平台（单平台 `linux/amd64` vs 多架构）——当前 `Dockerfile` 为 `GOOS=linux` 静态编译，是否需显式声明平台。

## Review Baseline

- Base commit：`359b8cceeebefe7b490f783c1ad5a118afd6e31e`（`develop` 头部；`feat/ci-image-publish-v1` 与 `develop` 同点，local == origin）。
- 任务开始时已有修改：无（working tree clean，无 staged / unstaged / untracked 变更）。
- 重叠修改的区分方式：本任务改动集中在 `.cnb.yml` 与 `docs/design/deployment.md`；不改动 `internal/` 业务代码，不改动 `Dockerfile`/`docker-compose.yml`/`scripts/*` 的既有行为（除为镜像发布必需的最小接入外）。

## Initial Route

交 Analyst（COMPLEX）。
