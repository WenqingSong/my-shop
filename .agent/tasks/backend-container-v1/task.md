# Task: Backend Container V1 — GoFrame 后端容器化

## Goal

将 SurgeCart GoFrame 后端容器化并接入现有 Docker Compose，在 CNB DinD 云开发环境中实现 MySQL、Redis、Prometheus 与 GoFrame 后端的统一启动、运行、停止和验证；不改变现有业务行为，并为后续独立的 Nginx V1 任务提供稳定的 `app:8000` 服务入口。

## Scope

- 新增后端多阶段 Dockerfile 与 `.dockerignore`，镜像内包含运行所需 GoFrame 配置与时区，且不写入任何密码、Token、七牛密钥。
- 在 `docker-compose.yml` 新增 `app` 服务，接入现有 MySQL、Redis、Prometheus，服务间使用 Compose DNS 名通信，配置环境变量、健康检查、持久化卷与启动依赖。
- 保持现有 Migration、配置校验、管理员初始化、七牛云依赖检查语义；数据库未就绪时不错误启动；启动幂等、不重复/并发初始化；容器重启、失败退出、重建行为明确。
- 将 Prometheus 抓取目标适配容器网络，确保 `/metrics` 可抓取，秒杀监控指标行为不变。
- 核实本地文件存储场景并配置持久化卷，容器重建不丢失。
- 适配 Makefile 与 `scripts/*` 启动/停止/状态/日志命令，明确原宿主机启动方式的保留或替代方案，避免宿主机进程与 Compose app 双启动。
- 更新 README 与部署文档（部署架构、Compose 服务说明、CNB DinD 启动步骤、环境变量与凭据、构建/启动/停止/日志/重建命令、Migration 与初始化、`/health`/`/metrics` 验证、持久化与排查）。
- 沉淀容器化部署模型到项目级长期设计 `docs/design/deployment.md`。

## Out of Scope

- Nginx 反向代理、Kubernetes / Ingress、Vue 前端接入、HTTPS / TLS。
- 多实例部署与负载均衡。
- 修改秒杀、订单、IAM 等业务逻辑。
- 新增不必要的数据库 migration 与错误码域。
- 提前宣称 Nginx 或 K8s 已部署。

## Milestone

Milestone: backend-container-v1

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/deployment.md

## Acceptance Criteria

- [ ] AC-001：给定 Go 1.23 源码与 `go.mod`，`docker build` 能成功构建后端多阶段镜像；最终镜像层与构建产物中不包含任何凭据（`.env`、AK/SK、JWT secret、超管密码、七牛密钥）。
- [ ] AC-002：`.dockerignore` 生效，构建上下文不包含 `.env`、`bin/`、`tmp/`、`storage/` 运行数据等敏感/无关文件。
- [ ] AC-003：镜像内包含 GoFrame 配置，容器以默认配置可读 `manifest/config/config.yaml`，`dashboard.timezone`（Asia/Shanghai）等时区语义生效。
- [ ] AC-004：`docker compose config` 校验通过；`app` 服务与 mysql、redis、prometheus 通过 Compose DNS 名（`mysql`、`redis`）通信，配置了健康检查、持久化卷与启动依赖。
- [ ] AC-005：`app:8000` 作为稳定的 Compose 内部服务入口可被其他服务（含 Prometheus）访问。
- [ ] AC-006：MySQL 未就绪或 schema 未就绪（未迁移 / dirty）时，应用不会以对外可用的业务服务错误启动，而是重试后以明确错误退出或保持不健康。
- [ ] AC-007：启动流程幂等：重复 `up`、容器重启不会重复执行 migration、不会重复创建超级管理员、不会并发执行初始化。
- [ ] AC-008：容器重启、失败退出、`down`+`up` 重建行为明确，持久数据不因重建丢失。
- [ ] AC-009：Prometheus 抓取目标适配容器网络，`app` 目标为 UP，`/metrics` 可抓取到真实秒杀指标（`flashsale_*`），现有监控指标行为不变。
- [ ] AC-010：需要持久化的本地文件（如轮播图存储）配置了 Volume，容器重建后已有文件不丢失。
- [ ] AC-011：`make up` / `down` / `status` / `logs` 等开发命令适配容器化，启动/停止/状态/日志查看可用，且不会同时启动宿主机进程与 Compose app 造成双启动。
- [ ] AC-012：README 与部署文档与真实实现一致，包含部署架构图、Compose 服务说明、CNB DinD 启动步骤、环境变量与敏感凭证说明、常用命令、Migration/初始化流程、`/health`/`/metrics` 验证、持久化与排查，且不宣称 Nginx / K8s 已部署。
- [ ] AC-013：容器环境集成验证通过：GoFrame 容器正常连接 MySQL、Redis，migration 正常执行，`/health` 返回正常，JWT 认证与关键业务 API 无回归。
- [ ] AC-014：`go build ./...`、`go vet ./...`、`go test -p 1 ./...` 通过；交付时区分真实执行通过的验证与因环境不具备而未验证（`NOT_VERIFIED`）的项目。
- [ ] AC-015：`docs/design/deployment.md` 落地，记录容器化部署模型（服务编排、启动生命周期、持久化、网络与健康检查语义）。

## Relevant Context

已核实事实：

- 当前后端以宿主机进程运行（`scripts/lib.sh` 中 `nohup $APP_BIN serve` + pid 文件），`docker-compose.yml` 只有 `mysql`/`redis`/`prometheus`，无 `app` 服务。
- `serve` 启动链路（`internal/cmd/cmd.go`）：`boot.Bootstrap`（配置 → JWT 密钥校验 → 依赖连通重试 → schema 就绪检查 → seed 超管 → seed 权限）→ `service.Upload().ValidateConfig`（七牛 required 依赖，fail-fast）→ 三个后台扫描器 → 轮播图存储 seed → metrics 路由 → `s.Run()`。
- migration 是显式命令（`my-shop migrate up`），`serve` 不自动执行，只读校验 schema 就绪（当前版本 < 最新 或 dirty 即 fail-fast），见 `internal/boot/boot.go`。
- 依赖连通检查：`waitForDependencies` 每 1 秒重试 MySQL PingMaster + Redis PING，超时 `STARTUP_DEPENDENCY_TIMEOUT`（默认 30s）。
- 环境变量命名：config 键 `.`→`_` 大写（如 `database.default.host`→`DATABASE_DEFAULT_HOST`）；`.env.example` 为完整清单，`scripts/lib.sh` 加载根目录 `.env`。
- 本地存储：`storage.local.root` 默认 `./storage`，当前仅 `configureBannerStorage` 幂等 seed `storage/banners/` 下 3 张占位图。
- Prometheus（`prometheus/prometheus.yml`）当前抓取 `host.docker.internal:8000`（宿主机进程），compose 用 `extra_hosts: host-gateway`；指标开关 `FLASH_SALE_METRICS_ENABLED=true` 才暴露 `GET /metrics`。
- 健康端点 `/health` 是 liveness（返回 `status=ok` + 时间），无依赖/readiness 语义。
- 无后端 Dockerfile、无 `.dockerignore`；Go 1.23（README 注明开发用 1.24）；module `cnb.cool/go-cloud-devops/my-shop`。
- 现有 `prometheus/Dockerfile` 注释说明：rootless/嵌套容器环境下 bind mount 单文件会退化为目录挂载。

Assumption：

- CNB DinD 环境支持 Docker 构建与 Compose 卷持久化；镜像构建与卷行为以真实环境验证为准。
- 容器化后仍以 `manifest/config/config.yaml` 为默认配置，通过环境变量覆盖连接 Compose DNS 名。

OPEN QUESTION：见「Analyst Questions」，均属设计选择，交由 Analyst 分析并由 Owner 决定，不在本任务预设方案。

## Verification

- AC-001 → `docker build` 成功；用 `docker history` / 解包镜像层检查不出现 `.env` 与凭据值（可配合扫描脚本）。
- AC-002 → `docker build` 时打印上下文文件清单，确认 `.env`/`bin`/`tmp`/`storage` 运行数据被 `.dockerignore` 排除。
- AC-003 → 容器内以默认配置启动，确认 config.yaml 可读、`dashboard.timezone` 无 DST 拒绝（Asia/Shanghai 生效）。
- AC-004 → `docker compose config` 退出码为 0 且无校验错误；`app` 服务环境变量指向 `mysql`/`redis` DNS 名。
- AC-005 → 在 compose 网络内（如 `docker compose exec` 或 Prometheus 容器）访问 `app:8000` 可达。
- AC-006 → 构造「MySQL 未就绪」「schema 未迁移」「dirty」场景，确认应用不以可用业务服务对外（重试后退出或健康检查为不健康）。
- AC-007 → 连续两次 `up` / 重启，核对 `schema_migrations` 无重复应用、超管账号仅一条、无并发初始化迹象。
- AC-008 → `down`（保留卷）+ `up` 重建后，核对 MySQL/Redis 数据与持久化文件仍在；失败退出场景进程非零退出。
- AC-009 → 开启 `FLASH_SALE_METRICS_ENABLED`，Prometheus Targets 页 `app` 为 UP，`/metrics` 含 `flashsale_*` 指标。
- AC-010 → 写入持久化目录文件 → 重建容器 → 确认文件仍在。
- AC-011 → 执行 `make up/down/status/logs`，确认行为正确且 `status` 不出现宿主机 App 进程与 Compose app 同时存在。
- AC-012 → 人工核对 README/部署文档与真实实现一致，无 Nginx/K8s 提前宣称。
- AC-013 → 真实 Docker Compose 环境走通：migration 执行、`/health` 返回 ok、JWT 登录与关键业务 API 无回归（真实 MySQL/Redis）。
- AC-014 → `go build ./...`、`go vet ./...`、`go test -p 1 ./...` 通过；环境不具备的项标 `NOT_VERIFIED` 并说明影响。
- AC-015 → 核对 `docs/design/deployment.md` 与最终实现一致（编排、启动生命周期、持久化、网络与健康检查语义）。

## Complexity

COMPLEX

原因：本任务涉及启动生命周期与部署模型的多种现实方案（migration 执行时机、依赖未就绪不启动的表达方式、健康检查与 readiness 语义、宿主机进程与容器双启动的取舍、Prometheus 抓取目标切换、本地文件持久化策略、CNB DinD 环境约束），不同方案会产生不同的可靠性、运维与兼容结果；且为新增 Deployment 模型（Design Impact = NEW）。按 Owner 约束需交由 Analyst 分析并由 Owner 决定。

## Analyst Questions

- migration 在容器中的执行时机与方式：现有「serve 不自动 migration」语义，在容器 entrypoint、一次性 compose 服务、或手动 `docker compose run` 之间如何选择，且不破坏「schema 未就绪不启动」与「幂等不并发」。
- 「数据库未就绪不启动」在容器中的表达：compose `depends_on: condition: service_healthy` 与应用内 `waitForDependencies` 重试如何组合；是否需要在 `/health`（liveness）之外引入 readiness 语义支撑健康检查与依赖编排。
- 启动前置校验（`qiniu check` / `admin check`）在容器内的形态：由 entrypoint 显式执行、还是复用 `serve` 自身 fail-fast（含超管 seed 与七牛 `ValidateConfig`），以及首次创建超管密码（`ADMIN_SUPER_PASSWORD`）缺失时的语义。
- Prometheus 抓取目标从 `host.docker.internal:8000` 切换到 Compose DNS `app:8000` 后，指标开关（`FLASH_SALE_METRICS_ENABLED`）在容器中的注入方式与抓取端口。
- 本地文件持久化取舍：轮播图占位图可由启动 seed 幂等重建，是否仍需为「容器重建不丢失」配置 Volume；Volume 的挂载方式在 rootless/嵌套容器环境的可行性。
- 开发脚本改造策略：`make up` 全面容器化后，原宿主机进程启动方式是移除还是保留为替代方案；如何从脚本层面杜绝双启动。
- CNB DinD 环境下的镜像构建、网络与卷持久化约束的核实方式（作为集成验证前提）。

## Review Baseline

- Base commit：`3ad322992663da9129aa95de8c64b4ab8bba6801`（分支 `feat/backend-container`，local == origin）。
- 任务开始时已有修改：无（working tree clean，无 staged / unstaged / untracked 变更）。
- 重叠修改的区分方式：本任务新增文件集中在根目录 `Dockerfile`、`.dockerignore`、`docker-compose.yml`、`scripts/*`、README 与 `docs/design/deployment.md`；Coder 不得改动 `internal/` 业务代码，除为容器化必需的最小配置接入外不触碰业务逻辑。

## Initial Route

交 Analyst（COMPLEX）。
