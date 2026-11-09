# Delivery Verification

## Milestone and Target

- Milestone：商品评价（Product Review）V1 核心闭环（提交/资格校验/服务端归属/唯一去重/公开列表与汇总/归属隔离/管理员下架/非法输入拒绝/迁移与长期设计）。
- Delivery Target：`237663eb5d6658fea2c4201fc6062a431ce40d14`（分支 `feat/comment`，HEAD）。该提交 = 最终实现 `c713729`（`feat(review): implement product review V1 core flow`）+ 仅新增 `.agent/tasks/product-review-v1/` 任务工件，无生产代码差异。
- Cleaner Review Target：`review.target_base = 237663eb5d6658fea2c4201fc6062a431ce40d14`，`review.target_paths` 共 15 文件（含 `docs/design/review.md`）。
- Target Match：YES（当前 HEAD 与 Cleaner Review Target 一致；`git diff --stat c713729..HEAD -- ':(exclude).agent/**'` 为空）。

## Environment

- OS：Linux（容器，x86_64）。
- 运行时：Go 1.24.1 linux/amd64。
- MySQL：8.0（容器 `my-shop-mysql`，healthy，`127.0.0.1:3306`，库 `my_shop`）。
- Redis：7-alpine（容器 `my-shop-redis`，healthy，`127.0.0.1:6379`）。
- Docker Compose：`docker-compose.yml`（mysql + redis）。
- Migration 版本：`20261001000009`（latest，dirty=0）。
- 配置来源：`manifest/config/config.yaml`（开发默认值）；Smoke 额外经环境变量 `ADMIN_SUPER_PASSWORD` 注入超级管理员初始密码（值不记录）。
- 版本标识：commit `237663e`（HEAD）；Registry Authority `origin/develop = 45e2df2`（错误码域 `10000-10999`、migration `20261001000009` 均 `RESERVED`，owner `product-review-v1`）。
- 测试数据与隔离：使用开发 MySQL 容器；Smoke 前清理 admins/RBAC/reviews/order_items/orders 表，以唯一时间戳命名重建数据（分类/商品/SKU/买家/订单/评价），未触碰生产环境。

## Verification

| Check | Result | Evidence |
|---|---|---|
| 正式构建 `go build ./...` | PASS | exit 0（另 `go build -o` 产出可执行二进制成功） |
| 静态检查 `go vet ./...` | PASS | exit 0 |
| 完整测试 `go test -p 1 ./...` | PASS | 全部包 `ok`，exit 0（含 `internal/cmd` 18.4s、`internal/migrations` 14.9s、`internal/boot` 4.8s，走真实 MySQL/Redis 与真实路由+Auth） |
| 并发去重 `-race` | PASS | `go test -p 1 ./internal/cmd -run 'TestReviewConcurrentDuplicateSingleRow\|TestReviewDuplicateRejected' -count=1 -race` → `ok`（8 并发仅 1 成功） |
| 服务启动 + 健康检查 + 配置加载 | PASS | 独立二进制 `serve` 启动日志：dependencies reachable(mysql,redis) → schema 已就绪(20261001000009) → 超级管理员已创建 → 权限 seed 已就绪 → listening :8000；`GET /health` → `{"code":0,"data":{"status":"ok"}}` |
| 核心 API Smoke（真实 HTTP） | PASS | 见下「Acceptance Evidence」；最终 DB 核对一致 |
| MySQL/Redis 真实交互 | PASS | 迁移/seed/订单状态机/评价写入均落 MySQL；登录会话经 Redis（Auth 校验）；`reviews` 唯一约束 `uk_order_item(order_item_id)` Non_unique=0 生效 |
| 最终数据核对 | PASS | `reviews` 仅 1 行（id=11, user_id=4, order_item_id=3, product_id=4, sku_id=4, rating=5, status=1=published）；`orders` status=50(completed)；重复提交未产生第二条 |

## Acceptance Evidence

- AC-001/INV-003（购买资格）：Smoke 中订单未完成时提交评价 → `10002`（409）；支付→发货→收货（completed）后提交 → 200，评价归属 `user_id=4` 正确。
- AC-002/INV-003（资格拒绝）：未完成订单项提交 → `10002`；未登录提交 → `1002`（401），均无写入。
- AC-003/INV-001（服务端归属绑定）：集成测试 `TestReviewServerDerivesOwnership` 通过（伪造 `user_id/product_id/sku_id` 被忽略，以 Principal 与订单项为准）；Smoke 中 `product_id/sku_id` 亦为服务端推导（=4/4）。
- AC-004/INV-002（每个已购项最多一次）：Smoke 重复提交 → `10003`（409）；`-race` 并发测试 8 并发仅 1 成功；DB `uk_order_item` 唯一兜底。
- AC-005/INV-005/INV-006（公开列表与汇总）：Smoke 提交后 `GET /products/4/reviews` → `count=1, avg=5, total=1`；集成测试 `TestReviewUpdateAndDelete` 验证修改/删除后 avg/count 正确回退。
- AC-006/INV-004（归属隔离）：集成测试 `TestReviewUserIsolation` 通过（他人 PUT/DELETE/不存在 → 404/10001 且无写入）。
- AC-007/INV-007（管理员下架）：集成测试 `TestReviewAdminTakeDownPermission` 通过（无权限/普通用户 403，超管下架后评价从公开列表消失）。
- AC-008（非法输入拒绝）：集成测试 `TestReviewInvalidInputRejected` 通过（星级越界/内容空或超长/缺 order_item_id → 400，无写入）。
- AC-009（数据模型与迁移）：迁移版本 `20261001000009`，`reviews` 表存在且结构正确（含 `uk_order_item`/`idx_user_id`/`idx_product_status`）；`internal/migrations` 测试通过。
- AC-010（长期设计）：`docs/design/review.md` 已纳入 Cleaner Review Target（Design Impact = NEW 已确认纳入，不重复 Design 审查）。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 重启/恢复/回滚演练 | Task/Contract 未要求（评价为同步写、无异步/无 MQ） | 无 |
| 性能压测（并发数/QPS/P95 等） | 非性能任务 | 无 |

## Remaining Risks

- Cleaner 遗留 P3（非阻塞，OPEN）：`contract.md` §Global Resource Reservation 第 153 行错误码域 owner 旧措辞 `review`（Registry 已统一为 `product-review-v1`）。不影响运行与交付，由 Analyst/Owner 决定是否对齐措辞。
- 公开评价 `user_id` 直接暴露、不 join 用户昵称（Contract §Open Risks，V1 Scope 外）。
- `image_urls` 预留列本次不实现上传，恒为 NULL（V1 Scope 外）。

## Result

PASS
