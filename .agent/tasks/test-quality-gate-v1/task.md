# Task: 自动化测试与代码质量门禁（test-quality-gate-v1）

## Goal

建立自动化测试与代码质量门禁，使代码质量问题（测试失败、静态检查告警、数据竞争、Migration 版本冲突、错误码 Registry 漂移）能被 CI 自动发现，并在构建/发布前阻止不合格产物；同时保证新增门禁不破坏现有测试流程，需要真实七牛凭据的 E2E 不被误标为通过。

## Scope

- 统一并审计 `make test`、`scripts/test.sh` 与 CNB CI（`.cnb.yml`）的测试执行流程，使 PR 与 push 两个流水线均运行完整 Go 测试门禁。
- 新增 `go test -race` 门禁，覆盖进程内共享内存状态的并发逻辑（熔断器等），并在与 DB 集成测试隔离的环境中运行。
- 将 `scripts/check-registry.sh` 纳入自动化门禁，覆盖 Migration 版本冲突与错误码 Registry 漂移检查。
- 保证测试、静态检查或 Registry 校验任一失败时，CI 构建/发布阶段不执行。
- 检查并修正 CI Go 工具链版本与 `go.mod`（`go 1.23.0`）及 `Dockerfile` 的一致性。
- 保持 `make test-storage`（真实七牛 E2E）为独立入口，不并入常规门禁、不以未执行冒充通过。
- 为关键门禁提供真实命令输出与失败证据，并验证新门禁不破坏现有测试流程。

## Out of Scope

- 不修改业务代码、数据模型、状态机、公开协议、错误码语义或任何 `docs/design/*` 长期架构事实（本任务为纯 CI/脚本/测试工程，`Design Impact = NONE`）。
- 不新增错误码域、migration 或任何全局资源（`resources.reservations` 为空，不占号、不改 `.agent/registry/*`）。
- 不引入新的 CI 平台、重型依赖或改变 GitHub 镜像同步逻辑。
- 不改变 `make test-storage` 的真实七牛 E2E 语义（仅保证其独立、不冒充通过）。
- 不新增实际的 Docker 镜像构建/推送流水线（当前 CI 无此步骤，见 Assumption）；如 Owner 需要，另行立任务。
- 不合并分支、不改动其它 task 目录或他人工作。

## Milestone

Milestone: test-quality-gate-v1 自动化测试与代码质量门禁交付验收（CI 完整 Go 测试 + `go vet` + `go test -race` + Registry 校验 + 失败阻止构建/发布 + 七牛 E2E 独立不冒充通过）

## Acceptance Criteria

- [ ] AC-001（CI 完整运行测试门禁）：PR 与 push 两个流水线在各自测试阶段运行统一 `make test`（内含 `go vet ./...` + `go test -p 1 ./...` + prometheus/container/nginx 静态断言），任一命令非零退出即导致该 stage 失败、流水线非零退出。
- [ ] AC-002（race 门禁覆盖核心并发代码）：新增 `go test -race` 门禁，至少覆盖进程内共享内存状态的核心并发逻辑（`internal/logic/flashsale` 的 `circuitBreaker` 熔断器状态机等）；数据竞争被 race detector 检测到时门禁非零退出。
- [ ] AC-003（race 隔离环境）：race 测试在与其他（DB 集成）测试隔离的环境中运行——race 目标为纯内存并发逻辑，运行过程不连接、不修改与 DB 集成测试共用的 MySQL/Redis 库表；普通测试与 race 测试可安全独立执行，不产生因共享数据库竞争导致的 flaky 失败。
- [ ] AC-004（Registry 校验纳入自动化）：`scripts/check-registry.sh` 纳入自动门禁（并入 `make test` 和/或 CI 独立 stage），其检查项覆盖 Migration 版本重复冲突与错误码域重叠/漂移；检测到冲突或漂移时输出具体冲突项并以非零退出。
- [ ] AC-005（Go 版本一致且兼容）：CI 所用 Go 工具链版本与 `go.mod` 声明（`go 1.23.0`）兼容且与 `Dockerfile`（`golang:1.23-alpine`）一致；修正 CI `golang:1.24` 与镜像构建 `golang:1.23` 的不一致，使 CI 验证工具链与生产镜像构建工具链一致（统一到与 `go.mod` 一致的最小稳定版本，或在无法统一时明确记录兼容依据）。
- [ ] AC-006（七牛 E2E 独立不冒充通过）：`make test-storage` 保持独立入口、不并入 `make test`/常规 CI 门禁；缺真实七牛凭据（QINIU_* 任一为空）时非零退出并明确提示，不以「未执行」冒充通过。
- [ ] AC-007（失败阻止构建/发布）：测试、静态检查或 Registry 校验任一失败时，CI 的构建/发布阶段不执行（流水线在门禁失败处停止，`build` 产物与后续发布步骤不生成）；门禁全部通过后构建/发布才继续。
- [ ] AC-008（门禁失败证据）：为 `vet`、`test`、`race`、`check-registry` 四类关键门禁分别提供真实命令输出与失败证据（例如注入一处 migration 版本冲突或错误码漂移后 `check-registry.sh` 非零退出并定位到具体冲突项），证明门禁能捕获真实问题而非恒通过。
- [ ] AC-009（不破坏现有测试流程）：门禁调整后 `make test` 在现有测试基线（含 test-prometheus / test-container / test-nginx 静态断言与既有 DB 集成测试）上仍完整通过，未因新增 race/registry 门禁破坏或跳过既有测试。

## Relevant Context

已核实事实（2026-10-13，TaskBuilder 调查）：

- Git 基线：分支 `test/test-quality-gate-v1`，HEAD == `origin/develop` == `origin/test/test-quality-gate-v1` == commit `359b8cceeebefe7b490f783c1ad5a118afd6e31e`，working tree clean（`git status --short` 为空），任务目录尚不存在。
- `make test` = `scripts/test.sh`：依次执行 `go vet ./...` → `go test -p 1 ./...` → `test-prometheus.sh` → `test-container.sh` → `test-nginx.sh`；用 `-p 1` 串行是因为各测试包共享同一 MySQL/Redis（`my_shop` 库，compose 默认密码 `root`）并清理/写入同一批表，跨包并行会数据竞争导致 flaky。
- `make test` 目前不含 `go test -race`，也不含 `scripts/check-registry.sh`。
- `.cnb.yml` 三处：`$: vscode`（goland-dev 开发环境，非门禁）、`main: pull_request: pr-check`（`golang:1.24`，跑 `docker compose up -d` + `make test`）、`main: push: main-ci`（`golang:1.24`，跑 `make test` → `go build -trimpath -o bin/my-shop .` → `sync-to-github-mirror`）。CI 无任何 Docker 镜像 build/push 步骤。
- Go 版本不一致：`go.mod` 声明 `go 1.23.0`；`Dockerfile` 用 `golang:1.23-alpine`；`.cnb.yml` 用 `golang:1.24`。1.24 ≥ 1.23.0 技术上可编译，但 CI 验证工具链与生产镜像构建工具链不一致（可能 CI 通过而镜像构建失败，或反之）。
- `scripts/check-registry.sh` 是只读机械校验器：检查①错误码域区间重叠、②migration version 重复、③错误码 ↔ `internal/codes/codes.go` 漂移、④migration ↔ `internal/migrations/sql/*.up.sql` 漂移；发现冲突/漂移非零退出并打印具体项。当前未纳入任何自动化门禁。
- `make test-storage` = `scripts/test-storage.sh`：独立真实七牛 E2E（签发→直传 1×1 PNG→校验 final_url→删除），缺 QINIU_* 四变量之一即非零退出并提示；已不在 `make test` 内，独立存在（本任务仅需确认并维持）。
- 核心进程内并发代码：`internal/logic/flashsale/circuit_breaker.go` 的 `circuitBreaker`（`sync.Mutex` 保护的 Closed→Open→Half-Open 状态机，字段 `consecutiveFails`/`halfOpenInFlight` 等）为纯内存状态，适合 `-race` 且无 DB/Redis 依赖。秒杀库存/排队计数的并发正确性依赖 Redis（Lua 原子）+ MySQL 唯一约束兜底，属跨进程，Go race detector 无法检测，仍由既有集成测试保证，不强制纳入 race 门禁。

Assumption（合理但未经 Owner 单独确认，交 Coder 按此落实；若与 Owner 意图冲突以 Owner 为准）：

- 「阻止镜像发布」按「质量门禁失败即阻止 CI 构建/发布阶段（`build` 及后续任何发布步骤）执行」理解，通过 CNB stage 顺序执行 + 非零退出实现；CI 当前并无实际 Docker 镜像推送流水线，不新增镜像发布步骤。
- race 门禁范围 = 进程内共享内存并发逻辑（circuitBreaker 等），天然无 DB 依赖即可满足隔离要求；不把 Redis/MySQL 跨进程并发纳入 race 门禁。
- Go 版本对齐方向（统一到 1.23 还是 1.24）由 Coder 依据「与 `go.mod` 一致、CI 与镜像构建一致」原则选择，不做业务决策。

## Verification

环境：docker-compose 提供的 MySQL 8.0 与 Redis 7（供 `make test` 的 DB 集成测试）；race 门禁仅需 Go 工具链（不依赖 DB）；check-registry 仅需源码文件；CNB 流水线真实运行由 Deliverer 验证。

- AC-001 → 命令验证：本地 `make test` 通过；Deliverer 在 CNB PR/push 流水线确认测试阶段实际执行 `make test` 且失败能阻断后续。
- AC-002 → 命令验证：`go test -race ./internal/logic/flashsale/...`（或等价 race 入口）在并发状态机逻辑上通过；对并发逻辑做一次可逆扰动（如移除 `circuitBreaker` 锁）后 race detector 报出 data race 并非零退出。
- AC-003 → 环境验证：race 门禁运行时无需 MySQL/Redis 连接（不依赖 `DATABASE_DEFAULT_PASS` 等 DB 注入即可通过）；普通 `go test -p 1 ./...` 与 race 门禁可先后独立运行，互不污染。
- AC-004 → 命令验证：本地 `bash scripts/check-registry.sh` 通过；注入一处 migration 版本重复或错误码漂移后脚本非零退出并定位冲突项。
- AC-005 → 静态核验：`.cnb.yml` 的 Go 镜像版本与 `Dockerfile`、`go.mod` 对齐；CI 构建工具链 == 镜像构建工具链（或明确记录兼容依据）。
- AC-006 → 命令验证：`make test-storage` 在缺七牛凭据时非零退出并提示；`make test` 中不包含 `test-storage`。
- AC-007 → 行为验证：在 CNB 流水线中使门禁失败（如注入 registry 漂移），确认 `build` 阶段与后续步骤不执行、流水线非零退出；门禁恢复后构建/发布正常继续。
- AC-008 → 证据验证：提交真实的四类门禁命令输出（成功 + 注入失败各一）作为交付证据。
- AC-009 → 命令验证：`make test` 完整通过（含既有静态断言与 DB 集成测试），无既有测试被删除/跳过/放宽断言。
- 通用命令：`gofmt -l`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`、`go test -race <并发包>`、`bash scripts/check-registry.sh`、`bash -n scripts/*.sh`。

## Complexity

NORMAL

原因：纯 CI/脚本/测试工程，不改变数据模型、状态机、公开协议、错误码域或 migration（`Design Impact = NONE`，不占全局资源）；「审计 make test / CI」「新增 race」「纳入 check-registry」「Go 版本对齐」均有清晰可观察行为约束，无多种会产生不同业务结果的关键方案选择；race 隔离通过「race 仅针对纯内存并发逻辑」即可满足，不涉及业务并发语义变更。剩余为实现细节，无需 Analyst 建立 Contract。

## Review Baseline

- Base commit：`359b8cceeebefe7b490f783c1ad5a118afd6e31e`（分支 `test/test-quality-gate-v1`，起始 working tree clean，local == `origin/test/test-quality-gate-v1` == `origin/develop`）。
- 任务开始时已有修改：无（`git status --short` 为空；无未暂存/暂存/新增文件）。
- 重叠修改的区分方式：本任务改动面集中在 `.cnb.yml`、`Makefile`、`scripts/*.sh`（`test.sh` 及可能的 race 入口脚本）与少量 Go 测试（race/门禁证据）；不触碰业务代码、`.agent/registry/*`、其它 task 目录。本任务仅新增 `.agent/tasks/test-quality-gate-v1/*`（TaskBuilder 所有物）与上述 CI/脚本/测试改动。

## Initial Route

交 Coder（NORMAL，`contract.status = NOT_REQUIRED`）
