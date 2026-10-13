# Delivery Verification

## Milestone and Target
- Milestone：container-runtime-hardening-v1 容器运行时加固交付验收（非 root 运行 + 存活/就绪/优雅退出语义 + Docker 环境验证）
- Delivery Target：`chore/container-runtime-hardening-v1` 的 CLEAN 实现快照
- Cleaner Review Target：`41c12a7`（完整 `41c12a74c306ee51062db5059d24bd8d237821f3`）
- Target Match：YES

## 验证版本（三个不可变 SHA）
- `review_target`：`41c12a74c306ee51062db5059d24bd8d237821f3`（CLEAN 实现快照）
- `feature_head`：`700ddf58ab19e6b7d67386a25607995140f752af`（实际构建镜像的 feature HEAD）
- `develop_base`：`7b3449fa3843110f19d57765326878c2dc68ddb5`（验证时的 origin/develop）

说明：`41c12a7..700ddf5` 仅变更 4 个 task artifact 文件（`core-logic.md`/`findings.md`/`owner-decision.md`/`state.yaml`），无生产代码变更，故从 `feature_head` 构建的镜像与 CLEAN 版本生产代码一致。

## Environment
- OS：Linux（amd64）
- Go：宿主机 go1.24.1；镜像 builder 为 `golang:1.23-alpine`（`go 1.23.0` 模块）
- Docker：29.6.2；Docker Compose：v5.3.1
- MySQL：`mysql:8.0`（容器 `my-shop-mysql`，127.0.0.1:3306，healthy）
- Redis：`redis:7-alpine`（容器 `my-shop-redis`，127.0.0.1:6379，healthy）
- 镜像：`my-shop-app:local`（从 `feature_head` 工作区 `docker compose build app` 构建）
- 配置来源：`docker-compose.yml` + 本地 `.env`（七牛 AK/SK/bucket/domain 有值，用于 serve 启动 `ValidateConfig`；敏感 Secret 值不记录）
- 隔离与测试数据：验收期间运行 `go test ./...` 清理了 `admins` 表（超管被清），导致 app 首次启动因缺 `ADMIN_SUPER_PASSWORD` fail-fast；随后以 `ADMIN_SUPER_PASSWORD`（开发测试值）重启并 seed 出唯一超管 `admin`。该超管为验收环境测试数据，非交付材料的一部分。

## Verification

| Check | Result | Evidence |
|---|---|---|
| `gofmt -l`（变更 .go） | PASS | 无输出 |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go test -p 1 -count=1 ./...`（真实 MySQL/Redis） | PASS | 全部包 `ok`，exit 0 |
| `scripts/test-container.sh`（静态断言） | PASS | 凭据分离/构建上下文/镜像/生命周期/编排全部通过 |
| 镜像构建 | PASS | `docker compose build app` 成功，产物 `my-shop-app:local` |
| AC-001 非 root 运行 | PASS | 容器内 `id`=`uid=10001(app) gid=10001(app)`、`whoami=app`；主进程 `serve` PID=1 的 `/proc/<pid>/status` Uid/Gid 均为 10001；`docker history` 含 `USER 10001:10001` |
| AC-002 非 root 目录可写 + seed | PASS | `/app/storage/banners` 属 `app:app`，含 `banner-1/2/3.png`（seed 成功）；`GET /storage/banners/banner-1.png` 返回 `200` + `image/png`（87B） |
| AC-003 `/health` liveness | PASS | `/health` 返回 `{"code":0,"data":{"status":"ok",...}}`；停 Redis 后 `/health` 仍 200 + status=ok |
| AC-004 `/ready` fail-closed | PASS | DB+Redis 正常时 `/ready` 200 `status=ready`；停 Redis 后 `/ready` 返回 `503`（业务码 `1005`/`服务不可用`），响应体无凭据/DSN/Redis 地址/堆栈 |
| AC-005 探测超时 + 缓存 | PASS | `TestReadinessCacheAvoidsReprobe`/`TestReadinessCacheExpiryReprobes`/`TestReadinessNotReadyCached`/`TestProbeReadyTimeout` 通过（`go test`，缓存窗口内不重复探测、超时受控） |
| AC-006 HTTP 优雅关闭 | PASS | `docker stop`（SIGTERM）→ 日志 `server gracefully shutting down by signal: terminated` + `all servers shutdown`，容器退出码 0，耗时 464ms |
| AC-007 扫描器/消费者停机 | PASS | 进程 464ms 干净退出（退出码 0，`wg.Wait` 未阻塞）；`TestScannersStopOnContextCancel` 通过 |
| AC-008 秒杀消费中断恢复 | PASS | `TestConsumeInterruptedByCtxCancel`（真实 MySQL/Redis）通过：事务回滚 + 请求保持 `queued` + 无重复下单 |
| AC-009 探针/退出测试 | PASS | `go test ./...` 全量通过（含 `health_test`/`scanner_test`/`consume_test`） |
| AC-010 非 root Docker 全链路 | PASS | 构建镜像 → `docker compose up -d app` healthy → `/health`/`/ready`/banner 静态资源全链路 → `docker stop` 优雅退出（含 DB/Redis 依赖） |
| AC-011 镜像内容审计 | PASS | 临时容器审计：无 `go` 工具链、无 `/src`、无 `*.go`、无 `.env`；`config.yaml` 的 `pass`/`secret`/`access_key`/`secret_key` 全空；`/app` 属 `app:app` |
| AC-012 部署模型文档 | PASS | `docs/design/deployment.md` §3/§4/§6/§8 已更新，与 Dockerfile/cmd.go/health.go 实现一致（非 root、`/health` liveness/`/ready` readiness、优雅退出与扫描器停机生命周期） |

## Acceptance Evidence

- INV-001（非 root）：运行态 `id -u=10001`、主进程 Uid/Gid=10001、`USER 10001:10001`。
- INV-002（`/health` liveness 独立）：停 Redis 后 `/health` 仍 200 + status=ok。
- INV-003（`/ready` fail-closed + 不泄漏）：停 Redis 后 `/ready` 503，响应体 `1005/服务不可用`，日志无 DSN/密码/Redis 地址/底层探测错误。
- INV-004（探测负载）：单元测试证明缓存窗口内重复调用只探测一次、探测超时受控。
- INV-005（优雅停机一致性）：`docker stop` 退出码 0；`TestConsumeInterruptedByCtxCancel` 证明事务回滚 + 请求保持 `queued` + 不重复下单。
- INV-006（扫描器停机）：进程干净退出（`wg.Wait` 未阻塞）；`TestScannersStopOnContextCancel` 证明 ctx 取消即退出。
- INV-007（非 root 目录可写）：`/app/storage/banners` 属 `app:app`、seed 成功、静态资源 200。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 运行态观察 `/ready` 缓存窗口内探测次数 | 探测成功路径不打日志，无法从运行日志直接计数 | 低：该不变量由单元测试 `TestReadinessCacheAvoidsReprobe`（断言探测次数==1）证明，属可区分正确/错误实现的测试 |
| K8s 编排清单（Deployment/Probe/SecurityContext） | Out of Scope（Task 明确不产出） | 无 |
| 多实例负载均衡 / HTTPS/TLS | Out of Scope | 无 |

## Remaining Risks

- **日志 Stack 观察**：`/ready` 未就绪时，服务端访问日志会附带 GoFrame 记录业务错误码创建位置的标准 trace（含源码符号路径 `internal/codes/codes.go:287`、`internal/logic/health/health.go:76`）。该 trace 不含 DSN、密码、Redis 地址等敏感凭据，响应体干净（`1005/服务不可用`），故不构成 INV-003 违反；属框架默认错误定位行为。
- **CLEAN-002（P3，探测失败无排障日志）**：Owner 已决定不阻塞当前交付，记录为后续可观测性增强项（sanitized readiness failure log）。
- **历史 root 属主 `app_storage` 卷**：若存在旧 root 属主 named volume，非 root 首次写会失败，需 `docker compose down -v` 重建或 `chown`（运维注意，非代码缺陷；本次用全新环境验证通过）。

## Result

PASS
