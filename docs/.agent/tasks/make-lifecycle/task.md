# Task: 统一项目生命周期入口（Makefile + scripts）

## Goal

建立统一的 `make` 生命周期入口，使开发者、Agent、CI 使用同一套命令管理项目的启动、停止、状态、健康检查、测试与清理，覆盖 help / bootstrap / up / down / restart / status / logs / health / test / clean。

## Scope

允许完成的内容：

- 顶层 `Makefile`，作为统一命令入口，提供 `help` 以及 `bootstrap / up / down / restart / status / logs / health / test / clean` 目标。
- 复杂逻辑下沉到 `scripts/` 目录（shell 脚本），Makefile 只做薄封装与转发。
- 配置与启动逻辑分离：依赖容器编排配置、服务启动配置、健康检查所需连接信息可复用现有 `docker-compose.yml` 与 `manifest/config/config.yaml` / 环境变量，不在脚本中硬编码。
- 启动相关操作幂等：`up` 可重复执行，不会因重复拉起而产生错误或重复容器/进程。
- 依赖就绪判断不使用固定 `sleep`，而是基于健康检查 / 探测（如 `docker compose` 的 `healthcheck`、`mysqladmin ping`、`redis-cli ping` 或等价轮询探测）。
- `make down` 只停止/移除容器与运行进程，不删除 MySQL / Redis 持久数据卷。
- `make health` 能够验证 App / MySQL / Redis 三者状态。
- `make test` 作为统一测试入口（可聚合 `go vet`、`go test` 等现有测试命令）。
- 敏感配置（密码等）不在 Makefile / scripts 中硬编码，复用现有环境变量注入机制。

## Out of Scope

明确本次不处理：

- 任何具体业务模块（商品、订单、用户、分类等）。
- 数据库表结构、迁移、种子数据。
- 缓存业务逻辑、认证鉴权、监控告警体系。
- MySQL / Redis 高可用、主从、集群。
- 生产部署、发布流水线、容器镜像打包与推送（本任务只覆盖本地开发/CI 生命周期命令）。
- 修改现有 Go 生产代码与测试（`internal/`、`api/` 等），除非仅为满足健康检查等命令所需且改动最小。

## Acceptance Criteria

- [ ] AC-001 在全新空白环境中，仅凭仓库内容执行 `make bootstrap` 即可进入可开发状态（依赖容器就绪、服务可启动），无需额外的本地手工步骤。
- [ ] AC-002 `make help` 能列出全部生命周期目标及简要说明，帮助新开发者/Agent 快速了解可用命令。
- [ ] AC-003 `make up` 可重复执行（幂等），重复执行不会报错、不会产生重复容器或重复进程。
- [ ] AC-004 `make down` 停止/移除运行中的容器与服务进程，但 MySQL、Redis 的持久数据卷不被删除，重新 `make up` 后数据仍可访问。
- [ ] AC-005 `make restart` 能先停止再启动，最终使服务恢复可用状态。
- [ ] AC-006 `make status` 能展示 App / MySQL / Redis 当前运行状态。
- [ ] AC-007 `make logs` 能查看服务（及/或依赖容器）日志。
- [ ] AC-008 `make health` 能分别验证 App / MySQL / Redis 三者是否健康，并输出可识别的健康/非健康结果（含任一组件异常时能明确反馈）。
- [ ] AC-009 `make test` 作为统一测试入口，能执行项目现有测试（`go test ./...` 等）并正确传递成功/失败结果。
- [ ] AC-010 `make clean` 能清理编译产物/临时产物，且不误删持久数据与仓库源码。
- [ ] AC-011 启动依赖就绪的判断不依赖固定 `sleep`，而是基于健康检查或探测结果（环境每日重置后仍能可靠拉起）。
- [ ] AC-012 配置与启动逻辑分离，Makefile 只作为统一入口，复杂逻辑位于 `scripts/`。
- [ ] AC-013 敏感配置（MySQL/Redis 密码等）不在 Makefile 与 scripts 中硬编码，通过环境变量注入。

## Relevant Context

- 技术栈：GoFrame v2（Go），依赖容器由 `docker-compose.yml` 编排（MySQL 8.0、Redis 7-alpine），各自已配置 `healthcheck`。
- 运行环境为 CNB DinD 临时开发环境，环境每天可能重建：容器关闭、数据卷重置。因此 `bootstrap` 必须能一键重建依赖环境。
- 现有 `docker-compose.yml` 已包含 MySQL/Redis 的健康检查（`mysqladmin ping` / `redis-cli ping`），可直接复用于“不用 sleep 判断依赖就绪”。
- 现有服务默认监听 `:8000`，健康检查接口为 `GET /health`（返回 `{"code":0,...}`）。
- 现有测试入口为 `go test ./...`，另有 `go vet ./...` 静态检查。
- 敏感配置通过环境变量注入，命名规则见 README（`SERVER_ADDRESS`、`DATABASE_DEFAULT_*`、`REDIS_DEFAULT_*`、`MYSQL_ROOT_PASSWORD` 等）。

Assumption:
- `up` 的含义为：先确保 MySQL/Redis 依赖容器就绪，再启动应用服务（应用以本地进程方式运行，如后台 `go run`/编译产物，或由 Coder 结合环境选择等价方式）。
- `health` 对 App 的验证基于现有 `GET /health` 接口；对 MySQL/Redis 的验证复用容器健康检查或等价探测命令。
- `make down` 不删除 `docker-compose.yml` 中声明的 `mysql_data` / `redis_data` 命名卷，即默认 `docker compose down`（不带 `-v`）。
- 应用服务作为本地进程而非容器运行（符合当前骨架为本地 `go run` 启动方式）；如未来需要容器化可另行扩展，本任务不引入应用容器镜像。

## Verification

建议至少执行：

- `make help` → 列出全部目标
- 全新环境 `make bootstrap` → 依赖容器就绪、可进入开发状态
- 连续两次 `make up` → 均成功、无报错、无重复容器
- `make status` → 正确展示 App/MySQL/Redis 状态
- `make health` → 返回 App/MySQL/Redis 三者健康
- `make logs` → 能查看日志
- `make test` → 执行 `go test ./...` 并通过
- `make restart` → 服务恢复可用
- `make down` → 容器/进程停止，但 `docker volume ls` 中 `mysql_data`/`redis_data` 仍存在
- `make clean` → 编译产物被清理，源码与持久数据不受影响

## Complexity

NORMAL

原因：

纯工程/开发工具链任务，不涉及业务逻辑、数据库模型、缓存一致性、并发、权限等复杂设计；方案单一且边界清晰（Makefile 薄封装 + scripts 下沉），无多个重大 trade-off。

## Status

READY_FOR_CODER
