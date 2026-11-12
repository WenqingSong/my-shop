# 历史数据迁移开发知识（接手必读）

架构与映射规则见 `docs/design/legacy-data-migration.md`；本文只记录代码入口、调用链、幂等实现与常见问题。

## 1. 代码入口

| 关注点 | 位置 |
| --- | --- |
| CLI 子命令 `legacy-migrate <dry-run|import>` | `internal/cmd/legacy_migrate.go` |
| 连接 staging/目标库、DSN（`ParseTime=true`） | `internal/logic/legacymigrate/db.go` |
| 引擎与执行顺序 | `internal/logic/legacymigrate/engine.go`（`Engine.Run`、`domainRunners`） |
| 域上下文、ID 映射、跳过/失败记录 | `internal/logic/legacymigrate/domain.go` |
| 统计与报告 | `internal/logic/legacymigrate/report.go` |
| 分类/商品/规格/SKU/库存 | `internal/logic/legacymigrate/domain_product.go`、`mapping.go` |
| 地区/用户/地址 | `internal/logic/legacymigrate/domain_user.go` |
| 订单/明细/状态流水 | `internal/logic/legacymigrate/domain_order.go` |
| 优惠券/评价/收藏 | `internal/logic/legacymigrate/domain_coupon.go` |
| 钱包/积分期初 | `internal/logic/legacymigrate/domain_wallet.go` |
| 真实 MySQL 集成测试夹具 | `internal/logic/legacymigrate/engine_test.go`（`setupEngineTest`） |
| B1-03-04 域测试 | `internal/logic/legacymigrate/b10304_test.go` |

## 2. 核心调用链

```text
cmd.runLegacyMigrate
  → legacymigrate.OpenConnections (source=staging, target=目标库)
  → NewEngine(source, target).Run(ctx, dryRun)
      → loadIDMap (一次读取 legacy_id_map)
      → for runner in domainRunners():
            runner.run(ctx, domainContext)
      → result.finalize()
  → result.String() 输出统计表 + 跳过/降级/失败明细
```

`domainRunner` 只需实现 `name()` 与 `run()`；新增数据域时在 `domainRunners` 的依赖位置注册，
并复用 `domainContext` 的 `mapped/required/plan/record/skip/warn/fail`，不要自行读 `legacy_id_map`。

## 3. 数据映射机制

- **旧 ID → 新 ID**：`domainContext.record(ctx, tx, entity, oldID, newID)` 在业务事务内写
  `legacy_id_map` 并更新内存映射；后续域用 `mapped/required` 读取。
- **Dry Run 占位**：`plan(entity, oldID)` 写入 `plannedID=-1`，让后续域继续做引用校验；
  Dry Run 分支禁止任何 `INSERT/UPDATE`。
- **跨域依赖**：优惠券先于订单；订单事务内回填 `user_coupons.order_id`；评价依赖订单明细映射。
- **软删除商品**：`loadReferencedProductIDs` 收集订单/评价/收藏/券范围引用；命中则保留为 `off_shelf`。
- **规格匹配**：`loadLegacySpecSchemas` 解析 `store_product_attr_result`；`matchSpecCombo` 按值多重集合匹配，
  不做字符串位置推断；`SpecSignature` 统一生成指纹。

## 4. 幂等实现

- 每个域先查内存映射；已迁移直接 `skipAlready`，不重复插入。
- 业务行与映射行同事务（`INV-006`）；失败只回滚当前行/批次，可安全重跑。
- 无 legacy 主键的域：
  - 地区：`regions.id` 已存在即跳过；
  - 钱包/积分：`wallet_accounts.uk_user_id` / `user_points_accounts.uk_user_id` 判断；
    期初流水用 `legacy:wallet_opening:<user_id>` / `legacy:points_opening:<user_id>` 唯一幂等键；
  - 优惠券范围：`uk_template_product` 冲突即跳过。
- 不要用删除目标数据的方式「重新迁移」；源库只读。

## 5. 执行顺序

```text
regions → categories → products → users → addresses → coupons → orders
→ wallets → points → reviews → favorites
```

原因：商品依赖分类；订单/评价/收藏/券范围依赖用户与商品；订单需要用户券映射；评价需要订单明细映射。

## 6. 常见问题

1. **`sql: Scan error ... converting NULL to int`**：源库很多列可空（如 `store_category.sort`），
   读取时用 `sql.NullInt64/NullString/NullTime`，不要直接 Scan 到基础类型。
2. **`unsupported Scan, storing driver.Value type []uint8 into *time.Time`**：DSN 必须 `ParseTime=true`
   （`buildDSN` 已设置）。
3. **`store_product_attr_value.sku` 顺序不可靠**：不要按逗号位置映射规格；用
   `store_product_attr_result.value[].detail` 的值集合匹配。
4. **`remove_order` 不是取消**：源库该事件是「用户删除订单列表记录」，目标只写
   `order_status_logs.change_type='delete'`，不把订单改成 60。
5. **历史订单 `store_order.status` 不可信**：必须用 `store_order_status` 时间线重建。
6. **金额**：一律 `YuanToCents`/`decimalToInteger` 定点转换，禁止 `float64`；`total_amount` 不复算。
7. **密码**：旧 MD5 写 `!legacy-md5-forced-reset` 占位；bcrypt 校验必然失败，不迁移可登录密码。
8. **二跑统计**：`符合条件` 是源侧稳定口径；`跳过` 含「已迁移」与「不满足条件」，明细只列后者及异常，
   已迁移不逐条刷屏。
9. **测试清理**：`setupEngineTest` 复制配置库结构到进程号隔离库，删除测试库数据不会动配置库；
   不要改成直接 `DELETE` 配置库。

## 7. 测试入口

```bash
# 纯映射单测
go test ./internal/logic/legacymigrate -run 'TestSpecSignature|TestYuanToCents|TestSplitLegacyAddress' -count=1

# 真实 MySQL（需 DATABASE_DEFAULT_PASS；测试库由夹具自动创建/清理）
DATABASE_DEFAULT_PASS=root DATABASE_DEFAULT_NAME=my_shop \
  go test ./internal/logic/legacymigrate -count=1

# 端到端（独立验证库）
my-shop migrate up
my-shop legacy-migrate dry-run
my-shop legacy-migrate import
my-shop legacy-migrate import   # 第二次应为 0 新增
```

## 8. 修改边界

- 新增数据域：新增 `domain_xxx.go` + 在 `domainRunners` 注册 + 新 `entity_type` 常量 + 测试。
- 修改共享框架（`engine.go`/`domain.go`/`report.go`/测试夹具）需评估所有域与既有测试。
- 不新增业务 Schema；如现有列无法承载，先提供证据与最小变更方案交 Owner 决策。
