# B1-04-03 订单金额明细与运费 —— 交付报告

> 状态：**部分交付（并行边界内的独立部分已完成，订单链路集成未执行）**。
> 本轮交付不等于 B1-04-03 整体完成；A6–A9 仍未完成，详见 §11。

## 1. 任务目标

为 SurgeCart 建立两条基础能力，并为后续订单重构提供唯一入口：

1. **运费模块**：运费模板 + 区域计费规则 + 按件计费 + 满额免邮，管理接口接入后台 RBAC。
2. **统一计价模块**：`pricing.Quote` 作为全项目唯一计价入口，输出六槽位金额明细并保证金额守恒，为 B1-04-05（积分）与 B1-04-08（会员折扣）预留扩展位。

订单算价接口、下单事务内复核、幂等指纹扩展与状态流水写入因并行依赖未满足而暂缓（§11）。

## 2. Git 基线及开发分支

| 项 | 值 |
| --- | --- |
| 仓库 | `/root/projects/my-shop` |
| 基础分支 | `feat/p2-backend` |
| 基础 SHA（核验后实际值） | `af512f9e8a64c133c705d3a0ed1dd106de3d5c60` |
| Worktree | `/root/projects/my-shop-b1-04-03` |
| 开发分支 | `feat/b1-04-03-order-pricing-freight` |
| 分支起点 | 与基础分支一致（`af512f9`） |

**基线偏差说明**：规划报告记录的基础版本为 `06c55f32b149da991e79d3bd97a80064e596e12c`，实际基础分支已前进两条提交（`06c55f3` → `86b79e1`/`5d0b296`/`af512f9`）。按任务要求以核验后的最新共同基线为准，未做任何回退。

`af512f9` 只修改了 `openapi/**` 与 `docs/phase2/**`（地区字典契约 + 地址结构化字段契约），**未包含 Go 实现**，这直接影响了下述两个问题：基线路由测试已红、地址无结构化地区字段。

## 3. 原有系统问题（核验结论）

以下问题均为实际核验所得，不是推测：

1. **Migration / 错误码 Registry 未预留**：`.agent/registry/migrations.md` 最大版本 `20261110000014`，`.agent/registry/error-codes.md` 最大域序 19；`origin/develop` 上更旧（最大 `20261001000024`）。仓库内不存在 Batch 0 预留记录。
2. **`order.go` 尚未接入运费与统一计价**：`orderRow`/`orderItemRow` 均无 `shipping_fee`、`spec_text` 字段映射，`Create` 直接写 `total_amount = original - discount`，`shipping_fee` 保持数据库默认 0。
3. **算价在事务外完成**：`Create` 在进入 `insertOrder` 事务前完成 `buildLines`、券折扣计算与总额计算；事务内只写库与扣库存。事务内外存在价格漂移窗口。
4. **`order_status_logs` 只有结构**：无任何业务写入路径。
5. **B1-04-02 未进入共同基线**：`feat/p2-backend` 无 B1-04-02 分支/提交，`order.go` 无 SKU 规格快照（`spec_text`）写入，`lineItem` 无规格字段。
6. **地址无结构化地区字段**：`addresses` 仅有 `province/city/district` 自由文本；`af512f9` 只在 OpenAPI 契约中声明了 `province_id/city_id/district_id` 与 `GET /regions*`，Go 侧不存在实现。因此运费只能以 `regions.id` 为输入，无法从 `address_id` 推导。
7. **基线路由测试已失败**：`TestRouteTableMatchesContract` 在 `af512f9` 上即 FAIL（OpenAPI 声明 `/regions` 但无对应路由），属既有基线问题。

## 4. 最终架构设计

```text
                 ┌───────────────────────────────┐
客户端 ──► API ──►│ 订单（未接入，待 B1-04-02）    │
                 └───────────────┬───────────────┘
                                 │
                 ┌───────────────▼───────────────┐
                 │ pricing.Quote（唯一计价入口）  │
                 │  Original/Coupon/Member/       │
                 │  Points/Shipping → Payable     │
                 └───────┬───────────────┬───────┘
                         │               │
        ┌────────────────▼──┐   ┌────────▼─────────────────┐
        │ ICoupon（既有）    │   │ IFreight.Quote（本任务）  │
        │ 券校验 + 折扣金额  │   │ 模板/区域规则/按件计费     │
        └───────────────────┘   └────────┬─────────────────┘
                                         │
                    ┌────────────────────▼──────────────────┐
                    │ freight_templates / freight_template_rules │
                    │ products.freight_template_id / regions     │
                    └───────────────────────────────────────┘
```

分层落点：

| 模块 | 路径 |
| --- | --- |
| 运费 API / Controller / Service / Logic | `api/freight/v1`、`internal/controller/freight`、`internal/service/freight.go`、`internal/logic/freight` |
| 计价 | `internal/logic/pricing` |
| Migration | `internal/migrations/sql/20261110000016_freight.{up,down}.sql` |
| 路由 / 权限 | `internal/cmd/routes_admin.go`、`internal/boot/seed.go` |
| 错误码 | `internal/codes/codes.go`（域序 20） |

设计原则：

- 计费内核（`calc.go`）是**纯函数**，不碰 DB，可完整单元测试；
- DB 读取（`quote.go`）只负责取权威数据，不复制公式；
- 计价（`pricing`）不依赖运费或优惠券，只接受已算好的槽位，避免循环依赖与"两套公式"。

## 5. 运费计算规则

### 5.1 按件计费

```text
数量 <= 首件数量：运费 = 首件费用
数量 >  首件数量：运费 = 首件费用 + ceil((数量 - 首件数量) / 续件数量) × 续件费用
```

- 金额一律整数分（`int64`），向上取整用整数除法（`extra/units` + 余数判断），不使用 float。
- 首件/续件数量必须 >= 1，否则 20007；费用必须非负，否则 20010；乘法前做溢出上界检查，溢出返回 20012。

### 5.2 区域匹配与上级回退

- 只按 `regions.id` 匹配，**不按名称**。
- 地区链 `[区, 市, 省]` 由 `parent_id` 上溯得到（最多 8 层 + 已访问集合防成环）。
- 由近及远首个命中生效。
- 本级地区不存在 → 20008；上级缺失 → 停止上溯；全链无规则 → 20009（**不静默免费**）。

### 5.3 免邮

- 模板 `is_free=1` → 该模板组 0。
- 生效阈值 = 区域规则 `free_threshold > 0 ? 区域值 : 模板值`。
- 比较基数为**该模板下商品原始金额合计**（`Σ skus.price × quantity`，不含运费与优惠），理由见 `docs/design/freight.md` §5。
- 命中阈值（`>=`）→ 该组 0。

### 5.4 多模板与历史兼容

- 多模板商品分别分组计费后求和（组内数量相加、金额相加）。
- **未绑定模板（`freight_template_id IS NULL`）→ 零运费**，不要求地区有效，历史商品行为不变。
- **已绑定但模板不存在/停用/无规则 → 明确报错**，不降级为免费。

## 6. 计价模型

```go
type Breakdown struct {
    OriginalAmount  int64
    CouponDiscount  int64
    MemberDiscount  int64 // 当前恒 0
    PointsDiscount  int64 // 当前恒 0
    ShippingFee     int64
    PayableAmount   int64
}
```

不变量：

```text
PayableAmount = OriginalAmount - CouponDiscount - MemberDiscount - PointsDiscount + ShippingFee
```

- 全部输入输出非负；
- 逐项扣减，任一项超过剩余商品金额即返回 20011（累计优惠必然 ≤ 商品金额）；
- 加运费前做 `math.MaxInt64` 溢出检查，结果再做 `INT UNSIGNED` 可写区间检查（超限 20012）；
- `Breakdown.Conserved()` 提供守恒自检。

## 7. 订单事务改造（本轮未执行）

结论：**未执行**，原因是 B1-04-02 未进入共同基线，`order.go` 与 `api/order/v1`、`internal/service/order.go` 被明确禁止修改。

已固化的接入清单（写入 `docs/dev/order-pricing.md` §4），B1-04-02 合入后按序执行：

1. `POST /orders/quote` 返回六槽位明细；
2. `insertOrder` 事务内重读 SKU 价格/状态/券/运费模板/区域规则并重新计价；
3. 落库金额取事务内结果；
4. `requestHash` 追加全部计价参数（保持既有前缀顺序，避免历史指纹失配）；
5. 状态推进同事务写 `order_status_logs`。

## 8. Migration 变更

| 项 | 值 |
| --- | --- |
| 版本 | `20261110000016`（Registry 状态 RESERVED，本任务持有） |
| 文件 | `20261110000016_freight.up.sql` / `.down.sql` |
| 新增表 | `freight_templates`、`freight_template_rules` |
| 新增列 | `products.freight_template_id BIGINT UNSIGNED NULL` + `idx_freight_template_id` |

要点：

- `uk_template_region(template_id, region_id)` 强制「同模板同区域唯一」。
- `fk_freight_rule_template ON DELETE CASCADE`：规则无独立生命周期。
- `products.freight_template_id` **可空、不回填**，保证历史商品零运费兼容；**不加外键**，避免模板删除级联改动商品行。
- down 完整：先删列/索引，再删规则表，再删模板表；注释声明破坏性（数据丢失）。
- **`20261110000015` 未被本任务占用**，Registry 中保持空缺并在备注中说明。

隔离库实测（`my_shop_b104_test`）：

- `migrate up` → `schema_migrations(20261110000016, dirty=0)`；
- 手工执行 down → `freight%` 表数量 0、`products.freight_template_id` 列数量 0；
- 追踪表回退到 `20261110000014` 后再次 `migrate up` → 两表重建、version 恢复 `20261110000016`、`dirty=0`。

## 9. API 变更

全部为**新增**后台接口，无既有接口变更。均要求 AdminAuth + 指定权限：

| 方法 | 路径 | 权限 |
| --- | --- | --- |
| POST | `/admin/freight-templates` | `freight:create` |
| GET | `/admin/freight-templates` | `freight:list` |
| GET | `/admin/freight-templates/:id` | `freight:view` |
| PUT | `/admin/freight-templates/:id` | `freight:update` |
| PUT | `/admin/freight-templates/:id/status` | `freight:update` |
| GET | `/admin/freight-templates/:id/rules` | `freight:view` |
| PUT | `/admin/freight-templates/:id/rules` | `freight:update` |

- 新增权限 code：`freight:create`、`freight:list`、`freight:view`、`freight:update`（`seed.go` 幂等写入）。
- 规则采用**整体替换**（`PUT .../rules`），避免逐条增删的中间态。
- **未新增** `/orders/quote` 与任何前台运费接口（见 §11）。
- **未更新** `openapi/**`：该目录被任务书列为禁止修改，由后续统一集成任务处理。因此 `TestRouteTableMatchesContract` 不会因本任务新增的路由而更新（见 §11）。

## 10. 幂等性与一致性保证（本轮范围）

本任务未改订单链路，因此「计价参数纳入 `requestHash`」**未实施**。本轮已交付并验证的一致性保证：

- **计算只读**：`IFreight.Quote` 不写任何表，无副作用；
- **规则唯一性由 DB 强制**：`uk_template_region` 兜底，应用层先行拒绝重复（20006）；
- **规则替换原子性**：单事务「删旧 + 插新」，地区存在性在同一事务内校验；
- **不静默降级**：模板停用/缺失/无规则全部显式报错；
- **无浮点、无溢出**：全部整数运算 + 显式上界检查；
- **快照自足**：模板/规则变更不回溯历史订单（`orders.shipping_fee` 一旦写入即为快照）。

## 11. 测试覆盖和实际结果

### 11.1 已执行并通过

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 编译 | `go build ./...` | PASS |
| 静态检查 | `go vet ./...` | PASS |
| 格式化 | `gofmt -l .`（新增/修改文件） | 无输出 |
| Registry 门禁 | `bash scripts/check-registry.sh` | **PASS**（20001–20012 落在 RESERVED 域，输出 INFO 而非 FAIL） |
| 空白错误 | `git diff --check` | PASS |
| 计价单测 | `go test ./internal/logic/pricing/... -count=1` | **PASS**（14 个顶层用例） |
| 运费单测 | `go test ./internal/logic/freight/... -count=1`（无 DB） | **PASS**（11 个单元用例，9 个集成用例 SKIP） |

计价用例覆盖：无优惠、仅券、仅运费、多槽位、折扣等于商品金额、折扣超商品金额、累计折扣超限、5 类负金额、nil 请求、int64 溢出、数据库列区间越界、边界值、零金额、守恒篡改检测、nil 明细。

运费用例覆盖：首件、续件、多续件向上取整、续件费用为 0、数量 0/负数、首件数量 0、续件数量 0、费用为负、整数溢出、区→市回退、区→省回退、无匹配、空链、空规则集、多模板求和、模板级包邮、模板满额免邮、未达阈值、区域阈值覆盖、模板不存在、模板停用、未绑定模板跳过、分组合并、分组溢出、阈值继承。

### 11.2 真实数据库集成测试（已执行，PASS）

环境：Docker `mysql:8.0`，容器 `my-shop-b104-test-mysql`，端口 `13306`，隔离库 **`my_shop_b104_test`**（新建空库，非生产/非既有开发库）。

```bash
DATABASE_DEFAULT_NAME=my_shop_b104_test \
DATABASE_DEFAULT_HOST=127.0.0.1 DATABASE_DEFAULT_PORT=13306 \
DATABASE_DEFAULT_USER=root DATABASE_DEFAULT_PASS=*** \
go test ./internal/logic/freight/... -count=1
```

结果：**20 个顶层用例全部 PASS**（11 单元 + 9 集成）。

9 个集成用例：

1. `TestIntegrationQuoteByPieceWithRegionFallback` —— 真实省/市/区数据 + 市级规则，区级收货回退命中，运费 600；
2. `TestIntegrationQuoteTemplateDisabled` —— 停用模板返回 20004；
3. `TestIntegrationQuoteMissingRegionRule` —— 规则在别的省，返回 20009；
4. `TestIntegrationQuoteUnboundTemplateIsFree` —— 未绑定模板、region=0 仍返回 0；
5. `TestIntegrationQuoteFreeShippingThreshold` —— 金额达阈值返回 0；
6. `TestIntegrationRegionRuleUniqueConstraint` —— 重复区域命中 1062；
7. `TestIntegrationTemplateDeleteCascadesRules` —— 删除模板后规则 0 条；
8. `TestIntegrationProductsFreightTemplateColumnNullable` —— `products.freight_template_id` 为 YES（可空）；
9. `TestIntegrationSchemaVersion` —— 版本 `20261110000016` 且 `dirty=0`。

安全护栏：集成测试硬编码 `integrationDBName = "my_shop_b104_test"`，环境变量不匹配即 `t.Skip`，连接库名不匹配即 `t.Fatalf` 拒绝执行。未对 `my_shop_b10304_verify` 或任何既有库执行任何操作。

### 11.3 失败 / 未执行（如实记录）

| 项 | 状态 | 原因分类 |
| --- | --- | --- |
| `internal/cmd` `TestRouteTable` | **FAIL**（149 条 vs 期望 142，7 条 freight 路由"未登记"） | 预期集成差异：任务书禁止修改 `routes_test.go` 与 `openapi/**` |
| `internal/cmd` `TestRouteCountByScope` | **FAIL**（分布断言 67/73/2） | 同上 |
| `internal/cmd` `TestRouteTableMatchesContract` | **FAIL** | **原有基线问题**（`af512f9` 上已 FAIL，OpenAPI 有 `/regions` 无实现）；本任务新增路由进一步扩大差异 |
| `internal/migrations` `TestMigration*` | **NOT RUN** | 会 `DROP` 全部业务表；且 `latestMigrationVersion=20261110000014`、`businessTables` 未含 freight 两表，断言必然失败。该文件被任务书禁止修改 |
| `internal/boot` `TestCheckSchemaReady*` / `TestSeed*` | **NOT RUN** | 需真实 DB 与 `DATABASE_DEFAULT_PASS`，默认环境未注入；且同样受迁移清单断言影响 |
| `internal/controller/*`（address/admin/cart…） | **FAIL** | 环境缺失（无 `DATABASE_DEFAULT_PASS`/Redis），与本任务改动无关 |
| 订单集成测试（`/orders/quote`、事务复核、幂等冲突、状态流水原子性） | **NOT RUN / 未实现** | 订单链路未接入（§11 边界） |

**未执行项均已标注原因，未将 NOT RUN 记为 PASS。**

## 12. 未完成事项

对应验收标准，以下项**未达成**：

| 编号 | 验收项 | 状态 | 说明 |
| --- | --- | --- | --- |
| A1 | Git 分支隔离 | ✅ | 独立 worktree + 独立分支 |
| A2 | 运费模板 | ✅ | 模板 + 区域规则 CRUD/启停/整体替换 |
| A3 | 运费计算 | ✅ | 首件/续件/免邮/区域回退，单测 + 真实库验证 |
| A4 | 统一计价 | ✅ | `pricing.Quote` 唯一入口（模块级） |
| A5 | 金额守恒 | ✅ | 公式 + `Conserved()` + 测试 |
| A6 | 订单落库 | ❌ | 订单未接入，`shipping_fee` 仍写 0 |
| A7 | 事务复核 | ❌ | 未实施 |
| A8 | 幂等性 | ❌ | `requestHash` 未扩展 |
| A9 | 状态流水 | ❌ | 未写入 |
| A10 | 测试 | ⚠️ 部分 | 本域单测/集成 PASS；订单集成未执行；全局门禁 FAIL（见 §11.3） |
| A11 | 文档 | ✅ | 设计/开发/交付文档齐备 |
| A12 | 兼容性 | ⚠️ 部分 | 未改订单链路，回归风险为零；但订单链路未接入故无法验证新链路 |

其他留白：

- `products.freight_template_id` **无写入接口**（属商品管理范围，本任务未新增）；
- 未实现模板删除接口（只提供启停，避免悬空绑定）；
- `/regions*` 与地址结构化字段仍无 Go 实现，运费只能以 `region_id` 为输入；
- `openapi/**` 未更新（禁止修改）。

## 13. 与其他 B1-04 任务的集成约定

### 与 B1-04-02（SKU 规格快照）

- **共同文件**：`internal/logic/order/order.go`、`api/order/v1`、`internal/service/order.go`。本任务**未做任何修改**。
- B1-04-02 进入 `feat/p2-backend` 后，B1-04-03 的订单集成按 `docs/dev/order-pricing.md` §4 清单执行，无需回退本任务代码。
- 若 B1-04-02 同时改动 `requestHash`，以 B1-04-02 的字段顺序为基线，再追加计价参数；避免双方各改一版导致指纹不一致。

### 与 B1-04-05（积分）

- 只使用 `pricing.QuoteRequest.PointsDiscount` 槽位，**不新增公式**。
- 积分换算比例、单笔上限、退款返还规则需先写入 `docs/design/pricing.md` 再实现。
- 订单落库若需要积分快照列，须单独申请 Migration 与错误码域。

### 与 B1-04-08（会员折扣）

- 只使用 `pricing.QuoteRequest.MemberDiscount` 槽位，**不新增公式**。
- `discount_amount` 的券语义不得复用；会员折扣若需快照，需独立列。
- 折扣基数与「券→会员→积分→运费」的顺序不得擅自调整（调整须 Owner 确认并同步设计文档与测试）。

### 与商品管理（模板绑定）

- 运费生效前提是 `products.freight_template_id` 被写入。该列的写入接口属商品管理，需与商品任务约定：新增/编辑商品时接受 `freight_template_id`，并校验模板存在（不存在 → 20001）。

## 14. 附：本轮文件清单

**新增**

```text
api/freight/v1/freight.go
internal/controller/freight/freight.go
internal/logic/freight/{calc.go,freight.go,quote.go,calc_test.go,integration_test.go}
internal/logic/pricing/{pricing.go,pricing_test.go}
internal/service/freight.go
internal/migrations/sql/20261110000016_freight.{up,down}.sql
docs/design/freight.md
docs/dev/freight.md
docs/dev/order-pricing.md
docs/reports/B1-04-03-order-pricing-freight.md
```

**修改**

```text
.agent/registry/migrations.md        （登记 20261110000016，Owner 授权）
.agent/registry/error-codes.md       （登记 20000-20999，Owner 授权）
internal/codes/codes.go              （域序 20，12 个错误码）
internal/cmd/routes_admin.go         （7 条后台路由）
internal/boot/seed.go                （4 个权限 code）
docs/design/pricing.md               （统一计价入口、运费、不变量）
docs/design/order.md                 （运费/计价接入边界）
docs/design/migration.md             （新增迁移清单）
docs/design/error-codes.md           （域序 20）
```

**未修改（遵守边界）**

```text
internal/logic/order/order.go
api/order/v1/**
internal/service/order.go
internal/cmd/routes_test.go
internal/migrations/migrations_test.go
internal/boot/boot_migration_test.go
tools/openapi-gen/**
openapi/**
```
