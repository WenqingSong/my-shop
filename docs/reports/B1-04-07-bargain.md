# B1-04-07 砍价业务完整实现 — 交付报告

> 状态：**独立开发分支交付，等待主 Chat 统一集成**。未合并回 `feat/p2-backend`，未合并其他任务分支，未合并 `main`。

## 1. Git 信息

| 项 | 值 |
| --- | --- |
| 仓库 | `/root/projects/my-shop` |
| 基础分支 | `feat/p2-backend` |
| 基础 SHA | `af512f9e8a64c133c705d3a0ed1dd106de3d5c60`（本地分支与 `origin/feat/p2-backend` 一致，`rev-list --left-right --count` = `0 0`，创建时工作区 clean） |
| 开发分支 | `feat/b1-04-07-bargain`（从 `origin/feat/p2-backend` 创建） |
| Worktree | `/root/projects/my-shop-b1-04-07` |
| 实现提交 | `1574e0587d55761c56e563d30dfe89f38e0aea81` — feat(bargain): 砍价活动、发起助力与状态推进完整实现 |
| 报告提交 | 本文件所在提交（HEAD） |
| 工作区状态 | 提交后 clean；仅存在本任务自建的隔离测试容器（MySQL `13307`、Redis `6379`） |

B1-04-03（运费/计价）在 `/root/projects/my-shop-b1-04-03` 的独立 worktree 中，本任务**只读**查看了它的错误码区间，未引用、未合并、未改动其任何文件。

## 2. 开发前调查结论

### 2.1 Java 参考实现（结论：UNVERIFIABLE）

任务指定的 `tmp/eshop-backend-java` 与 `tmp/shopping.sql` **在本机不存在**（`find /` 全盘检索无结果，
仓库主 worktree 也没有 `tmp/` 目录）。因此无法核对 Java 的砍价活动管理、发起、助力金额、成功判定、
重复助力、订单关联等实际行为。

按任务允许的替代依据，本实现**完全依据已批准设计**：

- `docs/design/marketing.md` §3（数据模型、状态机、§3.5 并发与重复助力的条件更新 SQL）；
- `internal/migrations/sql/20261110000012_bargain.up.sql` 顶部注释（同一算法的 DDL 事实源表述）。

未编造任何「Java 已有行为」。若 Owner 后续提供 Java 源码，建议重点复核两点：
助力金额是否为「名义额 + 最后一次补足差额」（本实现）以及砍价成功后是否强制下单。

### 2.2 现有数据库模型（结论：足够，无新增迁移）

`bargain_activities` / `bargain_instances` / `bargain_help_records` 的字段、索引、唯一约束、
生成列 `active_key`、外键均满足设计需求，本任务**未新增任何 migration**，也未修改 B1-03 的历史 DDL。

### 2.3 需要实现的 API

13 条（8 条前台 + 5 条后台），见 §5。公开浏览 2 条，登录用户 6 条，后台 5 条。

### 2.4 事务与并发保护

- 发起：单语句 `INSERT ... SELECT`（活动可参与判定 + 快照写入原子化）+ `uk_active_instance` 唯一索引；
- 助力：单事务 `SELECT ... FOR UPDATE` → 条件 `UPDATE` → 插入记录 → 达底价置成功，
  失败由 `uk_instance_helper` 与条件更新双重兜底；
- 过期：读取路径的条件 `UPDATE`（幂等），不引入 MQ/Redis/新调度器。

### 2.5 新增错误码与权限码

- 错误码域 `21000-21999`（域序 21），具体 `21001-21009`；避开 B1-04-03 已占用的 `20000-20999`。
- 权限码 `bargain:create` / `bargain:update` / `bargain:list` / `bargain:view`。

### 2.6 预计/实际修改文件

见 §6 文件清单。

## 3. 功能实现

### 3.1 砍价活动管理（后台）

- 创建：校验名称（trim 后 1~64 字符）、`product_id`/`sku_id` 存在性且 SKU 归属该商品、
  `1 ≤ floor_price < start_price ≤ 99999999`、`help_amount ≥ 1`、`max_help_count ≥ 1`、
  时间窗 `start_time < end_time` 且 `end_time > now`、以及**可达成性**
  `ceil((start_price-floor_price)/help_amount) ≤ max_help_count`。
- 编辑：可改名称/价格/助力参数/时间窗；`product_id`/`sku_id` 不可改绑（在途实例以活动为展示来源）。
- 启停：`POST /admin/bargain-activities/:id/status`，幂等；停用只阻止新发起，不影响已创建实例。
- 列表：分页 + 状态/商品筛选，带商品名/主图/SKU 名展示字段；详情同样带展示字段。

### 3.2 用户发起砍价

- 公开活动列表（仅启用且在时间窗内）与详情（含 `is_joinable`）。
- 发起：单语句 `INSERT ... SELECT` 从活动行快照 `start_price`/`floor_price`/`current_price=start_price`/
  `max_help_count`/`expire_at=end_time`；活动不存在 `21001`、不可参与 `21003`、已有进行中实例 `21005`。
- 我的砍价记录（分页）、实例详情（登录用户均可查看，便于好友打开分享）、剩余目标金额派生返回。

### 3.3 好友助力（核心）

- 禁止给自己助力（`21006`，在写之前拒绝）。
- 同一用户对同一实例只能助力一次（`uk_instance_helper`，`21008`）。
- 终态/过期不可助力（`21007`）；次数用尽 `21007`；已到底价 `21007`。
- 金额算法 `cut = min(help_amount, current_price - floor_price)`，`price_after ≥ floor_price` 恒成立；
  最后一次只补足差额。
- 助力记录与实例金额/次数**同事务提交**；撞唯一键整体回滚（价格不落库）。
- 助力记录列表返回 `cut_amount`/`price_before`/`price_after`/助力者昵称与头像。

### 3.4 成功 / 失败 / 过期

- 成功（20）：`price_after == floor_price` 时在同一事务内条件更新 `status=20, completed_at=NOW()`。
- 失败（30）：发起者本人放弃（`POST /bargain-instances/:id/abandon`），仅进行中可放弃，重复放弃 `21009`。
- 过期（40）：读取/放弃前懒推进 `status=10 AND expire_at<=NOW() → 40`，条件更新可重复执行且无副作用，
  不写 `completed_at`；过期后 `active_key` 释放，可重新发起。
- 终态不可重复推进；未引入定时任务，理由见 `docs/design/marketing.md` §7。

### 3.5 砍价订单关联（本期不实现）

现有订单创建始终重读 `skus.price` 计价，不支持「按砍价成交价下单」；实现它必须改动订单金额公式/计价入口，
属于 B1-04-03 与后续计价集成任务范围，本任务明令禁止。因此：

- `bargain_instances.order_id` 本期恒为 NULL；`uk_order_id` 已就位，后续集成任务在下单成功后回填即可；
- 砍价成功只表达「价格已到目标价」，不锁库存、不产生支付/履约状态；
- 该限制已写入 `marketing.md` §3.11 与 §7。

## 4. 数据库与事务

### 4.1 使用的表（未新建、未改结构）

| 表 | 用途 | 关键约束 |
| --- | --- | --- |
| `bargain_activities` | 活动配置（价格/助力参数/时间窗/启停） | FK → `products`/`skus` RESTRICT |
| `bargain_instances` | 用户实例与价格推进事实 | `uk_active_instance(active_key)`、`uk_order_id`、`idx_user_status`、`idx_status_expire` |
| `bargain_help_records` | 助力凭据（只追加） | `uk_instance_helper(instance_id, helper_user_id)` |

### 4.2 事务边界

- 只有 `Help` 开启显式事务。原因：锁实例、改价格、写记录、置成功必须原子。
- 其它写操作是单语句：发起用 `INSERT ... SELECT`，放弃/过期用条件 `UPDATE`，创建/编辑活动为单条写，
  语义上已是原子的，不额外包事务。

### 4.3 锁与幂等

- `SELECT ... FOR UPDATE` 锁实例行：同一实例的并发助力排队，不同实例互不影响；无全局锁、无 Redis 锁。
- 条件更新（`status=10 AND helped_count<max_help_count AND current_price>floor_price AND expire_at>NOW()`）
  是绕过锁也成立的第二道闸门。
- 幂等：重复发起 `21005`；重复助力 `21008`（事务回滚，价格不变）；重复放弃 `21009`；重复过期推进 0 行。
- 时间比较全部在 SQL 侧（`NOW()`），避免 Go↔MySQL 时区漂移；实例的 `expire_at` 是活动 `end_time` 快照。

## 5. API、权限码与错误码

### 5.1 新增 API（13 条）

| 方法 | 路径 | 认证/权限 |
| --- | --- | --- |
| GET | `/bargain-activities` | 公开 |
| GET | `/bargain-activities/:id` | 公开 |
| POST | `/bargain-activities/:id/instances` | 用户 JWT |
| GET | `/my/bargain-instances` | 用户 JWT |
| GET | `/bargain-instances/:id` | 用户 JWT |
| GET | `/bargain-instances/:id/helps` | 用户 JWT |
| POST | `/bargain-instances/:id/help` | 用户 JWT |
| POST | `/bargain-instances/:id/abandon` | 用户 JWT |
| POST | `/admin/bargain-activities` | `bargain:create` |
| GET | `/admin/bargain-activities` | `bargain:list` |
| GET | `/admin/bargain-activities/:id` | `bargain:view` |
| PUT | `/admin/bargain-activities/:id` | `bargain:update` |
| POST | `/admin/bargain-activities/:id/status` | `bargain:update` |

路由总数由 142 条增至 155 条；`internal/cmd/routes_test.go` 的清单、分布断言（75/78/2）与
OpenAPI 数断言已同步更新，未放宽断言。

### 5.2 权限码（seed 幂等写入）

`bargain:create`（创建活动）、`bargain:update`（编辑 + 启停）、`bargain:list`（列表）、`bargain:view`（详情）。

### 5.3 错误码（`internal/codes/codes.go`，域序 21）

| code | 常量 | 语义 | HTTP |
| --- | --- | --- | --- |
| 21001 | `CodeBargainActivityNotFound` | 活动不存在 | 404 |
| 21002 | `CodeBargainActivityInvalid` | 活动参数非法（价格/时间/商品/SKU/助力参数/改绑商品） | 400 |
| 21003 | `CodeBargainActivityUnavailable` | 活动不可参与（停用/未开始/已结束） | 409 |
| 21004 | `CodeBargainInstanceNotFound` | 实例不存在或不属于当前用户 | 404 |
| 21005 | `CodeBargainInstanceExists` | 已有进行中的实例 | 409 |
| 21006 | `CodeBargainSelfHelpForbidden` | 不能给自己助力 | 409 |
| 21007 | `CodeBargainHelpUnavailable` | 实例不可助力（终态/过期/次数用尽/到底价） | 409 |
| 21008 | `CodeBargainHelpDuplicated` | 已助力过该实例 | 409 |
| 21009 | `CodeBargainInvalidStatusTransition` | 实例状态迁移非法 | 409 |

`.agent/registry/error-codes.md` 已登记 `21000-21999 | B1-04-07 | RESERVED`（域序 21，跳号原因：域序 20 归 B1-04-03）。
注意：Registry 的共享生效仍需主 Chat 在 `develop` 侧确认（本分支的登记不构成跨分支有效预留）。

## 6. 文件变更

### 6.1 新增

| 文件 | 说明 |
| --- | --- |
| `api/bargain/v1/bargain.go` | 砍价 DTO 与路由元信息 |
| `internal/service/bargain.go` | `IBargain` 接口与注册 |
| `internal/logic/bargain/bargain.go` | 业务实现（活动/发起/助力/状态/查询） |
| `internal/logic/bargain/bargain_test.go` | 纯函数单测（算法、校验、映射、分页） |
| `internal/logic/bargain/integration_test.go` | 真实 MySQL 集成与并发测试 |
| `internal/controller/bargain/bargain.go` | 前台 + 后台 Controller |
| `internal/controller/bargain/bargain_test.go` | 真实 HTTP 端到端测试（认证/权限/错误码） |
| `openapi/paths/bargain.yaml` | 生成的分域契约视图 |
| `docs/dev/marketing.md` | 砍价开发知识文档（新增） |
| `docs/reports/B1-04-07-bargain.md` | 本报告 |

### 6.2 修改

| 文件 | 说明 |
| --- | --- |
| `internal/codes/codes.go` | 砍价错误码 + 码表 |
| `internal/boot/seed.go` | 4 个 `bargain:*` 权限 seed |
| `internal/logic/logic.go` | 注册 bargain 逻辑包 |
| `internal/cmd/routes_frontend.go` | 公开 2 条 + 登录用户 6 条路由 |
| `internal/cmd/routes_admin.go` | 后台 5 条路由（AdminAuth + RequirePermission） |
| `internal/cmd/routes_test.go` | 路由清单 142→155、分布 75/78/2、契约数断言 |
| `tools/openapi-gen/{gen,model,parse_codes}.go` | 登记 bargain 业务域、Schema 命名、错误码域、计数 155 |
| `tools/openapi-gen/unused-components-allowlist.yaml` | 登记 13 个砍价请求 DTO（内联生成约定） |
| `tools/openapi-gen/main_test.go` | 路由解析数 140→153 |
| `scripts/contract-check.sh` | 成功文案计数 142→155 |
| `docs/design/marketing.md` | 砍价实现章节 §3.6–§3.11、不变量 INV-BG09/BG10、留白更新 |
| `docs/design/rbac.md` | 权限清单追加 `bargain:*` |
| `docs/design/error-codes.md` | 模块错误码章节补 `marketing.md` |
| `.agent/registry/error-codes.md` | 登记 `21000-21999`（RESERVED） |
| `openapi/**`（生成物） | 重新生成：新增砍价、错误码、Schema；清理基线漂移（见 §9.1） |

### 6.3 删除

| 文件 | 原因 |
| --- | --- |
| `openapi/paths/region.yaml` | 基线遗留的未实现契约（详见 §9.1） |

## 7. 测试结果

测试环境：本任务自建的隔离容器
`my-shop-b104-07-test-mysql`（MySQL 8.0，`127.0.0.1:13307`，库 `my_shop_bargain_test`）与
`my-shop-b104-07-test-redis`（Redis 7，`127.0.0.1:6379`），应用数据目录与其它任务完全隔离。

通用环境变量（下称 `$TESTENV`）：

```bash
DATABASE_DEFAULT_HOST=127.0.0.1 DATABASE_DEFAULT_PORT=13307 \
DATABASE_DEFAULT_USER=root DATABASE_DEFAULT_PASS=bargain-test-pass \
DATABASE_DEFAULT_NAME=my_shop_bargain_test
```

### 7.1 命令与退出码

| # | 命令 | 退出码 | 结果 |
| --- | --- | --- | --- |
| 1 | `gofmt -l <本次修改/新增的 .go 文件>` | 0（无输出） | PASS |
| 2 | `go build ./...` | 0 | PASS |
| 3 | `go vet ./...` | 0 | PASS |
| 4 | `env $TESTENV go test ./internal/logic/bargain/ -count=1 -v` | 0 | PASS（5 个单测 + 9 个集成用例，含并发） |
| 5 | `env $TESTENV go test ./internal/logic/bargain/ -run 'TestBargainHelpConcurrentFloor\|TestBargainStartConcurrentSingleActive' -race -count=1` | 0 | PASS |
| 6 | `env $TESTENV go test ./internal/controller/bargain/ -count=1 -v` | 0 | PASS（7 个子场景） |
| 7 | `env $TESTENV go test -p 1 ./internal/logic/...` | 0 | PASS |
| 8 | `env $TESTENV go test -p 1 ./internal/controller/...` | 0 | PASS |
| 9 | `go test ./internal/service/...` | 0 | PASS（`no test files`，接口层无独立测试） |
| 10 | `go test ./internal/cmd/... -count=1` | 0 | PASS（155 条路由双向锁定 + 契约一致） |
| 11 | `env $TESTENV go test -p 1 -count=1 $(go list ./... \| grep -v '/test/e2e')` | 0 | PASS（全仓除 e2e 全部通过，含 migrations 282s） |
| 12 | `go run ./tools/openapi-gen -repo .` | 0 | PASS（生成 155 条 API） |
| 13 | `bash scripts/contract-check.sh` | 0 | PASS（13 项检查，NOT RUN 0） |
| 14 | `go test ./tools/openapi-gen/... -count=1` | 0 | PASS（生成器自测） |

未执行项：

| 项 | 状态 | 原因 |
| --- | --- | --- |
| `test/e2e/**` | NOT_RUN | 需要 Nginx + 已部署服务 + 完整依赖栈，不属于本任务范围；本任务已用真实 HTTP 集成测试覆盖砍价链路 |
| Java 参考实现逐项比对 | NOT_VERIFIED | `tmp/eshop-backend-java` 与 `tmp/shopping.sql` 在本机不存在 |
| 前端页面 | NOT_RUN | 任务明确不开发前端 |
| 真实 Kubernetes/生产部署 | NOT_VERIFIED | 本任务不涉及部署变更（无新配置、无新迁移） |

### 7.2 业务不变量 ↔ 测试映射

| 不变量 | 测试 |
| --- | --- |
| 无效活动不能发起 | `TestBargainStart`（停用/已结束/未开始 → 21003；不存在 → 21001）、HTTP `停用活动阻止新发起` |
| 活跃实例唯一（同用户同活动） | `TestBargainStartConcurrentSingleActive`（8 并发只成功 1 次，DB 仅 1 条）、`TestBargainStart`（重复 → 21005） |
| 助力唯一（同用户同实例） | `TestBargainHelpDuplicateAndSelf`（重复 → 21008，记录数 1，价格只降一次）、HTTP `重复助力` |
| 禁止自助 | `TestBargainHelpDuplicateAndSelf`（21006，无记录）、HTTP `给自己助力` |
| 金额下限（不低于底价） | `TestBargainHelpConcurrentFloor`（20 并发只成功 5 次，终价恰为 9500）、`TestCalcCutAmount` |
| 金额正确性（记录与实例一致） | `TestBargainHelpConcurrentFloor`（逐条 `price_after=price_before-cut`，Σcut = start−final） |
| 并发安全 | 同上两个并发用例 + `-race` 运行 |
| 事务一致（记录与价格原子） | `TestBargainHelpRollbackOnDuplicateRecord`（撞唯一键 → 价格/次数保持 10000/0） |
| 状态合法（终态不可推进） | `TestBargainExpire`（过期后助力 21007、放弃 21009）、`TestBargainHelpAfterSuccess`（成功后助力 21007）、`TestBargainAbandonAndRestart` |
| 活动配置合法性 | `TestBargainAdminCreate`（商品不存在/SKU 归属不符/底价=原价 → 21002；改绑商品 → 21002） |
| 权限 | HTTP `认证与权限`（未登录 401、用户 token 403、无角色管理员 403） |

## 8. 已知限制

1. **砍价成功后未下单**：本任务不创建订单、不回填 `order_id`（理由见 §3.5）。
2. **过期依赖读取路径推进**：无人读取的过期实例在库中仍是 `status=10`；不影响助力判定（助力条件更新自带
   `expire_at > NOW()`），但若后续要做「到期自动统计/清理」，需要补扫描器。
3. **`help_amount` 不快照**：助力额取活动当前配置；活动改价只影响后续助力的单次砍减额，底价受实例快照保护。
   这是 DDL 事实（实例无 `help_amount` 列），不是遗漏。
4. **活动不能删除**：有实例的活动受 FK RESTRICT 保护，只能停用（表结构无逻辑删除列）。
5. **后台无「强制结束活动并批量失败实例」**：终止在途实例目前只能由用户放弃或等待过期。
6. **未做防刷**（同 IP/设备、助力者关系、黑名单），与设计文档原有留白一致。

## 9. 后续集成注意事项

### 9.1 基线契约漂移（必须知悉）

`feat/p2-backend` 的 `af512f9` 提交只提交了 OpenAPI 契约，没有对应 Go 代码：
`PUT /me`、`GET /regions`、`GET /regions/:id` 以及地址结构化字段（`province_id`/`city_id`/`district_id`）
在契约中存在但无实现。基线自身即无法通过契约门禁（在纯净基线上 `TestRouteTableMatchesContract` 报
`PUT /me`、`/regions` 为「虚构接口」，OpenAPI 145 条 vs 预期 142）。

本任务按要求重新生成 OpenAPI 后，这些**无代码支撑的路径被生成器自然移除**，并删除了不再对应任何代码的
`openapi/paths/region.yaml`。合并时请注意：

- 若地区/用户资料任务同时合入，请在**它自己**的分支上重新生成契约（`go run ./tools/openapi-gen -repo .`），
  不要手工恢复本分支已删除的路径；
- `openapi/surgecart.openapi.yaml` / `components/schemas.yaml` 的 diff 很大（生成器重排 + 基线漂移清理），
  合并时建议以「重新生成」为准，避免手工解冲突。

### 9.2 错误码与 Registry

- 砍价域 `21000-21999`（域序 21）已登记在 `.agent/registry/error-codes.md`；该文件位于 `develop` 时为权威事实源，
  本分支的登记需由主 Chat 在共享分支确认后才对其他任务生效。
- 与 B1-04-03 的 `20000-20999`（域序 20）**无重叠**；若 B1-04-03 后续扩展到 `21000+` 会冲突，需协调。

### 9.3 订单/计价集成

- 砍价成交价下单需要订单侧支持「外部成交价」或计价插件点；当前订单创建只认 `skus.price`。
  建议集成任务复用 `Order.Create` 时增加明确的营销价来源参数，并在同一事务内回填 `bargain_instances.order_id`。
- `uk_order_id` 已就位：一个订单只能绑定一个实例；若未来支持「同一订单买多件砍价商品」，需要重新设计（当前不支持）。
- 本任务未改动 `internal/logic/pricing`、订单金额公式、运费、库存、秒杀、拼团。

### 9.4 合并冲突热点

| 文件 | 冲突原因 | 处理建议 |
| --- | --- | --- |
| `openapi/**` | 生成物，多任务并行必冲突 | 以合并后重新生成为准 |
| `internal/cmd/routes_test.go` | 路由总数断言（155） | 合并后按实际条数重新计数 |
| `tools/openapi-gen/gen.go` | 计数常量与 tag 顺序 | 取并集后重新生成 |
| `internal/codes/codes.go` | 各任务追加域 | 保留双方新增块，注意逗号与码表 |
| `internal/boot/seed.go` | 权限 seed 追加 | 取并集 |
| `docs/design/marketing.md` | 拼团任务可能同改 | 本任务只改动砍价章节（§3.6–§3.11、INV-BG09/10、§7），拼团章节未动 |

## 10. 结论

砍价业务（活动管理、发起、助力、金额推进、成功/失败/过期、实例与助力记录查询）已在
`feat/b1-04-07-bargain` 完整实现并通过真实 MySQL 并发验证、真实 HTTP 端到端验证与契约门禁；
唯一明确的外部依赖是「砍价成交价下单」，按要求留给后续订单/计价集成任务。

**最终状态：独立开发分支交付，等待主 Chat 统一集成。**
