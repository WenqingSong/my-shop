# Technical Contract

## Decision Status
APPROVED

## Problem

让 `my-shop` 后端具备 K8s 所需容器运行能力：镜像以非 root 运行、明确 liveness/readiness 语义、SIGTERM/SIGINT 驱动的优雅退出并与三个后台扫描器生命周期关联、非 root 下运行时目录可写、镜像不含构建工具/源码/敏感信息。不产出 K8s 编排清单（Out of Scope）。

## Verified Current Behavior

- VERIFIED：`Dockerfile` 为多阶段构建（builder → `alpine:3.20`），无 `USER` 指令 → 当前容器以 root 运行；`RUN mkdir -p /app/storage` 目录属 root。
- VERIFIED：`/health` 仅返回 `{code:0, message:"OK", data:{status:"ok", time}}`（经 `middleware.Response` 统一包装），无任何依赖探测；`api/health/v1/health.go`、`internal/logic/health/health.go`、`internal/controller/health/health.go`。无 `/ready` 端点。
- VERIFIED：`gctx.GetInitCtx()` 返回的是 `otel.Extract(context.Background(), …)`，**不含信号取消**（`gf/v2@v2.10.3/os/gctx/gctx.go`）。`main` → `cmd.Main.Run(gctx.GetInitCtx())` → `serve(ctx)`，三个扫描器（`startOrderCancelScanner`/`startFlashSaleReconcileScanner`/`startFlashSaleConsumeScanner`）复用的就是该 ctx → 停机时永不 `ctx.Done()`。
- VERIFIED：GoFrame `s.Run()` 已内置信号处理（`gproc.shutdownSignalMap` 覆盖 SIGINT/SIGQUIT/SIGKILL/SIGTERM/SIGABRT），收到信号后 `shutdownWebServersGracefully` → `s.Shutdown()` → 各 `graceful.Server.Shutdown()` 优雅 drain 在途 HTTP 连接后返回。即 HTTP 优雅关闭「已具备」，缺的是扫描器 ctx 接线与 drain 协调。
- VERIFIED：HTTP drain 超时实际由 `server.gracefulTimeout`（默认 2s）驱动——`newGracefulServer` 将 `s.config.GracefulTimeout` 映射为 graceful server 的 `GracefulShutdownTimeout`（`ghttp_server_graceful.go`），`graceful.Server.Shutdown()` 用它做 `context.WithTimeout`。`server.gracefulShutdownTimeout`（默认 5s）当前未接线到该路径。
- VERIFIED：三个扫描器均为「ticker + `select { case <-ctx.Done(): return; case <-ticker.C }`」结构，每轮迭代是若干**独立单行事务**（`CancelExpired` 逐单取消、`ConsumeQueued` 逐条 `FOR UPDATE SKIP LOCKED` 单事务落单、`ReconcileCache` 幂等 SET）。不存在跨批次的 batch 级事务，故「在途批次」= 当前正在执行的那一条单事务。
- VERIFIED：秒杀消费中断恢复机制已存在：`consumeOne` 单事务（出队→校验→落单→状态更新）任一失败整体回滚、请求保持 `queued`；技术失败指数退避重试（上限 3 → `dead`）；提交后 Redis 权威值收敛（`convergeStock` 幂等 SET）。`consumeBeforeCommitHook` 已用于模拟「提交前崩溃」。`recovery_test.go`/`consume_test.go` 已覆盖孤儿标记自愈、终态收敛、repair。
- VERIFIED：`boot.checkDependencies` 用 MySQL `PingMaster` + Redis `PING`（仅启动连通，非运行时 readiness）；`waitForDependencies` 以 `STARTUP_DEPENDENCY_TIMEOUT`（默认 30s）重试。
- VERIFIED：`codes.CodeServiceUnavailable = 1005 → 503`（`internal/codes/codes.go`），可复用于 `/ready` 未就绪响应，无需新增错误码。
- VERIFIED：`storage.Local.PrepareBannerPlaceholders` 用 `MkdirAll(0o755)` + 写 PNG 到 `STORAGE_LOCAL_ROOT/banners`（容器内 `/app/storage/banners`）；`docker-compose.yml` 以 named volume `app_storage` 挂载 `/app/storage`。
- VERIFIED：Git 分支 `chore/container-runtime-hardening-v1` @ `359b8cce`，working tree 仅含未跟踪的 task artifacts，无遗留改动。
- UNKNOWN：`signal.NotifyContext` 与 GoFrame `gproc.Listen()` 同时监听同一信号的实际时序（Go 标准库语义支持多 channel 同时接收，但未在本项目运行时验证，需 AC-010 集成验证）。

## Recommendation

**RECOMMENDATION：在不动业务规则/数据模型的前提下，分四块最小加固。**

1. **非 root（R1）**：runtime 阶段创建专用用户 `app`（固定 `uid=10001:gid=10001`，比 alpine `nobody` 更利于 K8s `runAsNonRoot`/`securityContext` 预测），`USER 10001:10001`；`chown -R app:app /app` 并确保 `/app/storage` 属 app。named volume 首次挂载会继承镜像目录的 owner（Docker populate-volume 语义），故 `/app/storage/banners` 非 root 可写。entrypoint（`migrate up` + `exec serve`）无特权操作，非 root 可直接运行。
   - 关键取舍：优先「镜像内 chown + 专用 uid」，而非「运行时卷权限约定」。因为现有 `app_storage` 是 named volume，chown 在镜像内即可让首次挂载拿到正确 owner，无需改 compose 或引入 init 容器。
   - 运维注意：若已存在历史 root 属主 `app_storage` 卷，需 `docker compose down -v` 重建或 `chown`，否则非 root 首次写会失败（AC-010 用全新环境验证）。

2. **readiness `/ready`（R2）**：新增 `/ready`，**fail-closed**。判定 = MySQL `PingMaster` + Redis `PING` 均可达；schema 就绪由启动 bootstrap 保证（`serve` 仅在 `checkSchemaReady` 通过后才监听），不逐次探测。探测带短超时（可配置，默认 1s），结果进程内缓存 TTL（可配置，默认 3s），缓存窗口内重复探测不新增 DB/Redis 访问（满足 AC-005）。未就绪 → 返回 `codes.CodeServiceUnavailable`（1005→503，经既有 `Response` 中间件，体为 `{code:1005,message:"服务不可用"}`，不泄漏凭据/路径/堆栈）。
   - `/health` 保持 liveness 语义不变（进程存活即 200，不探测依赖）。
   - 关键取舍：缓存 TTL 取 3s 且建议 ≥ 未来 K8s `periodSeconds`，权衡「故障摘流恢复时延」与「探测负载」。fail-closed 由 AC-004 直接规定，非可选。

3. **优雅关闭协调（R3）**：在 `serve` 中引入 `signal.NotifyContext(context.Background(), SIGINT, SIGTERM)` 作为根 ctx，替换传给三个扫描器的 ctx（扫描器 ticker 循环已 `select <-ctx.Done()` 即退出，无需改循环结构）；保留 `s.Run()` 负责 HTTP drain（GoFrame 处理同一批信号）；`s.Run()` 返回后 `sync.WaitGroup.Wait()` 等扫描器收尾。
   - 停机时序：信号 → HTTP 停收新连接并 drain（`server.graceful_timeout`，建议显式配置为 10s）→ 扫描器 ctx 取消 → 在途单事务回滚（安全）或自然完成 → 进程退出。每个扫描操作的原子性已由各自单事务保证，取消不产生半成品。
   - 关键取舍：接受「在途事务以回滚为主」而非「等待在途完成」。因为消费/取消/对账均为单行独立事务且短，回滚后由重启重扫 + 既有幂等/重试/对账兜底恢复，与 AC-007「完成或安全回滚」、AC-008 完全相容；无需引入跨扫描器的复杂 drain 调度。

4. **AC-008 验证为主 + 回归测试（R4）**：确认既有「事务回滚 + 请求保持 queued + 重试/死信 + 缓存对账」已覆盖优雅停机打断；新增一个测试：消费事务进行中取消 ctx → 断言事务回滚、请求仍 `queued`、重启消费恰好产生一条订单（不重复、不半成品）。复用 `consumeBeforeCommitHook` 或 ctx 取消注入。

5. **镜像审计 + Compose（R5）**：扩展 `scripts/test-container.sh` 静态断言：`Dockerfile` 含非 root `USER`、runtime 层不含 Go 工具链/源码/`.env`。Compose app 与 nginx 的 healthcheck 保持 `/health`（liveness，进程存活门控，启动就绪已由 `depends_on: service_healthy` + bootstrap 保证）；`/ready` 作为未来 K8s readinessProbe 目标写入 `deployment.md`，不改 Compose 探针（避免引入 Compose 无法自动摘流却改变现有启动门控语义的副作用）。

### 全局资源

无需 Reservation：不新增 migration（无 schema 变更）、不新增错误码（复用 `CodeServiceUnavailable=1005`）。`state.resources.reservations` 保持空。

## Selected Design

经 Owner 确认（`record_owner_decision`），最终设计如下：

1. **非 root（D-001）**：Dockerfile 创建专用用户 `app:app`（`uid=10001`、`gid=10001`），`USER 10001:10001`；必要目录（`/app`、`/app/storage`）提前 `chown`。不使用 alpine `nobody`（避免不同基础镜像 UID/GID 行为差异），固定运行身份便于后续 K8s `SecurityContext` 对接。保证 `/app/storage/banners` 非 root 可写。
2. **readiness（D-002）**：`/ready` 探测 MySQL Ping + Redis PING，探测超时 1s、结果进程内缓存 TTL 3s；任一失败 → HTTP 503（`codes.CodeServiceUnavailable`），响应/日志不得泄漏数据库地址、Redis 信息、内部错误堆栈。`/health` 保持 liveness 语义不变，不依赖 DB/Redis。
3. **优雅关闭（D-003）**：`signal.NotifyContext(SIGINT, SIGTERM)` 作为后台消费者根 ctx；`server.graceful_timeout=10s`；在途事务采用**回滚优先**模型（当前消费为单批次单事务 + 已有幂等 + `queued` 可恢复 + 重启可重新消费）。生命周期：SIGTERM → cancel ctx → worker 回滚/退出 → HTTP graceful drain → WaitGroup 收尾。禁止：强杀消费者导致半提交、修改订单状态机、引入新的 drain 调度系统。
4. **Compose healthcheck（D-004）**：Compose 保持 `/health`（liveness）作 app/nginx 探针；`/ready` 仅作未来 K8s readinessProbe 目标，不修改 Compose 启动门控（Compose 无 Service 摘流机制，用 `/ready` 作 healthcheck 会放大 DB/Redis 抖动为 unhealthy + restart 误触发）。

### 实施范围
- 包含：Dockerfile 非 root 运行、镜像目录权限修复、`/ready` endpoint、readiness 缓存与超时、SIGTERM 生命周期管理、后台消费者 ctx 接入、`graceful_timeout` 配置、非 root 容器测试、消费取消回滚测试。
- 不包含：Kubernetes Deployment / Probe 配置 / SecurityContext YAML / HPA / Service/Ingress。

## Interfaces and Data

- 新增公开端点 `GET /ready`（与 `/health` 同组、公开、经 `RequestID`+`Response` 中间件），成功 `{code:0, data:{status:"ready"}}`，未就绪 503 `{code:1005}`。
- `GET /health` 行为与响应格式不变（liveness）。
- `IHealth` 接口新增 `Readiness(ctx) (*ReadinessRes, error)`；实现内使用 `g.DB()`/`g.Redis()` 探测（带超时 ctx）+ 进程内缓存。
- `serve` 生命周期：新增信号根 ctx + `sync.WaitGroup`；三个扫描器 `start*Scanner(ctx, wg)` 签名接受可取消 ctx 与 wg。
- 新增配置（`manifest/config/config.yaml` + 环境变量）：
  - `server.graceful_timeout`（HTTP drain 秒，建议 10，对应 `SERVER_GRACEFUL_TIMEOUT`）；
  - `readiness.probe_timeout`（探测超时秒，默认 1，`READINESS_PROBE_TIMEOUT`）；
  - `readiness.cache_ttl`（结果缓存秒，默认 3，`READINESS_CACHE_TTL`）。
- `Dockerfile`：新增 `addgroup/adduser` + `USER 10001:10001` + `chown`。
- `docs/design/deployment.md`：更新 §6（`/health` liveness + `/ready` readiness）、§4/新增（非 root、优雅退出与扫描器停机生命周期）。

## Business Invariants

- INV-001：容器主进程有效 UID 非 0（非 root）。
- INV-002：`/health` 判定不依赖 DB/Redis，进程存活即 200 + `status=ok`（liveness）。
- INV-003：`/ready` 在 DB 或 Redis 不可达时返回 503，响应与日志不泄漏凭据、路径、堆栈。
- INV-004：`/ready` 探测带超时 + 结果缓存，缓存窗口内重复调用不新增 DB/Redis 探测。
- INV-005：优雅停机不产生半成品：消费事务被打断时整体回滚，请求保持 `queued`，重启后重试/对账恢复至一致，不重复下单。
- INV-006：停机后扫描器停止领取新任务，在途单事务完成或回滚，无半成品或不一致状态。
- INV-007：非 root 下 `/app/storage/banners` 可写，轮播图占位图 seed 成功，`/storage/banners/*` 可访问。

## Failure and Consistency Semantics

- 成功：`/ready` 200 代表「DB 与 Redis 当前可达（受缓存窗口内可能延迟反映恢复）」；不代表刚执行 migration。`/health` 200 代表进程存活。
- MySQL/Redis 探测失败：fail-closed 503，结果缓存 TTL 内不重复打库/打缓存；恢复后在下一 TTL 窗口返回 200。
- 信号触发：HTTP 停收新连接 → 在途请求 drain（`graceful_timeout`）→ 扫描器 ctx 取消 → 在途单事务回滚或完成 → `wg.Wait()` → 进程退出。
- 重复/乱序/重试/部分完成：由既有幂等约束（幂等键、`uk_flash_one_per_user`、`uk_flash_idempotency`、`FOR UPDATE SKIP LOCKED`、条件更新 + `RowsAffected`）与「请求保持 queued + 指数退避 + 死信 + 缓存对账」兜底；本次不引入新机制。
- 事实来源不变：MySQL 为业务权威；Redis 为派生缓存；无 MQ。

## Allowed / Forbidden Changes

- 允许：新增 `/ready`；改 `Dockerfile`（USER/chown）；改 `serve` 生命周期（信号 ctx + WaitGroup）；新增上述 config；扩展 `test-container.sh`；新增探针/优雅退出/停机/中断恢复测试；更新 `docs/design/deployment.md`。
- 禁止：产出 K8s 编排清单；改秒杀/订单业务规则、公开业务 API、错误码语义或数据模型；引入 MQ；把 `/health` 改成依赖探测；改动统一响应格式；改 `serve`/`migrate` 等子命令的既有公开行为。

## Verification Requirements

- INV-001 → 构建镜像启动容器，`id -u`/`/proc/<pid>/status` 确认 euid 非 0；静态断言 `Dockerfile` 含非 root `USER`。
- INV-002 → 停止 mysql/redis 后 `/health` 仍 200 + `status=ok`（等价隔离环境）。
- INV-003 → 分别停 mysql、redis，`/ready` 返回 503，响应体无凭据/路径/堆栈。
- INV-004 → 缓存窗口内重复 `/ready` 不产生新 DB/Redis 探测（测试/日志证明），探测超时受控。
- INV-005 → 消费事务中取消 ctx，断言回滚 + 请求仍 `queued` + 重启消费恰好一单（回归测试）。
- INV-006 → 停机后扫描器 goroutine 退出、无新出队/取消；在途事务回滚或完成，无半成品。
- INV-007 → 非 root 容器内 `/app/storage/banners` 可写、seed 成功、`GET /storage/banners/banner-1.png` 200。
- 通用：`gofmt` / `go build ./...` / `go vet ./...` / `go test ./...`（业务，与引擎测试分开）；AC-010 真实 Docker 环境全链路验证。

## Open Risks

- 信号共存：`signal.NotifyContext` 与 GoFrame `gproc.Listen()` 的时序需 AC-010 集成验证（预期无冲突，Go 标准库支持多 channel 接收）。
- 历史 root 属主 `app_storage` 卷需重建或 chown，否则非 root 首次写失败（运维注意，非代码问题）。
- `server.gracefulTimeout`（默认 2s）才是实际 drain 超时，需测试确认显式配置后确实生效（GoFrame 内部 `gracefulTimeout`/`gracefulShutdownTimeout` 映射易混淆）。
- `/ready` 缓存为进程内缓存：多实例各自独立，符合单实例当前形态；未来多实例无需改语义（各实例独立 readiness 本就是期望）。

## Owner Decision Record

- D-001（非 root）：采用专用 `app` 用户 `uid=10001:gid=10001`，`USER 10001:10001`，必要目录提前 `chown`，`/app/storage/banners` 非 root 可写；不使用 alpine `nobody`。适用范围：`Dockerfile` 运行时用户与目录权限。
- D-002（readiness 参数）：探测超时 1s、缓存 TTL 3s；`/ready` = MySQL Ping + Redis PING，任一失败返回 503 且不泄漏数据库地址/Redis 信息/错误堆栈；`/health` 保持 liveness。适用范围：`/ready` 判定与探测策略。
- D-003（优雅关闭模型）：`graceful_timeout=10s`，在途事务回滚优先；`signal.NotifyContext(SIGINT, SIGTERM)` 作后台消费者根 ctx；禁止强杀导致半提交、修改订单状态机、引入新 drain 调度系统。适用范围：`serve` 生命周期与扫描器 ctx 接线。
- D-004（Compose healthcheck）：Compose 保持 `/health`（liveness）作探针；`/ready` 仅作未来 K8s readinessProbe 目标，不改 Compose 启动门控。适用范围：`docker-compose.yml` 与 `docs/design/deployment.md`。
- 总适用范围：上述决定仅约束 `container-runtime-hardening-v1` 的容器运行时/生命周期实现，不改变业务规则、公开业务 API、错误码语义或数据模型，不产出 K8s 编排清单。
