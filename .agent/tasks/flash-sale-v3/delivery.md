# Delivery Verification

## Milestone and Target

- Milestone：秒杀 V3 异步下单闭环
- Delivery Target：feature `feat/flash-sale-v3` @ `fa7626fc4598b07a5edf6c5ded53d790315ac286`（feature_head）
- Cleaner Review Target：`8f0db1137d78505368f855830a15f0adf8833935`（C1'，Owner 已 ACCEPT）
- Target Match：YES（`8f0db11` 是 feature_head 的祖先，其后仅 review/delivery-neutral artifacts；生产代码自 CLEAN 后未发生变化）

## Environment

- OS：Linux（amd64）
- Go：`go1.24.1 linux/amd64`
- MySQL：`mysql:8.0`（docker `my-shop-mysql`，healthy，`127.0.0.1:3306`，`FOR UPDATE ... SKIP LOCKED` 可用）
- Redis：`redis:7-alpine`（docker `my-shop-redis`，healthy，`127.0.0.1:6379`）
- 无 MQ：队列载体为 MySQL 出队表 `flash_sale_order_requests`（与 Contract 一致）
- 配置来源：`manifest/config/config.yaml` 开发默认值；smoke 通过环境变量注入 `AUTH_JWT_SECRET` / `ADMIN_SUPER_PASSWORD`（均为本地一次性值，不记录 Secret 实际值）
- 数据隔离与清理：smoke 使用专用 fixture（category/product/sku id 90001~90003）与唯一用户名，验证后已删除产生的 activity/orders/requests/sku/product/category/users，Redis `FLUSHDB`，无残留

## Verification

| Check | Result | Evidence |
|---|---|---|
| 集成（develop_base + feature_head） | PASS | `git merge-tree` 无冲突；`origin/develop` 相对 merge-base 仅新增 Registry 一行（`20261001000015 flash_sale_order_requests RESERVED`），与 feature 代码零重叠 |
| 构建 gofmt | PASS | 改动 Go 文件 `gofmt -l` 输出为空 |
| 构建 go build ./... | PASS | exit 0 |
| 构建 go vet ./... | PASS | exit 0 |
| 构建二进制 | PASS | `go build -o /tmp/my-shop-deliverer .` 成功，产物可执行 |
| 迁移 | PASS | `migrate version` = `20261001000015 (dirty=false)`；`flash_sale_order_requests` 表结构、`uk_request_idempotency(user_id,idempotency_key)`、`idx_dequeue(status,next_attempt_at,id)` 与 Contract 一致 |
| 全量测试 | PASS | `go test -p 1 ./...` 全部 `ok`（含 `internal/cmd`、`internal/migrations`、`internal/logic/flashsale`） |
| 并发/不变量 race | PASS | `go test -race -run TestFlashSale ./internal/cmd/...` PASS（20 并发、stock=5，成功订单=sold=5 不超卖） |
| 重试/死信 race | PASS | `go test -race -run TestConsumeRetryThenDeadLetter ./internal/logic/flashsale/...` 及全包 race PASS |
| 独立进程启动 + 健康检查 | PASS | `/tmp/my-shop-deliverer serve` 真实启动：依赖连通、schema 就绪、超级管理员 seed、权限 seed、HTTP 监听 `:8010`；`GET /health` → `200` `{"code":0}` |
| 核心主链路 smoke（真实 HTTP） | PASS | 见下「Acceptance Evidence」：admin 建活动 → 用户注册/登录 → 下单入队 `queued` → 后台消费 → 结果查询 `success` + 订单 |
| 归属隔离（真实 HTTP） | PASS | 第二用户查询他人 `idempotency_key` → `404/1004`，无副作用 |
| 幂等（真实 HTTP） | PASS | 同键重提 → 返回 `success` 既有结果，订单数/sold 不变（1/1/1） |

## Acceptance Evidence

- AC-001（入队快速响应）：`POST /flash-sales/23/orders` → `200` `{"status":"queued"}`，同一时刻结果查询为 `queued`，未同步落单。
- AC-002（消费落单 + 不变量）：等待 3s 后台消费后结果查询返回 `status":"success"` + 订单（`flash_price=1000`、`quantity=1`）；最终 MySQL `orders=1`、`sold=1`（=total 10 内，不超卖）；race 并发用例证明 20 并发仅 5 单成交。
- AC-003（幂等/去重）：同键重提返回 `success` 既有结果，`orders/sold/requests` 保持 `1/1/1`，不重复建单、不重复扣减。
- AC-005（结果查询 + 归属隔离）：本人查询 `queued→success`；第二用户查询 → `404/1004`。
- AC-004（重试/死信）与 AC-006（失败补偿）与 AC-007（fail-closed 降级）：由全量测试 + race 覆盖（`TestConsumeRetryThenDeadLetter`、`TestFlashSaleV3Compensation`、`TestFlashSaleV3FailClosed`），本次运行全绿（见 Cleaner 亦已 CLEAN，Deliverer 独立重跑确认）。
- INV-001/002：最终 `sold=1 ≤ total_stock=10`，`COUNT(orders)=1 = sold`。
- INV-004：`uk_request_idempotency` 唯一约束 + 幂等标记，重提无重复业务效果。
- INV-007：越权查询 404 且无副作用。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 生产环境部署 | 未经 Owner 授权，不部署/修改生产 | 无（本里程碑为开发环境验收） |
| 死信修复运维接口 | 本任务 Out of Scope（仅运维 SQL/日志最小途径） | 已在 Contract Open Risks 记录，非阻塞 |
| Redis 持久化/AOF | 未配置，Redis 仅非权威闸门，中间态已落 MySQL | 不影响正确性（对账收敛） |

## Remaining Risks

- `FOR UPDATE SKIP LOCKED` 依赖 MySQL 8.0（部署版本需 Owner 确认，若 <8.0 需降级实现）。
- 死信修复途径本任务仅运维 SQL/日志最小满足。
- smoke 中 fixture 的 SKU/商品中文名出现乱码，系本次 seed 用 `docker exec mysql` 未指定 `--default-character-set=utf8mb4` 所致（seed fixture 侧问题），非交付物缺陷：经 admin HTTP API 创建的活动名中文正确存储，秒杀价/库存/订单/状态流转均正确。

## Result

PASS
