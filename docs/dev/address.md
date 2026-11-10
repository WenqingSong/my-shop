# 地址与地区模块知识（接手必读）

地址业务语义、默认地址一致性与错误码见 `docs/design/address.md`；本文只记录代码入口、地区结构与接入时的注意点。

## 1. 当前实现状态

- `addresses` 的增删改查、默认地址唯一（生成列 `default_key` + `uk_user_default`）已完整。
- 地区字典 `regions`（省/市/区三级）由 migration `20261110000003_regions` 建表，
  B1-03-04 从 `system_city` 导入 3938 条数据（34 省 + 369 市 + 3535 区县）。
- B1-04-01 起 `addresses` 增加可空 `region_id`（软引用 `regions.level=3`），并新增只读地区接口；
  省/市/区自由文本列保留，历史地址与旧客户端行为不变。

## 2. 代码入口

| 关注点 | 位置 |
| --- | --- |
| 地址增删改查/设默认/数据隔离 | `internal/logic/address/address.go` |
| 地区只读业务（列表/树/详情/链路校验） | `internal/logic/region/region.go` |
| 地区 API/Controller | `api/region/v1/region.go`、`internal/controller/region/region.go` |
| 服务接口与注册 | `internal/service/address.go`、`internal/service/region.go` |
| 对外结构 | `api/address/v1/address.go`、`api/region/v1/region.go` |
| 路由（前台；地区为公开只读） | `internal/cmd/routes_frontend.go`（`// B1-04-01` 锚点） |
| 地址接口测试 | `internal/controller/address/address_test.go`、`address_region_test.go` |
| 地区接口测试 | `internal/controller/region/region_test.go` |
| 地址字段纯校验单测 | `internal/logic/address/address_internal_test.go` |
| `regions` 结构与 ID 规则测试 | `internal/migrations/product_model_migration_test.go`（`TestRegionsHierarchy`） |

## 3. `regions` 结构要点

```text
regions(id, parent_id, level, name, created_at, updated_at)
  id        BIGINT UNSIGNED AUTO_INCREMENT，导入时显式写原 system_city.city_id
  parent_id BIGINT UNSIGNED NOT NULL DEFAULT 0（省级为 0）
  level     TINYINT NOT NULL（1=省、2=市、3=区/县）
  name      VARCHAR(64)
```

- 索引：`idx_parent (parent_id)`、`idx_level (level)`。
- **没有自引用外键**：省级 `parent_id=0` 无法用 FK 表达；父子/层级正确性由导入工具与应用层校验。
- **没有名称唯一约束**：历史数据存在重名/占位节点，唯一约束会阻塞导入。
- **ID 规则**：直接复用 `system_city.city_id`，不重新编号；新地区若不在历史字典内，才使用自增 ID。

## 4. 地址 ↔ 地区写入规则

- `region_id` 只接受 `level=3`（区/县）节点；省/市 ID 一律 1001。
- 校验走 `service.Region().ResolveDistrict`：单次 JOIN 同时要求 `d.level=3 && c.level=2 && p.level=1 && p.parent_id=0`，
  不存在/层级不对/链路断裂统一 1001，**不做多次逐级查询**。
- 传 `region_id` 时省/市/区文本由字典覆盖（忽略客户端文本），避免伪造地区文本。
- 更新时：
  - `region_id` 省略 → 不修改结构化地区；若同时修改任一地区文本，则清空 `region_id`（退出结构化模式）。
  - `region_id=0` → 清空 `region_id`，保留现有文本。
  - `region_id>0` → 校验并覆盖三列文本。
- 历史地址 `region_id` 为 NULL：详情/列表正常返回 `region_id: null`，不做回填。

## 5. 默认地址并发控制

- 创建/设默认在**同一事务**内先 `SELECT id FROM users WHERE id=? FOR UPDATE`（`lockUserForDefault`），
  再取消旧默认、写入新默认；首条地址的 `count==0` 判定也在锁内。
- 行锁把同一用户的默认变更串行化；`uk_user_default`（VIRTUAL 生成列 `default_key`）是最终防线。
- **不要**删掉 users 行锁改成纯应用层「先查后改」；也不要把默认唯一改成普通索引。
- `default_key` 只读，插入/更新不得写该列。

## 6. 不要改的部分

- `addresses.default_key` 是 VIRTUAL 生成列，只读；插入/更新不得写该列。
- 默认地址唯一性依赖 `uk_user_default`，不要改成应用层「先查再写」。
- 地址隔离一律 `WHERE id AND user_id = Principal.UserID`；不存在与他人地址统一 404。
- 地区字典只读，业务请求不得写 `regions`；数据导入走独立迁移任务。

## 7. 测试方式

```bash
# 独立测试库（禁止使用 my_shop_b10304_verify）
mysql -h127.0.0.1 -uroot -p -e "CREATE DATABASE IF NOT EXISTS my_shop_b104_test"
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root go run . migrate up
# 地区数据（测试库需导入 3938 条，可从验证库只读导出）
mysqldump -h127.0.0.1 -uroot -p --no-create-info my_shop_b10304_verify regions \
  | mysql -h127.0.0.1 -uroot -p my_shop_b104_test
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root \
  go test -count=1 ./internal/controller/address/ ./internal/controller/region/
```

测试内部会两次核实库名（环境变量 + `SELECT DATABASE()`），不一致直接失败，不会执行 DELETE。
