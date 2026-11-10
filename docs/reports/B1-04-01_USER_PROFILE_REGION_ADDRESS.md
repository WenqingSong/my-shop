# B1-04-01 用户资料与地区地址 · 交付报告

## 1. 任务范围

严格按 B1-04-01 冻结范围交付三部分：

1. **用户资料 Profile**：新增独立 `profile` 模块，提供当前登录用户资料的读取与更新
   （昵称、头像 URL、手机号、生日），手机号唯一由数据库唯一约束兜底。
2. **地区 Region**：新增独立 `region` 只读模块，提供省/市/区三级树与按父级查询直接子级；
   地区数据复用 B1-03-04 已导入的 3938 条，不新增、不修改、不重建地区数据。
3. **地址 Address**：在既有 `address` 模块上扩展 `region_id`（可空，指向 `regions.level=3`），
   保留原有 CRUD 与自由文本字段；默认地址唯一性增加「用户行锁 + DB 唯一约束」两层保护。

不在本任务范围（未实现）：短信验证码、手机号验证服务、微信登录、密码修改、地区写接口、
地址 `region_id` 历史回填、OpenAPI 生成与全局集成文件更新。

## 2. Git 基线与开发分支

| 项 | 值 |
| --- | --- |
| 基础分支 | `feat/p2-backend` |
| 基线 Commit | `af512f9e8a64c133c705d3a0ed1dd106de3d5c60`（`af512f9`） |
| 远端基线 | `origin/feat/p2-backend` 与本地一致（0 ahead / 0 behind） |
| 开发分支 | `feat/b104-01-profile-region-address`（从基线新建，未重置/未覆盖） |
| 工作区 | 开发前干净；未使用 `reset --hard` / `clean` / `stash` |

## 3. 实际实现的模块

| 模块 | 路径 |
| --- | --- |
| Profile DTO / Controller / Logic / Service | `api/profile/v1/`、`internal/controller/profile/`、`internal/logic/profile/`、`internal/service/profile.go` |
| Region DTO / Controller / Logic / Service | `api/region/v1/`、`internal/controller/region/`、`internal/logic/region/`、`internal/service/region.go` |
| Address 扩展 | `api/address/v1/address.go`、`internal/logic/address/address.go` |
| 路由锚点（共享文件） | `internal/cmd/routes_frontend.go` 的 `// B1-04-01 BEGIN/END` |
| Logic 注册锚点（共享文件） | `internal/logic/logic.go` 的 `// B1-04-01 BEGIN/END` |
| 错误码（IAM 域内新增编号） | `internal/codes/codes.go`：`2016 CodePhoneExists`（OpenAPI 契约已登记） |
| Migration | `internal/migrations/sql/20261110000015_profile_region_address.{up,down}.sql` |

## 4. API 清单

| 方法 | 路径 | 认证 | 说明 |
| --- | --- | --- | --- |
| GET | `/profile` | `Auth`（用户 JWT） | 查询当前登录用户资料 |
| PUT | `/profile` | `Auth` | 更新当前登录用户资料（省略=不改，空串=清空） |
| GET | `/regions` | 公开 | 按 `parent_id` 查询直接子级；缺省/0 返回省级 |
| GET | `/regions/tree` | 公开 | 省/市/区三级树（`items` + `total`） |
| GET | `/regions/:id` | 公开 | 地区详情与 `province`/`city`/`district` 链路 |
| POST | `/addresses` | `Auth` | 既有接口，新增可选 `region_id` |
| GET | `/addresses` | `Auth` | 既有接口，列表新增返回 `region_id` |
| GET | `/addresses/:id` | `Auth` | 既有接口，详情新增返回 `region_id` |
| PUT | `/addresses/:id` | `Auth` | 既有接口，新增 `region_id`（0=清空） |
| DELETE | `/addresses/:id` | `Auth` | 既有接口，保持不变 |

`GET /me`、注册/登录/refresh/登出/会话接口均未改动，保持兼容。

## 5. Schema 与 Migration 变化

版本：`20261110000015`（`profile_region_address`），up/down 成对提供。

| 变更 | DDL | 说明 |
| --- | --- | --- |
| `users.birthday` | `DATE NULL AFTER phone` | 实际核查确认原 Schema 无该列；不创建 `user_profiles` 表 |
| `addresses.region_id` | `BIGINT UNSIGNED NULL AFTER district` | 软引用 `regions.level=3`，允许 NULL 兼容历史地址 |
| `addresses.idx_region_id` | `KEY idx_region_id (region_id)` | 按地区反查地址 |

- 未修改任何历史 Migration，未重建/未导入 `regions`，未改历史地址数据。
- 未重复创建既有索引/唯一约束；`uk_user_default`、`default_key` 生成列保持原样。
- down 正确回滚本任务新增结构（已在测试库实际执行验证）。

## 6. 核心业务规则

### 6.1 Profile

- 用户 id 只取自已认证 `Principal.UserID`；DTO 不声明 `user_id`，请求体伪造无效。
- 响应仅含 `id/username/nickname/avatar_url/phone/birthday`，不含 `password_hash`/`status`/`auth_epoch`。
- 字段语义：`nil`（省略）=不修改；`""`=清空（`nickname`/`avatar_url` → 空串，`phone`/`birthday` → NULL）。
- 手机号：`^1[3-9]\d{9}$`，未设置写 NULL；唯一性由 `uk_phone` 保证，1062 → `2016`（409），无「先查再写」。
- 生日：严格 `YYYY-MM-DD`（拒绝 `1990-2-3`、`1990-02-30` 等），输出同格式。
- 头像：非空须 `http`/`https` 绝对 URL 且 ≤512 字符，拒绝 `javascript:`/`data:`。
- 昵称 trim 后 ≤32 字符；用户不存在返回 `2015`（404，防御性）。

### 6.2 Region

- 只读，无任何写端点；地区数据由 B1-03-04 导入，数量 3938（34 省 + 369 市 + 3535 区县）。
- 树查询：单次全量 SQL + 内存建树，无 N+1；构建时校验省级 `level=1 && parent_id=0`、
  子级 `parent.level == level-1`、节点唯一、全部被根覆盖；数据不完整返回 1000，不输出残缺树。
- 同级按 `id` 升序，顺序稳定；`parent_id=-1` → 1001；不存在的正数父级 → 空列表。
- 地址写入校验：单次 JOIN 要求 `d.level=3 && c.level=2 && p.level=1 && p.parent_id=0`。

### 6.3 Address

- `region_id` 省略：不修改结构化地区；若同时修改任一地区文本 → 清空 `region_id`（退出结构化模式）。
- `region_id=0`：清空引用，保留文本；`region_id>0`：校验并覆盖省/市/区文本（以字典为准）。
- `region_id<0`、不存在、非区县、链路断裂 → 1001（400），无写入。
- 历史地址 `region_id=NULL`：详情/列表正常返回 `null`，不回填。

## 7. 并发安全实现

**Profile 手机号**

- 不做「查询是否重复再写入」；直接写入并捕获 MySQL 1062，映射为 `2016`。
- 并发测试验证：两个用户并发写同一号码，恰好 1 次成功、1 次 409，DB 中该号码仅 1 行。

**Address 默认地址**

- 创建/设默认在同一事务内先执行 `SELECT id FROM users WHERE id=? FOR UPDATE`（`lockUserForDefault`），
  再「取消旧默认 → 置新默认」；首条地址的 `count==0` 判定也在锁内。
- `uk_user_default`（`default_key` VIRTUAL 生成列）作为最终防线，1062 → 7002（409）。
- 并发测试（非仅 HTTP 断言）：
  - 12 个并发创建首条地址：最终 12 条地址、**恰好 1 个默认**；
  - 24 个并发切换默认：全部返回 0 或 7002，最终**恰好 1 个默认**，且该行 `default_key == user_id`。

## 8. 测试用例及结果

运行环境：MySQL 8.0.46（127.0.0.1:3306）、Redis（DB 4）、独立测试库 `my_shop_b104_test`。
测试内部两道库名核实：`DATABASE_DEFAULT_NAME` 必须为 `my_shop_b104_test`，且 `SELECT DATABASE()` 必须一致；
不一致直接 `t.Fatalf`，不执行任何 DELETE。**未在 `my_shop` / `my_shop_b10304_verify` 上执行破坏性测试。**

| 测试 | 覆盖点 | 结果 |
| --- | --- | --- |
| `TestProfileGetAndUpdateFields` | 读取默认资料、昵称 trim、头像、手机号、生日、省略字段不变 | PASS |
| `TestProfileClearVersusOmit` | 空请求体不改、空串清空、phone/birthday 清为 NULL | PASS |
| `TestProfileValidation` | 手机号格式、生日格式（含 `02-30`/未补零）、头像协议、昵称超长 | PASS |
| `TestProfilePhoneUnique` | 重复手机号 409/2016、并发同号仅 1 成功、DB 唯一 | PASS |
| `TestProfileIsolation` | 伪造 user_id 被忽略、未登录 401、不存在用户 2015 | PASS |
| `TestRegionListByParent` | 省（34）/市/区县查询、空父级、负 parent_id、不存在父级、排序稳定 | PASS |
| `TestRegionTree` | total=3938、34 根、父子关系、无重复、无孤儿、层级分布 34/369/3535 | PASS |
| `TestRegionDetail` | 区县三级链路、省级链、 null 层级、不存在 1001 | PASS |
| `TestAddressRegionCreateAndUpdate` | 创建/更新 region_id、字典覆盖伪造文本、列表/详情返回、0 清空、文本切换清空 ID | PASS |
| `TestAddressRegionValidation` | 不存在/省级/市级/链路断裂/0/负数 → 400，且无写入 | PASS |
| `TestAddressLegacyNullRegion` | 历史地址 region_id NULL 的详情与列表读取 | PASS |
| `TestAddressDefaultUniqueAcrossUsers` | 首条自动默认、显式默认取消旧默认、多用户互不影响 | PASS |
| `TestAddressDefaultConcurrencyWithLock` | 并发创建首条 + 并发切换默认，校验最终 DB 状态 | PASS |
| 既有 `TestAddressCrudAndDefaultSwitch` 等 5 项 | 地址 CRUD/隔离/校验/并发回归 | PASS |
| `internal/logic/profile` 单测 | `parseBirthday`、`isHTTPURL` | PASS |
| `internal/logic/address` 单测 | 收货人/手机号/地区/详情的 trim 与长度 | PASS |

执行命令：

```bash
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root \
  go test -count=1 ./internal/logic/profile/ ./internal/logic/address/ \
                 ./internal/controller/profile/ ./internal/controller/region/ ./internal/controller/address/
```

Migration 回滚验证（测试库）：执行 down → `users.birthday`/`addresses.region_id`/`idx_region_id` 均消失；
执行 up → 三者恢复，`regions` 仍为 3938 条。

## 9. 构建与静态检查结果

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 构建 | `go build ./...` | PASS |
| 静态检查 | `go vet ./...` | PASS |
| 本任务单元测试 | 见 §8 | PASS |
| 本任务集成测试 | 见 §8（独立库） | PASS |
| 全局路由清单测试 | `go test ./internal/cmd/ -run TestRoute` | **FAIL（预期）** |
| OpenAPI 契约检查 | `scripts/contract-check.sh` | 未运行（预期失败，不修改集成文件） |

路由测试失败原因（并行开发预期，未擅自修改 `internal/cmd/routes_test.go`）：

- 新增 5 条路由未登记：`GET/PUT /profile`、`GET /regions`、`GET /regions/tree`、`GET /regions/:id`；
- 实际路由数 147，测试清单期望 142；
- `openapi/surgecart.openapi.yaml` 未含 `/profile`、`/regions/tree`，而含本任务未实现的 `PUT /me`。

## 10. 未完成事项

1. **全局集成文件未更新**（任务禁止）：`internal/cmd/routes_test.go` 路由清单/数量断言、
   `tools/openapi-gen/gen.go`、`openapi/**`、`contract-check.sh`、`scripts/test-*.sh` 均未改动。
   需集成所有者统一更新路由断言与 OpenAPI 契约后，`routes_test` / `contract-check` 才能转绿。
2. **Registry 未登记**：`.agent/registry/migrations.md` 无 `20261110000015` 的 RESERVED 记录
   （当前最后一条为 `20261110000014`）。按任务要求未自行修改全局 Registry，需 Owner 回填。
3. **共享文件锚点缺失**：`internal/cmd/routes_frontend.go` 与 `internal/logic/logic.go` 开发前不存在
   B1-04-01 专属锚点；本任务以 `// B1-04-01 BEGIN/END` 注释包裹新增内容（最小侵入），
   建议后续任务沿用同一锚点规范并在预留阶段预置。

## 11. 已知问题

1. **OpenAPI 契约与冻结范围不一致（需 Owner 决策）**：
   - 契约定义 `PUT /me`（`iam.UpdateMe`），本任务按冻结范围实现 `GET/PUT /profile`，未实现 `PUT /me`；
   - 契约地址字段为 `province_id/city_id/district_id` 三 ID，本任务按冻结范围实现单列 `region_id`；
   - 契约 `UserMeResponse` 期望 `GET /me` 返回 nickname/avatar_url/phone，本任务保持 `/me` 原结构不变。
   这三点均需在集成阶段统一，避免契约漂移。本任务未修改 `openapi/**`。
2. **测试库预置状态异常（需 Owner 关注）**：`my_shop_b10401_migrate` 的 `schema_migrations` 已记录
   `20261110000015`，且 `addresses` 已是 `province_id/city_id/district_id` 结构、`users` 无 `birthday`。
   仓库任何分支均不存在该版本的 migration 文件，说明该库由并行/预置 DDL 写入。本任务未使用、未修改该库，
   全部验证在新建的 `my_shop_b104_test` 完成。若该预置库代表最终方向（三 ID），则本任务 `region_id`
   需按 Owner 决策调整，`20261110000015` 也需重新分配版本。
3. `my_shop`（默认配置库）当前 `regions` 为 0 条、版本停在 `20261110000014`；本任务测试不依赖该库。
4. 地区树为全量内存构建（3938 节点），当前规模可接受；字典显著增长时需改为懒加载/缓存。
5. 历史地址不做 `region_id` 回填（无法可靠猜测文本与节点的对应关系）。

## 12. 文件变更清单

### 新增

```text
api/profile/v1/profile.go
api/region/v1/region.go
internal/controller/profile/profile.go
internal/controller/profile/profile_test.go
internal/controller/region/region.go
internal/controller/region/region_test.go
internal/controller/address/address_region_test.go
internal/logic/profile/profile.go
internal/logic/profile/profile_internal_test.go
internal/logic/region/region.go
internal/logic/address/address_internal_test.go
internal/service/profile.go
internal/service/region.go
internal/migrations/sql/20261110000015_profile_region_address.up.sql
internal/migrations/sql/20261110000015_profile_region_address.down.sql
docs/reports/B1-04-01_USER_PROFILE_REGION_ADDRESS.md
```

### 修改

```text
api/address/v1/address.go                      # 新增 region_id 字段
internal/logic/address/address.go              # region 校验/字典回填/用户行锁
internal/controller/address/address_test.go    # 增加测试库隔离断言
internal/cmd/routes_frontend.go                # B1-04-01 锚点内新增 5 条路由
internal/logic/logic.go                        # B1-04-01 锚点内新增 2 个 logic 注册
internal/codes/codes.go                        # 2016 CodePhoneExists
docs/design/iam.md                             # §3.9 资料读写、错误码、代码位置
docs/design/address.md                         # region_id、地区只读模块、并发控制
docs/design/migration.md                       # 15 号迁移清单
docs/dev/iam.md                                # 资料接口实现与坑位
docs/dev/address.md                            # 地址↔地区规则、并发控制、测试方式
```

### 明确未修改（集成所有者范围）

```text
internal/cmd/routes_test.go
tools/openapi-gen/gen.go
openapi/**
internal/migrations/migrations_test.go
scripts/test-unit.sh
scripts/test-race.sh
.agent/registry/migrations.md
.agent/registry/error-codes.md
```

## 13. Git 提交信息

```text
feat(b104-01): implement user profile and region address
```

本报告与代码在同一开发分支提交，未合并回 `feat/p2-backend`，未推送、未修改其他任务分支。
最终 Commit SHA 见提交后的 `git log -1 --format=%H`（随 Owner 汇报提供）。
