# Delivery Verification

## Milestone and Target

- Milestone：商品收藏（Product Favorite）V1 核心闭环（添加/取消/列表/是否已收藏 + 幂等去重 + 用户隔离 + 商品校验 + 迁移与长期设计）。`task.md` 无独立 `Milestone` 字段，按任务标题/Goal 读取。
- Delivery Target：HEAD `62f14da309f732f2f613acce9bea8cfeb1cb05b0`（分支 `feature/product-favorite`）。
- Cleaner Review Target：`review.target_base = e90fc5ae36e2f3e487618f2b0c4be75384b6c4aa`（CLEAN 绑定对象）。
- Target Match：YES（`git diff e90fc5a..HEAD --name-only` 仅 `.agent/tasks/product-favorite-v1/findings.md` 与 `state.yaml`，均为 Review-neutral 簿记，未触碰任何生产代码/测试/迁移/配置）。

## Environment

- OS：Linux（linux/amd64，容器）。
- 运行时：Go 1.24.1。
- Docker：29.6.2；Compose v5.3.1。
- MySQL：8.0（容器 `my-shop-mysql`，healthy，`127.0.0.1:3306`，库 `my_shop`）。
- Redis：7-alpine（容器 `my-shop-redis`，healthy，`127.0.0.1:6379`）。
- 无 MQ/Kafka（本任务事实来源单一 MySQL，Redis 仅会话，无异步）。
- 配置来源：`manifest/config/config.yaml` 开发默认值；Smoke 服务以 `SERVER_ADDRESS=:18080` 隔离端口启动，`ADMIN_SUPER_PASSWORD` 注入一次性测试值（值不记录）。
- 测试数据与隔离：Smoke 用独立注册用户（`smokeuser*`）+ 直接 SQL 注入唯一前缀 `smoke-*` 分类/商品；完成后已清理（favorites/products/categories/users 均归零）并停止隔离服务。

## Verification

| Check | Result | Evidence |
|---|---|---|
| 正式构建 `go build ./...` | PASS | exit 0，另 `go build -o /tmp/my-shop-smoke .` 产出可执行二进制成功 |
| 静态检查 `go vet ./...` | PASS | exit 0，无告警 |
| 格式 `gofmt -l`（涉及文件） | PASS | 空输出 |
| Registry 校验 `scripts/check-registry.sh` | PASS | 11001 落在 RESERVED 域 11000-11999；无重复/漂移 |
| 收藏集成测试（真实 MySQL/Redis） | PASS | `go test -count=1 -p 1 ./internal/cmd/ -run TestFavorite`：8 个测试全 PASS（4.1s） |
| 并发去重 `-race` | PASS | `TestFavoriteConcurrentDuplicateSingleRow` 无 data race，8 并发幂等仅 1 条 |
| 迁移/结构等价 | PASS | `./internal/migrations/ ./internal/boot/` 全 PASS；`TestSchemaStructureMatchesBaseline` 校验 favorites 结构 + `uk_user_product`；`latestMigrationVersion=20261001000010` |
| 全量测试 `go test ./...` | PASS（1 项 Out-of-Scope） | 除 `internal/controller/health` 外全 PASS；health 因 `:8000` 被遗留 `my-shop-deliver` 进程（pid 49969）占用 bind 失败，收藏未触碰 health |
| 服务启动 + 健康检查 | PASS | 独立二进制 `serve` 启动：dependencies reachable → schema 已就绪(20261001000010) → listening :18080；`GET /health` → `{"code":0,"status":"ok"}` |
| API Smoke 主链路 | PASS | 见下「Acceptance Evidence」；最终 DB 核对一致 |
| 拒绝结果 | PASS | 404/4001（不存在）、409/11001（off_shelf/draft）、400/1001（product_id=0）、401/1002（无/非法 token），均无写入 |
| 用户隔离 | PASS | 用户 B 列表空、check=false、取消 A 的收藏幂等成功但不删 A 记录（DB 仍 1 行、user_id=1） |
| 软引用/可用性标识 | PASS | 下架→`off_shelf`、删除→`product_deleted`（name 空/price null），收藏软引用保留 |

## Acceptance Evidence

关键 AC/INV → 本次真实 HTTP + DB 结果：

- AC-001/INV-001（添加收藏·登录+归属）：`POST /favorites` code=0，返回 item 含实时联查商品名/主图/价格与 `created_at`；DB `favorites` user_id=1、product_id 正确，列表可见。
- AC-002/INV-002（重复+并发唯一）：顺序重复收藏 code=0 且仍 1 条；`-race` 8 并发均幂等成功且仅 1 条（`uk_user_product` 兜底）。
- AC-003/INV-004（取消+幂等）：`DELETE /favorites/:id` code=0，DB 行数 1→0；取消未收藏商品幂等成功。
- AC-004（列表分页）：`total=1/page=1/size=20`，item 含实时联查商品信息与收藏时间。
- AC-005（是否已收藏）：收藏后 `favorited=true`，取消后 `favorited=false`。
- AC-006/INV-001（用户隔离）：B 列表 total=0、check=false、取消 A 的收藏幂等成功但不删除 A 记录。
- AC-007/INV-003（商品校验）：不存在 404/4001、off_shelf/draft 409/11001、product_id=0 400/1001，DB 0 行。
- AC-008（必须登录）：无 token/非法 token 401/1002，无数据返回。
- AC-009（数据模型与迁移）：迁移版本 `20261001000010`，`TestSchemaStructureMatchesBaseline`/`TestUpCreatesSchemaAndIsIdempotent` PASS。
- AC-010/INV-005（长期设计+软引用）：`docs/design/favorite.md` 已纳入 Cleaner Review Target 且与实现一致；下架/删除后收藏软引用保留并标识 `off_shelf`/`product_deleted`。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 重启/恢复/回滚演练 | Task/Contract 未要求（收藏为同步写、无异步/无 MQ） | 无 |
| 性能压测（并发/QPS/P95 等） | 非性能任务 | 无 |

## Remaining Risks

- 全量 `go test ./...` 唯一非 PASS 项为 `internal/controller/health` 的 `:8000` 端口冲突（遗留 `my-shop-deliver` 进程），属 Out-of-Scope 既有环境问题，与本次收藏交付无关；由 Owner 决定是否清理遗留进程。
- Registry 中错误码域 `11000-11999` 与 migration `20261001000010` 为 `RESERVED`（Reservation 已在共享 `develop` 生效、Feature 尚未合并进 `develop`），为预期中间态，合并时需同步为 `ACTIVE`。

## Result

PASS
