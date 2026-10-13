# Delivery Verification

## Milestone and Target

- Milestone：SurgeCart 历史数据迁移与集成验收（范围经 Owner 调整后：验证最新后端 + 最新 MySQL Schema 兼容性、迁移工具合成数据正确性、核心业务回归）
- Delivery Target（feature_head）：`e68b4d864dd9d4856568818e533a4c6fa166c42c`（当前 `origin/feat/db-migration` HEAD）
- Cleaner Review Target：`04350e374224db9a6c8eb1573aa327c6d45c987f`
- Develop Base：`657cd977ecae3f7b0e1baf17434a1aa819a7decd`（验证时 `origin/develop`）
- Target Match：YES（`feature_head` 相对 `review.target` 仅含 review-neutral 元数据提交，业务实现与 Cleaner 审查对象一致）

## Owner 范围调整（2026-10-12）

原始历史 `shopping.sql` 已被 Owner 删除、不再提供、不要求恢复。正式调整验收目标：**不再要求历史真实数据导入**，改为验证最新 SurgeCart 后端与当前最新 MySQL Schema 的兼容性、迁移工具的合成数据正确性以及核心业务回归。

- 原始 SQL 归档（AC-001 的归档动作）与真实历史数据导入（AC-013 的「真实导入」）**标记为 Owner 取消 / 不适用**，不得伪造 PASS。
- 原始历史数据的迁移能力**只能表述为「已通过合成数据验证」**，不能声称已完成真实历史数据迁移。
- 本次范围调整不改变生产代码、测试或核心约束（迁移映射规则、不变量不变），`review.target` 之后仅有 review-neutral 元数据提交，**CLEAN 快照仍有效**，无需重新审查。

## Environment

- OS：Linux（Debian 系）；Go 1.24.1 linux/amd64；Docker 29.6.2；MySQL 8.0（容器，healthy）；Redis 7-alpine（容器，healthy）
- 目标库：本次验收使用全新隔离库 `my_shop_delivery_test`（从零 Migration 到 `20261001000023`），验证后已删除；回归测试使用 `my_shop`
- 配置来源：七牛凭据经 `.env` 注入（`qiniu check` 通过）；`DATABASE_DEFAULT_PASS=root`（compose 开发默认）；JWT 密钥与超管密码为开发临时值（不记录 Secret 值）
- 测试数据：已脱敏合成数据（覆盖分类层级/同名不同父/软删除、商品单规格/多规格/软删除/下架/坏分类、金额精度、SKU 明细、库存去重、超长 URL 触发回滚）；验证后已清理

## Verification

| Check | Result | Evidence |
|---|---|---|
| 正式构建 `go build ./...` | PASS | exit 0 |
| 静态检查 `go vet ./...` | PASS | 无输出 |
| 全新库从零 Migration | PASS | `my_shop_delivery_test` 空库 `migrate up` → version `20261001000023`、dirty=0、38 张表全就绪 |
| Schema 结构与约束 | PASS | `categories` uk(parent_id,name)、`products` fk→categories、`skus` uk(product_id,name)+fk→products、`inventories` uk(sku_id)+fk→skus、`legacy_id_map` uk(source,entity_type,old_id) 均在；订单/秒杀/IAM 业务表约束完整 |
| 七牛配置校验 | PASS | `qiniu check`「七牛云配置与 bucket 可用性校验通过」 |
| 迁移单元 + 集成测试 | PASS | `go test ./internal/logic/legacymigrate/... -count=1` 全绿 |
| schema 等价测试 | PASS | `go test ./internal/migrations/...` ok |
| 合成数据 Dry Run | PASS | 目标库执行前后零写入 |
| 合成数据 Import（幂等/金额/状态/回滚/重复导入） | PASS | 分类 5/商品 4/SKU 5/库存 5；金额 19.90→1990 等精确；软删除/下架→off_shelf、SKU disabled；坏分类商品跳过无孤儿；超长 URL 商品触发整事务回滚无残留；二次 import 零新增 |
| 服务启动 | PASS | `my-shop serve` 真实启动监听 `:8000`，依赖/schema/超管/权限/七牛校验全通过 |
| API 主链路 | PASS | `/categories` 分类树（软删除分类不在前台）、`/products` 仅 on_shelf、`/products/:id` 含 enabled SKU、`/admin/products` 含 off_shelf、`/admin/products/:id` 含 disabled SKU、`/admin/inventories/:sku_id` 库存正确；未认证后台访问 401 |
| 核心业务回归（订单/秒杀/IAM-RBAC） | PASS | `internal/cmd`（order/refund/flashsale/identity_isolation 等，98s）、`internal/logic/iam`、`internal/controller/admin`、`internal/controller/iam`、`internal/middleware`、`internal/auth`、`internal/logic/flashsale` 均 ok |
| 全量测试 `go test -p 1 ./...` | PASS | exit 0，全部包 ok（含 migrations、legacymigrate、cmd、flashsale） |
| 敏感配置检查 | PASS | `.env` 未被 Git 跟踪；`.gitignore` 排除 `.env` 与 `storage/private/legacy/`；`.dockerignore` 排除 `.env`/`storage/`；报告/文档无七牛/JWT 明文 |

## Acceptance Evidence

- INV-001（金额精确）：单测 + DB/API 断言 `1990/2990/990/880`，无浮点误差。
- INV-002（库存不重复计算）：单规格 50（商品级）、多规格 10+20（SKU 级，不叠加商品级 99）；`inventories`↔`skus` 一对一。
- INV-003（映射唯一/幂等）：`legacy_id_map` 11 条；二次 import 零新增。
- INV-004（约束不绕过）：唯一/FK 约束完整，导入未关闭外键校验。
- INV-006（失败原子/无残留）：坏分类商品跳过无孤儿；超长 URL 商品触发整事务回滚（`Data too long`），products/legacy_id_map/skus/inventories 均无残留。
- INV-007（SKU 可见性安全）：软删除/下架商品 SKU `disabled`、前台不可见；on_shelf 商品 SKU `enabled`。
- 全新库从零 Migration 到最新版本 `20261001000023`，Schema 与业务表约束完整（订单/秒杀/IAM 未受影响）。
- 核心业务回归（订单、秒杀、IAM/RBAC）全部通过，新增 `legacy_id_map` migration 未破坏既有业务。

## Scope Adjustments（Owner 取消 / 不适用）

| AC | 原要求 | 现状 |
|---|---|---|
| AC-001 原始 SQL 归档 | `shopping.sql` 移动归档 + SHA-256 | Owner 取消：SQL 已删除，归档动作不适用；`.gitignore`/`.dockerignore` 排除规则仍保留有效 |
| AC-013 真实历史数据导入 | 真实 `shopping.sql` 导入后 API 查询 | Owner 取消真实数据部分；改为合成数据导入 + API 查询，已验证通过 |
| AC-015 安全边界（原始 SQL 不进 Git/镜像/CI） | 阻止原始 SQL 泄露 | 原始 SQL 已删除，安全边界自然满足 |

> 原始历史数据的迁移能力仅表述为「已通过合成数据验证」，未声称完成真实历史数据迁移。

## Remaining Risks

- 全量测试存在既有的**跨包数据库隔离问题**：`internal/logic/flashsale` 的 `setupConsumeTest` 清理列表遗漏 `inventories`/`inventory_logs`（二者有 FK 引用 `skus`），当其它包（如 `cmd` 的库存/订单测试）先运行并留下 inventory 数据时，`DELETE FROM skus` 会因 FK 约束失败。使用标准 `go test -p 1 ./...`（串行 + Go 自动包排序）时不会触发，全量测试通过；属既有测试健壮性问题，非本次 `legacy_id_map` migration 引入，且不影响本次交付结论。
- 真实历史脏数据形态未用真实数据兜底（Owner 已取消该范围，仅以合成数据验证映射规则）。

## Result

PASS

最新后端可基于最新 Migration（`20261001000023`）完成空库初始化、正常启动及核心 API 运行；合成数据迁移（幂等、精确金额、状态映射、事务回滚、重复导入）与关键业务回归（订单、秒杀、IAM/RBAC）验证通过；敏感配置未进入 Git/镜像/报告。真实历史数据迁移已由 Owner 取消，相应能力仅表述为「已通过合成数据验证」。
