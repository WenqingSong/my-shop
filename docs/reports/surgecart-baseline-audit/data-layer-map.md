# D2 数据层定位清单：表、索引、事务、状态与一致性机制

> Review Baseline：`95c13910c7226b4e32487ed046702caf75d74fc6`（源码等价于 `473d6f5253e3abcb98ece9d79a5a8059563a3e8d`）
> 证据优先级：Migration SQL + 运行期实测 SQL/事务日志 > 代码注释 > 设计文档

## 1. 秒杀域数据表总表

| 表 | Migration 文件 | 主键 | 唯一约束 | 普通索引 | 外键 |
| --- | --- | --- | --- | --- | --- |
| `flash_sale_activities` | `internal/migrations/sql/20261001000011_flash_sale.up.sql:11-22` | `id BIGINT UNSIGNED AUTO_INCREMENT` | 无 | `idx_status(status)`、`idx_time(start_time,end_time)` | 无 |
| `flash_sale_activity_skus` | 同上 `:24-37` | `id BIGINT UNSIGNED` | `uk_activity_sku(activity_id,sku_id)` | `idx_sku_id(sku_id)` | `fk_flash_sku_activity(activity_id) → flash_sale_activities(id) ON DELETE CASCADE` |
| `flash_sale_orders` | 同上 `:39-61` | `id BIGINT UNSIGNED` | `uk_flash_order_no(order_no)`、`uk_flash_idempotency(user_id,idempotency_key)`、`uk_flash_one_per_user(activity_id,sku_id,user_id)` | `idx_activity(activity_id)`、`idx_user(user_id)` | 无（`user_id/activity_id/sku_id/product_id` 均软引用） |
| `flash_sale_order_requests` | `20261001000015_flash_sale_order_requests.up.sql:11-28` | `id BIGINT UNSIGNED` | `uk_request_idempotency(user_id,idempotency_key)` | `idx_dequeue(status,next_attempt_at,id)` | 无（全部软引用） |
| `flash_sale_request_audits` | `20261001000018_flash_sale_request_audits.up.sql:7-20` | `id BIGINT UNSIGNED` | 无 | `idx_request_id(request_id)`、`idx_operator(operator_admin_id)` | 无（软引用，避免级联破坏审计） |

引擎/字符集：全部 `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`（各文件末尾）。

运行期核对（只读，见 D3 §7）：

```text
SELECT VERSION();                       -> 8.0.46
SELECT version, dirty FROM schema_migrations; -> 20261001000023 , 0
SHOW TABLES LIKE 'flash_sale%';         -> 5 张表全部存在
```

即：Migration 已在测试库真实执行，`schema_migrations` 版本为最新、未处于 dirty 状态。

## 2. 关键字段与语义

### 2.1 `flash_sale_order_requests`（异步请求/出队表）

| 字段 | 类型 | 语义 | 代码锚点 |
| --- | --- | --- | --- |
| `id` | BIGINT UNSIGNED | 请求主键，出队排序键 | `request.go:29` |
| `user_id` | BIGINT UNSIGNED | 归属用户（软引用） | 同上 |
| `activity_id` / `sku_id` | BIGINT UNSIGNED | 活动 × SKU | 同上 |
| `idempotency_key` | VARCHAR(64) | 客户端幂等键，`(user_id,key)` 全局唯一 | `flashsale.go:276-279` |
| `request_hash` | VARCHAR(64) | `sha256(activity_id|sku_id)`，用于同键异内容判定 | `flashsale.go:817-820` |
| `status` | TINYINT | 0=queued / 1=success / 2=failed / 3=dead | `request.go:20-25` |
| `retry_count` | INT UNSIGNED | 技术失败次数 | `consume.go:196` |
| `next_attempt_at` | DATETIME NULL | 技术失败退避到期时间；NULL 表示立即可领 | `consume.go:214-218` |
| `last_error_code` | INT NULL | 最近失败码（观测用） | `consume.go:186/204/216` |
| `flash_order_id` | BIGINT UNSIGNED NULL | success 时关联 `flash_sale_orders.id`（软引用，无 FK） | `consume.go:173-176` |
| `created_at` / `updated_at` | DATETIME | 默认 CURRENT_TIMESTAMP / ON UPDATE | Migration |

### 2.2 状态取值与迁移

```text
             INSERT (enqueue)
                    │
                    ▼
        ┌────── queued(0) ──────┐
        │  retry_count+1,        │  业务失败 (12001~12007/5001/4001)
        │  next_attempt_at       │
        │  (5/10/20s 退避)       ▼
        └──────────────► failed(2)  （终态，不可再迁移）
                    │
                    │ 第 4 次技术失败 (retry_count>3)
                    ▼
                 dead(3) ──── 管理端 repair（dead→queued，同事务写审计） ──► queued(0)
                    │
   success 路径：queued ──(订单 INSERT + sold+1 + status=1 同事务)──► success(1)
```

| 迁移 | 负责函数 | 位置 |
| --- | --- | --- |
| （创建）→ queued | `enqueue` | `request.go:61-92` |
| queued → success | `successRequestInTx` | `consume.go:172-180`（在 `consumeOne` 事务内 `:130`） |
| queued → failed | `failRequestInTx` | `consume.go:183-191`（`:104`/`:116` 调用） |
| queued → queued（重试） | `retryOrDeadRequestInTx` | `consume.go:195-222`（`:214` 更新 retry/next_attempt） |
| queued → dead | `retryOrDeadRequestInTx` | `consume.go:197-209` |
| dead → queued（管理员） | `RepairRequest` | `repair.go:78-107`（条件 `WHERE status=3` + `RowsAffected` 判定） |

不存在：success/failed 的后续迁移、超时自动失败、定期清理、批量重排。

## 3. 并发控制手段（逐项实测/源码）

| 机制 | 位置 | 实际作用 |
| --- | --- | --- |
| `FOR UPDATE SKIP LOCKED` | `consume.go:79-85` | 多实例互斥领取同一行；跳过被锁行而不是等待。实测 SQL 见 D3 §6 |
| `FOR UPDATE`（绑定行） | `loadBindingForUpdate` `flashsale.go:702-715` | 序列化同一 activity×SKU 的落单：秒杀价快照、`sold` 判定、`sold+1` 在同一条行锁下 |
| 条件更新 + `RowsAffected` | `insertOrderInTx` `flashsale.go:530-540` | `UPDATE ... SET sold=sold+1 WHERE sold<total_stock`；受影响 0 行 → 12003（注释称为不可达防御分支） |
| 条件更新 + `RowsAffected` | `updateBindings` `flashsale.go:548-575` | 改库存前 `FOR UPDATE` 读 `sold`，要求 `total_stock>=sold` |
| 条件更新 + `RowsAffected` | `RepairRequest` `repair.go:79-94` | 仅 `status=dead` 可迁移，并发下只有一个管理员成功 |
| 唯一约束兜底 | 三处唯一键见 §1 | 一人一单、请求幂等、订单幂等、订单号撞号 |
| Redis 原子脚本 | `redis.go:65-141`、`ratelimit.go:25-31` | 预扣/标记、固定窗口计数在 Redis 内原子 |
| 进程内锁 | `circuit_breaker.go:26-31` | 熔断器状态互斥 |

实测事务序列（同一 `txid:3`，完整日志见 D3 §6）：

```sql
BEGIN
INSERT INTO `flash_sale_orders`(...) VALUES(...)
UPDATE `flash_sale_activity_skus` SET `sold`=sold + 1,`updated_at`=... WHERE (`id`=68) AND (sold < total_stock)
UPDATE `flash_sale_order_requests` SET `status`=1,`flash_order_id`=75,`updated_at`=... WHERE `id`=87
COMMIT
```

## 4. 事务边界

| 事务 | Begin / Commit 位置 | 包含的写操作 | 回滚语义 |
| --- | --- | --- | --- |
| 消费落单事务 | `g.DB().Transaction(ctx, func...)` `consume.go:76` | 领取行锁、`INSERT flash_sale_orders`、`UPDATE flash_sale_activity_skus.sold`、`UPDATE flash_sale_order_requests.status` | 任一失败整体回滚：订单不落库、`sold` 不增加、请求保持原状态（queued） |
| 创建活动事务 | `flashsale.go:135-158` | `INSERT flash_sale_activities` + 循环 `INSERT flash_sale_activity_skus` | 全部回滚 |
| 更新活动事务 | `flashsale.go:224-239` | `UPDATE flash_sale_activities` + `updateBindings` | 全部回滚 |
| 人工修复事务 | `repair.go:78-107` | `UPDATE flash_sale_order_requests`（dead→queued）+ `INSERT flash_sale_request_audits` | 审计与状态迁移原子 |
| **无事务** | `enqueue` `request.go:61-92` | 单条 `INSERT`（自动提交）；入队与 Redis 预扣跨系统，非原子 | 失败走补偿，不回滚 Redis 预扣（由收敛修正） |

## 5. 幂等与唯一约束的实际覆盖

| 业务幂等要求 | 依赖的唯一键 / 判断 | 覆盖情况 |
| --- | --- | --- |
| 同一用户同一幂等键至多一个请求 | `uk_request_idempotency(user_id,idempotency_key)`（migration 15:26） | 覆盖；撞键时 `resultForExistingRequest` 回读并比对 hash（`request.go:96-113`） |
| 同一用户同一幂等键至多一个订单 | `uk_flash_idempotency(user_id,idempotency_key)`（migration 11:57） | 覆盖；撞键由 `classifyConsumeError` 归为业务失败 12005（`consume.go:232-233`） |
| 一人一单 | `uk_flash_one_per_user(activity_id,sku_id,user_id)`（migration 11:58） | 覆盖；撞键 → 12004（`consume.go:230-231`） |
| 订单号唯一 | `uk_flash_order_no(order_no)`（migration 11:56） | 覆盖；撞号归为技术失败重试（`consume.go:234-235`） |
| 活动 × SKU 绑定不重复 | `uk_activity_sku(activity_id,sku_id)`（migration 11:34） | 覆盖 |
| 同键异内容拒绝 | `request_hash` 比对：Redis 层 `IDEMPOTENT_CONFLICT`（`redis.go:105-111`）+ MySQL 层 hash 比对（`flashsale.go:381-383`、`request.go:104-106`） | 覆盖（应用层判断，无 DB 约束） |
| 请求 ↔ 订单关联一致性 | 无约束：`flash_order_id` 可空、无 FK、无唯一 | **不覆盖**（仅靠同一事务写入的应用逻辑） |
| `sold <= total_stock` | 无 CHECK 约束 | **不覆盖**（靠行锁 + 条件更新；直接改库可破坏） |

## 6. `flash_sale_order_requests` 的真实职责判定

| 候选职责 | 是否存在 | 证据 | 说明 |
| --- | --- | --- | --- |
| 请求排队 | **是** | `status=0` + `next_attempt_at` + `idx_dequeue`；`consume.go:79-85` | DB 表即队列；无独立队列中间件 |
| 请求持久化 | **是** | `enqueue` 单条 INSERT（`request.go:62-69`） | HTTP 200 即已落库 |
| 幂等控制 | **是（请求级）** | `uk_request_idempotency`；撞键回读（`request.go:73-76`） | 订单级幂等由 `uk_flash_idempotency` 另行兜底 |
| 消费者领取 | **是（行锁领取）** | `FOR UPDATE SKIP LOCKED`（实测 SQL） | 无「处理中」状态、无租约、无心跳；锁即领取标记 |
| 请求状态查询 | **是** | `GetOrderResult` → `findRequestByActivityKey`（`request.go:117-150`） | 客户端轮询依据 |
| 失败恢复 | **部分** | 技术失败重试 + 死信（`consume.go:195-222`）；人工 repair（`repair.go`） | 无自动重排、无超时失败、无定期清理 |
| 对账恢复 | **否** | `ReconcileCache` 只读计数（`request.go:167-185`）并操作 Redis；不写该表 | 对账不修复遗留请求；遗留请求靠消费者重启后重新领取 |
| 归档 / 清理 | **否** | 全仓库无对 `flash_sale_order_requests` 的 DELETE/归档（`grep -rn` 结果为空） | 表无界增长 |

**结论**：该表是「持久化请求记录 + 基于行锁的 DB 队列」，承担排队、持久化、请求级幂等、领取、状态查询与有限失败恢复；**不承担对账修复与生命周期清理**，也不是带租约/可见性超时的可靠消息队列。

## 7. 专项检查结论（对应任务阶段 3 的 7 条）

| # | 检查项 | 结论 | 证据 |
| --- | --- | --- | --- |
| 1 | 请求表是可靠队列还是仅持久化记录 | 介于两者之间：有 `SKIP LOCKED` 领取与重试/死信，但无租约、无可见性超时、无清理、无死信自动恢复 | §6；`consume.go`、`repair.go` |
| 2 | 领取机制是否存在并发竞争窗口 | 同一行不会被两个事务同时领取（行锁 + SKIP LOCKED）。存在的是**吞吐窗口**：领取锁覆盖整个处理事务（含 SKU 查询、绑定行锁等待），慢请求会阻塞该行；未发现可导致重复领取的窗口 | 实测 SQL + `consume.go:76-131` |
| 3 | 是否存在重复消费 | 正常情况下否。崩溃回滚后同一请求会被重新领取（at-least-once 投递），业务效果由唯一约束保证一次（`TestConsumeCrashBeforeCommitRedeliversOnce` 通过） | `consume.go:120-126`；D3 |
| 4 | 请求记录与订单数据不一致的失败窗口 | 消费事务内不存在（同一 `txid`）；跨系统窗口在「Redis 预扣 → INSERT 请求」之间（崩溃则 Redis 多扣，MySQL 无记录，由 ≤60s 对账修正）；`success` 但订单缺失仅可能由库外改动造成，查询层显式报 500 | `flashsale.go:304/341`；`request.go:131-142`；`redis.go:382-427` |
| 5 | 库存扣减与订单创建是否原子 | 是（秒杀配额维度）：`flash_sale_activity_skus.sold` 与 `flash_sale_orders` 同事务；但**不联动** `inventories`/`inventory_logs`，普通库存不变 | 实测 `txid:3`；`grep` 该包无 `inventories` 引用 |
| 6 | 唯一约束是否覆盖业务幂等 | 覆盖请求幂等、订单幂等、一人一单、订单号、绑定唯一；不覆盖 `request→order` 关联一致性与 `sold<=total_stock` 的 DB 级校验 | §5 |
| 7 | 对账能否处理消费者异常退出后的遗留请求 | **不能，也不需要**：对账只收敛 Redis。消费者异常退出后，请求行仍为 `queued`（事务回滚），重启后的 ticker 会重新领取；无租约故无「僵尸处理中」状态需要回收 | `redis.go:388-425`；`consume.go:76-90` |

## 8. 不变量与约束的对应关系

| 不变量（设计文档） | 本基线中的实际守护手段 | 结论 |
| --- | --- | --- |
| INV-001 库存不为负 | `INT UNSIGNED` + 条件更新 `sold<total_stock` + 绑定行 `FOR UPDATE` | 已实现 |
| INV-002 成功订单数 ≤ 初始库存 | 同事务 `INSERT order` + `sold+1`；`COUNT(orders)=sold` 由同一事务保证 | 已实现（异常中断由事务回滚保证） |
| INV-003 一人一单 | `uk_flash_one_per_user` + Redis `bought` 标记 | 已实现 |
| INV-004 失败不建单 | 单事务回滚 + 闸门前置校验 | 已实现 |
| INV-005 幂等不重复扣减 | `uk_flash_idempotency`/`uk_request_idempotency` + Redis `idem` | 已实现 |
| INV-012 请求级幂等 | `uk_request_idempotency` | 已实现 |
| INV-013 请求状态与订单一致 | `successRequestInTx` 与订单同事务 | 已实现 |
| INV-015 崩溃重投一次效果 | 事务回滚 + 唯一约束 + SKIP LOCKED | 已实现，有测试 |
| INV-016 结束收敛 | `convergeEndedActivity`（grace 60s，inflight=0 后失效缓存） | 已实现，有测试 |
| INV-017 修复审计原子性 | 修复 + 审计同事务，无 UPDATE/DELETE 接口 | 已实现，有测试 |
| INV-009 配额预分配 | 秒杀库存独立于 `inventories`，无自动划拨 | 运营前置约定，非机械校验 |

## 9. 执行计划观测（有限样本）

```sql
EXPLAIN SELECT id FROM flash_sale_order_requests
WHERE status=0 AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
ORDER BY id LIMIT 1;
-- type=index, key=PRIMARY, rows=1, Extra=Using where
```

注意：执行时表内仅 1 行（测试库），优化器样本不足，**不能据此断言生产规模下的执行计划**；`idx_dequeue(status,next_attempt_at,id)` 是否被选中需在有数据量的环境复核。该限制已列入 D4「未知事项」。

## 10. 与设计文档宣称的差异对照（AC-4 要求）

对照对象：`docs/design/flash-sale.md`（339 行）。核对范围：§2.4 请求表、§5.3 跨系统一致性、§5.5 对账、§5.6 崩溃窗口、§6.1 Key/TTL、§6.2 Lua 边界、§7 权限、§8 错误码。

### 10.1 一致项（抽核）

| 文档宣称 | 实现证据 | 结论 |
| --- | --- | --- |
| 请求表字段/索引/四态（§2.4） | migration 15:11-28；`request.go:20-25` | 一致 |
| 出队 `FOR UPDATE SKIP LOCKED`（§2.4:88） | `consume.go:79-85`；实测 SQL | 一致（实现为 `LIMIT 1`，文档写 `LIMIT N`，语义等价） |
| 对账公式 `remaining=total-sold-inflight_queued`（§5.5） | `redis.go:304-321`、`:259`、`:316` | 一致 |
| 结束活动 grace 60s 收敛（§5.5/§5.6） | `redis.go:409-418`、`:434-483` | 一致 |
| 三崩溃窗口与自愈（§5.6） | 孤儿标记自愈 `flashsale.go:371-405`；事务回滚 `consume.go:76-131`；对账兜底 `redis.go:327-347` | 一致（对应测试存在，见 D3） |
| 错误码 12001~12010（§8） | `internal/codes/codes.go:104-113`、HTTP 映射 `:226-235` | 一致 |
| 权限码与入口（§7） | `routes_admin.go:147-151`；`internal/boot/seed.go:61-63` 登记 `flash_sale:create/update/repair` | 一致 |

### 10.2 不一致项（逐条）

| # | 文档位置 | 文档宣称 | 实现事实 | 影响 |
| --- | --- | --- | --- | --- |
| 1 | `docs/design/flash-sale.md:160` | 「Lua 预扣成功 → MySQL 事务失败 → 同请求补偿 Redis（`INCR remaining`）；一人一单/幂等标记因下单成功后才写入，故无需补偿」 | 补偿为幂等 `SET remaining=权威值`（`redis.go:327-347`）；标记在 `GATE_PASSED` 即写入（`redis.go:134-140`），失败时 `DEL` 清除（`redis.go:349-357`） | 属 V1/V2 残留描述，与同文档 `:166-168`、`:224` 自相矛盾；误导读补偿机制与标记时机 |
| 2 | `docs/design/flash-sale.md:207-208` | Key 表「`bought` 一人一单（下单成功后写入）」「`idem` 幂等（下单成功后写入）」 | V3 起在 `GATE_PASSED`（入队）时原子写入（`redis.go:134-139`） | 同上；影响对「孤儿标记」与「补偿范围」的判断 |
| 3 | `docs/design/flash-sale.md:88` | 出队 `ORDER BY id LIMIT N` | 实现为每次事务 `LIMIT 1`，由 `ConsumeQueued` 循环至 `limit` | 语义等价，仅粒度差异；不构成缺陷 |

上述 1、2 已登记为 `findings.md` F7（MINOR，文档一致性）。本任务不修改文档。
