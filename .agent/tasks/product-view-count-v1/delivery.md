# Delivery Verification

## Milestone and Target
- Milestone：商品浏览量计数（Product View Count）V1 核心闭环
- Delivery Target：`df272012781fa29120f5a1d5aec09cbde041d02b`（Cleaner CLEAN 的 immutable implementation commit）
- Cleaner Review Target：`df272012781fa29120f5a1d5aec09cbde041d02b`
- Target Match：YES（`review.target == owner.review_target == df27201...`）

## Environment
- OS：Linux（amd64）
- Go：go1.24.1 linux/amd64（Task 要求 Go 1.23+，满足）
- MySQL：mysql:8.0（Docker 容器 `my-shop-mysql`，healthy，监听 127.0.0.1:3306）
- Redis：redis:7-alpine（Docker 容器 `my-shop-redis`，healthy；本任务计数事实来源为单一 MySQL，Redis 不参与计数）
- Docker：29.6.2 / Docker Compose v5.3.1
- 配置来源：`manifest/config/config.yaml` 默认值 + 环境变量覆盖（本次无自定义覆盖）；不记录 Secret 值
- 数据库：`my_shop`；`schema_migrations` 最新 `version=20261001000012`、`dirty=0`
- 集成验证快照：`develop_base`（`origin/develop`）`d07ffd4d1e14c279fb6e4f1dda43d753f54a8d09` + `feature_head` `f5bfc76eff620514db047814feeffacb0cdbfcef`
- 测试数据与清理：冒烟测试直接向 MySQL 插入 `delivery-smoke-*` 商品（on_shelf id=5 / off_shelf id=6），验证后已 `DELETE` 清理，`products` 恢复为 0 行

## Verification

| Check | Result | Evidence |
|---|---|---|
| Gate `delivery-start` | PASS | `go run ./cmd/workflow-check gate delivery-start .agent/tasks/product-view-count-v1` → `PASS ... gate=delivery-start`（exit 0） |
| Build `go build ./...` | PASS | exit 0；另经 `scripts/up.sh` 产出 `/workspace/bin/my-shop` 可执行二进制 |
| `go vet ./...` | PASS | exit 0 |
| `gofmt -l`（5 个变更文件） | PASS | 无输出（已格式化） |
| Unit/Integration `go test -p 1 ./...` | PASS | 全包通过（`internal/migrations` 13.775s、`internal/controller/product` 7.950s 等，无失败包） |
| Race `go test ./internal/controller/product/ -race -count=1`（3 个视图计数测试） | PASS | `TestProductViewCount`(3.64s)/`TestProductViewCountNoWriteOn404`(1.73s)/`TestProductViewCountConcurrent`(2.13s) 均 `--- PASS`，`-race` 无数据竞争 |
| 服务启动与健康 | PASS | `bash scripts/up.sh` 完成 build→migrate→serve；`GET /health` → `{"code":0,"data":{"status":"ok"}}` |
| 迁移（AC-006，幂等） | PASS | `products.view_count` = `bigint unsigned NOT NULL DEFAULT 0`；`schema_migrations version=20261001000012 dirty=0`；重复 `./bin/my-shop migrate up` → `没有待执行的 migration` |
| Core Flow（计数 +1 / 详情语义不变） | PASS | 实时 HTTP：第 1 次 `GET /products/5` → 200 且 `view_count=1`；第 2 次 → `view_count=2`；`updated_at` 保持 `2026-10-06 09:48:57` 不变 |
| 并发计数不丢失（AC-002 / INV-001） | PASS | 50 并发 `GET /products/5`（xargs -P 20）全部 200；DB 最终 `view_count=52`（= 2 顺序 + 50 并发），无丢失 |
| 404 无写入（AC-005 / INV-002） | PASS | `GET /products/6`（off_shelf）→ 404 `code=4001`，DB `view_count=0` 无写入；`GET /products/999999`（不存在）→ 404 |
| 展示位置（AC-004 / INV-004） | PASS | 前台列表 `GET /products`、后台详情 `GET /admin/products/5`、后台列表 `GET /admin/products` 均含 `view_count`；后台详情访问后 `view_count` 仍为 52（不计数） |
| `updated_at` 保护（INV-003） | PASS | 全部浏览/并发/后台访问后 `products.updated_at` 始终为 `2026-10-06 09:48:57`，未被浏览刷新 |
| 长期设计（AC-007） | PASS | `docs/design/product.md` §2.1（数据模型）、§2.4（浏览量计数语义/触发/展示/写入/失败/并发）、§5（可见性+计数）与 APPROVED Contract 及最终实现一致 |
| 全局资源授权 | PASS | `origin/develop:.agent/registry/migrations.md` 已登记 `20261001000012 | product_view_count | product-view-count-v1 | RESERVED` |

## Acceptance Evidence

- AC-001（计数触发 +1 且详情语义不变）：实时 `GET /products/5` 两次，`view_count` 1→2，响应正常 200。
- AC-002（并发计数不丢失）：50 并发全部 200，DB 最终 `view_count=52`（初始 0 + 2 顺序 + 50 并发），无 lost update；独立 `-race` 集成测试亦通过。
- AC-003（详情展示最新累计值）：详情响应 `view_count=52`，为最新累计值。
- AC-004（展示位置符合 Owner 确认语义）：`view_count` 出现在前台详情/列表、后台详情/列表响应中；后台访问不触发计数。
- AC-005（不存在/非上架 404 且无写入）：off_shelf（id=6）与不存在（999999）均 404 `code=4001`，`view_count` 无写入。
- AC-006（数据模型与迁移、幂等）：`view_count BIGINT UNSIGNED NOT NULL DEFAULT 0`；`schema_migrations=20261001000012 dirty=0`；重复 migrate 无待执行。
- AC-007（长期设计一致）：`docs/design/product.md` 已沉淀且与 Contract/实现一致。
- INV-001（并发正确）：单条原子自增，50 并发不丢失。
- INV-002（仅 on_shelf 计数、404 无写入）：off_shelf/不存在 404 且无写入。
- INV-003（计数不改 updated_at）：全程 `updated_at` 未变。
- INV-004（后台不计数）：后台详情访问后 `view_count` 未增加。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 重启/恢复/回滚演练 | Task/Contract 未要求；本任务为单表同步计数、无跨系统事务/异步，无恢复语义可演练 | 无（超出里程碑范围） |
| 性能压测（QPS/P95/P99） | 非性能任务；计数写放大为已知风险（Open Risks 已记录），V1 接受 | 无（超出里程碑范围） |

## Remaining Risks

- 写放大：计数为同步 UPDATE 落在前台详情热读路径，高并发热点商品存在行锁竞争（Contract Open Risks 已声明，V1 接受）。
- 计数非幂等：客户端重试会重复计数（总浏览量语义下预期，去重 Out of Scope）。
- 以上均非本次交付阻塞项。

## Result

PASS
