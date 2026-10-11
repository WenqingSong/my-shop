# Delivery Verification

## Milestone and Target

- Milestone：backend-container-v1（GoFrame 后端容器化，接入 Docker Compose，CNB DinD 环境统一启动/停止/验证）
- Delivery Target：`de81327a4ede8137e2590888fac5328a7b47f45b`（feature `feat/backend-container` HEAD，local == origin）
- Cleaner Review Target：`42a49efcbf41b434c9fd1407a4059c4212f59e9d`（C1，实现 Evidence Commit）
- Target Match：YES（`review.target == owner.review_target == 42a49ef`；`feature_head` 为 review-neutral tail（findings/core-logic/owner-decision/state.yaml）叠加于 C1 之上，生产代码与 C1 完全一致）

## Environment

- OS：Linux（CNB DinD 容器化环境，Docker server 运行于 Alpine Linux 3.24）
- Docker：29.6.2（build dfc4efb）；Docker Compose：v5.3.1
- Go 工具链：go1.24.1 linux/amd64（本地 go build/vet/test）；镜像 builder `golang:1.23-alpine`、runtime `alpine:3.20`
- 依赖：MySQL 8.0、Redis 7-alpine、Prometheus（自建 `my-shop-prometheus:local`）
- 配置来源：根目录 `.env`（本地、已 gitignore，含七牛 AK/SK/bucket/domain、JWT secret、超管密码等真实值，值不记录）；Compose 变量替换与 `manifest/config/config.yaml` 默认值
- 隔离方式：所有破坏性场景（dirty / MySQL 不可达 / 冷启动迁移）均在临时数据库 `my_shop_dirty_verify` / `my_shop_cold_verify` 中验证，验证后已 DROP；主 dev 数据仅做幂等重建（down+up 保留卷），未破坏

## Verification

| Check | Result | Evidence |
|---|---|---|
| 静态断言（凭据分离/构建上下文/镜像模型/生命周期/编排/Prometheus） | PASS | `bash scripts/test-container.sh`、`test-prometheus.sh`、`test-lib.sh` 均 exit 0 |
| Compose 校验（AC-004） | PASS | `docker compose config -q` exit 0 |
| 全新构建（AC-001） | PASS | `docker build --no-cache -t my-shop-app:deliverer-verify .` exit 0（约 38s） |
| 凭据不落镜像/仓库（INV-003 / AC-001/002） | PASS | 镜像内 `config.yaml` 敏感字段（`pass`/`secret`/`password`/`access_key`/`secret_key`）全为 `""`；镜像内无 `.env`；受跟踪树无非空七牛 AK/SK 与历史 dev JWT 值（仅 `docker-compose.yml` 的 `${VAR:-default}` dev 默认值，符合 Contract D5）；`.env` 被 gitignore |
| 时区/默认配置（AC-003） | PASS | 镜像含 `/usr/share/zoneinfo/Asia/Shanghai` 与 `manifest/config/config.yaml`；`my-shop --help` 子命令正常 |
| 冷启动迁移（INV-002） | PASS | 空库 `my_shop_cold_verify` 经 entrypoint `migrate up` 建 33 表，`schema_migrations` = version 20261001000019、dirty=0 |
| dirty 不对外（INV-002 / AC-006） | PASS | 构造 `schema_migrations(version=1,dirty=1)` → `migrate up` 失败 exit 1，未打印「启动 HTTP 服务」 |
| MySQL 不可达不对外（INV-002 / AC-006） | PASS | `DATABASE_DEFAULT_HOST=nonexistent-db` → `migrate up` 连接失败 exit 1 |
| 启动幂等（INV-001 / AC-007） | PASS | down+up 重建后 `schema_migrations` 仍 1 行、超管仍 1 条、权限仍 43 条；日志「没有待执行的 migration」 |
| 持久化（INV-004 / AC-008/010） | PASS | down（保留卷）+ up 重建后 marker 文件与 banner 占位图仍在 |
| 监控指标（INV-005 / AC-009） | PASS | `FLASH_SALE_METRICS_ENABLED=true` 时 `/metrics` HTTP 200 含 `flashsale_requests_total{interface="order",result="gate_rejected"} 2` 与 histogram；Prometheus target `http://app:8000/metrics` → `up`、`lastError=""`；关闭时 `/metrics` 404 |
| app:8000 内部入口（AC-005） | PASS | Prometheus `scrapeUrl=http://app:8000/metrics`，同 Compose 网络可达 |
| JWT + 业务主链路（AC-013） | PASS | `POST /register` → `POST /login`（签发 JWT）→ `GET /me` 返回用户；无 token 访问 `/me` 401；`POST /flash-sales/999999/orders` 返回业务码 12001「秒杀活动不存在」；MySQL `users` 新增 `deliverer1791707942`，Redis 新增 `iam:session:*` |
| make 命令（AC-011） | PASS | `make status` 列出 4 容器 healthy、`make health` 四服务 healthy；`lib.sh` 无 nohup/pid 宿主机进程管理 |
| 文档一致（AC-012/015） | PASS | `README.md` 与 `docs/design/deployment.md` 与实现一致（编排/生命周期/凭据分离/持久化/健康检查/Prometheus `app:8000`），无 Nginx/K8s 提前宣称 |
| go build/vet/test（AC-014） | PASS | `go build ./...`、`go vet ./...` exit 0；`DATABASE_DEFAULT_PASS=root go test -count=1 -p 1 ./...` 全包 ok（app 已停止以释放 8000） |

## Acceptance Evidence

- INV-001（启动幂等不并发）：down+up 后 `schema_migrations`=1、超管=1、权限=43，migrate 日志「没有待执行的 migration」。
- INV-002（schema 未就绪不对外）：dirty → exit 1 不 serve；MySQL 不可达 → exit 1 不 serve；空库 → migrate 建表后才可用。
- INV-003（凭据不落镜像/仓库）：镜像内敏感字段全空、无 `.env`；受跟踪树无真实凭据；`.env` 未跟踪。
- INV-004（持久数据不因重建丢失）：down（保留卷）+ up 后 marker 文件与 banner 占位图仍在。
- INV-005（监控指标语义不变）：开启时 `/metrics` 含 `flashsale_*` 且 Prometheus `app` UP；关闭时 404。
- AC-013（容器集成验证）：真实 MySQL/Redis + 真实七牛凭据下 migration 执行、`/health` ok、JWT 登录与业务 API 无回归。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 恢复/回滚演练 | Task/Contract 未要求回滚演练 | 无（非交付要求） |
| 性能/压测 | 本任务为容器化部署模型，非性能任务 | 无（非交付要求） |
| 并发启动竞态（多实例同时 `up`） | 单机 Compose，非多实例部署（Out of Scope）；migrate 并发由 golang-migrate `GET_LOCK` + 唯一约束兜底（机制已由静态断言与幂等重建覆盖） | 低，机制覆盖 |

## Remaining Risks

- `CLEAN-001`（P3，OPEN）：`make test` 与 `make up` 并存时 health 测试端口冲突（8000）。本次已按「停止 app 再跑测试」规避并全量通过，不影响正确性。
- `CLEAN-002`（P3，OPEN）：`internal/boot/boot.go` 仍保留 `defaultDBPass="root"` 硬编码回退（当前 config.yaml 显式空值 + compose 注入，实为死代码）。属既有代码、非本任务引入，由 Owner 决定是否另开任务。
- 测试数据：验收在 dev MySQL 创建前台用户 `deliverer1791707942`（id=2）与对应 Redis 会话，未清理；已标记为本次验收数据，可随时删除。

## Result

PASS
