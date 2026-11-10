# 营销（拼团 / 砍价）模块知识（接手必读）

砍价的业务语义、状态机与不变量见 `docs/design/marketing.md` §3；本文只记录**已落地代码**的入口、
调用链、事务边界与排障方法。拼团（`group_buy_*`）在本分支仍只有表结构，没有业务代码。

## 1. 模块定位

砍价回答「哪个 SKU 的原价/底价是多少、这个用户当前砍到多少、谁帮过忙、还差多少、是否已成功/失败/过期」。
它是**营销侧的价格推进**能力，不拥有库存、不拥有支付、不拥有订单金额：

- 不扣库存、不建活动库存列、不读写 `inventories`/`flash_sale_*`；
- 不创建订单：`bargain_instances.order_id` 本期保持 NULL，成交价下单属于后续计价集成任务；
- 事实来源单一 MySQL，无 MQ；Redis 只用于登录会话（与砍价逻辑无关）。

## 2. 当前实现状态

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

## 3. 代码地图

| 关注点 | 位置 |
| --- | --- |
| DTO 与路由元信息（`g.Meta`） | `api/bargain/v1/bargain.go` |
| Controller（前台 / 后台） | `internal/controller/bargain/bargain.go` |
| 服务接口 `IBargain` | `internal/service/bargain.go` |
| 业务实现（校验/发起/助力/状态/查询） | `internal/logic/bargain/bargain.go` |
| 纯函数单测 | `internal/logic/bargain/bargain_test.go` |
| 真实 MySQL 集成与并发测试 | `internal/logic/bargain/integration_test.go` |
| 路由注册 | `internal/cmd/routes_frontend.go`、`internal/cmd/routes_admin.go` |
| 路由表冻结清单 | `internal/cmd/routes_test.go`（`fullRouteTable`，155 条） |
| 错误码 | `internal/codes/codes.go`（砍价域 `21001-21009`） |
| 权限 seed | `internal/boot/seed.go`（`bargain:create/update/list/view`） |
| OpenAPI | `openapi/paths/bargain.yaml` + `openapi/surgecart.openapi.yaml`（生成，不手工改） |

表结构事实源：`internal/migrations/sql/20261110000012_bargain.up.sql`（B1-03-03 交付，本任务未改）。

## 4. 核心调用链

发起砍价：

```text
POST /bargain-activities/:id/instances
  → controller/bargain.Start（取 Principal.UserID）
  → logic/bargain.Start
      INSERT INTO bargain_instances (...) SELECT ... FROM bargain_activities
        WHERE id=? AND status=1 AND start_time<=NOW() AND end_time>NOW()
      0 行 → 21001/21003；1062（uk_active_instance）→ 21005
  → loadInstance（联查活动/商品/SKU 展示字段）
```

好友助力（核心）：

```text
POST /bargain-instances/:id/help
  → controller/bargain.Help（取 Principal.UserID）
  → logic/bargain.Help → g.DB().Transaction
      1. SELECT ... FROM bargain_instances WHERE id=? FOR UPDATE   // 行锁 + (expire_at<=NOW()) AS expired
      2. 本人助力 → 21006；非进行中/过期 → 21007；次数用尽 → 21007
      3. 读 bargain_activities.help_amount，cut = min(help, current-floor)
      4. UPDATE bargain_instances SET current_price=current_price-?, helped_count=helped_count+1
           WHERE id=? AND status=10 AND helped_count<max_help_count
             AND current_price>floor_price AND expire_at>NOW()
         0 行 → 21007（回滚）
      5. INSERT bargain_help_records（撞 uk_instance_helper → 21008，整体回滚）
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

## 5. 关键数据结构

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

## 6. 业务不变量与落点

| 不变量 | 实现位置 |
| --- | --- |
| 无效活动不能发起 | `Start` 的 `INSERT ... SELECT` 条件（status + 时间窗），失败分类 `classifyStartRejection` |
| 同用户同活动一个进行中实例 | `uk_active_instance`；`Start` 捕获 1062 → `21005` |
| 同用户不能重复助力 | `uk_instance_helper`；`Help` 捕获 1062 → `21008` 并回滚 |
| 禁止自助 | `Help` 中 `inst.UserId == helperID` → `21006`（在写之前拒绝） |
| 价格不低于底价 | `calcCutAmount` + 条件更新中的 `current_price > floor_price` + `price_after >= floor_price` |
| 助力记录与金额一致 | 同事务先更新实例再插入记录；记录值取锁内快照 |
| 并发安全 | `SELECT ... FOR UPDATE` + 条件更新 + 唯一索引三重保障 |
| 终态不可推进 | `Help`/`Abandon` 均以 `status=10` 为条件；终态返回 `21007`/`21009` |
| 过期幂等 | 懒过期为条件更新（`status=10 AND expire_at<=NOW()`），重复 0 行 |

## 7. 事务、锁与幂等

- **事务边界**：只在 `Help` 里显式开启事务（锁实例 + 改价格 + 写记录 + 置成功必须原子）。其它写操作都是单语句，
  依赖语句原子性（`INSERT ... SELECT`、条件 `UPDATE`）。
- **锁**：`FOR UPDATE` 加在实例行上；同一实例的并发助力排队，不同实例互不影响。没有全局锁、没有 Redis 锁。
- **幂等**：
  - 发起：重复请求 → `21005`（不产生新实例）；
  - 助力：重复请求 → `21008`（不重复扣减，事务回滚保证价格不变）；
  - 放弃：重复 → `21009`；
  - 过期：重复推进 → 0 行，无副作用。
- **失败语义**：所有拒绝路径都不留半成品——事务内错误统一回滚；单语句 0 行即无写入。

## 8. API 与错误码

请求/响应字段与权限见 `docs/design/marketing.md` §3.10。工程侧要点：

- 前台用户身份一律取 `middleware.PrincipalFromContext`（不信任请求体 user_id）；
- 后台接口经 `AdminAuth + RequirePermission`，controller 内再校验 `AdminPrincipalFromContext`；
- 实例详情与助力记录对**任何登录用户**开放（好友需要打开分享链接），只有发起/放弃限本人；
- 错误码：`21001` 活动不存在、`21002` 活动参数非法、`21003` 活动不可参与、`21004` 实例不存在/非本人、
  `21005` 已有进行中实例、`21006` 给自己助力、`21007` 实例不可助力、`21008` 重复助力、`21009` 非法状态迁移；
- 参数错误（分页越界、路径 id ≤0）复用通用 `1001`。

改完 API 必须执行：

```bash
go run ./tools/openapi-gen -repo .
bash scripts/contract-check.sh
```

新增 DTO 若生成了未被 `$ref` 引用的请求组件，需在 `tools/openapi-gen/unused-components-allowlist.yaml`
的 `dto-request` 分组逐项登记，否则 `-check` 会失败（这是有意设计，防止契约出现幽灵组件）。

## 9. 测试地图

| 类型 | 文件 | 覆盖 |
| --- | --- | --- |
| 纯函数单测 | `internal/logic/bargain/bargain_test.go` | 砍减额算法、可达成性、活动参数校验（含非法价格/时间/助力参数）、状态映射、分页 |
| 真实 MySQL 集成 | `internal/logic/bargain/integration_test.go` | 创建校验、发起/重复发起/并发发起、并发助力底价保护、重复助力、自助拒绝、撞唯一键回滚、过期、放弃与重启、成功后终态拒绝 |
| 路由契约 | `internal/cmd/routes_test.go` | 155 条路由双向锁定 + OpenAPI 一致 |
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

## 10. 排障

| 现象 | 排查顺序 |
| --- | --- |
| 发起返回 21003 | 活动 `status` 是否为 1；`NOW()` 是否落在 `[start_time, end_time)`；注意时间比较在 SQL 侧，容器/会话时区不一致会让「看起来在窗口内」的活动被判过期 |
| 发起返回 21005 | 该用户在该活动已有 `status=10` 实例：`SELECT id,status,active_key FROM bargain_instances WHERE user_id=? AND activity_id=?` |
| 助力返回 21007 | 实例是否已 `20/30/40`；`helped_count >= max_help_count`；`current_price` 是否已等于 `floor_price`；`expire_at` 是否已过 |
| 助力返回 21008 | `uk_instance_helper` 命中：该 helper 已助力过（含并发重复请求） |
| 助力返回 21006 | 助力者是实例发起者本人 |
| 价格「停在半路」且状态仍进行中 | 检查是否 `expire_at` 已过但没人读过（懒过期未触发）；读一次详情即可推进到 40 |
| 后台列表看不到活动 | `GET /admin/bargain-activities` 需要 `bargain:list`；前台列表只返回启用且在时间窗内的活动 |

## 11. 维护注意事项

- **不要**给实例加 `help_amount` 快照列：当前语义是「助力额随活动配置，底价随实例快照」，改语义需要 Owner 决策并同步设计文档。
- **不要**把 `active_key` 改成普通列或改为应用层查重：并发正确性完全依赖生成列 + 唯一索引。
- **不要**把助力改成「先查再写」或去掉 `FOR UPDATE`：并发下会出现价格与助力记录不一致。
- **不要**在助力事务里调用其它模块的写方法（如扣库存、锁券）：会让事务跨越模块边界，且本期不需要。
- 活动有实例后 `product_id`/`sku_id` 不可改绑；需要换商品请新建活动（见 `AdminUpdate`）。
- 新增前台路由时同步 `internal/cmd/routes_test.go` 的 `fullRouteTable`、`openapi` 生成结果与契约脚本的成功文案计数。
- 砍价错误码域为 `21000-21999`；`20000-20999` 已归 B1-04-03（运费/计价），不要越界使用。
