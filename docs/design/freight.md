# 运费设计（Freight）

本文说明 SurgeCart 的运费模板建模、区域匹配、按件计费与免邮规则，以及它与统一计价入口、订单金额快照的关系。

范围：**按件计费 + 区域差异化 + 满额免邮**。不涉及按重量/体积计费、第三方快递、物流轨迹与物流查询。

## 1. 职责与边界

- 回答「一件/多件商品发往某个地区，运费是多少、按什么顺序计算」。
- 承载运费模板（`freight_templates`）与区域计费规则（`freight_template_rules`）的管理能力（仅后台接口，不含前端页面）。
- 提供**服务端唯一运费计算入口** `service.IFreight.Quote`，供统一计价与订单链路调用。
- 不负责：订单金额汇总与应付计算（见 `pricing.md` 的 `pricing.Quote`）、优惠券门槛判定、库存、支付与退款。
- 不读取客户端提交的运费金额或地区名称：运费只由服务端依据权威数据计算。

## 2. 数据模型

### 2.1 `freight_templates`（运费模板）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `name` | VARCHAR(64) | 非空，唯一 `uk_name`；trim 后 1-64 字符 |
| `is_free` | TINYINT | 非空默认 0；`1`=模板级无条件包邮（跳过区域规则匹配） |
| `free_threshold` | INT UNSIGNED | 非空默认 0；满额免邮阈值（整数分，0=不启用） |
| `status` | TINYINT | 非空默认 1；`1`=启用、`0`=停用 |
| `remark` | VARCHAR(255) | 非空默认 ''；备注 |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_name(name)`、`idx_status(status)`。

### 2.2 `freight_template_rules`（区域计费规则）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `template_id` | BIGINT UNSIGNED | 非空，FK → `freight_templates.id` `ON DELETE CASCADE` |
| `region_id` | BIGINT UNSIGNED | 非空，指向 `regions.id`（省/市/区任一层级，软引用无 FK） |
| `first_quantity` | INT UNSIGNED | 非空默认 1，首件数量（>=1） |
| `first_fee` | INT UNSIGNED | 非空默认 0，首件费用（整数分） |
| `additional_quantity` | INT UNSIGNED | 非空默认 1，续件数量（>=1） |
| `additional_fee` | INT UNSIGNED | 非空默认 0，续件费用（整数分） |
| `free_threshold` | INT UNSIGNED | 非空默认 0，该区域满额免邮阈值（0=继承模板值） |
| `created_at`/`updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

索引：`uk_template_region(template_id, region_id)`（**同一模板同一地区至多一条规则**，由数据库强制）、`idx_region_id(region_id)`。

规则整体替换（`PUT /admin/freight-templates/:id/rules`）在单事务内先删后插，保证不会出现「旧规则 + 新规则」并存的中间态。

### 2.3 `products.freight_template_id`（商品绑定）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `freight_template_id` | BIGINT UNSIGNED | **可空**，默认 NULL；`idx_freight_template_id` |

绑定语义：

- `NULL` = 历史商品或未指定模板，**保持既有零运费兼容行为**；迁移不回填，避免历史商品突然产生运费。
- 非 `NULL` = 下单计价必须按该模板计算。模板不存在、已停用、或当前收货地区链上没有匹配规则时**明确报错**，绝不静默按 0 处理。
- 不建外键：模板与本列由应用层校验，避免删除模板时级联修改商品行或阻塞模板清理。
- 本批次**不新增商品写接口**；绑定入口由商品管理侧写入该列（与 B1-04 商品任务集成，见 `docs/reports/B1-04-03-order-pricing-freight.md`）。

## 3. 计费规则（按件）

```text
数量 <= 首件数量：
    运费 = 首件费用

数量 > 首件数量：
    运费 = 首件费用
         + ceil((商品数量 - 首件数量) / 续件数量) × 续件费用
```

- 全部金额为整数分（`int64`），向上取整用整数除法实现，**不使用 float64/decimal**。
- `first_quantity`/`additional_quantity` 必须 >= 1：为零或负数属于非法规则，返回 20007，而不是除零或退化为免费。
- 溢出：`首件费用 + 续件单位数 × 续件费用` 在计算前做上界检查，溢出返回 20012。

示例（首件 1 件 500 分，续件 2 件 100 分）：

| 数量 | 续件单位 | 运费 |
| --- | --- | --- |
| 1 | 0 | 500 |
| 3 | 1 | 600 |
| 5 | 2 | 700 |
| 6 | 3（向上取整） | 800 |

## 4. 区域匹配与上级回退

- 匹配维度是 `regions.id`，**不按地区名称匹配**：不同层级可能存在同名地区，按名称模糊匹配会把运费落到错误区域。
- 收货地址在计价时转换为结构化地区 id（`regions.id`），从该节点沿 `parent_id` 上溯，得到地区链 `[区, 市, 省]`。
- 规则匹配由近及远：先找区级规则，未命中找市级，再找省级，**首个命中即生效**。
- 上溯边界：
  - 本级地区不存在 → 20008（拒绝把地址脏数据当成「无规则」）；
  - 上级缺失 → 停止上溯，已收集层级仍然有效；
  - `parent_id` 成环 → 由已访问集合与深度上限（8 层）截断。
- 全链无命中 → 20009，**不静默返回免费运费**。

## 5. 免邮规则

计算顺序（同一模板分组内）：

1. 模板 `status != 1` → 20004（停用模板不可计价）。
2. 模板 `is_free = 1` → 该组运费 0（模板级无条件包邮，不需要区域规则）。
3. 匹配区域规则；无规则 → 20009。
4. 生效阈值 = 规则 `free_threshold > 0 ? 规则值 : 模板 free_threshold`。
5. 阈值 > 0 且「该模板下商品金额合计」>= 阈值 → 该组运费 0。
6. 否则按 §3 计费。

**免邮比较基数是商品原始金额**（`Σ skus.price × quantity`，不含运费、不含优惠后金额），按模板分组分别累计。选择原始金额而非应付金额，是因为：

- 运费不参与优惠券门槛判断（见 `pricing.md`），两者使用同一基数更一致；
- 用应付金额会让「免邮」与「优惠券」互相影响，产生先有券才免邮的循环语义。

区域规则可以设置比模板更高的阈值（如偏远地区），但不支持低于模板阈值——`free_threshold = 0` 表示继承，非 0 表示覆盖。

## 6. 多模板与分组

同一订单可能包含绑定不同运费模板的商品，也可能包含未绑定模板的历史商品：

- 未绑定模板的行：运费 0，不参与分组，也不要求地区有效；
- 已绑定模板的行：按 `template_id` 分组，组内数量相加（按件计费）、组内金额相加（免邮基数）；
- 各组分别计算后求和；
- 只要有一个已绑定模板的组失败，整笔运费计算失败（不做「部分免费」的降级）。

## 7. 业务不变量

- INV-F01（单位唯一）：所有运费金额为整数分；任何新字段/接口不得引入元或小数金额。
- INV-F02（服务端唯一真源）：运费只由 `service.IFreight.Quote` 依据服务端权威数据计算；客户端金额、地区名称、购物车快照均不参与。
- INV-F03（不静默免费）：模板不存在/停用/无匹配区域规则必须返回明确错误；只有「未绑定模板」与「显式包邮/免邮」才产生 0 运费。
- INV-F04（区域不可重复）：同一模板同一 `region_id` 至多一条规则，由 `uk_template_region` 强制。
- INV-F05（区域按 id 匹配）：匹配只使用 `regions.id` 与 `parent_id` 层级，不使用名称。
- INV-F06（整数与守恒）：运费非负、无浮点、无溢出；分组求和不超过 `INT UNSIGNED` 可写区间。
- INV-F07（历史兼容）：`products.freight_template_id` 为 NULL 的商品运费恒 0，迁移不回填。
- INV-F08（快照自足）：订单一旦写入 `shipping_fee`，后续改模板、改规则、改商品绑定都不回溯修改历史订单。

## 8. 一致性模型与失败语义

- 事实来源：单一 MySQL（`freight_templates`、`freight_template_rules`、`regions`、`products`）。无缓存、无 MQ、无跨系统事务。
- 计算只读：`Quote` 不写任何表，可在订单事务内外安全调用；订单事务内复核由订单侧负责（见 `pricing.md` 与订单集成约定）。
- 管理写操作：
  - 创建/更新模板为单条 INSERT/UPDATE，名称冲突由 `uk_name` 兜底（1062 → 20002）；
  - 规则整体替换为单事务「删旧 + 插新」，地区存在性在事务内校验；重复 `region_id` 在应用层先行拒绝（20006），数据库唯一约束兜底。
- 失败语义：模板不存在 20001、停用 20004、无匹配规则 20009、地区不存在 20008、规则非法 20007、金额溢出 20012；均为明确错误，无「静默 0 运费」路径。

## 9. 安全与权限

后台路由（`internal/cmd/routes_admin.go`，AdminAuth + `RequirePermission`）：

| 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- |
| POST | `/admin/freight-templates` | `freight:create` | 创建模板 |
| GET | `/admin/freight-templates` | `freight:list` | 模板列表 |
| GET | `/admin/freight-templates/:id` | `freight:view` | 模板详情（含规则） |
| PUT | `/admin/freight-templates/:id` | `freight:update` | 更新模板基础信息 |
| PUT | `/admin/freight-templates/:id/status` | `freight:update` | 启用/停用 |
| GET | `/admin/freight-templates/:id/rules` | `freight:view` | 查询区域规则 |
| PUT | `/admin/freight-templates/:id/rules` | `freight:update` | 整体替换区域规则 |

权限 code 由 `internal/boot/seed.go` 幂等写入。前台不暴露运费模板查询或运费试算接口。

## 10. 错误码域

域序 20（`20000-20999`），Registry 状态 RESERVED（本任务持有）：

| code | 常量 | 语义 | HTTP |
| --- | --- | --- | --- |
| 20001 | `CodeFreightTemplateNotFound` | 运费模板不存在 | 404 |
| 20002 | `CodeFreightTemplateNameExists` | 模板名已存在 | 409 |
| 20003 | `CodeFreightTemplateInvalid` | 模板参数非法 | 400 |
| 20004 | `CodeFreightTemplateDisabled` | 模板已停用，不可计价 | 409 |
| 20005 | `CodeFreightRuleNotFound` | 区域规则不存在 | 404 |
| 20006 | `CodeFreightRuleDuplicate` | 同模板同区域规则重复 | 409 |
| 20007 | `CodeFreightRuleInvalid` | 规则参数非法（数量为 0 等） | 400 |
| 20008 | `CodeFreightRegionNotFound` | 地区不存在 | 404 |
| 20009 | `CodeFreightRegionRuleMissing` | 无匹配当前地区的规则 | 409 |
| 20010 | `CodePricingInvalidAmount` | 计价金额非法（负数等） | 400 |
| 20011 | `CodePricingDiscountExceeds` | 优惠累计超过可优惠金额 | 400 |
| 20012 | `CodePricingAmountOverflow` | 计价金额溢出 | 400 |

`20010`–`20012` 属于统一计价错误，定义在 `pricing` 语义下，但与运费同处域序 20（同任务独占域）。

## 11. 跨模块关系

- `regions`：区域链唯一来源（`id`/`parent_id`/`level`）；只读。
- `products.freight_template_id`：商品到模板的绑定；本批次只建模与读取，写入入口属商品管理。
- `pricing.Quote`：统一计价入口的 `ShippingFee` 槽位来自本模块（见 `pricing.md`）。
- `orders.shipping_fee`：订单运费快照列（`20261110000004` 已建），订单链路写入时机与事务边界见 `order.md`。
- 建表经 `20261110000016_freight`（含 down），见 `migration.md`。

## 12. Deferred / 已知留白

- **订单链路接入**：`/orders/quote`、下单事务内运费复核、订单 `shipping_fee` 落库、幂等指纹纳入计价参数——待 B1-04-02（SKU 规格快照）进入共同基线后实施。
- **商品绑定入口**：`products.freight_template_id` 的写入接口属商品管理，本批次未新增。
- 按重量/体积计费、多地区组合规则（如「华东+华北统一价」）、阶梯运费、承运商与物流轨迹。
- 运费模板的删除接口（当前只提供启停，避免与商品绑定产生悬空引用）。
- 省市区域字典的查询接口（`GET /regions*`）已在 OpenAPI 契约中声明，但 Go 实现尚不在本任务范围。
