# 工程质量与基础设施盘点（T01 阶段 4 证据）

> Review Baseline：`95c13910c7226b4e32487ed046702caf75d74fc6`
> 状态标签含义（本文件统一使用）：
> - **已实现且有源码证据**：源码/配置/命令输出可直接证明存在与行为。
> - **已配置但未验证运行**：配置存在、静态校验通过，但未在真实运行环境中观测。
> - **已发现但不完整**：存在部分实现，覆盖范围明显小于宣称或需要。
> - **未发现相关实现**：全仓库检索无对应代码/配置。
> - **当前环境无法验证**：受环境（CNB、生产、无压测）限制，本地无法执行。

## 1. Go 测试

| 项目 | 状态 | 证据 |
| --- | --- | --- |
| 测试文件分布 | 已实现且有源码证据 | 共 61 个 `*_test.go`：`internal/**` 41 个、`test/e2e` 20 个 |
| 纯内存单元测试 | 已实现且有源码证据 | `scripts/test-unit.sh` 枚举 11 个包/用例组合，实跑退出码 0（D3 §2） |
| 集成测试（真实 MySQL/Redis + 真实 HTTP） | 已实现且有源码证据 | `scripts/test-integration.sh` → `go test -p 1 ./...`，退出码 0；E2E 走 `migrations.Up` + `boot.Bootstrap` + 真实路由（`test/e2e/identity_isolation_test.go:93-133`） |
| 并发测试 | 已实现且有源码证据 | `TestFlashSaleConcurrentNoOversell`（20 并发 / 5 库存，断言不超卖）；`TestCircuitBreakerStateMachineRace`（`-race`） |
| 故障注入 | 已发现但不完整 | 有 `WRONGTYPE` Redis 故障（`flashsale_v2_test.go:253-282`）、进程内事务 hook（`consume.go:29-32`）、DB 改行构造超时/结束（`flashsale_v3_test.go:139-175`）；**无真实停机、网络分区、SIGKILL、死锁注入** |
| 秒杀链路测试 | 已实现且有源码证据 | 逻辑层 22 个顶层用例 + E2E 20 个用例全部 PASS（D3 §3） |
| 覆盖的业务边界 | 已实现且有源码证据 | 不超卖、一人一单、幂等、时间窗、价格快照、失败不建单、预扣/售罄/对账、fail-closed、重试/死信/修复、崩溃重投、孤儿标记自愈（D3 §5） |
| 未覆盖关键场景 | 已发现但不完整 | 多实例并发领取、真实故障、大数据量执行计划、>100 活动对账、Docker/CNB/Prometheus 实跑（D3 §8） |
| 测试是否会在依赖缺失时 Skip | 已实现且有源码证据 | 否；仅 7 处 `t.Skip`，均为时区数据缺失（`internal/boot/boot_test.go:113` 等），秒杀测试无 Skip 守卫 |

## 2. 质量门禁

| 检查 | 是否在仓库/CI 中执行 | 状态 | 证据 |
| --- | --- | --- | --- |
| `gofmt` | 仓库脚本**未包含**；本次手工执行 | 已实现且有源码证据（手工） | `gofmt -l .` 输出 0 行 |
| `go vet` | `scripts/test.sh:18`，CI `make test` | 已实现且有源码证据 | 实跑退出码 0 |
| `go build` | CI push 阶段 `make build`（`scripts/build.sh`） | 已配置但未验证运行（本地 `go build ./...` 通过；`make build` 产物未执行） | `.cnb.yml:75-76`；`go build ./...` 退出码 0 |
| `go test` | `scripts/test.sh` → unit + integration | 已实现且有源码证据 | 实跑退出码 0 |
| `go test -race` | `scripts/test.sh` 外的独立 `make test-race`，CI 单独 stage | 已发现但不完整 | `.cnb.yml:49-50`、`:72-73`；`scripts/test-race.sh:16` **仅** `-run 'TestCircuitBreakerStateMachine' ./internal/logic/flashsale/...`，即 race 只覆盖熔断器，不覆盖消费/E2E |
| 错误码 Registry 校验 | `scripts/check-registry.sh` | 已实现且有源码证据 | 实跑退出码 0 |
| Prometheus 配置静态校验 | `scripts/test-prometheus.sh` | 已实现且有源码证据 | 退出码 0；无 promtool 时跳过语法校验（WARN） |
| 容器化静态校验 | `scripts/test-container.sh` | 已实现且有源码证据 | 退出码 0；含 `docker compose config -q` |
| Nginx 配置校验 | `scripts/test-nginx.sh` | 已配置但未验证运行 | 静态断言通过；未构建镜像故跳过 `nginx -t`（WARN） |
| `lib.sh` 语义校验 | `scripts/test-lib.sh` | 已实现且有源码证据 | 退出码 0 |
| 完整 `make test` | `scripts/test.sh` 串起以上门禁 | 已配置但未验证运行（分项已跑，未以单条 `make test` 串跑） | `scripts/test.sh` 内容 |

## 3. Docker

| 项目 | 状态 | 证据 |
| --- | --- | --- |
| Dockerfile 存在与多阶段构建 | 已实现且有源码证据 | `Dockerfile:15`（builder `golang:1.23-alpine`）、`:30`（runtime `alpine:3.20`） |
| 静态编译参数 | 已实现且有源码证据 | `Dockerfile:25`：`CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w"` |
| 非 root 运行 | 已实现且有源码证据 | `Dockerfile:38`、`:53`（固定 `10001:10001`） |
| 启动命令 | 已实现且有源码证据 | `Dockerfile:58` → `scripts/docker-entrypoint.sh`：`migrate up` 后 `exec serve` |
| Dockerfile 内 HEALTHCHECK | 未发现相关实现 | `Dockerfile` 无 `HEALTHCHECK`；健康检查仅在 compose 定义 |
| 镜像构建实际执行 | 当前环境无法验证（本任务未执行） | 未运行 `docker build`；`test-container.sh` 只做静态断言 |
| `docker-compose.yml` 服务依赖 | 已实现且有源码证据 | 5 服务：mysql、redis、app、nginx、prometheus；`app` 依赖 mysql/redis `condition: service_healthy`（`:44-48`） |
| 启动命令 | 已实现且有源码证据 | app 无 command，走镜像 entrypoint；prometheus 显式 `--config.file` / `--storage.tsdb.path`（`:121-123`） |
| 环境变量注入 | 已实现且有源码证据 | `docker-compose.yml:49-81`：DB/Redis/JWT/超管/存储/七牛/指标开关；`MYSQL_ROOT_PASSWORD`、`AUTH_JWT_SECRET` 使用 `${VAR:?}` 无默认回退 |
| 健康检查 | 已实现且有源码证据 | mysql `mysqladmin ping`、redis `redis-cli ping`、app `/health`、nginx `/health`、prometheus `/-/ready`（`docker-compose.yml:15-19/31-35/87-92/108-113/132-137`） |
| 端口暴露边界 | 已实现且有源码证据 | mysql/redis 仅 `127.0.0.1`；app 不映射宿主端口；nginx `8080→80`；prometheus `9090` 暴露 |
| 完整栈实际启动 | 当前环境无法验证（本任务未执行） | 只启动了 mysql/redis（`make deps-start`） |
| `.dockerignore` 凭据隔离 | 已实现且有源码证据 | 排除 `.env`、`bin/`、`tmp/`、`storage/`、`.git/`、`docs/`、`.agent/`、`*_test.go` |

## 4. CNB 流水线（`.cnb.yml`）

| 项目 | 状态 | 证据 |
| --- | --- | --- |
| 流水线文件 | 已实现且有源码证据 | `.cnb.yml`（4034 字节） |
| PR 触发 | 已配置但未验证运行 | `.cnb.yml:26-50`：`pull_request` → `start-dependencies`（`make deps-start`）→ `make test` → `make test-race` |
| push 触发 | 已配置但未验证运行 | `.cnb.yml:52-84`：`push`（`main`）额外 `make build` → `make docker-build` → `make docker-push` |
| 测试命令 | 已配置但未验证运行 | `make test`、`make test-race`（复用仓库脚本，非另写命令） |
| 镜像构建与发布 | 已配置但未验证运行 | `scripts/docker-build.sh`（短 SHA + latest 双标签）、`scripts/docker-push.sh`；镜像名由 `CNB_DOCKER_REGISTRY`/`CNB_REPO_SLUG_LOWERCASE`/`CNB_COMMIT_SHORT` 派生 |
| 部署流程 | 未发现相关实现 | `.cnb.yml` 无 deploy/release/environment 步骤 |
| GitHub 镜像同步 | 已配置但未验证运行 | `.cnb.yml:86-123`，仅 `CNB_REPO_SLUG=framework-station/my-shop` 时执行 |
| 流水线实际结果 | 当前环境无法验证 | 本地无 CNB runner，未触发 |

## 5. Prometheus / 指标

| 项目 | 状态 | 证据 |
| --- | --- | --- |
| Metrics 定义 | 已实现且有源码证据 | `internal/metrics/metrics.go:44-56`：`flashsale_requests_total`、`flashsale_request_duration_seconds` |
| 注册位置 | 已实现且有源码证据 | `internal/metrics/metrics.go:58-60` `prometheus.MustRegister`（默认 registry） |
| 采集入口 | 已实现且有源码证据 | `internal/cmd/cmd.go:180-187` `GET /metrics`（仅 `flash_sale.metrics.enabled=true`）；`metrics.Handler()` 用 `promhttp.Handler()`（`metrics.go:90-92`） |
| 采集入口实际运行 | 已配置但未验证运行（生产） | 本地由 `TestFlashSaleV5MetricsEndpoint` 在测试进程内验证 200 + 指标文本（PASS）；完整 compose 栈未启动 |
| HTTP 请求指标（全站） | 未发现相关实现 | 无 metrics 中间件；`grep` 仅 `internal/metrics` 使用 prometheus |
| Redis 相关指标 | 未发现相关实现 | 无 Redis collector / pool stats 导出 |
| MySQL 相关指标 | 未发现相关实现 | 无 `DBStats()` 采集、无 SQL 耗时指标 |
| 消费者指标 | 已实现且有源码证据 | `ObserveConsume`（`metrics.go:81-87`），在 `consume.go:138`、`:167` 调用，`interface="consume"`，result ∈ success/failed/dead/error |
| 秒杀业务指标 | 已发现但不完整 | 有请求计数/耗时；**无** `sold`、`queued` 深度、`dead` 独立计数、活动×SKU 维度（标签故意低基数） |
| 高基数标签风险 | 已实现且有源码证据（低风险） | 标签仅 `interface`（2 值）与 `result`（7 值），无 user/activity/sku 标签（`metrics.go:44-56`）；`TestLabelSchemaLowCardinality` 覆盖（`metrics_test.go:108-149`） |
| recording rules | 已实现且有源码证据 | `prometheus/rules/flashsale_latency.yml:20-23`：p95/p99，`interface="order"`，`sum by (le)` |
| 抓取配置 | 已实现且有源码证据 | `prometheus/prometheus.yml:17-23`：`job_name=my-shop`、`metrics_path=/metrics`、target `app:8000`、15s |
| 抓取配置实际生效 | 已配置但未验证运行 | prometheus 容器未启动；`test-prometheus.sh` 仅静态/语法校验 |
| 指标与业务对账 | 当前环境无法验证 | 需要压测/生产数据对比 MySQL 订单数与 `requests_total` |

## 6. 与工程能力相关的既有脚本资产

| 资产 | 状态 | 说明 |
| --- | --- | --- |
| `scripts/flashsale-loadtest/`（`run.sh`、`test-run.sh`、`compare.sh`、`hotkeys.sh`、`make-summary.sh`） | 已实现且有源码证据，未执行 | 压测脚本存在（共 5 个），本任务**未运行**，不给出性能结论 |
| `scripts/build.sh` | 已实现且有源码证据，未执行 | 与 Dockerfile 一致的静态编译参数 |
| `scripts/migrate.sh` | 已实现且有源码证据，未执行 | 本任务未执行任何 Migration |
| `.env.example` | 已实现且有源码证据 | 仅 `FLASH_SALE_METRICS_ENABLED` 一个秒杀相关变量；限流/排队/熔断/扫描周期无示例变量 |
