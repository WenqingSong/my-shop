# 拼团模块开发知识（接手必读）

业务语义、状态机、不变量与跨模块边界见 `docs/design/marketing.md`；
本文只记录**真实代码入口**、调用链、事务/锁/幂等实现、测试地图与排障要点。
当前仓库只实现了拼团（B1-04-06），砍价（B1-04-07）尚未实现。

## 1. 模块定位与职责边界

拼团回答「哪个 SKU 以什么拼团价、几人成团、团何时过期、谁开团/谁参与、团成功还是失败/过期」。

不要越界实现：

- 不建活动库存、不改 `inventories` 语义、不读写 `flash_sale_*`。
- 不建第二套订单/支付/退款系统：拼团订单就是普通订单，支付/取消/退款全部复用既有链路。
- 不在 `orders` 增加拼团列：订单与拼团的关系只通过 `group_buy_participants.order_id`。
- 不自动退款：团失败只取消待支付订单；已支付订单由用户走现有退款流程。
- 活动停用/结束不影响已开的团（团使用自身快照）。

## 2. 代码地图

| 关注点 | 位置 |
| --- | --- |
| 对外 DTO / 路由元信息 | `api/groupbuy/v1/groupbuy.go` |
| 前台/后台控制器 | `internal/controller/groupbuy/groupbuy.go`（`ControllerV1` / `AdminControllerV1`） |
| 服务接口与注册表 | `internal/service/groupbuy.go`（`IGroupBuy` / `GroupBuy()` / `RegisterGroupBuy`） |
| 业务规则 + 内联 SQL | `internal/logic/groupbuy/groupbuy.go` |
| 服务注册空导入 | `internal/logic/logic.go` |
| 事务内拼团下单 | `internal/service/order.go`（`GroupBuyOrderInput`、`CreateGroupBuyInTx`）、`internal/logic/order/order.go` |
| 事务内取消订单 | `internal/service/order.go`（`CancelInTx`）、`internal/logic/order/order.go` → `cancelInTx` |
| 前台路由 | `internal/cmd/routes_frontend.go`（公开 3 条 + Auth 4 条） |
| 后台路由 | `internal/cmd/routes_admin.go`（`/admin/group-buys*`、`/admin/group-buy-teams*`） |
| 权限 seed | `internal/boot/seed.go`（`group_buy:create/update/list/view/close`） |
| 错误码 | `internal/codes/codes.go`（21001-21009，域 21000-21999） |
| 配置 | `internal/config/contract.go`、`manifest/config/config.yaml`（`group_buy.expire_scan_interval`） |
| 超时扫描器 | `internal/worker/scanner.go`（`StartGroupBuyExpireScanner`） |
| 扫描器装配 | `internal/cmd/cmd.go`（`serve` 内独立 goroutine，HTTP drain 后停止） |
| OpenAPI | 生成器 `tools/openapi-gen/*`；产物 `openapi/paths/groupbuy.yaml`、`openapi/components/*.yaml` |
| 集成/并发测试 | `test/e2e/groupbuy_test.go` |
| 建表迁移（B1-03-03 已建，本任务未改） | `internal/migrations/sql/20261110000011_group_buy.{up,down}.sql` |

## 3. 核心调用链

### 3.1 开团（含下单）

```text
POST /group-buys/:id/teams
  → routes_frontend.go: user.POST(...)（Auth）
  → controller/groupbuy.ControllerV1.CreateTeam        // 取 Principal.UserID
  → service.GroupBuy().CreateTeam(ctx, userID, req)
  → logic/groupbuy.sGroupBuy.CreateTeam
      g.DB().Transaction:
        1) SELECT * FROM group_buy_activities WHERE id=? FOR UPDATE      // 快照与编辑串行化
        2) activityOpenInTx：status=1 AND start_time<=NOW()<end_time     // 否则 21003
        3) INSERT group_buy_teams（price/required_size/expire_at 快照，joined_count=1）
        4) service.Order().CreateGroupBuyInTx(ctx, tx, ...)
             - service.Address().Detail（归属校验，7001）
             - resolveLine：SKU enabled + product on_shelf + 重读 skus.price（否则 21008）
             - group_price > skus.price → 21004
             - INSERT orders（original/discount/total）+ order_items（price=拼团价）
             - IInventory.DeductInTx（不足 6001）
        5) INSERT group_buy_participants（team_id,user_id,order_id）
```

### 3.2 参团（含成团推进）

```text
POST /group-buy-teams/:id/join
  → controller/groupbuy.ControllerV1.JoinTeam
  → logic/groupbuy.sGroupBuy.JoinTeam
      1) 快速幂等：SELECT participant WHERE team_id=? AND user_id=? → 命中直接返回团详情
      g.DB().Transaction:
        2) SELECT team WHERE id=?                         // 仅区分 21005/21006 与取 activity_id
        3) UPDATE group_buy_teams SET joined_count=joined_count+1
             WHERE id=? AND status=10 AND joined_count<required_size AND expire_at>NOW()
           → RowsAffected=0 = 21006（满员/终态/过期）
        4) 重读 team（本次占用后的权威 joined_count）
        5) 读 activity 取 sku_id（不校验活动启停：已开的团不受影响）
        6) service.Order().CreateGroupBuyInTx（价格=team.group_price）
        7) INSERT participant（1062 → 21007，整体回滚）
        8) 若 joined_count>=required_size：
             UPDATE group_buy_teams SET status=20, closed_at=NOW()
             WHERE id=? AND status=10 AND joined_count>=required_size
```

### 3.3 超时过期

```text
worker.StartGroupBuyExpireScanner（ticker，group_buy.expire_scan_interval 默认 60s）
  → service.GroupBuy().ExpireTeams(ctx, 100)
  → SELECT id FROM group_buy_teams WHERE status=10 AND expire_at<=NOW() LIMIT 100
  → 逐团 terminateTeam(teamID, target=40, onlyExpired=true)
       g.DB().Transaction:
         1) UPDATE group_buy_teams SET status=40, closed_at=NOW()
              WHERE id=? AND status=10 AND expire_at<=NOW()
            → 0 行：已被成团/关闭/其他实例处理，跳过
         2) SELECT participants WHERE team_id=? AND status=10
         3) 对每条 order_id：service.Order().CancelInTx
              → UPDATE orders SET status=60,cancel_reason=2,cancelled_at=NOW() WHERE id=? AND status=10
              → 0 行：已支付/已取消，无副作用
              → 命中：IInventory.IncreaseInTx + Coupon().ReleaseInTx
```

### 3.4 运营关闭

```text
POST /admin/group-buy-teams/:id/close（group_buy:close）
  → controller/groupbuy.AdminControllerV1.AdminCloseTeam
  → logic/groupbuy.sGroupBuy.AdminCloseTeam
      1) 先查团存在性（不存在 21005）
      2) terminateTeam(teamID, target=30, onlyExpired=false)
      3) 条件更新未命中 → 21009
```

## 4. 关键数据结构

```go
// 状态常量（DB TINYINT）
activityStatusDisabled = 0; activityStatusEnabled = 1
teamStatusPending  = 10  // 拼团中
teamStatusSuccess  = 20  // 已成团（终态）
teamStatusFailed   = 30  // 已失败（终态，运营关闭）
teamStatusExpired  = 40  // 已过期（终态，超时）
participantStatusActive = 10

// 行结构：activityRow / teamRow / participantRow（json tag 与列名一致，供 GoFrame Scan）
// 摘要：activityMeta / teamMeta / skuSummary / productSummary / activityTeamCount / userNameRow
// 我的拼团联表行：myTeamRow（participants JOIN teams）
```

关键常量：`maxNameLen=64`、`minGroupSize=2`、`maxGroupSize=1000`、
`minTeamTTLMinutes=1`、`maxTeamTTLMinutes=10080`、`maxPrice=99_999_999`、
`participantQuantity=1`、分页 `1/20/100`。

## 5. 业务不变量与实现映射

| 不变量 | 实现 | 测试 |
| --- | --- | --- |
| INV-GB01 价格/人数快照 | 开团事务内写 `group_price`/`required_size`/`expire_at`；参团只读团 | `TestGroupBuyCreateTeamOrdering` |
| INV-GB02 容量不超卖 | 条件更新 `joined_count<required_size` + 行锁 | `TestGroupBuyConcurrentJoinCapacity` |
| INV-GB03 一人一团一次 | `uk_team_user` + 1062 回滚（21007） | `TestGroupBuyConcurrentDuplicateJoin` |
| INV-GB04 一单一团一次 | `uk_order_id` | 参与插入失败即回滚订单（事务原子性） |
| INV-GB05 团长归属 | 开团同事务写团长参与记录 | `TestGroupBuyCreateTeamOrdering` |
| INV-GB06 状态机只进一次 | 终态全部为 `WHERE status=10` 条件更新 | `TestGroupBuyJoinSuccessAndIdempotency`、`TestGroupBuyExpireRaceWithJoin` |
| INV-GB07 活动停用不影响已开团 | 参团不校验活动启停 | `TestGroupBuyActivityManagement`（停用后禁止**新开团**） |
| INV-GB08 订单/参与一致 | 同事务写订单与参与 | `TestGroupBuyCreateTeamOrdering` |
| INV-GB09 补偿只一次 | 团终态条件更新 + 订单 `status=10` 条件取消 | `TestGroupBuyExpireAndRefundReuse` |

## 6. 事务、锁与幂等

- **无独立锁服务**：并发控制全部依赖 InnoDB 行锁 + 条件更新 + 唯一约束；不引入 Redis 锁/MQ。
- **开团**：`SELECT ... FOR UPDATE` 锁活动行，保证快照与后台改价/启停串行；订单号撞 `uk_order_no` 时在事务内重试（最多 3 次）。
- **参团**：容量闸门是 `UPDATE ... WHERE joined_count<required_size`；不要改成「先 COUNT 再 INSERT」。
- **重复参团**：先做一次非事务的参与记录读（常规重复请求幂等返回团详情）；
  并发重复由 `uk_team_user` 兜底（1062 → 21007 并回滚 `joined_count`）。
- **同一幂等键**：订单 `uk_user_idempotency` 冲突返回 9006 并回滚；不会出现「团建了但订单没建」。
- **成团**：与最后一次参团同事务；条件更新 `status=10 AND joined_count>=required_size`。
- **过期/关闭**：团终态条件更新 + 订单 `status=10` 条件取消；两个闸门都检查 `RowsAffected`。
- **多实例**：扫描器可多实例运行，未命中的实例不补偿订单/库存。

## 7. 错误码与 API 契约

错误码（`internal/codes/codes.go`，域 21000-21999）：

| code | 常量 | HTTP | 触发点 |
| --- | --- | --- | --- |
| 21001 | `CodeGroupBuyNotFound` | 404 | 活动详情/开团/参团活动缺失 |
| 21002 | `CodeGroupBuyInvalidArgument` | 400 | 名称/价格/人数/有效期/时间窗/SKU 归属/分页非法 |
| 21003 | `CodeGroupBuyActivityInactive` | 409 | 开团时活动停用或不在时间窗 |
| 21004 | `CodeGroupBuyPriceInvalid` | 400 | 拼团价 ≤0 或高于 SKU 现价 |
| 21005 | `CodeGroupBuyTeamNotFound` | 404 | 团不存在 |
| 21006 | `CodeGroupBuyTeamNotJoinable` | 409 | 已满/已成团/已失败/已过期 |
| 21007 | `CodeGroupBuyAlreadyJoined` | 409 | `uk_team_user` 冲突 |
| 21008 | `CodeGroupBuySkuUnavailable` | 409 | SKU 禁用/商品下架/商品-SKU 不匹配 |
| 21009 | `CodeGroupBuyTeamNotClosable` | 409 | 关闭非拼团中的团 |

复用码：`9006`（订单幂等键冲突）、`6001`（库存不足）、`7001`（地址）、
`18002`（退款状态迁移）、`1001`/`1002`/`1003`。

API 契约由 `api/groupbuy/v1/*.go` 的 `g.Meta` 生成；**禁止手工修改 `openapi/`**：

```bash
go run ./tools/openapi-gen -repo .        # 生成
go run ./tools/openapi-gen -repo . -check # 校验
bash scripts/contract-check.sh
```

新增接口后必须同步：

1. `tools/openapi-gen/gen.go`：`expectedTotal/expectedFrontend/expectedAdmin`、`pathFileOrder`、tag 描述；
2. `tools/openapi-gen/model.go`：`domains` 与 `entitySchemaNames`；
3. `internal/cmd/routes_test.go`：`fullRouteTable` 与三处计数断言；
4. `tools/openapi-gen/unused-components-allowlist.yaml`：新的 Request DTO；
5. `tools/openapi-gen/opmeta.go`：有 `v:"required"` body 字段的写接口需登记 D1 已知问题；
6. `tools/openapi-gen/main_test.go`：路由数（总数 - 2 条自动绑定 health）。

## 8. 测试地图

| 测试 | 覆盖 |
| --- | --- |
| `TestGroupBuyActivityManagement` | 创建/修改/启停、公开列表与详情、无效参数（人数/时间窗/价格/SKU/商品）、禁用 SKU |
| `TestGroupBuyCreateTeamOrdering` | 开团 + 订单金额快照 + 订单项价格 + 参与记录关联 + 库存扣减 |
| `TestGroupBuyJoinSuccessAndIdempotency` | 参团、满员成团、第三人拒绝、重复参团幂等、库存只扣一次 |
| `TestGroupBuyConcurrentJoinCapacity` | 10 用户并发抢 2 名额：成功数=2、joined=3、订单=3、库存=97 |
| `TestGroupBuyConcurrentDuplicateJoin` | 同一用户 8 并发重复参团：1 参与、1 订单、joined=2、库存=98 |
| `TestGroupBuyExpireAndRefundReuse` | 过期：未支付订单取消恢复库存；已支付订单保持 paid；走现有退款链路后订单 70、库存 100 |
| `TestGroupBuyExpireRaceWithJoin` | 过期推进与 6 个参团并发：全部 409/21006、团 expired、joined=1、库存恢复 |

运行命令（需要真实 MySQL/Redis）：

```bash
DATABASE_DEFAULT_PASS=root go test -count=1 -run 'TestGroupBuy' ./test/e2e/ -v
DATABASE_DEFAULT_PASS=root go test -race -count=1 -run 'TestGroupBuyConcurrent' ./test/e2e/
```

并发测试有效性证据：临时把参团条件更新改为「先查 `joined_count` 再无条件 +1」后，
`TestGroupBuyConcurrentJoinCapacity` 实测 `success=10`（超员）并失败；恢复条件更新后恢复 PASS。

## 9. 排障

| 现象 | 排查点 |
| --- | --- |
| 活动列表为空 | `group_buy_activities.status=1` 且 `start_time<=NOW()<end_time`；检查 `start_time/end_time` 是否被写成 UTC（应写业务时区字面量） |
| 开团返回 21003 | 活动被停用或不在时间窗；`activityOpenInTx` 用 MySQL `NOW()`，与会话 `time_zone` 一致 |
| 开团返回 21004 | 活动创建后 SKU 被下调价格，`group_price > skus.price`；需同步改活动价 |
| 参团返回 21006 | 团已满/已终态/`expire_at<=NOW()`；先看 `SELECT status,joined_count,required_size,expire_at FROM group_buy_teams` |
| 参团返回 21007 | 该用户已有参与记录（含并发重复请求被唯一键兜底） |
| 参团返回 9006 | 同一 `idempotency_key` 被复用到不同团/不同内容；客户端应每团每用户使用唯一键 |
| 参团返回 21008 | SKU 被禁用或商品下架；或活动 `product_id/sku_id` 不匹配 |
| 库存不恢复 | 团是否真的进入 30/40；订单是否仍是 `status=10`（已支付订单不自动恢复） |
| 团一直 pending | 扫描器是否启动（`group_buy.expire_scan_interval`）、`expire_at` 是否已过 |

## 10. 维护注意事项

- **时区**：活动时间写入必须转成 `dashboard.timezone` 字面量；不要直接把 `*gtime.Time` 交给 gdb
  （会按 UTC 格式化，与连接层 `time_zone` 的 `NOW()` 相差 8 小时）。
  `activityTimeValue` 是唯一入口。
- **不要在参团路径加活动启停校验**：已开的团必须能在活动结束后继续参团（INV-GB07）。
- **不要用 `COUNT(*)` 判定名额**：容量事实是 `joined_count`，唯一键只做重复兜底。
- **不要给已支付订单直接改状态**：失败补偿只取消 `status=10` 的订单。
- **测试清理顺序**：`group_buy_participants → group_buy_teams → group_buy_activities` 必须先于
  `skus/products`（FK RESTRICT），`setupOrderServer` 已包含该顺序。
- **路由/契约计数**：新增接口会同时影响 `internal/cmd/routes_test.go`、生成器期望数、
  allowlist 与 `docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md`；改完必须重新生成并跑
  `bash scripts/contract-check.sh`。
- **权限 seed**：新增 `group_buy:*` 权限已登记在 `internal/boot/seed.go`，启动时幂等写入；
  超级管理员自动放行，其他管理员需角色授权。
