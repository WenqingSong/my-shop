# Cleaner Findings

## Review Target

- 任务：`container-runtime-hardening-v1`（容器运行时加固：非 root + 存活/就绪/优雅退出）。
- 分支：`chore/container-runtime-hardening-v1`。
- 任务基线：`359b8cc`（分支起点，`git status` 干净，任务前无已有修改）。
- Contract：`APPROVED`，`contract.target = 8a6b545`（A1）。
- 复审对象（Implementation Evidence Commit C1′）：`41c12a7` `fix(container): 修复 CLEAN-001 优雅关闭超时键改为蛇形 graceful_timeout`。
- Coder metadata commit（C2′）：`4565f99`（`review.status=PENDING`、`review.target=41c12a7`）。
- 上一轮：初审查对象 `914c9f6`（C1），结论 `CHANGES_REQUIRED`（CLEAN-001 P2），review commit `166a708`。
- 本轮完整实现范围：`359b8cc..41c12a7`（= 初版 C1 `914c9f6` 的 13 个文件 + 修复 `41c12a7` 的 5 个文件）。
- 修复 `41c12a7` 变更文件：`manifest/config/config.yaml`、`internal/boot/boot.go`、`internal/boot/boot_test.go`（新增回归测试）、`contract.md`、`docs/design/deployment.md`（键名对齐，实质决策 D-001~D-004 不变）。
- 全局资源：Contract 声明「无需 Reservation」（不新增 migration、不新增错误码）；实现未触碰 `internal/codes/codes.go` 与 `internal/migrations/sql/*`，三边一致性成立。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 容器非 root | PASS | 运行态实测 `id -u=10001`、`id -g=10001`、`whoami=app`；`Dockerfile` 含 `USER 10001:10001` + `addgroup/adduser` + `chown` |
| AC-002 非 root 目录可写 + seed | PASS | 运行态实测 `/app/storage/banners` 属 `app:app`、含 `banner-1/2/3.png`（seed 成功）、`GET /storage/banners/banner-1.png` 返回 200 |
| AC-003 `/health` liveness | PASS | 运行态实测 `/health` 返回 `{code:0,status:ok}`；`Liveness` 不探测依赖；`TestLiveness` + 路由测试覆盖 |
| AC-004 `/ready` fail-closed 503 | PASS | 运行态实测 `/ready` 返回 `{code:0,status:ready}`；负路径 `TestReadinessNotReadyReturns503` 断言 `CodeServiceUnavailable`→503 且不泄漏底层错误 |
| AC-005 探测超时 + 缓存 | PASS | `probeReady` 用 `context.WithTimeout`+select；`TestReadinessCacheAvoidsReprobe`/`TestReadinessCacheExpiryReprobes`/`TestReadinessNotReadyCached`/`TestProbeReadyTimeout` 覆盖缓存与超时 |
| AC-006 HTTP 优雅关闭 | PASS | 运行态实测 `docker stop`（SIGTERM）→ 日志 `server gracefully shutting down by signal: terminated` → 退出码 0（0.88s）；`SetGracefulTimeout` 映射已核对 `ghttp_server_graceful.go` |
| AC-007 扫描器/消费者停机 | PASS | `TestScannersStopOnContextCancel` 证明三扫描器 `ctx.Done()` 即退出、`wg.Wait` 返回；签名改为 `(ctx, wg)` 接入可取消根 ctx |
| AC-008 秒杀消费中断恢复 | PASS | `TestConsumeInterruptedByCtxCancel`（真实 MySQL/Redis）断言事务回滚 + 请求仍 `queued` + 无订单不扣库存 + 重投恰一单；与既有 `recovery_test.go`/`consume_test.go` 共同覆盖 |
| AC-009 探针/退出测试 | PASS | 新增 `health_test`/`scanner_test`/`consume_test` + 修复回归测试 `TestApplyServerConfigGracefulTimeoutEnvOverride`，均断言最终业务结果；`go test ./...` 全量通过 |
| AC-010 非 root Docker 全链路 | PASS | 运行态实测 `docker compose up -d app` + `/health`/`/ready`/banner 静态访问 + `docker stop` 优雅退出（含 DB/Redis 依赖） |
| AC-011 镜像内容审计 | PASS | 运行态实测：无 `go` 工具链、无 `/src`、无 `*.go` 源文件、无 `.env`；`config.yaml` 的 `pass`/`secret` 均为空（凭据分离）；`test-container.sh` 静态断言通过 |
| AC-012 部署模型文档更新 | PASS | `docs/design/deployment.md` 已更新（非 root、`/health` liveness/`/ready` readiness、优雅退出与扫描器停机生命周期），与实现一致（键名已对齐为 `server.graceful_timeout`） |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `gofmt -l`（本次变更 .go 文件） | PASS | 无输出 |
| `go test -count=1 -run TestApplyServerConfigGracefulTimeoutEnvOverride ./internal/boot/...` | PASS | 回归测试通过（断言 `SERVER_GRACEFUL_TIMEOUT=42` → `GetGracefulTimeout()==42`） |
| `go test -p 1 -count=1 ./...`（真实 MySQL/Redis，DB 已重置） | PASS | 22 个包全部 `ok`，无 FAIL |
| `scripts/test-container.sh` | PASS | 静态断言通过 |
| `scripts/check-registry.sh` | PASS | 未发现 Reservation 重复或 Registry ↔ 实现漂移 |
| Docker 全链路（`docker compose up -d app`） | PASS | app 容器 healthy；`/health` 200、`/ready` 200、`/storage/banners/banner-1.png` 200、`/app/storage/banners` 属 `app:app` |
| 优雅关闭（`docker stop` SIGTERM） | PASS | 日志 `server gracefully shutting down by signal: terminated` + `all servers shutdown`，退出码 0 |
| 镜像内容（`docker run --entrypoint sh`） | PASS | 无 go 工具链、无 `/src`、无 `*.go`、`/app` 属 `app:app` |

说明：`internal/logic/flashsale` 的 `setupConsumeTest` 清理顺序未含 `inventories`（`fk_inventories_sku` 引用 `skus`），孤立运行该包且 DB 残留其它包 inventory 行时会因 `DELETE FROM skus` 触发外键失败；全量 `go test -p 1 ./...` 在干净库上通过。此为既存测试隔离弱点，非本任务引入，不单独建 Finding。

## Findings

### CLEAN-001：`server.gracefulTimeout` 环境变量名与 Contract/配置注释漂移

- Severity：P2
- Status：CLOSED
- Location（原）：`manifest/config/config.yaml`、`internal/boot/boot.go`、`contract.md`、`docs/design/deployment.md`
- AC / Invariant：AC-006（优雅关闭「可配置」）；Contract/Design ↔ 实现四者一致
- 修复：`41c12a7` 将配置键改为蛇形 `server.graceful_timeout`（`config.yaml`、`boot.go` 的 `cfgInt` 键），并同步 `contract.md`、`docs/design/deployment.md` 中键名引用；新增回归测试 `TestApplyServerConfigGracefulTimeoutEnvOverride`（设 `SERVER_GRACEFUL_TIMEOUT=42` → 断言 `GetGracefulTimeout()==42`）
- 复审验证：回归测试单独运行 PASS；若键退回 camelCase，`FormatEnvKey("server.gracefulTimeout")` 产出 `SERVER_GRACEFULTIMEOUT`，该 env 被忽略、回落默认 10，测试会失败——即测试能区分正确与错误实现。全仓库已无项目级 `gracefulTimeout`（camelCase）残留（`contract.md` 中仅余两处指 GoFrame 库内部字段 `GracefulTimeout` 的准确描述，非项目键）
- 结论：修复符合 Required Fix Boundary，四处（Contract ↔ deployment.md ↔ config.yaml 注释 ↔ 实现）一致

### CLEAN-002：readiness 探测失败完全吞掉错误、无可观测性日志

- Severity：P3
- Status：OPEN
- Location：`internal/logic/health/health.go:70-96`
- AC / Invariant：AC-004（响应不泄漏敏感信息）；可观测性
- 说明：`probeReady` 将探测错误折叠为 `bool` 丢弃，`/ready` 未就绪时无任何日志。保持 fail-closed 与不泄漏的前提下，可补一条 sanitized 失败日志（如「readiness 探测失败」+ 依赖类型），便于排障
- 结论：P3 由 Owner 决定是否处理，不阻塞 CLEAN
