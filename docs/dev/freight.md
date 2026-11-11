# 运费模块开发指南（Freight）

本文面向接手运费模块的开发者，说明代码落点、调用方式、易错点与测试方法。设计语义以 `docs/design/freight.md` 为准，本文只讲「怎么改、怎么验」。

## 1. 代码落点

| 层 | 路径 | 职责 |
| --- | --- | --- |
| API | `api/freight/v1/freight.go` | 后台请求/响应结构与 `g.Meta` 路由声明 |
| Controller | `internal/controller/freight/freight.go` | HTTP 适配 + 管理员身份检查 |
| Service | `internal/service/freight.go` | `IFreight` 接口、`FreightItem`/`FreightQuoteReq` 领域类型 |
| Logic | `internal/logic/freight/` | 模板 CRUD、区域规则、计费内核 |
| Migration | `internal/migrations/sql/20261111000002_freight.*.sql` | 两张表 + `products.freight_template_id` |
| 路由 | `internal/cmd/routes_admin.go` | `/admin/freight-templates*`，按权限分组 |
| 权限 | `internal/boot/seed.go` | `freight:create/list/view/update` |

Logic 包内部结构：

- `calc.go`：**纯函数**计费内核（`feeByPieces`、`matchRule`、`groupItems`、`computeShippingFee`），不碰 DB；
- `freight.go`：后台管理（模板 CRUD、启停、规则整体替换）与入参校验；
- `quote.go`：DB 读取（地区链、模板快照、规则）并调用内核。

**改动原则**：任何计费规则变化只改 `calc.go`，不要在 `quote.go` 或控制器里复制公式。

## 2. 计费内核怎么用

```go
fee, err := feeByPieces(quantity, fareRule{
    FirstQuantity:      1,
    FirstFee:           500,
    AdditionalQuantity: 2,
    AdditionalFee:      100,
})
// quantity 1..2 → 500；3..4 → 600；5..6 → 700
```

要点：

- `FirstQuantity`/`AdditionalQuantity` 必须 >= 1，否则返回 `21007`；
- `FirstFee`/`AdditionalFee` 必须 >= 0，否则返回 `21010`；
- 向上取整用整数除法：

```go
units := extra / rule.AdditionalQuantity
if extra%rule.AdditionalQuantity != 0 {
    units++
}
```

不要写成 `(extra + add - 1) / add`：`extra` 极大时会先溢出。

## 3. 区域匹配

`matchRule(chain, rules)` 的 `chain` 必须是 `[本级, 上级, ...]` 顺序：

```go
chain := []int64{districtID, cityID, provinceID}
rule, ok := matchRule(chain, rules)
```

`chain` 由 `loadRegionChain` 从 `regions` 表沿 `parent_id` 上溯得到，最多 8 层并记录已访问节点，防止脏数据成环导致死循环。

**禁止**用 `addresses.province/city/district` 文本去 `regions.name` 做匹配：不同层级存在同名地区，按名称会命中错误区域。

## 4. 新增一个计费维度（示例：首重/续重）时怎么扩展

1. 先在 `docs/design/freight.md` 写清规则与不变量；
2. 在 `freight_templates` 增加 `charge_type`（`by_piece`/`by_weight`），并给出默认值与回填策略；
3. 在 `calc.go` 增加新的纯函数，由 `computeShippingFee` 按 `charge_type` 分派；
4. 补单元测试（边界、非法输入、溢出）与集成测试（真实规则行）；
5. 更新 `docs/design/migration.md` 与 Registry。

不要在一个函数里塞多套 `if chargeType == ...` 的公式体；分派到独立函数更易测试。

## 5. 后台规则整体替换

`AdminReplaceRules` 的语义是「全量替换」：

- 事务内先 `DELETE` 该模板全部规则，再逐条 `INSERT`；
- `region_id` 重复在应用层先行拒绝（21006），`uk_template_region` 兜底；
- 空数组表示清空全部规则（合法操作）；
- 地区存在性在事务内批量校验，任一缺失返回 21008。

**不要**改成「按 region_id 做 upsert + 删除多余项」：多一步中间态，且删除语义容易遗漏。

## 6. 常见坑

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| 运费算成 0 但不该包邮 | 走了「未绑定模板」分支 | 确认 `FreightTemplateID` 非 nil 且模板 id > 0 |
| 返回 21009 | 地区链上没有该模板的规则 | 检查是否只在市级配了规则、而收货地区是另一个省的区 |
| 返回 21004 | 模板被停用 | 停用是「不可用」而非「免费」 |
| 免邮不生效 | 比较基数是**商品金额**而非含运费金额 | 见设计 §5 |
| `products.freight_template_id` 写入失败 | 该列在 `products`，本模块不提供写接口 | 属商品管理范围 |

## 7. 测试

```bash
# 纯单元测试（无需数据库）
go test ./internal/logic/freight/... -count=1

# 真实数据库集成测试（要求隔离库）
export DATABASE_DEFAULT_HOST=127.0.0.1
export DATABASE_DEFAULT_PORT=13306
export DATABASE_DEFAULT_USER=root
export DATABASE_DEFAULT_PASS=<本地隔离实例密码>
export DATABASE_DEFAULT_NAME=my_shop_b104_test
go test ./internal/logic/freight/... -count=1 -v
```

集成测试有**硬编码库名护栏**（`integration_test.go` 的 `integrationDBName`）：`DATABASE_DEFAULT_NAME` 不是 `my_shop_b104_test` 时直接 `t.Skip`，库名不匹配时 `t.Fatalf` 拒绝执行。新增集成测试必须复用 `setupIntegrationDB`，不要自己拼 DSN 绕开护栏。

测试数据用 `uniqueSuffix()` 加后缀，并通过 `t.Cleanup` 清理；**不要**依赖清空整表。

## 8. B1-04 集成后的订单接入

- 商品绑定入口：`POST/PUT /admin/products`（`freight_template_id`，0=解绑），
  写入前调用 `IFreight.ValidateTemplateForBinding`（不存在 21001、停用 21004）。
- 订单创建：`sOrder.freightFee` 组装 `FreightItem{FreightTemplateID, Quantity, Amount}` 调用 `IFreight.Quote`；
  未绑定模板的商品零运费兼容；一旦有商品绑定模板，收货地址必须有有效 `region_id`，否则 21008。
- 报价与落库：`/orders/quote` 与下单事务内复核都走同一入口，运费写入 `orders.shipping_fee` 快照。
- 迁移版本：`20261111000002_freight`（由原拟 `20261110000016` 上移，避免被已应用的 `20261111000001` 跳过）。
