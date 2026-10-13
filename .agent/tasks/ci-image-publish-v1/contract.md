# Technical Contract

## Decision Status
APPROVED

## Problem
`main` 分支更新后，CI 自动完成测试、Docker 构建与镜像发布（短 SHA + `latest` 双标签同源、失败不更新 `latest`），并保留现有云开发环境、PR 检查与 Push 测试/构建/GitHub 镜像同步。改动集中在 `.cnb.yml` 与 `docs/design/deployment.md`；不改业务代码、`Dockerfile`/`docker-compose.yml`/`scripts/*` 既有构建产物与 entrypoint 语义；不新增 migration/错误码域。

## Verified Current Behavior
- VERIFIED：现有 `.cnb.yml` 含 `$`（vscode 云开发环境）、`main.pull_request`（`pr-check`：start-dependencies + `make test`）、`main.push`（`main-ci`：start-dependencies + `make test` + `go build` + `sync-to-github-mirror` 条件同步）。**无** Docker 构建/推送、**无**制品库配置。
- VERIFIED：`make test` = `scripts/test.sh`（`set -euo pipefail`；`go vet` + `go test -p 1 ./...` + 三项静态校验），任一失败非零退出 → stage/job/check 失败（PR 检查失败信号正确）。
- VERIFIED：后端 `Dockerfile` 多阶段（golang:1.23-alpine → alpine:3.20），`CGO_ENABLED=0 GOOS=linux` 静态编译，entrypoint `scripts/docker-entrypoint.sh`（`migrate up` → `exec serve`）；`.dockerignore` 排除 `.env`/`bin`/`tmp`/`storage`。
- VERIFIED：remote = `https://cnb.cool/framework-station/my-shop`，`CNB_REPO_SLUG = framework-station/my-shop`。
- VERIFIED（CNB 官方文档）：镜像地址 `${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}:<tag>`；`CNB_DOCKER_REGISTRY = docker.cnb.cool`（与 `.cnb.yml` goland 镜像引用一致）；`CNB_COMMIT_SHORT` = 完整 `CNB_COMMIT` 前 **8** 位；内置 `CNB_TOKEN_USER_NAME`（固定 `cnb`）+ `CNB_TOKEN`（运行期临时令牌，只读）；推送 CNB 制品库**无需手动 `docker login`**（流水线内置触发者凭据）。
- UNKNOWN：CNB 制品库镜像仓库是否需预先在平台创建/授权（不影响 `.cnb.yml` 设计，但影响 AC-004 首次运行结果）。
- UNKNOWN：PR「仓库保护规则 required check」是否已启用（Owner 平台侧动作，非本任务可改）。

## Recommendation
RECOMMENDATION：在现有 `main.push` 的 `main-ci` job 内，以「单 job 顺序 stage」扩展镜像构建与发布；不改 `$`、`pull_request`，不新增 top-level 触发分支。

关键取舍：单 job 顺序 stage 依赖「任一 stage 失败则后续 stage 不执行」的平台语义即可满足 AC-007，无需多 job 隔离或独立收尾 step；代价是 build/push 与 go test 共用一个 job 失败信号（任一失败整个 push job 变红），与现状一致。

镜像与标签：
- 镜像：`${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}`（= `docker.cnb.cool/framework-station/my-shop`，无子路径；fork 后自动指向 fork 自身 slug，不污染上游）。
- 标签：短 SHA `${CNB_COMMIT_SHORT}`（8 位，平台内置）+ `latest`。
- 平台：单平台 linux/amd64（native build，不显式 `--platform`、不引入 buildx/QEMU，符合 Out of Scope）。

阶段顺序（`main-ci` 内）：`start-dependencies` → `test`（make test）→ `build`（go build，保留）→ `docker-build`（`docker build -t <repo>:<sha>` + `docker tag <repo>:latest`）→ `docker-push`（先 `docker push <repo>:<sha>`，再 `docker push <repo>:latest`）→ `sync-to-github-mirror`（保留，条件不变）。

## Selected Design
Owner 已确认采用推荐方案（见 Owner Decision Record）：

- 短 SHA 标签：`${CNB_COMMIT_SHORT}`（8 位，平台内置只读变量，不在流水线内跑 `git rev-parse`）。
- 流水线编排：单 job 顺序 stage（在 `main.push` 的 `main-ci` 内扩展，任一 stage 失败即中断）。
- 镜像目标平台：单平台 linux/amd64（native build，不显式 `--platform`、不引入 buildx/QEMU）。
- 镜像仓库命名：`${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}` = `docker.cnb.cool/framework-station/my-shop`（无子路径）。

阶段顺序（`main-ci` 内）：
`start-dependencies` → `test`（make test）→ `build`（go build，保留）→ `docker-build`（`docker build -t <repo>:${CNB_COMMIT_SHORT}` + `docker tag <repo>:latest`）→ `docker-push`（先 `docker push <repo>:${CNB_COMMIT_SHORT}`，再 `docker push <repo>:latest`）→ `sync-to-github-mirror`（保留，条件不变）。

## Interfaces and Data
- 保留：`$`、`main.pull_request`（pr-check）、`main.push` 的 `start-dependencies`/`test`/`build`/`sync-to-github-mirror` 既有 job 与关键步骤不变。
- 新增：`main.push` main-ci 的 `docker-build`、`docker-push` 两个 stage。
- 镜像名与标签：`docker.cnb.cool/framework-station/my-shop:<CNB_COMMIT_SHORT>` 与 `:latest`（同 digest）。
- 凭据：仅依赖 CNB 内置凭据（自动注入 / `CNB_TOKEN`），`.cnb.yml` 不出现任何密码明文。

## Business Invariants
- INV-001（失败不发布）：test 或 docker build 任一失败时，不推送任何镜像标签（`<sha>` 与 `latest` 均不 push），`latest` 保持指向上一成功构建。
- INV-002（双标签同源）：`<sha>` 与 `latest` 由同一次构建、同一镜像经 `docker tag` 派生，二者 digest 相同。
- INV-003（凭据安全）：制品库认证仅用 CNB 内置凭据机制，`.cnb.yml` 与运行日志无硬编码密码。

## Failure and Consistency Semantics
- 成功（main-ci job 绿）= test + go build + docker build + 双标签 push 全部成功；`latest` 与 `<sha>` 指向本次新 digest。
- test / go build / docker build 失败 → job 在对应 stage 失败，后续不执行，不推送，`latest` 不变。
- `<sha>` push 失败 → job 失败，`latest` 不变（短 SHA 也未推）。
- `<sha>` push 成功但 `latest` push 失败 → job 失败，`<sha>` 已推但 `latest` 不变（部分完成，可接受：`latest` 始终指向最近一次「全流程成功」构建，下次成功构建会更新 `latest`）。
- 重试/重复推送同 commit：`<sha>` 标签值确定（同 commit 同短 SHA），`docker push` 幂等（同 digest no-op）。
- CNB 制品库为镜像事实源；本任务不引入其他存储/异步系统。

## Allowed / Forbidden Changes
- 允许：修改 `.cnb.yml` 的 `main.push`（新增 docker build/push stage）与 `docs/design/deployment.md`。
- 禁止：删除或改写 `$`/`pull_request`/现有 push 的 `start-dependencies`/`test`/`build`/`sync-to-github-mirror`；改业务代码、`Dockerfile`/`docker-compose.yml`/`scripts/*` 既有构建产物与 entrypoint 语义；新增 migration/错误码域；硬编码制品库密码。

## Verification Requirements
- INV-001 → 构造 test 或 build 失败的提交，观察 push job 失败、制品库无新 `<sha>` 标签、`latest` digest 未变（AC-007）。
- INV-002 → `docker pull <repo>:<sha>` 与 `:latest` 比对 `RepoDigests` 一致（AC-006）。
- INV-003 → 审查 `.cnb.yml` 与运行日志，无明文密码，确认使用 CNB 内置凭据（AC-008）。
- AC-004 → `main` push 成功后制品库可查到 `<sha>` 标签镜像。
- AC-009 → 用 `<sha>` 标签拉取并运行，容器 `/health` 正常（Deliverer 集成验证）。

## Open Risks
- CNB 制品库镜像仓库是否需要预创建/授权；若首次 push 因仓库不存在失败，需 Owner 在平台创建（环境动作，不改设计）。
- PR 仓库保护规则（required check）是否启用由 Owner 平台侧决定，未启用则 AC-002 的「禁止合并」标 `NOT_VERIFIED`。
- CNB 运行器默认架构（amd64）为推断；若实际为 arm64 需在 `docker build --platform` 显式声明（低概率）。

## Owner Decision Record
- 2026-10-13：Owner 确认采用 Analyst 推荐方案，四项均选 A——短 SHA 标签用 8 位 `CNB_COMMIT_SHORT`；流水线用单 job 顺序 stage；镜像目标平台单平台 linux/amd64；制品库命名无子路径 `docker.cnb.cool/framework-station/my-shop`。同时明确实现边界：不改 Dockerfile 无关内容、不引入多架构、不硬编码 CNB Token/仓库凭据，保证测试失败不发布镜像、构建失败不更新 `latest`、短 SHA 标签与 `latest` 指向同一镜像 digest。
