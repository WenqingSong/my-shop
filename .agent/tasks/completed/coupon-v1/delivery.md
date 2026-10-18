# Delivery Verification

## Milestone and Target
- Milestone：优惠券（Coupon）V1 核心闭环（管理员模板管理 + 用户领取/查询 + 普通订单用券与优惠计算 + Mock 支付核销 / 取消退券）
- Delivery Target（review.target）：`d4bbf7b0735674a8a0be80e3f0d2bd6c80feae86`（C1 实现证据 commit）
- Cleaner Review Target：`d4bbf7b0735674a8a0be80e3f0d2bd6c80feae86`
- Target Match：YES（`review.target` 之后仅 review/delivery-neutral Artifact 与 OwnerGate metadata，无生产代码实质变更，已用 `git diff --stat d4bbf7b..HEAD -- . ':!.agent/tasks/coupon-v1'` 核验为空）

## Delivery Snapshot（不可变 SHA）
- `review_target`：`d4bbf7b0735674a8a0be80e3f0d2bd6c80feae86`
- `feature_head`：`4441ebad3f6cccf2753de684dfd321eda3b780a0`（当前 `feat/coupon-v1` HEAD，local == origin/feat/coupon-v1）
- `develop_base`：`e2f82f620d5711ff15c2bae554e85b599213a9b7`（验证时 `origin/develop`）

## Environment
- OS：Linux（Docker 容器运行 MySQL/Redis）
- Go：go1.24.1 linux/amd64（满足任务 Go 1.23+ 要求）
- MySQL：8.0.46（`mysql:8.0` 容器，`my-shop-mysql`，healthy，端口 3306）
- Redis：7.4.11（`redis:7-alpine` 容器，`my-shop-redis`，healthy，端口 6379，`PONG`）
- Docker：29.6.2
- 迁移状态：`schema_migrations` 最新 version `20261001000022`，`dirty=0`；`coupon_templates`、`user_coupons` 两表已创建
- 配置来源：`manifest/config/config.yaml` 开发默认值（MySQL root/root、Redis 127.0.0.1:6379）；不记录任何 Secret
- 测试隔离：集成测试自建隔离（清空优惠券/订单域业务表，cleanup 再清空），未修改任何真实生产数据

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build（`go build ./...`） | PASS | 退出码 0，无错误 |
| Vet（`go vet ./...`） | PASS | 退出码 0，无错误 |
| gofmt（变更文件） | PASS | 退出码 0，无输出 |
| 全量回归（`go test -p 1 ./...`，真实 MySQL/Redis 串行） | PASS | 退出码 0；`internal/cmd` 80.273s、`internal/migrations` 20.438s、`internal/boot` 11.621s 等全部 ok |
| 并发正确性（`go test -race -run 'TestCoupon' ./internal/cmd/`） | PASS | `ok ... 58.856s`；并发领取/并发占券等走真实 MySQL 事务 + race |
| 真实外部依赖交互 | PASS | MySQL 8.0.46 可达、Redis 7.4.11 `PONG`；迁移已落到 `20261001000022` 且两表存在 |
| 全局资源授权（`origin/develop` Registry） | PASS | `19000-19999`（coupon-v1 RESERVED）、`20261001000021`（coupon RESERVED）、`20261001000022`（order_coupon_amounts RESERVED） |
| 出口 Gate（`delivery-start`） | PASS | `.agent/bin/workflow-check gate delivery-start .agent/tasks/coupon-v1` → `PASS`（exit 0） |

## Acceptance Evidence

本次独立执行 `go test -p 1 ./...`（全量，含 16 个 `TestCoupon*` 用例经真实路由 `RegisterFrontendRoutes` + `RegisterAdminRoutes` + `middleware.Auth` / `AdminAuth` / `RequirePermission` + 真实 MySQL/Redis）与 `go test -race -run 'TestCoupon'`，全部通过。关键 AC/INV 对应本次证据：

| AC / INV | 结果 | 对应测试（本次运行通过） |
|---|---|---|
| AC-001 / INV-C09 权限与模板管理 | PASS | `TestCouponAdminTemplateManagement`（超管建/查/停，无权限管理员与普通用户 403 且无写入） |
| AC-002/AC-003/AC-004/AC-005 | PASS | `TestCouponClaimAndListMine`、`TestCouponClaimRejections`（重复 19004、未开始/已结束/已停止 19005、总量满 19006） |
| AC-006 / INV-C01/C02 并发不超发/不重复 | PASS | `TestCouponConcurrentClaim`（-race，真实 MySQL 唯一约束 + 条件原子闸门） |
| AC-007 用户隔离 | PASS | `TestCouponUserIsolation`（他人券 19003 且无写入） |
| AC-008/AC-009 / INV-C06 门槛与金额计算 | PASS | `TestCouponOrderWithDiscount`、`TestCouponOrderThresholdNotMet`、`TestCouponOrderExpiredCoupon`（服务端算 original/discount/total，discount≤original） |
| AC-010 / INV-C07 无券兼容 | PASS | `TestCouponOrderNoCouponCompat`（无券 original=total、discount=0） |
| AC-011 快照不变 | PASS | `TestCouponOrderSnapshotStable`（改价/改模板后快照不变） |
| AC-012/AC-013 / INV-C04/C05 同事务预占、并发不重复占券 | PASS | `TestCouponOrderRollbackOnFailure`、`TestCouponConcurrentOrderSameCoupon`（-race，至多 1 单占用、失败不残留 locked） |
| AC-014 / INV-C10 幂等含 coupon_id | PASS | `TestCouponIdempotencyWithCoupon`（同键同券幂等、同键不同券 9006） |
| AC-015/AC-016/AC-017 / INV-C03/C08 核销/释放/不返券 | PASS | `TestCouponPayMarkUsed`、`TestCouponCancelRelease`、`TestCouponRefundNoReturn` |
| AC-018 并发无非法状态组合 | PASS | 状态联动均走条件更新 + RowsAffected；`-race` 并发用例通过 |
| AC-019 现有回归 | PASS | `go test -p 1 ./...` 全绿（含 order/dashboard/iam/rbac/migrations） |
| AC-020 多方一致 | PASS | Registry ↔ Contract ↔ 实现一致（错误码域 19000-19999、迁移 21/22、权限 `coupon:*`）；`docs/design/coupon.md` 已存在 |

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 独立 `serve` 进程启动 + `/health` HTTP Smoke | `serve` 对七牛云 `service.Upload().ValidateConfig` 做启动 fail-fast（需真实 AK/SK/bucket/domain + `GetBucketInfo`），本机无 `.env`/真实七牛凭据；该依赖属上传模块，与优惠券里程碑无关。启动等价性已由集成测试覆盖（`setupIsolationServer` 复用生产同款 `RegisterFrontendRoutes` + `RegisterAdminRoutes` + `boot` 就绪检查，走真实 MySQL/Redis） | 低——不影响优惠券核心闭环结论；`serve` 进程级启动与七牛可用性非本次里程碑范围 |

## Remaining Risks

- `CLEAN-001`（P3，OPEN）：「已使用（used 终态）」券再次下单缺直接回归用例；行为正确（与 locked 共用 `status=available` 闸门），不阻塞本次验证，是否补测由 Owner 决定。
- `docs/design/dashboard.md` 的 `today_order_amount` 口径已变更为「优惠后应付」（对用券订单绝对数值变小），为 Owner 已确认的语义收敛，非缺陷。

## Result

PASS
