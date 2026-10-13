# Task: 容器运行时加固（非 root + 存活/就绪/优雅退出）

## Goal

让 Go 服务（`my-shop`）具备 Kubernetes 所需的容器运行能力：镜像以非 root 用户运行，具备明确的存活（liveness）、就绪（readiness）与优雅退出（graceful shutdown）语义，且必要目录在非 root 下可正常读写、镜像不含多余构建文件或敏感信息。

## Scope

- 审计 `Dockerfile` 的运行用户、目录权限与文件依赖，将运行时容器改为非 root 用户运行。
- 确保 `/app/storage`（及 `banners` 子目录等运行时写入目录）在非 root 下可正常读写，轮播图占位图 seed 与静态资源访问不受影响。
- 审计镜像内容，确认 runtime 层不含构建工具链、源码、`.env`、真实凭据等多余或敏感文件。
- 保留现有 `/health`，明确其为 liveness 语义（进程存活即 ok，不依赖 DB/Redis）。
- 新增独立 `/ready` 接口作为 readiness：定义 DB、Redis 故障时的判定方式与超时策略，避免依赖检查对 DB/Redis 引发额外负载。
- 完善 HTTP Server 优雅关闭配置；将 SIGTERM/SIGINT 与后台扫描器（订单超时取消、秒杀对账、秒杀消费）及异步消费者的生命周期关联。
- 停机时停止领取新任务，允许在限定时间内完成已有工作或安全回滚。
- 检查秒杀消费者中断后能否通过重试与对账恢复（验证为主，必要时补测试或小修）。
- 增加健康探针与优雅退出相关测试。
- 验证非 root 镜像在 Docker 环境中正常运行。
- 更新项目级部署模型设计 `docs/design/deployment.md`（Design Impact=UPDATE 的交付物）。

## Out of Scope

- 实际 Kubernetes 编排清单（Deployment/Pod/StatefulSet/Probe YAML）：本次只让容器具备 K8s 所需的运行能力，不产出 K8s 部署文件。
- 多实例负载均衡、HTTPS/TLS、Ingress（`deployment.md` §11 已声明 deferred，非本次范围）。
- 改变秒杀/订单等业务规则、公开业务 API、错误码语义或数据模型；秒杀消费者仅做「中断恢复」验证与必要加固，不重写其消费/对账机制。
- 引入 MQ：当前秒杀异步下单为「MySQL 出队表 + 扫描器」模型（无 MQ），本次不引入。

## Milestone

Milestone: container-runtime-hardening-v1 容器运行时加固交付验收（非 root 运行 + 存活/就绪/优雅退出语义 + Docker 环境验证）

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/deployment.md

本任务修改既有项目级长期事实：将 `deployment.md` 当前记录的「`/health` 为 liveness、readiness 独立端点 deferred、单实例无摘流量需求」部署模型，更新为「非 root 运行 + `/health` liveness + `/ready` readiness + SIGTERM/SIGINT 驱动的优雅退出与后台扫描器生命周期」。该 Artifact 已纳入 Scope 与 AC-012。

## Acceptance Criteria

- [ ] AC-001：容器以非 root 用户运行。给定构建完成的镜像并启动容器，容器内主进程（`my-shop serve`）的有效 UID 非 0（root）。
- [ ] AC-002：非 root 下必要目录可写。给定非 root 运行的容器启动完成后，`/app/storage/banners` 等运行时写入目录存在且可写，轮播图占位图 seed 成功，`/storage/banners/*` 静态资源可正常访问。
- [ ] AC-003：`/health` 保持 liveness 语义。给定进程存活且 HTTP server 在监听，`/health` 返回 200 且 `status=ok`，其判定不依赖 DB/Redis 可用性。
- [ ] AC-004：新增 `/ready` 作为 readiness。给定 DB 与 Redis 均可达且 schema 就绪，`/ready` 返回 200（就绪）；给定 DB 或 Redis 不可达，`/ready` 返回非 200（未就绪），且响应/日志不泄漏数据库、Redis、路径等敏感信息。
- [ ] AC-005：`/ready` 的依赖探测不引发额外负载。DB/Redis 探测具备超时与结果缓存策略，使高频探针调用不会对 DB/Redis 造成每探一次实质打库/打缓存的额外压力。
- [ ] AC-006：HTTP Server 优雅关闭。给定 SIGTERM 或 SIGINT，HTTP server 停止接收新连接，在限定（可配置）时间内等待在途请求完成，超时后按既定策略退出；进程以可控方式结束。
- [ ] AC-007：后台扫描器/消费者生命周期关联。停机时后台扫描器（订单超时取消、秒杀对账、秒杀消费）停止领取新任务，在途批次在限定时间内完成或安全回滚，不产生半成品或不一致状态。
- [ ] AC-008：秒杀消费者中断恢复。秒杀消费在提交前崩溃或优雅停机打断后，能通过既有「事务回滚 + 请求保持 queued + 重试/死信 + 缓存对账」机制恢复到一致状态，不产生重复下单或遗留半成品。
- [ ] AC-009：健康探针与优雅退出测试。为 `/health`、`/ready`、优雅退出、后台扫描器停机新增测试，且测试能区分正确与错误实现（而非仅断言 HTTP 200 或调用次数）。
- [ ] AC-010：非 root 镜像 Docker 环境验证。构建镜像并在 Docker 环境真实运行，验证非 root、`/health`、`/ready`、优雅退出全链路（含 DB/Redis 依赖）。
- [ ] AC-011：镜像内容审计。runtime 镜像层不含 Go 工具链、源码、`.env`、真实 AK/SK/JWT secret/密码等，审计结论与验证方式记录在案。
- [ ] AC-012：部署模型文档更新。`docs/design/deployment.md` 更新为准确记录非 root 运行、`/health`(liveness)/`/ready`(readiness) 语义、优雅退出与后台扫描器停机生命周期，并保持与实现一致。

## Relevant Context

- 已核实事实：
  - `Dockerfile` 为多阶段构建（builder `golang:1.23-alpine` → runtime `alpine:3.20`），`WORKDIR /app`，`CGO_ENABLED=0` 静态编译，`apk add tzdata ca-certificates`，`RUN mkdir -p /app/storage`，无 `USER` 指令 → 当前容器以 root 运行。
  - `.dockerignore` 已排除 `.env`、`.env.*`、`bin/`、`tmp/`、`storage/`、`docs/`、`.agent/`、`*.md`、`*_test.go`、前端目录、`.git/` 等；`scripts/test-container.sh` 有静态断言覆盖部分镜像/编排约束。
  - 现有 `/health` 仅返回 `status=ok` + 时间，无依赖探测（`api/health/v1/health.go`、`internal/logic/health/health.go`）；无 `/ready` 端点。
  - `docs/design/deployment.md` §6 明确「`/health` 为 liveness；readiness 独立端点 deferred（单实例、无摘流量需求）」、§11 将 readiness 独立端点列为 deferred —— 本任务反转该结论。
  - 生产 `serve`（`internal/cmd/cmd.go`）启动 3 个后台扫描器（`startOrderCancelScanner`、`startFlashSaleReconcileScanner`、`startFlashSaleConsumeScanner`），goroutine 使用 `gctx` 派生的、不会被信号取消的 ctx；`serve` 未显式监听 SIGTERM/SIGINT 或协调优雅关闭，仅调用 GoFrame `s.Run()`。
  - `docker-compose.yml`：app 服务 `STORAGE_LOCAL_ROOT=/app/storage`，`app_storage` named volume 挂载 `/app/storage`，healthcheck 用 `wget /health`；app 不对外暴露宿主机端口，Nginx 为唯一入口。
  - `boot.checkDependencies` 用 MySQL `PingMaster` + Redis `PING` 做启动连通校验，`waitForDependencies` 以 `STARTUP_DEPENDENCY_TIMEOUT`（默认 30s）重试；此逻辑仅用于启动，不是运行时 readiness。
  - 秒杀消费者为「MySQL 出队表 + 扫描器」模型（`internal/logic/flashsale/consume.go`），`FOR UPDATE SKIP LOCKED` 互斥领取、单事务落单、技术失败指数退避重试（上限 3 次 → 死信 dead）、事务提交后 Redis 补偿；`ReconcileCache` 对账收敛 `remaining`。中断恢复能力已有设计基础（`recovery_test.go`、`consume_test.go` 存在）。
  - 技术栈：Go 1.23、GoFrame `gf/v2 v2.10.3`、`contrib/mysql`、`contrib/redis`。

- Assumption：
  - 「让服务具备 K8s 所需容器运行能力」本次落点为容器/应用级（非 root、探针、优雅退出），不含 K8s 编排清单（见 Out of Scope）。
  - `/app/storage` 的可写性保障方式（镜像内 chown vs named volume 权限约定 vs 其他）属实现选择，需在非 root 下实际可写即可，具体方案由 Analyst 定。

- OPEN QUESTION：
  - 无（以下策略性选择已列入 Analyst Questions，不阻塞任务定义）。

## Verification

- AC-001 → 构建镜像并启动容器，`docker exec` 查看主进程 `id -u` 或 `/proc/<pid>/status`，确认 euid 非 0；静态断言 `Dockerfile` 含 `USER` 非 root 指令。
- AC-002 → 容器启动后检查 `/app/storage/banners` 目录 owner 为运行用户且可写，`GET /storage/banners/banner-1.png` 返回 200。
- AC-003 → 在 DB/Redis 不可达情况下（如停止 mysql/redis 容器）`/health` 仍返回 200 + `status=ok`；需真实依赖或等价隔离环境验证。
- AC-004 → DB+Redis 正常时 `/ready` 返回 200；分别停止 mysql、redis 后 `/ready` 返回非 200（503），响应体不含凭据/路径/堆栈。
- AC-005 → 通过测试或日志观察证明：结果缓存窗口内重复 `/ready` 不产生新的 DB/Redis 探测；探测超时受控。
- AC-006 → 发慢请求 + 发送 SIGTERM，观察新连接被拒、在途请求完成、进程在超时内退出；退出码与日志符合预期。
- AC-007 → 停机后确认后台扫描器 goroutine 退出，无新出队/取消动作；在途批次完成或回滚，无半成品。
- AC-008 → 运行既有 `recovery_test.go`/`consume_test.go` 并新增中断场景测试：提交前崩溃/停机打断后，重启消费能恢复、不重复下单、对账收敛 Redis。
- AC-009 → `go test ./...`（业务）跑通新增探针/优雅退出/停机测试；必要时对并发路径跑 `go test -race`。
- AC-010 → 真实 Docker 环境 `docker compose up` 后验证非 root、`/health`、`/ready`、`docker stop`（SIGTERM）优雅退出全链路（需 MySQL/Redis/七牛真实依赖）。
- AC-011 → `docker history` / 解包 runtime 镜像层，确认无 go 工具链、源码、`.env`、真实凭据；沿用/扩展 `scripts/test-container.sh` 静态断言。
- AC-012 → 核对 `docs/design/deployment.md` 与实现一致（非 root、`/health`/`/ready` 语义、优雅退出、扫描器停机生命周期）。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`（业务）；引擎测试与业务测试分开运行。

## Complexity

COMPLEX

原因：本任务涉及多个会产生不同业务/可靠性/运维结果的方案选择——readiness 的依赖判定与探测负载策略、SIGTERM/SIGINT 驱动的优雅关闭协调模型（HTTP 停收新连接 + 在途请求 drain + 后台扫描器停止领取/完成或回滚）、非 root 用户与 named volume 权限的落地方式、以及优雅停机对 in-flight 消费事务的影响。这些属于架构/生命周期/并发一致性选择，应先由 Analyst 调查并形成 Contract，不直接交 Coder。

## Analyst Questions

1. readiness 判定语义与探测策略：DB/Redis 故障如何判定（主动 ping vs 被动信号）、超时与结果缓存时长如何设定以「避免每探一次都打库/打缓存」、故障时 fail-closed（摘流量）还是 fail-open，以及启动阶段与运行阶段的 readiness 状态如何衔接。
2. 优雅关闭协调模型：SIGTERM/SIGINT → ctx 取消的接线方式；HTTP server 停收新连接 + 在途请求 drain 的超时预算；三个后台扫描器「停止领取新任务 + 在途批次完成或回滚」的时间预算与强退行为；优雅停机超时后未完成任务如何安全处理（是否依赖现有幂等/重试/对账兜底）。
3. 秒杀消费者中断恢复：确认现有「事务回滚 + 保持 queued + 重试/死信 + 缓存对账」是否已完整覆盖「消费中断后恢复」，优雅停机对 in-flight 消费事务的影响，以及是否需要补测试或小修。
4. 非 root 用户与目录权限：选择哪个 UID/GID（如 alpine `nobody` 或专用 uid），以及 `/app/storage` named volume 在非 root 下的可写性保障方式（镜像内 chown vs 卷权限约定 vs 其他），需保证 AC-002 在真实 Docker 环境成立。
5. 探针语义在 Compose 与未来 K8s 的一致性：现有 Compose healthcheck 是否需同步改为区分 liveness（`/health`）与 readiness（`/ready`），以及 `docs/design/deployment.md` 中与本次结论冲突的表述的更新边界。

## Review Baseline

- Base commit：`359b8cceeebefe7b490f783c1ad5a118afd6e31e`（分支 `chore/container-runtime-hardening-v1`）。
- 任务开始时已有修改：无。`git status --short` 为空，working tree clean，无暂存/未暂存/未跟踪改动。
- 重叠修改的区分方式：无既有未提交改动需要区分；本任务改动集中在 `Dockerfile`、`internal/cmd`（serve/扫描器生命周期）、health 相关 controller/logic/api、`manifest/config/config.yaml`、`docker-compose.yml`、`docs/design/deployment.md` 及对应测试。后续以本 Base commit 为界，`git diff <base>..HEAD` 即本任务变更。

## Initial Route

交 Analyst（COMPLEX）
