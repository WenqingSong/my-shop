# 地区字典数据初始化（接手必读）

本文说明 `regions` 表（省/市/区三级地区字典）的数据来源、字段映射、完整性规则、
初始化入口、幂等/冲突语义与故障恢复方式。设计语义见 `docs/design/address.md` §2.2。

## 1. 数据来源

| 项 | 值 |
| --- | --- |
| 原始文件 | `tmp/shopping.sql`（Owner 提供，`.gitignore` 已忽略，**不纳入版本库、不得修改/删除**） |
| 原始文件 SHA-256 | `568da34e4cd084e497450e07e75709966b67153daa99e68ae14e291a01cc23c2` |
| 原始文件大小 | 5,671,481 字节（Navicat 导出，MySQL 5.7.17） |
| 原始导出日期 | 2021-09-27（文件头 `Date` 字段） |
| 原始表 | `system_city`（城市表，3938 行） |
| 仓库内数据文件 | `internal/regiondata/regions_v1.tsv`（TSV，105,346 字节，`go:embed` 内嵌） |
| 数据文件 SHA-256 | `2a78fe9a3dad454ef42db4dfe5cc3d3e4fb0223d9db2eb5c92337d600732918f` |
| 数据版本 | `v1`（`internal/regiondata.DataVersion`） |
| 规模 | 3938 = 省 34 + 市 369 + 区县 3535 |

原始表结构（`system_city`）：

```text
id, city_id, level, parent_id, area_code, name, merger_name, lng, lat, is_show
```

只提取地区字典需要的 4 个字段，**不导入** `area_code`/`merger_name`/`lng`/`lat`/`is_show`。
原始 `shopping.sql` 的其余 82 张表与全部业务数据不进入本项目。

### 1.1 提取规则（可复核）

等价 SQL（在原始库执行即可得到同样结果）：

```sql
SELECT city_id, parent_id, level + 1, name
FROM system_city
ORDER BY level, city_id;
```

生成 TSV 时的固定规则：

1. `regions.id ← system_city.city_id`（**不重新编号**，保证历史地址里的地区 id 继续有效）；
2. `regions.parent_id ← system_city.parent_id`（其取值空间同为 `city_id`，省级为 0）；
3. `regions.level ← system_city.level + 1`（原始 0/1/2 → 目标 1/2/3）；
4. `regions.name ← system_city.name`（原样，不 trim 以外不做改写）；
5. `created_at`/`updated_at` 使用表默认值（不迁移历史时间）；
6. 全量导入，不按 `is_show` 过滤（原始 3938 行全部 `is_show=1`）。

历史 B1-03-04 的 `legacymigrate` 地区域与此映射完全一致（`city_id → id`、`level+1`、`parent_id`/`name` 原样），
因此两种来源导入的数据可互换、可幂等对齐。

## 2. 数据版本与更新策略

- 当前版本 `v1`，对应上述 3938 行快照；版本号写在数据文件头与 `regiondata.DataVersion`。
- 数据文件头部记录 `data_version`、`source_file`、`source_sha256`、`source_table`、`source_export_date`、`rows`。
- **仅当原始数据确实更新时**才递增版本（例如 Owner 提供新的 `system_city` 导出）：
  1. 保留旧 `regions_v{old}.tsv`（历史可追溯），新增 `regions_v{new}.tsv`；
  2. 更新 `regiondata` 中的 `DataVersion`/`Source*` 常量与 `//go:embed` 目标；
  3. 更新 `docs/design/address.md`、本文与相关测试的规模断言；
  4. 对已有环境**不做自动改写**：新增节点会补插，`id` 内容变化会触发冲突并拒绝（见 §4），
     由 Owner 决策后再人工处理。
- 不做「定时同步行政区划」：字典是快照事实，更新需走代码评审与数据版本变更。

## 3. 字段映射

| 源（`system_city`） | 目标（`regions`） | 转换 | 说明 |
| --- | --- | --- | --- |
| `city_id` | `id` | 直接复用 | 历史地址/订单中的地区 id 不需要二次映射 |
| `parent_id` | `parent_id` | 原样 | 省级为 0；其余指向父级 `city_id` |
| `level` | `level` | `+1` | 0→1 省、1→2 市、2→3 区/县 |
| `name` | `name` | 原样 | `VARCHAR(64)`，实测最长 15 个字符 |
| — | `created_at`/`updated_at` | 默认值 | 不迁移历史时间 |

## 4. 完整性规则

`regiondata.Validate`（数据文件加载时强制执行）与数据库侧核对共同保证：

- `id` 唯一且 > 0；`level ∈ {1,2,3}`；`name` 非空且 ≤ 64 字符；
- 省级节点 `parent_id = 0`；
- 非省级节点的父节点必须存在，且 `parent.level = child.level - 1`；
- 每个节点最多 3 层可追溯到省级根节点（超过即判定断链/成环）；
- 导入前差异检查：目标库不得存在「数据文件之外的额外节点」，同 id 的
  `parent_id/level/name` 必须完全一致。

v1 实测：0 重复、0 空名、0 孤儿、0 层级不连续、0 环、最大深度 3。

## 5. 初始化入口

```bash
# 只校验数据文件与目标库差异，不写库（打印统计）
DATABASE_DEFAULT_NAME=<db> DATABASE_DEFAULT_PASS=<pass> go run . region-seed dry-run

# 幂等写入（空库写入 3938 行；已有数据只补缺失；冲突则整体拒绝）
DATABASE_DEFAULT_NAME=<db> DATABASE_DEFAULT_PASS=<pass> go run . region-seed import
```

- 命令实现在 `internal/cmd/region_seed.go`，数据与逻辑在 `internal/regiondata`；
- 数据库配置复用 `boot.ApplyDatabaseConfig`（与 serve 同一来源），**不启动 HTTP/Redis/Kafka**；
- 集成测试的 setup 也调用同一 `regiondata.Ensure`（`internal/controller/region`、
  `internal/controller/address`），因此新建隔离测试库无需人工导入；
- `regions` 只读：业务请求不得写入，测试不得整表 `DELETE`（自建节点要按 id 清理）。

## 6. 幂等与冲突语义

`Ensure(ctx, db, dryRun)`：

1. 读取目标库现有 `id,parent_id,level,name`；
2. 逐 id 比对：
   - 完全一致 → 计入 `Matched`，不写；
   - 目标库缺失 → 计入待插入；
   - 同 id 内容不同 → **冲突**；
   - 目标库存在数据文件之外的 id → **额外节点**；
3. 存在冲突或额外节点：**不做任何写入**，返回错误并给出示例（不覆盖、不删除）；
4. 否则在**单事务**内按 id 升序分批（500/批）`INSERT`，提交前复核
   `SELECT COUNT(*) FROM regions == 数据文件总行数`，不一致整体回滚；
5. `dry-run` 只执行 1–3，不写库。

重复执行结果：第二次 `Inserted=0 / Matched=3938`。

## 7. 测试方法

```bash
# 纯内存：数据文件完整性、来源元信息、校验器拒绝脏数据
go test -count=1 -run 'TestDatasetIntegrity|TestMetadataSource|TestValidateRejectsBrokenData' ./internal/regiondata/

# 真实 MySQL：幂等、冲突拒绝、额外节点拒绝、规模核对（要求隔离库）
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root \
  go test -count=1 -run 'TestEnsureIntegration' ./internal/regiondata/ -v

# 依赖地区字典的接口用例
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root go test -p 1 -count=1 \
  ./internal/controller/region/ ./internal/controller/address/
```

## 8. 故障恢复

| 现象 | 处理 |
| --- | --- |
| `目标库缺少 regions 表` | 先执行 `my-shop migrate up`（建表迁移 `20261110000003_regions`） |
| `内容冲突 N 个` | 逐条核对错误信息中的 id：确认是历史脏数据还是数据版本升级；**不要**手工改数据文件迁就现有库，也不要 `DELETE` 整表。由 Owner 决策后按版本升级流程处理 |
| `额外节点 N 个` | 这些 id 不在数据文件内（多为测试/人工写入）。确认归属后按 id 删除，再重跑 `region-seed import` |
| 导入中断 | 写入在单事务内，失败即整体回滚；直接重跑命令即可（幂等） |
| 误删部分地区 | 重跑 `region-seed import` 会补齐缺失节点（已有一致节点跳过） |
| 需要全量重建 | 仅限隔离测试库：`DELETE FROM regions` 后重跑 `region-seed import`；**禁止**在生产/共享库整表清空 |
| 锁等待 | 导入持有 `regions` 行锁/插入意向锁；确认无长事务后再重试 |

## 9. 与历史迁移工具的关系

- `my-shop legacy-migrate import` 仍可从 staging（`system_city`）迁移全量历史数据，地区映射规则与本文一致；
- 新的 `region-seed` 用于**只补地区字典**，不依赖 staging 库，适用于新建测试库、纯后端部署与灾难恢复；
- 两者都幂等，可先后执行：先 `legacy-migrate` 后 `region-seed` 时，字典部分全部 `Matched`。
