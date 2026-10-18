# Cleaner Findings

## Review Target

- Task：`backend-container-v1`（GoFrame 后端容器化）
- 任务基线（Base）：`3ad322992663da9129aa95de8c64b4ab8bba6801`（分支 `feat/backend-container`，任务开始时 working tree clean、无已有修改）
- 分支：`feat/backend-container`
- Contract：APPROVED，`state.contract.target = 3be44598b5590f3be748a77c20a89fc4db143374`（A1）
- Design Artifact：`docs/design/deployment.md`（Design Impact = NEW）
- **被审查对象 C1（implementation Evidence Commit）**：`42a49efcbf41b434c9fd1407a4059c4212f59e9d`
- C2（Coder 的 review-request metadata commit，不审）：`c721eff77f35fe081df7569ed736a2e7a95586c2`
- 审查时 working tree clean，local HEAD == C2；`review.target` 保持 C1 不变。

任务前已有修改的区分方式：任务基线声明「无」，且从 Base 到 C1 的 diff 仅含本任务相关文件（Dockerfile/.dockerignore/docker-compose.yml/prometheus.yml/scripts/*/Makefile/README/docs/design/deployment.md/manifest/config/config.yaml/env.example，以及唯一 `internal/` 内文件 `internal/boot/boot_test.go`——仅测试写法 `genv.Set/Remove`→`t.Setenv`，无业务逻辑改动）。

全局资源：本任务无 Shared Resource 预留（`state.resources.reservations = {}`）；diff 中无 `internal/migrations/*`、`internal/codes/*` 变更，无新增 migration / 错误码域，Registry↔Contract↔实现三边一致。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `Dockerfile` 多阶段（`golang:1.23-alpine` builder `CGO_ENABLED=0` → `alpine:3.20`）；`docker run --rm my-shop-app:local` 检查镜像内无 `.env`、`config.yaml` 敏感字段（`secret/pass/password/access_key/secret_key`）均为 `""`；`git grep` 未在受跟踪树发现七牛 AK / 非空 `secret_key`。 |
| AC-002 | PASS | `.dockerignore` 排除 `.env`/`.env.*`（`!.env.example` 例外）/`bin/`/`tmp/`/`storage/`；`scripts/test-container.sh` 静态断言通过。 |
| AC-003 | PASS | 镜像含 `manifest/config/config.yaml`（`dashboard.timezone: Asia/Shanghai`）；镜像含 `tzdata`（`/usr/share/zoneinfo/Asia/Shanghai` 存在）；应用以默认配置启动成功（`databaseTimeZoneExtra` 无 DST 拒绝）。 |
| AC-004 | PASS | `docker compose config -q` 退出码 0；`app` 服务 `environment` 指向 `mysql`/`redis` DNS 名，含 `healthcheck`、`app_storage` 卷、`depends_on: condition: service_healthy`。 |
| AC-005 | PASS | Prometheus `scrapeUrl` 实测为 `http://app:8000/metrics`，`app:8000` 为稳定 Compose 内部入口（Prometheus 与 app 同网络）。 |
| AC-006 | PASS | entrypoint `set -e` + `migrate up`（失败非零退出、不 exec serve）→ `exec serve`（Bootstrap：`waitForDependencies` 30s 重试 + `checkSchemaReady` dirty/未迁移 fail-fast + 七牛 `ValidateConfig`）；app 仅在 bootstrap 全部通过后才监听端口。结构 + 静态断言 + 运行观察（app healthy 即代表 schema 已就绪）支持。破坏性「MySQL 未就绪」场景未独立复跑，机制由 `depends_on`+entrypoint+serve fail-fast 覆盖。 |
| AC-007 | PASS | 多次 `up`/`down` 重建后 `schema_migrations` 仍 1 行（version `20261001000019`，dirty=0）、`admins` 超管仍 1 条、`permissions` 43 条；`migrate up` 幂等 + golang-migrate `GET_LOCK` + 唯一约束兜底。 |
| AC-008 | PASS | `docker compose down`（保留卷）→ `up -d --build` 后，`app_storage` 中 banner 占位图 + 测试 marker 文件、MySQL `schema_migrations`/`admins` 数据均在，app 重建后 healthy。 |
| AC-009 | PASS | `FLASH_SALE_METRICS_ENABLED=true` 启动后 `/metrics` HTTP 200，含 `flashsale_requests_total{interface="order",result="gate_rejected"} 1` 与 `flashsale_request_duration_seconds` histogram；Prometheus Targets `http://app:8000/metrics` → `up`（lastError none）。关闭时 `/metrics` 返回 404（不暴露），行为不变。 |
| AC-010 | PASS | named volume `app_storage:/app/storage`；`docker exec` 写入 marker 文件 → `down`+`up` 重建后文件与 banner 占位图仍在。 |
| AC-011 | PASS | `make up/down/status/logs/health` 均走 Compose；`lib.sh` 已移除 `nohup`/pid 宿主机进程管理；`make health` 四服务 healthy、`make status` 仅 Compose 容器、无宿主机进程双启动。 |
| AC-012 | PASS | `README.md` 与 `docs/design/deployment.md` 与实现一致（两阶段初始化、entrypoint migrate→serve、凭据分离、持久化、健康检查、Prometheus `app:8000`），无 Nginx/K8s 提前宣称。 |
| AC-013 | PASS | 真实 Compose 环境：migration 执行（`schema_migrations` 1 行）、`/health` 返回 `status=ok`、七牛 `ValidateConfig` 通过（真实 AK/SK/bucket 注入）、前台 register/login 签发 JWT、秒杀下单 API 返回业务错误 `12001 秒杀活动不存在`（链路连通、无回归）。 |
| AC-014 | PASS | `go build ./...`、`go vet ./...`、`go test -count=1 -p 1 ./...` 全通过（fresh，非 cache）；静态脚本 `test-container.sh`/`test-prometheus.sh`/`test-lib.sh` 通过（promtool 因本机无 prometheus 镜像而跳过 recording-rules 语法校验，静态断言仍执行）。 |
| AC-015 | PASS | `docs/design/deployment.md` 已落地，记录服务编排、镜像/配置（凭据分离）、启动生命周期、持久化、网络与健康检查语义，与最终实现一致。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `docker compose config -q` | PASS | 退出码 0 |
| `bash scripts/test-container.sh` | PASS | 凭据分离/构建上下文/镜像/生命周期/编排静态断言通过 |
| `bash scripts/test-prometheus.sh` | PASS | 抓取目标 `app:8000`/recording rules 静态断言通过（promtool 跳过） |
| `bash scripts/test-lib.sh` | PASS | 空值不覆盖默认等语义 |
| `go build ./...` | PASS | 退出码 0 |
| `go vet ./...` | PASS | 退出码 0 |
| `go test -count=1 -p 1 ./...` | PASS | fresh 全量通过（`DATABASE_DEFAULT_PASS=root` 注入；app 容器停止以释放 8000） |
| 镜像凭据扫描 | PASS | 镜像内无 `.env`，`config.yaml` 敏感字段全空；受跟踪树无七牛 AK / 非空 secret_key；`.env` 在 `.gitignore` 且未被跟踪 |
| 运行健康 | PASS | app/mysql/redis/prometheus 四容器 `(healthy)` |
| `/health` | PASS | `{"code":0,...,"status":"ok"}` |
| `/metrics`（开启后） | PASS | HTTP 200，含 `flashsale_requests_total` / `flashsale_request_duration_seconds` |
| Prometheus target | PASS | `http://app:8000/metrics` → `up`（metrics 开启时） |
| JWT + 业务 API | PASS | register→login 签发 token → 秒杀下单返回业务错误码（链路正常） |
| 持久化重建 | PASS | `down`+`up --build` 后 marker/banner/MySQL 数据均在 |
| 幂等 | PASS | `schema_migrations`=1、超管=1 经多次 up/down 不重复 |
| Mutation 隔离验证 | PASS | 在 `/tmp` 隔离副本中：移除 entrypoint `migrate up` → test-container.sh 非零退出；config.yaml 写回 dev JWT → test-container.sh 非零退出（两者均正确捕获） |

## Findings

无 P0/P1/P2 阻塞问题。以下为两条 P3（低风险、由 Owner 决定是否处理，均不阻塞 CLEAN）：

### CLEAN-001：`make test` 与 `make up` 并存时 health 测试端口冲突（P3）

- Severity：P3
- Status：OPEN
- Location：`internal/controller/health/health_test.go`（`TestLivenessHTTP` 经 `g.Server(guid.S()).Start()` 绑定默认 `:8000`）
- AC / Invariant：AC-014（`go test -p 1 ./...` 通过）
- Trigger：先 `make up`（app 容器映射宿主机 8000），再 `make test` / `go test ./...`
- Actual：`internal/controller/health` FAIL：`listen tcp :8000: bind: address already in use`
- Expected：测试在「依赖已就绪、app 未运行（8000 空闲）」下通过（README 推荐 `make init` → `make test` → `make up`）
- Impact：开发者误在 app 运行时跑测试会得到 false-negative；属工作流易用性，非正确性缺陷。既有测试的端口敏感，非本次引入（宿主机进程时代同样存在）。
- Evidence：app 运行中执行 `go test -p 1 ./...` 复现该 FAIL；停止 app 后 `go test -count=1 -p 1 ./...` 全通过。
- Required Fix Boundary：可在 README「构建与测试」补一句「`make test` 需 8000 空闲（可先 `make down` 或仅 `make init` 起依赖）」，或让 health 测试绑定随机端口；不强制，交 Owner 决定。

### CLEAN-002：`internal/boot/boot.go` 仍保留 `defaultDBPass="root"` 硬编码回退（P3）

- Severity：P3
- Status：OPEN
- Location：`internal/boot/boot.go`（`defaultDBPass = "root"`）
- AC / Invariant：Contract D5 / INV-003（敏感凭据不固化、不回退公开默认密码）
- Trigger：`manifest/config/config.yaml` 若移除 `database.default.pass` 键（当前显式为 `pass: ""`，不会触发）
- Actual：`cfgString(ctx, "database.default.pass", defaultDBPass)` 在键缺失时回退 `"root"`
- Expected：与 Contract「不回退公开默认密码」精神一致，敏感字段不应有硬编码回退默认
- Impact：潜在静默回退公开密码；当前因 config.yaml 显式空值 + 镜像始终 COPY 该文件而实际不触发（死代码），且为既有代码、本次未改动
- Evidence：`internal/boot/boot.go:25` `defaultDBPass = "root"`；运行验证 app 经 compose 注入 `DATABASE_DEFAULT_PASS` 连接，未回退。
- Required Fix Boundary：可将 `defaultDBPass` 改为 `""` 或显式 fail-fast；非本任务 Scope，交 Owner 决定是否另开任务。
