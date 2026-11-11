# 营销（拼团 + 砍价）开发知识（接手必读）

> 本文件包含两个模块：B1-04-06 拼团（§1-§10）与 B1-04-07 砍价（§11）。
> 两者共享 `group_buy_*` / `bargain_*` 表与「订单通过参与记录/实例反查」的边界约定。

业务语义、状态机、不变量与跨模块边界见 `docs/design/marketing.md`；
本文只记录**真实代码入口**、调用链、事务/锁/幂等实现、测试地图与排障要点。
本文件 §1-§10 是拼团（B1-04-06）的实现，§11 是砍价（B1-04-07）的实现，两者均已合入 `feat/p2-backend`。

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
| 错误码 | `internal/codes/codes.go`（25001-25009，域 25000-25999） |
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
        2) activityOpenInTx：status=1 AND start_time<=NOW()<end_time     // 否则 25003
        3) INSERT group_buy_teams（price/required_size/expire_at 快照，joined_count=1）
        4) service.Order().CreateGroupBuyInTx(ctx, tx, ...)
             - service.Address().Detail（归属校验，7001）
             - resolveLine：SKU enabled + product on_shelf + 重读 skus.price（否则 25008）
             - group_price > skus.price → 25004
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
        2) SELECT team WHERE id=?                         // 仅区分 25005/25006 与取 activity_id
        3) UPDATE group_buy_teams SET joined_count=joined_count+1
             WHERE id=? AND status=10 AND joined_count<required_size AND expire_at>NOW()
           → RowsAffected=0 = 25006（满员/终态/过期）
        4) 重读 team（本次占用后的权威 joined_count）
        5) 读 activity 取 sku_id（不校验活动启停：已开的团不受影响）
        6) service.Order().CreateGroupBuyInTx（价格=team.group_price）
        7) INSERT participant（1062 → 25007，整体回滚）
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
      1) 先查团存在性（不存在 25005）
      2) terminateTeam(teamID, target=30, onlyExpired=false)
      3) 条件更新未命中 → 25009
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
| INV-GB03 一人一团一次 | `uk_team_user` + 1062 回滚（25007） | `TestGroupBuyConcurrentDuplicateJoin` |
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
  并发重复由 `uk_team_user` 兜底（1062 → 25007 并回滚 `joined_count`）。
- **同一幂等键**：订单 `uk_user_idempotency` 冲突返回 9006 并回滚；不会出现「团建了但订单没建」。
- **成团**：与最后一次参团同事务；条件更新 `status=10 AND joined_count>=required_size`。
- **过期/关闭**：团终态条件更新 + 订单 `status=10` 条件取消；两个闸门都检查 `RowsAffected`。
- **多实例**：扫描器可多实例运行，未命中的实例不补偿订单/库存。

## 7. 错误码与 API 契约

错误码（`internal/codes/codes.go`，域 25000-25999）：

| code | 常量 | HTTP | 触发点 |
| --- | --- | --- | --- |
| 25001 | `CodeGroupBuyNotFound` | 404 | 活动详情/开团/参团活动缺失 |
| 25002 | `CodeGroupBuyInvalidArgument` | 400 | 名称/价格/人数/有效期/时间窗/SKU 归属/分页非法 |
| 25003 | `CodeGroupBuyActivityInactive` | 409 | 开团时活动停用或不在时间窗 |
| 25004 | `CodeGroupBuyPriceInvalid` | 400 | 拼团价 ≤0 或高于 SKU 现价 |
| 25005 | `CodeGroupBuyTeamNotFound` | 404 | 团不存在 |
| 25006 | `CodeGroupBuyTeamNotJoinable` | 409 | 已满/已成团/已失败/已过期 |
| 25007 | `CodeGroupBuyAlreadyJoined` | 409 | `uk_team_user` 冲突 |
| 25008 | `CodeGroupBuySkuUnavailable` | 409 | SKU 禁用/商品下架/商品-SKU 不匹配 |
| 25009 | `CodeGroupBuyTeamNotClosable` | 409 | 关闭非拼团中的团 |

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
| `TestGroupBuyExpireRaceWithJoin` | 过期推进与 6 个参团并发：全部 409/25006、团 expired、joined=1、库存恢复 |

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
| 开团返回 25003 | 活动被停用或不在时间窗；`activityOpenInTx` 用 MySQL `NOW()`，与会话 `time_zone` 一致 |
| 开团返回 25004 | 活动创建后 SKU 被下调价格，`group_price > skus.price`；需同步改活动价 |
| 参团返回 25006 | 团已满/已终态/`expire_at<=NOW()`；先看 `SELECT status,joined_count,required_size,expire_at FROM group_buy_teams` |
| 参团返回 25007 | 该用户已有参与记录（含并发重复请求被唯一键兜底） |
| 参团返回 9006 | 同一 `idempotency_key` 被复用到不同团/不同内容；客户端应每团每用户使用唯一键 |
| 参团返回 25008 | SKU 被禁用或商品下架；或活动 `product_id/sku_id` 不匹配 |
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

---

## 11. 砍价模块（B1-04-07）
砍价的业务语义、状态机与不变量见 `docs/design/marketing.md` §3；本文只记录**已落地代码**的入口、
调用链、事务边界与排障方法。拼团（`group_buy_*`）在本分支仍只有表结构，没有业务代码。

### 1. 模块定位

砍价回答「哪个 SKU 的原价/底价是多少、这个用户当前砍到多少、谁帮过忙、还差多少、是否已成功/失败/过期」。
它是**营销侧的价格推进**能力，不拥有库存、不拥有支付、不拥有订单金额：

- 不扣库存、不建活动库存列、不读写 `inventories`/`flash_sale_*`；
- 不创建订单：`bargain_instances.order_id` 本期保持 NULL，成交价下单属于后续计价集成任务；
- 事实来源单一 MySQL，无 MQ；Redis 只用于登录会话（与砍价逻辑无关）。

### 2. 当前实现状态

| 能力 | 状态 |
| --- | --- |
| 活动创建/编辑/启停/列表/详情（后台） | 已实现 |
| 可参与活动列表/详情（公开） | 已实现 |
| 发起砍价（快照 + 一人一活动一个进行中实例） | 已实现 |
| 好友助力（金额推进 + 次数上限 + 底价保护 + 同事务状态推进） | 已实现 |
| 我的砍价记录 / 实例详情 / 助力记录 | 已实现 |
| 放弃砍价（10 → 30） | 已实现 |
| 过期（10 → 40） | 已实现，**读取路径懒推进**，无独立扫描器 |
| 砍价成功后的下单 / 订单金额联动 | 未实现（后续集成任务） |
| 后台取消活动并批量失败已有实例 | 未实现（仅停用，停用不影响已创建实例） |

### 3. 代码地图

| 关注点 | 位置 |
| --- | --- |
| DTO 与路由元信息（`g.Meta`） | `api/bargain/v1/bargain.go` |
| Controller（前台 / 后台） | `internal/controller/bargain/bargain.go` |
| 服务接口 `IBargain` | `internal/service/bargain.go` |
| 业务实现（校验/发起/助力/状态/查询） | `internal/logic/bargain/bargain.go` |
| 纯函数单测 | `internal/logic/bargain/bargain_test.go` |
| 真实 MySQL 集成与并发测试 | `internal/logic/bargain/integration_test.go` |
| 路由注册 | `internal/cmd/routes_frontend.go`、`internal/cmd/routes_admin.go` |
| 路由表冻结清单 | `internal/cmd/routes_test.go`（`fullRouteTable`，210 条） |
| 错误码 | `internal/codes/codes.go`（砍价域 `23001-23009`） |
| 权限 seed | `internal/boot/seed.go`（`bargain:create/update/list/view`） |
| OpenAPI | `openapi/paths/bargain.yaml` + `openapi/surgecart.openapi.yaml`（生成，不手工改） |

表结构事实源：`internal/migrations/sql/20261110000012_bargain.up.sql`（B1-03-03 交付，本任务未改）。

### 4. 核心调用链

发起砍价：

```text
POST /bargain-activities/:id/instances
  → controller/bargain.Start（取 Principal.UserID）
  → logic/bargain.Start
      INSERT INTO bargain_instances (...) SELECT ... FROM bargain_activities
        WHERE id=? AND status=1 AND start_time<=NOW() AND end_time>NOW()
      0 行 → 23001/23003；1062（uk_active_instance）→ 23005
  → loadInstance（联查活动/商品/SKU 展示字段）
```

好友助力（核心）：

```text
POST /bargain-instances/:id/help
  → controller/bargain.Help（取 Principal.UserID）
  → logic/bargain.Help → g.DB().Transaction
      1. SELECT ... FROM bargain_instances WHERE id=? FOR UPDATE   // 行锁 + (expire_at<=NOW()) AS expired
      2. 本人助力 → 23006；非进行中/过期 → 23007；次数用尽 → 23007
      3. 读 bargain_activities.help_amount，cut = min(help, current-floor)
      4. UPDATE bargain_instances SET current_price=current_price-?, helped_count=helped_count+1
           WHERE id=? AND status=10 AND helped_count<max_help_count
             AND current_price>floor_price AND expire_at>NOW()
         0 行 → 23007（回滚）
      5. INSERT bargain_help_records（撞 uk_instance_helper → 23008，整体回滚）
      6. 若 price_after<=floor_price → UPDATE ... SET status=20, completed_at=NOW() WHERE status=10
  → 提交后重新读取实例与助力记录返回
```

读取路径的懒过期：

```text
GET /bargain-instances/:id | GET /my/bargain-instances | POST /bargain-instances/:id/abandon
  → UPDATE bargain_instances SET status=40
      WHERE (id=? 或 user_id=?) AND status=10 AND expire_at<=NOW()
     （条件更新，重复执行 0 行，无副作用；不写 completed_at）
```

### 5. 关键数据结构

`bargain_activities`（活动，运营配置）：

- `start_price`/`floor_price`/`help_amount`/`max_help_count` + 时间窗 + `status`（1/0）。
- 活动是**配置**，改价只影响之后创建的实例与之后的助力额。

`bargain_instances`（实例，用户参与事实）：

- 快照：`start_price`/`floor_price`/`current_price`/`max_help_count`/`expire_at(=活动 end_time)`；
- `help_amount` **不快照**（DDL 事实），助力时读活动当前值；
- `active_key` 为 STORED 生成列：`status=10` 时 `"activity_id:user_id"`，否则 NULL；
- `uk_active_instance(active_key)`：一人一活动至多一个进行中实例；
- `uk_order_id(order_id)`：一个订单只能对应一个实例（本期恒 NULL）。

`bargain_help_records`（助力凭据，只追加）：

- `uk_instance_helper(instance_id, helper_user_id)`：同一用户对同一实例只能助力一次；
- `cut_amount`/`price_before`/`price_after` 三者必须自洽（`price_after = price_before - cut_amount`，`price_after ≥ floor_price`）。

对外状态映射（DB → API）：`10→in_progress`、`20→success`、`30→failed`、`40→expired`；
`remaining_amount = current_price - floor_price`（不足 0 时按 0 兜底，属防御分支）。

### 6. 业务不变量与落点

| 不变量 | 实现位置 |
| --- | --- |
| 无效活动不能发起 | `Start` 的 `INSERT ... SELECT` 条件（status + 时间窗），失败分类 `classifyStartRejection` |
| 同用户同活动一个进行中实例 | `uk_active_instance`；`Start` 捕获 1062 → `23005` |
| 同用户不能重复助力 | `uk_instance_helper`；`Help` 捕获 1062 → `23008` 并回滚 |
| 禁止自助 | `Help` 中 `inst.UserId == helperID` → `23006`（在写之前拒绝） |
| 价格不低于底价 | `calcCutAmount` + 条件更新中的 `current_price > floor_price` + `price_after >= floor_price` |
| 助力记录与金额一致 | 同事务先更新实例再插入记录；记录值取锁内快照 |
| 并发安全 | `SELECT ... FOR UPDATE` + 条件更新 + 唯一索引三重保障 |
| 终态不可推进 | `Help`/`Abandon` 均以 `status=10` 为条件；终态返回 `23007`/`23009` |
| 过期幂等 | 懒过期为条件更新（`status=10 AND expire_at<=NOW()`），重复 0 行 |

### 7. 事务、锁与幂等

- **事务边界**：只在 `Help` 里显式开启事务（锁实例 + 改价格 + 写记录 + 置成功必须原子）。其它写操作都是单语句，
  依赖语句原子性（`INSERT ... SELECT`、条件 `UPDATE`）。
- **锁**：`FOR UPDATE` 加在实例行上；同一实例的并发助力排队，不同实例互不影响。没有全局锁、没有 Redis 锁。
- **幂等**：
  - 发起：重复请求 → `23005`（不产生新实例）；
  - 助力：重复请求 → `23008`（不重复扣减，事务回滚保证价格不变）；
  - 放弃：重复 → `23009`；
  - 过期：重复推进 → 0 行，无副作用。
- **失败语义**：所有拒绝路径都不留半成品——事务内错误统一回滚；单语句 0 行即无写入。

### 8. API 与错误码

请求/响应字段与权限见 `docs/design/marketing.md` §3.10。工程侧要点：

- 前台用户身份一律取 `middleware.PrincipalFromContext`（不信任请求体 user_id）；
- 后台接口经 `AdminAuth + RequirePermission`，controller 内再校验 `AdminPrincipalFromContext`；
- 实例详情与助力记录对**任何登录用户**开放（好友需要打开分享链接），只有发起/放弃限本人；
- 错误码：`23001` 活动不存在、`23002` 活动参数非法、`23003` 活动不可参与、`23004` 实例不存在/非本人、
  `23005` 已有进行中实例、`23006` 给自己助力、`23007` 实例不可助力、`23008` 重复助力、`23009` 非法状态迁移；
- 参数错误（分页越界、路径 id ≤0）复用通用 `1001`。

改完 API 必须执行：

```bash
go run ./tools/openapi-gen -repo .
bash scripts/contract-check.sh
```

新增 DTO 若生成了未被 `$ref` 引用的请求组件，需在 `tools/openapi-gen/unused-components-allowlist.yaml`
的 `dto-request` 分组逐项登记，否则 `-check` 会失败（这是有意设计，防止契约出现幽灵组件）。

### 9. 测试地图

| 类型 | 文件 | 覆盖 |
| --- | --- | --- |
| 纯函数单测 | `internal/logic/bargain/bargain_test.go` | 砍减额算法、可达成性、活动参数校验（含非法价格/时间/助力参数）、状态映射、分页 |
| 真实 MySQL 集成 | `internal/logic/bargain/integration_test.go` | 创建校验、发起/重复发起/并发发起、并发助力底价保护、重复助力、自助拒绝、撞唯一键回滚、过期、放弃与重启、成功后终态拒绝 |
| 路由契约 | `internal/cmd/routes_test.go` | 210 条路由双向锁定 + OpenAPI 一致 |
| 契约工具 | `tools/openapi-gen` 单测 | 生成器自身的路由/DTO/错误码/权限一致性 |

集成测试需要**隔离 MySQL**（默认 `127.0.0.1:13307`，库 `my_shop_bargain_test`），可用环境变量覆盖：

```bash
BARGAIN_TEST_MYSQL_HOST=127.0.0.1 BARGAIN_TEST_MYSQL_PORT=13307 \
BARGAIN_TEST_MYSQL_USER=root BARGAIN_TEST_MYSQL_PASS=*** \
BARGAIN_TEST_MYSQL_NAME=my_shop_bargain_test \
go test ./internal/logic/bargain/ -run TestBargain -v
```

测试自己执行 `migrations.Up`，并在用例前后清空 `bargain_*`/`skus`/`products`/`categories`/`users`；
**不要**把它指向开发库或其它任务的测试库。并发用例（`TestBargainHelpConcurrentFloor`、
`TestBargainStartConcurrentSingleActive`）是底价与唯一性的真实证据，不能用 Mock 或「先查再写」的单测替代。

### 10. 排障

| 现象 | 排查顺序 |
| --- | --- |
| 发起返回 23003 | 活动 `status` 是否为 1；`NOW()` 是否落在 `[start_time, end_time)`；注意时间比较在 SQL 侧，容器/会话时区不一致会让「看起来在窗口内」的活动被判过期 |
| 发起返回 23005 | 该用户在该活动已有 `status=10` 实例：`SELECT id,status,active_key FROM bargain_instances WHERE user_id=? AND activity_id=?` |
| 助力返回 23007 | 实例是否已 `20/30/40`；`helped_count >= max_help_count`；`current_price` 是否已等于 `floor_price`；`expire_at` 是否已过 |
| 助力返回 23008 | `uk_instance_helper` 命中：该 helper 已助力过（含并发重复请求） |
| 助力返回 23006 | 助力者是实例发起者本人 |
| 价格「停在半路」且状态仍进行中 | 检查是否 `expire_at` 已过但没人读过（懒过期未触发）；读一次详情即可推进到 40 |
| 后台列表看不到活动 | `GET /admin/bargain-activities` 需要 `bargain:list`；前台列表只返回启用且在时间窗内的活动 |

### 11. 维护注意事项

- **不要**给实例加 `help_amount` 快照列：当前语义是「助力额随活动配置，底价随实例快照」，改语义需要 Owner 决策并同步设计文档。
- **不要**把 `active_key` 改成普通列或改为应用层查重：并发正确性完全依赖生成列 + 唯一索引。
- **不要**把助力改成「先查再写」或去掉 `FOR UPDATE`：并发下会出现价格与助力记录不一致。
- **不要**在助力事务里调用其它模块的写方法（如扣库存、锁券）：会让事务跨越模块边界，且本期不需要。
- 活动有实例后 `product_id`/`sku_id` 不可改绑；需要换商品请新建活动（见 `AdminUpdate`）。
- 新增前台路由时同步 `internal/cmd/routes_test.go` 的 `fullRouteTable`、`openapi` 生成结果与契约脚本的成功文案计数。
- 砍价错误码域为 `21000-21999`；`20000-20999` 已归 B1-04-03（运费/计价），不要越界使用。

### B1-04 集成状态

| 能力 | 状态 |
| --- | --- |
| 砍价活动管理（后台 CRUD/启停） | 已集成（B1-04-07） |
| 用户发起砍价 / 好友助力 / 并发与最低价保护 / 懒过期 | 已集成 |
| 砍价成功后创建订单 | **未实现**：成交价、下单时机与订单关联方式尚无冻结契约，待 Owner 决策；不得绕过 `pricing.Quote` 创建特殊订单 |
| 拼团 | **已集成**（B1-04-06）：活动管理、开团/参团、成团/过期/运营关闭、超时扫描器、订单/库存/退款协同 |
