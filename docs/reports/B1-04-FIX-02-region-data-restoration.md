# B1-04-FIX-02 历史地区数据恢复与初始化机制报告

| 项目 | 内容 |
| --- | --- |
| 任务 | B1-04-FIX-02：恢复历史地区数据与初始化机制 |
| 分支 | `feat/p2-backend` |
| 基线 HEAD | `bd2d8f0`（工作区干净） |
| 目标数据库 | `my_shop_b104_test`（隔离测试库），MySQL 8.0.46 |
| 最终结论 | **PASS** |

## 1. 最终结论

**PASS。** Owner 提供的 `tmp/shopping.sql` 中 `system_city` 表共 3938 行，结构与内容完整、层级关系自洽；
已按历史迁移约定（`id ← city_id`、`level+1`、`parent_id/name` 原样）提取为受版本管理的数据文件，
并以 `my-shop region-seed import` 幂等写入隔离测试库：实际导入 3938 行（34 省 / 369 市 / 3535 区县），
0 失败、0 冲突。此前失败的 5 个 Region/Address 用例全部恢复通过，且新建空库可由测试 setup 自愈。

未修改 Owner 的原始文件（SHA-256 校验前后一致），未导入 `shopping.sql` 中任何其他表/业务数据。

## 2. 原始数据来源

| 项 | 值 |
| --- | --- |
| 文件路径 | `/root/projects/my-shop/tmp/shopping.sql`（`.gitignore` 忽略 `/tmp/`，不纳入版本库） |
| 文件格式 | Navicat MySQL 导出 SQL（`INSERT INTO ... VALUES`），MySQL 5.7.17 源 |
| 文件大小 | 5,671,481 字节 |
| SHA-256 | `568da34e4cd084e497450e07e75709966b67153daa99e68ae14e291a01cc23c2` |
| 原始表 | `system_city`（注释「城市表」），83 张表中仅使用该表 |
| 原始记录数 | 3938（`INSERT INTO \`system_city\`` 行数，与导出 DDL 一致） |
| 数据版本 | 导出日期 2021-09-27（文件头 `Date: 27/09/2021`）；本次定义的导入版本为 `v1` |

原始表结构（`system_city`）：

```text
id, city_id, level, parent_id, area_code, name, merger_name, lng, lat, is_show
KEY: PRIMARY KEY (id)
```

## 3. 数据映射

| 源（`system_city`） | 目标（`regions`） | 转换规则 |
| --- | --- | --- |
| `city_id` | `id` | 直接复用，**不重新编号**（历史地址/订单地区 id 继续有效） |
| `parent_id` | `parent_id` | 原样（省级为 0，其余指向父级 `city_id`） |
| `level` | `level` | `+1`：0→1 省、1→2 市、2→3 区/县 |
| `name` | `name` | 原样（`VARCHAR(64)`，实测最长 15 字） |
| `area_code`/`merger_name`/`lng`/`lat`/`is_show` | — | 不导入（目标表无对应列，非地区字典必需字段） |
| — | `created_at`/`updated_at` | 表默认值（不迁移历史时间） |

映射与 B1-03-04 `legacymigrate` 的地区域完全一致（`legacyRegion` → `INSERT INTO regions (id,parent_id,level,name) VALUES (..., level+1, ...)`）。

关键样本核对：`1 北京市(省) → 2 北京市(市) → 3 东城区(区县)`、`203 西城区(区县, parent=2)`。

## 4. 数据质量检查

对原始 3938 行逐条解析后检查（Python，未修改原文件）：

| 检查项 | 结果 |
| --- | --- |
| 原始记录数 | 3938 |
| 省级（原始 level=0） | 34 |
| 市级（原始 level=1） | 369 |
| 区县级（原始 level=2） | 3535 |
| 重复 `city_id` | 0 |
| 空名称 | 0 |
| 名称超过 64 字符 | 0 |
| 非法层级（不在 0/1/2） | 0 |
| 孤儿节点（父级不存在） | 0 |
| 父子层级不连续 | 0 |
| 循环父子关系 | 0（每个节点 ≤3 跳到达省级根） |
| 省级 `parent_id != 0` | 0 |
| `is_show=0`（被过滤风险） | 0（3938 行全部为 1） |

与历史参考值（34/369/3535/3938）完全一致，无需解释数据版本或行政区划差异，也未为了凑数修改任何数据。

## 5. 实际导入结果

| 项 | 值 |
| --- | --- |
| 目标数据库 | `my_shop_b104_test`（`SELECT DATABASE()` 实测） |
| MySQL 版本 | 8.0.46 |
| Migration 版本 | `20261111000004`（`dirty=false`） |
| 导入前 `SELECT COUNT(*) FROM regions` | 0 |
| 导入后记录数 | 3938 |
| 实际新增 | 3938（单事务、8 批 × 500 内） |
| 实际更新 | 0（不覆盖既有行） |
| 导入失败 | 0 |
| 重复执行（第二次 `region-seed import`） | 新增 0 / 已一致 3938 |
| 导入后层级分布 | 省 34 / 市 369 / 区县 3535 |
| 导入后孤儿节点 / 非法层级 / 环 | 0 / 0 / 0 |

命令（真实输出见 §7）：

```bash
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root go run . region-seed import
```

## 6. 初始化机制

| 组成 | 位置 | 说明 |
| --- | --- | --- |
| 数据文件 | `internal/regiondata/regions_v1.tsv`（`go:embed`，105,346 字节，SHA-256 `2a78fe9a…`） | 只含 `id/parent_id/level/name`，带来源与版本头注释 |
| 数据/校验/导入实现 | `internal/regiondata/regiondata.go` | `Records`/`Metadata`/`Validate`/`Ensure`；结构校验、幂等差异比较、冲突拒绝、单事务批量写入 + 行数复核 |
| CLI 入口 | `internal/cmd/region_seed.go` → `my-shop region-seed <dry-run|import>` | 复用 `boot.ApplyDatabaseConfig`（与 serve 同一配置来源），不启动 HTTP/Redis/Kafka |
| 集成测试自动初始化 | `internal/controller/region/region_test.go`、`internal/controller/address/address_test.go` | 测试 setup 调用同一 `regiondata.Ensure`，空字典自动补齐 |
| 运维文档 | `docs/dev/region-data-initialization.md` | 来源、版本、映射、规则、入口、幂等、更新、测试、恢复 |

幂等/冲突语义：目标库为空 → 全量写入；已有部分 → 只补缺失；同一 `id` 内容不同或存在数据文件之外的
额外节点 → **不做任何写入**并报错（不覆盖、不删除）；重复执行第二次 `Inserted=0`。

## 7. 测试结果

环境：隔离库 `my_shop_b104_test`，MySQL 8.0.46，`-p 1` 串行（多包共享同一库的既有约定）。

| # | 命令 | 退出码 | 状态 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `gofmt -l .` | 0 | **PASS** | 无输出 |
| 2 | `go build ./...` | 0 | **PASS** | — |
| 3 | `go vet ./...` | 0 | **PASS** | — |
| 4 | `go test -run 'TestRegionListByParent\|TestRegionTree\|TestRegionDetail' ./internal/controller/region/` | 0 | **PASS** | 此前 3 个失败用例全部恢复 |
| 5 | `go test -run 'TestAddressRegionCreateAndUpdate\|TestAddressDefaultUniqueAcrossUsers' ./internal/controller/address/` | 0 | **PASS** | 此前 2 个失败用例全部恢复 |
| 6 | `go test -p 1 -count=1 ./internal/controller/region/ ./internal/controller/address/`（先 `DELETE FROM regions`） | 0 | **PASS** | 空字典下由测试 setup 自动初始化 3938 行后通过 |
| 7 | `go test -race -p 1 -count=1 ./internal/controller/region/ ./internal/controller/address/ ./internal/regiondata/` | 0 | **PASS** | Region/Address Race |
| 8 | `go test -count=1 -run 'TestDatasetIntegrity\|TestMetadataSource\|TestValidateRejectsBrokenData' ./internal/regiondata/` | 0 | **PASS** | 数据文件完整性/来源/校验器（纯内存） |
| 9 | `go test -run 'TestEnsureIntegration' ./internal/regiondata/` | 0 | **PASS** | 幂等、额外节点拒绝、内容冲突拒绝（真实 MySQL） |
| 10 | `bash scripts/test-unit.sh` | 0 | **PASS** | 已登记 regiondata 纯内存用例 |
| 11 | `bash scripts/contract-check.sh` | 0 | **PASS** | 210 常驻 API + 4 planned、490 Schema；无契约变化 |
| 12 | `bash scripts/check-registry.sh` | 0 | **PASS** | 本次未新增 migration，Registry 无变化 |
| 13 | `go test -p 1 -count=1 ./...`（全量，真实执行） | 0 | **PASS** | 48 个包 `ok`，0 失败（此前 5 个地区用例已恢复） |
| 14 | `bash scripts/test-integration.sh`（同环境） | 0 | **PASS** | 与 #13 同一命令的项目脚本入口，退出码 0 |
| 15 | `go test -p 1 ./...`（官方命令，复用成功缓存） | 0 | **PASS** | 48 个包 `ok`、0 失败 |
| 16 | `go run . migrate version`（隔离库） | 0 | **PASS** | `20261111000004 (dirty=false)` |

本次**未新增 migration**（地区数据属于数据初始化，不进 migration），因此无需 Up/Down/Up 迁移验证；
`regions` 表结构与索引未改动。

### 7.1 全量回归

`go test -p 1 -count=1 ./...`：48 个包 `ok`，**0 个失败**（此前唯一的 5 个地区用例已恢复），
退出码 0；`bash scripts/test-integration.sh` 复跑同样退出码 0。

说明：`internal/migrations` 的用例会重建空 schema（地区表被清空），随后 `internal/regiondata` 的
集成用例会重新写入字典；即使顺序变化，Controller 测试 setup 也会自愈——这正是本任务要建立的机制。

## 8. Git 信息

| 项 | 值 |
| --- | --- |
| 分支 | `feat/p2-backend` |
| 基线 SHA | `bd2d8f0` |
| 实现提交 | `4bf7a7d` `fix(region): restore region dictionary data` |
| 本报告提交 | 见 `git log -1` |
| 工作区 | 干净（`git status --short` 无输出） |
| Push / Merge | 未 Push、未合并 `main`/`develop` |
| 原始数据 | `tmp/shopping.sql` 未被修改（SHA-256 前后一致），未被提交 |

## 9. 遗留问题

1. **数据版本只到 2021-09-27 快照**：行政区划后续调整不会自动同步；更新需 Owner 提供新导出并按
   `docs/dev/region-data-initialization.md` §2 递增数据版本。
2. **并行包执行**：多个测试包共享同一测试库时仍需 `-p 1`；并行执行时 `regiondata.Ensure` 会把并发测试
   临时插入的地区节点视为「额外节点」并 fail-closed（属预期保护，不是数据错误）。
3. **生产初始化需显式执行**：`serve` 不会自动写入地区字典，部署流程需在 `migrate up` 后执行
   `my-shop region-seed import`（幂等，可重复）。未在本任务中修改部署编排。
4. **地区 API 的既有边界未变**：无 code/经纬度字段、树接口一次全量读取（3938 行）等设计未改动。
