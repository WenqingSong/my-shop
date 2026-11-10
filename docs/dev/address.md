# 地址与地区模块知识（接手必读）

地址业务语义、默认地址一致性与错误码见 `docs/design/address.md`；本文只记录代码入口、地区结构与接入时的注意点。

## 1. 当前实现状态

- `addresses` 的增删改查、默认地址唯一（生成列 `default_key` + `uk_user_default`）已完整。
- 地区字典 `regions`（省/市/区三级）**只有数据库结构**（migration `20261110000003_regions`）：
  没有导入数据、没有业务代码、`addresses` 也没有 `region_id` 或外键。
- 因此地址接口仍只接收省/市/区自由文本，行为与之前完全一致。

## 2. 代码入口

| 关注点 | 位置 |
| --- | --- |
| 地址增删改查/设默认/数据隔离 | `internal/logic/address/address.go` |
| 对外结构 | `api/address/v1/address.go` |
| 路由（前台 `middleware.Auth`） | `internal/cmd/routes_frontend.go` |
| 地址接口测试 | `internal/controller/address/address_test.go` |
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

## 4. 不要改的部分

- `addresses.default_key` 是 VIRTUAL 生成列，只读；插入/更新不得写该列。
- 默认地址唯一性依赖 `uk_user_default`，不要改成应用层「先查再写」。
- 地址隔离一律 `WHERE id AND user_id = Principal.UserID`；不存在与他人地址统一 404。

## 5. 后续接入结构化地区时

- 以 additive 方式给 `addresses` 加 `region_id`（可空）+ 索引；先双写（文本 + ID），历史地址允许 NULL，不做批量回填猜测。
- 校验链：`region_id` 必须存在、`level=3`、其父链 `parent_id` 与提交的省/市一致；不通过返回现有 `1001`（400）。
- 地区字典只读，不建议在业务请求里写 `regions`；导入走独立迁移任务（`system_city` → `regions`）。
- 订单仍保存地址文本快照；`region_id` 不作为订单金额或履约事实来源。
