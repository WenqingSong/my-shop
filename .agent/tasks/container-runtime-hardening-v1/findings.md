# Cleaner Findings

## Review Target

- 任务：`container-runtime-hardening-v1`（容器运行时加固：非 root + 存活/就绪/优雅退出）。
- 分支：`chore/container-runtime-hardening-v1`。
- 任务基线：`359b8cc`（分支起点，`git status` 干净，任务前无已有修改）。
- Contract：`APPROVED`，`contract.target = 8a6b545`（A1，含 `contract.md` 与 `docs/design/deployment.md` 更新）。
- 审查对象（Implementation Evidence Commit C1）：`914c9f6` `feat(container): 非 root 运行 + /ready readiness + 优雅退出`。
- Coder metadata commit（C2）：`a4ac354`（`review.status=PENDING`、`review.target=914c9f6`）。
- 审查时 HEAD：`a4ac354`；本结论对 C1 `914c9f6` 有效，不覆盖 C2 或后续 working tree。
- C1 变更文件（13 个，`git diff 359b8cc..914c9f6` 中非任务工件部分）：
  - `Dockerfile`、`manifest/config/config.yaml`、`scripts/test-container.sh`
  - `api/health/v1/health.go`、`internal/service/health.go`、`internal/controller/health/health.go`、`internal/logic/health/health.go`
  - `internal/boot/boot.go`、`internal/cmd/cmd.go`
  - 测试：`internal/logic/health/health_test.go`、`internal/cmd/scanner_test.go`、`internal/cmd/routes_test.go`、`internal/logic/flashsale/consume_test.go`
- 全局资源：Contract 声明「无需 Reservation」（不新增 migration、不新增错误码）；C1 未触碰 `internal/codes/codes.go` 与 `internal/migrations/sql/*`，三边一致性成立（见 Verification）。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 容器非 root | PASS | Docker 实测 `id -u=10001`、`id -g=10001`、`whoami=app`；`Dockerfile` 含 `USER 10001:10001` + `addgroup/adduser` + `chown`；`test-container.sh` 静态断言通过 |
| AC-002 非 root 目录可写 + seed | NOT_VERIFIED | `/app/storage` 属主 `app:app` 已实测；banner 占位图 seed 与 `/storage/banners/*` 静态访问需 `serve` 运行（七牛 + DB/Redis 链路），属 AC-010 运行时里程碑，未在本轮独立运行 |
| AC-003 `/health` liveness | PASS | `Liveness` 不探测依赖（仅 `status=ok` + 时间）；`TestLiveness` + `internal/cmd` 路由/控制器测试覆盖；`GET /health` 路由存在 |
| AC-004 `/ready` fail-closed 503 | PASS | `Readiness` 任一依赖不可达返回 `codes.CodeServiceUnavailable(1005)`；`codes.HTTPStatus(1005)=503` + `Response` 中间件写 503；`TestReadinessNotReadyReturns503` 断言码与不泄漏底层错误 |
| AC-005 探测超时 + 缓存 | PASS | `probeReady` 用 `context.WithTimeout`+select 兜底超时；`TestReadinessCacheAvoidsReprobe`/`TestReadinessCacheExpiryReprobes`/`TestReadinessNotReadyCached` 证明缓存窗口内不重复探测；`TestProbeReadyTimeout` 证明超时受控 |
| AC-006 HTTP 优雅关闭 | PASS（代码级） | `signal.NotifyContext(SIGINT,SIGTERM)` + `s.Run()` drain + `wg.Wait()`；`SetGracefulTimeout(10)` 映射到 GoFrame 实际 drain 超时（已核对 `ghttp_server_graceful.go:22` 将 `GracefulTimeout` 映射为 graceful 的 `GracefulShutdownTimeout`）；运行时 drain 观察属 AC-010 |
| AC-007 扫描器/消费者停机 | PASS | `TestScannersStopOnContextCancel` 证明三扫描器 `ctx.Done()` 即退出、`wg.Wait` 返回；三扫描器签名改为 `(ctx, wg)` 接入可取消根 ctx |
| AC-008 秒杀消费中断恢复 | PASS | `TestConsumeInterruptedByCtxCancel`（真实 MySQL/Redis）断言事务回滚 + 请求仍 `queued` + 无订单不扣库存 + 重投恰一单；与既有 `recovery_test.go`/`consume_test.go` 共同覆盖 |
| AC-009 探针/退出测试 | PASS | 新增 `health_test`/`scanner_test`/`consume_test` 断言最终业务结果而非调用次数；`go test ./...` 全量通过 |
| AC-010 非 root Docker 全链路 | NOT_VERIFIED | 已独立完成镜像构建 + 非 root + 镜像内容实测；「`docker compose up` 全链路 + `docker stop` 优雅退出（含 DB/Redis 依赖）」属 Deliverer 里程碑运行验收，未在本轮执行 |
| AC-011 镜像内容审计 | PASS | Docker 实测：无 `go` 工具链、无 `/src`、无 `*.go` 源文件；`test-container.sh` 静态断言 runtime 层不 COPY 源码/`.env`；`config.yaml` 的 `pass`/`secret` 均为空（凭据分离） |
| AC-012 部署模型文档更新 | PASS | `docs/design/deployment.md` 已更新 §3/§4/§6 与 §8 INV-008~012，非 root、`/health`/`/ready` 语义、优雅退出与扫描器停机生命周期与实现一致（除 CLEAN-001 的 env 名漂移） |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `gofmt -l`（本次变更 .go 文件） | PASS | 无输出 |
| `go test -p 1 -count=1 ./...`（真实 MySQL/Redis，DB 已重置） | PASS | 全部 `ok`（含 `internal/logic/health`、`internal/cmd`、`internal/logic/flashsale`、`internal/migrations` 等） |
| `scripts/test-container.sh` | PASS | 静态断言通过（非 root/凭据分离/镜像模型/生命周期/编排） |
| `scripts/check-registry.sh` | PASS | 校验通过：未发现 Reservation 重复或 Registry ↔ 实现漂移 |
| `docker build` + 镜像运行态检查 | PASS | `id -u=10001`、`id -g=10001`、`whoami=app`；`/app` 属 `app:app`；无 go 工具链/无源码/无 `.env` |
| GoFrame `SetGracefulTimeout` 映射 | PASS | 核对 `ghttp_server_graceful.go`：`GracefulShutdownTimeout: s.config.GracefulTimeout`，故 `SetGracefulTimeout` 是实际 drain 超时的正确接线 |

说明：`internal/logic/flashsale` 的 `setupConsumeTest` 清理顺序未含 `inventories`（`fk_inventories_sku` 引用 `skus`），当 DB 残留其它包创建的 inventory 行时，孤立运行该包会因 `DELETE FROM skus` 触发外键失败。全量 `go test -p 1 ./...` 在干净库上通过（flashsale 先于 cmd 的库存建数包运行）。此为既存测试隔离弱点，非本任务引入，本次不单独建 Finding。

## Findings

### CLEAN-001：`server.gracefulTimeout` 环境变量名与 Contract/配置注释漂移

- Severity：P2
- Status：OPEN
- Location：`manifest/config/config.yaml:7`（键 `server.gracefulTimeout`、注释「对应环境变量 SERVER_GRACEFUL_TIMEOUT」）；`internal/boot/boot.go:92`（`cfgInt(ctx, "server.gracefulTimeout", ...)`）；`contract.md` §Interfaces and Data 与 §Selected Design D-003（声明「对应 `SERVER_GRACEFUL_TIMEOUT`」）
- AC / Invariant：AC-006（优雅关闭「可配置」）；Contract/Design ↔ 实现四者一致
- Trigger：运维按 Contract/config 注释设置环境变量 `SERVER_GRACEFUL_TIMEOUT=30` 期望覆盖 drain 超时
- Actual：GoFrame `GetEffective` 对配置键 `server.gracefulTimeout` 生成的环境变量名是 `SERVER_GRACEFULTIMEOUT`（`utils.FormatEnvKey` 仅做 `ToUpper` + 点转下划线，不在驼峰边界插入下划线）。因此 `SERVER_GRACEFUL_TIMEOUT` 被静默忽略，实际仍取 config 文件默认 10s
- Expected：文档声明的环境变量名能真实生效；或文档与键名改为蛇形以对齐
- Impact：优雅关闭超时是运维常用可调项，按文档设置后静默失效，可能在生产发布时因 drain 时间不足导致在途请求被强制断开；且是 Contract/config 注释 ↔ 实现的公开接口漂移，违反「四者一致」
- Evidence：`github.com/gogf/gf/v2@v2.10.3/internal/utils/utils_str.go:155-157` 的 `FormatEnvKey`；`os/gcfg/gcfg.go:182-192` 的 `GetEffective` 仅用 `FormatEnvKey` 做一次 env 查询；项目其余配置键均为 snake_case（`order.pay_timeout`、`flash_sale.consume_scan_interval`、`readiness.cache_ttl` 等），仅 `gracefulTimeout` 为 camelCase
- Required Fix Boundary：使文档声明的 `SERVER_GRACEFUL_TIMEOUT` 与实现一致。优先把键改为蛇形 `server.graceful_timeout`（对齐项目 convention，`FormatEnvKey` 产出 `SERVER_GRACEFUL_TIMEOUT`），同步更新 `manifest/config/config.yaml`、`internal/boot/boot.go` 的 `cfgInt` 键、`contract.md` 与 `docs/design/deployment.md` 中对该键/环境变量名的引用；或（次选）保留 camelCase 键、把文档与注释中的环境变量名改为 `SERVER_GRACEFULTIMEOUT`。修复后 Contract ↔ deployment.md ↔ config.yaml 注释 ↔ 实现四处一致

### CLEAN-002：readiness 探测失败完全吞掉错误、无可观测性日志

- Severity：P3
- Status：OPEN
- Location：`internal/logic/health/health.go:70-96`（`probeReady` 将 `probeDependencies` 错误转为 bool 丢弃，`Readiness` 失败分支不记日志）
- AC / Invariant：AC-004（响应不泄漏敏感信息）；可观测性
- Trigger：DB/Redis 不可达时，`/ready` 返回 503，但任何日志都看不到「为何未就绪」
- Actual：底层探测错误（含 DB/Redis 连通失败）被 `probeReady` 折叠为 `bool` 丢弃，`Readiness` 仅返回 `codes.New(CodeServiceUnavailable)`，无任何日志
- Expected：保持不泄漏（不记 host/port/凭据/堆栈），但应记一条 sanitized 的失败日志（如「readiness 探测失败」+ 依赖类型），便于定位
- Impact：生产 `503` 无据可查，排障只能靠外部探针/监控，运维成本上升
- Evidence：`health.go:82-96` 无 `glog` 调用；`codes.New` 只携带码表安全文案
- Required Fix Boundary：在不泄漏敏感信息的前提下增加一条 sanitized 失败日志；不改探测语义与 fail-closed 行为

P3 由 Owner 决定是否处理，不阻塞 CLEAN。
