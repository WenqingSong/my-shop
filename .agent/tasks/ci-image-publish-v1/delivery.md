# Delivery Verification

## Milestone and Target

- Milestone：ci-image-publish-v1（`main` 分支更新后 CI 自动测试、Docker 构建与镜像发布）
- Delivery Target：`.cnb.yml`（`main.push.main-ci` 新增 docker-build / docker-push 两个 stage）+ `docs/design/deployment.md`（§11 镜像发布、版本标识与回滚）
- Cleaner Review Target：`6f1e2a660334ab46ea2c5bf10ba89fd698679b13`（state 中 `review.target=6f1e2a6`）
- Target Match：YES —— `review.target..feature_head` 的生产文件（`.cnb.yml`、`docs/design/deployment.md`）diff 为空，生产代码在 CLEAN 后未发生实质变化。

## Environment

- OS：Debian GNU/Linux 12（bookworm），linux/amd64（x86_64）
- Go：go1.24.1 linux/amd64
- Docker：Docker 29.6.2-7（server），Docker Compose v5.3.1
- 运行环境：CNB 云开发环境（`.cnb.yml` 的 `$`/vscode 环境，DinD）；本环境 `CNB_REPO_SLUG=zsy-deve/zsy-dev`（fork slug，非目标 `framework-station/my-shop`）
- 依赖：mysql:8.0、redis:7-alpine（`docker compose up -d mysql redis` 拉起，验证后已 `docker compose down` 清理）
- 测试数据与清理：本地临时构建镜像 `my-shop-deliverer-test:<sha>`（验证后已 `docker rmi` 删除）；未触碰任何 CNB 制品库、未推送任何镜像
- 凭据来源：仅读取 CNB 内置环境变量（`CNB_DOCKER_REGISTRY` / `CNB_REPO_SLUG_LOWERCASE` / `CNB_COMMIT_SHORT`）；未记录、未使用任何 Secret 值

## Verification

| Check | Result | Evidence |
|---|---|---|
| delivery-start Gate | PASS | `.agent/bin/workflow-check gate delivery-start .agent/tasks/ci-image-publish-v1` → exit 0 |
| `.cnb.yml` YAML 合法性 | PASS | `gopkg.in/yaml.v3` 解析成功 |
| 凭据扫描（无硬编码） | PASS | 无 `docker login` / `password:` / `token:` 明文；仅命中注释「无需手动 docker login」 |
| build stage（`go build`） | PASS | `go build -trimpath -o bin/my-shop .` 成功 |
| test stage（`make test`） | FAIL（pre-existing） | exit 126：`scripts/test-nginx.sh: Permission denied`（该文件 git mode 100644，基线 `359b8cc` 即如此） |
| `go vet ./...` | PASS | 通过 |
| docker-build stage（`docker build`） | PASS | 多阶段构建成功（golang:1.23-alpine → alpine:3.20） |
| 双标签同源（INV-002，本地） | PASS | `docker tag` 后 `<sha>` 与 `latest` 的 ImageId 均为 `sha256:5a46c615...` |
| 镜像可运行（bootstrap） | PASS | 容器启动 → `migrate up` → 依赖连通(mysql/redis) → schema 就绪 → 超管/权限 seed 均成功；仅在七牛 `ValidateConfig` 无凭据 fail-fast |
| AC-002 / AC-003（PR / main push 触发与顺序） | NOT_EXECUTED | 需真实 CNB 流水线（feature 未并入 `main`，无法触发） |
| AC-004 / AC-005 / AC-006 / AC-007（制品库 push / 双标签 / digest / 失败不更新 latest） | NOT_EXECUTED | 需 `main` push + 授权制品库 `docker.cnb.cool/framework-station/my-shop` |
| AC-009（制品库 SHA 标签 pull + run + `/health`） | NOT_EXECUTED | 制品库 pull 需授权；本地构建+运行已 PASS（见上） |

## Acceptance Evidence

- AC-001（保留现有流水线）：静态比对 PASS —— `$`/`main.pull_request.pr-check`/`main.push.main-ci` 的 start-dependencies / test / build / sync-to-github-mirror 均保留未删改，仅新增 docker-build / docker-push（本 Deliverer 复核 `git diff base..HEAD`）。
- AC-008（凭据安全）：PASS —— `.cnb.yml` 无明文密码，仅用 CNB 内置变量与内置触发者凭据。
- AC-009 本地部分：镜像可构建、可启动、bootstrap 完成（migrate + 依赖 + schema + seed）；`/health` 端到端需七牛凭据，未验证。
- INV-002（双标签同源）：本地 `docker tag` 同 digest 已验证 PASS；制品库侧 digest 一致性 NOT_EXECUTED。
- INV-003（凭据安全）：`.cnb.yml` 无 `docker login`、无明文密码，PASS。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 真实 CNB 流水线（PR 检查 / main push / 制品库 push） | 需 feature 并入 `main` + 真实 push + 授权制品库；Deliverer 禁止 push develop/main、禁止部署 | 流水线运行时行为（镜像是否真能推入 CNB 制品库、PR 失败信号、失败不更新 latest）无真实证据 |
| 制品库双标签 digest 一致性（AC-006） | 需真实制品库 push 后 `docker manifest inspect` | 本地已证 `docker tag` 同源，制品库侧未验证 |
| 失败不更新 `latest`（AC-007） | 需在 `main` 上构造失败提交 | 顺序 stage「任一失败即中断」静态成立，运行时未验证 |
| `/health` 端到端（AC-009） | 无七牛 AK/SK/bucket/domain | serve 阶段 fail-fast，`/health` 未验证 |

## Remaining Risks

- 前置阻塞（pre-existing，非本任务引入，`OUT_OF_SCOPE_EXISTING_ISSUE`）：`make test` 在 `scripts/test-nginx.sh`（git mode 100644，基线即如此）处 `Permission denied`（exit 126）失败，会导致 PR 检查与 main-ci 的 test stage 变红，后续 docker-build / docker-push 永不执行，镜像发布无法达成。需 Owner 决定是否另行修复。
- CNB 制品库 `framework-station/my-shop` 是否需预创建/授权（Contract Open Risks），首次 push 可能因仓库不存在失败。
- 本环境 `CNB_REPO_SLUG=zsy-deve/zsy-dev`（fork），非目标 slug；制品库侧验证需在 `framework-station/my-shop` 上下文进行。

## Result

BLOCKED
