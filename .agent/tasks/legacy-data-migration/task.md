# Task: SurgeCart 历史电商数据迁移与集成验收（legacy-data-migration）

## Goal

在不破坏 SurgeCart 现有 Schema、业务逻辑与数据一致性约束的前提下，通过独立数据迁移工具，把根目录历史生产数据库 `shopping.sql` 中的商品、分类、SKU、库存及必要图片数据，适配并导入隔离测试环境，完成真实 API 验证；迁移过程安全、可重复、可验证。最终可通过现有 GoFrame API 查询到成功迁移的历史商品、SKU 与库存，且不破坏已有核心业务能力。

## Scope

- 原始 SQL 安全归档：`shopping.sql` 移动到 `storage/private/legacy/shopping.sql`，`.gitignore` 阻止跟踪，SHA-256 前后一致校验，`docs/legacy-data.md` 记录来源/用途/范围/安全注意，检查 SQL 的历史 Git 跟踪情况。
- 历史数据适配规则（仅 5 张旧表 → 5 张目标表）：
  - `store_category` → `categories`
  - `store_product` → `products` + `product_images`
  - `store_product_attr` / `store_product_attr_result` / `store_product_attr_value` → `skus`
  - `store_product`（商品级 stock）与 `store_product_attr_value`（SKU 级 stock）→ `inventories`
  - 覆盖：软删除/上下架状态映射、旧 ID→新 ID 映射、分类关联转换、金额「元→整数分」精确转换、规格→SKU 映射、商品级与 SKU 级库存去重、缺失 SKU/非法分类/重复规格等异常数据、默认 SKU 有据判定、无法安全迁移数据的原因记录。
- 独立 Data Migration 工具：独立 Go CLI；原始 SQL 仅导入严格隔离的临时 staging 库；支持 Dry Run 与真实导入；支持旧 ID→新 ID 持久映射；支持重复执行幂等（不重复商品/SKU/库存、不重复增加库存）；导入失败具备事务回滚或安全重试；不覆盖非迁移来源业务数据；不通过关闭外键约束绕过校验。Schema Migration 与 Data Migration 严格区分，若现有结构已满足则不得新增 Schema Migration。
- 数据迁移集成验收：隔离测试环境真实导入后，验证分类树、商品分类关联、商品 SKU 关联、SKU 库存关系、金额精度、图片数据、唯一/FK 约束、可重复执行、失败原子性，并通过现有 API 查询迁移结果；输出真实执行的成功/跳过/失败数量与原因。
- 自动化测试与文档：字段映射/金额转换/ID 映射/异常数据处理的单元测试；可重复执行的集成测试或验收脚本；迁移操作说明、结果报告与已知限制；保证原始 SQL 不进入 Docker 镜像、CI 产物或远程仓库。

## Out of Scope

- 历史用户及密码迁移、历史订单及订单明细迁移、历史优惠券迁移、旧支付/物流/资金/积分业务。
- 新的商城业务 API、前端适配、Kubernetes 部署、大规模合成数据生成与性能压测。
- 重复此前已完成的全量数据库审计（83 表、商品 226、SKU 111、分类 57 等结论以 Owner 提供为准，本任务不重新审计全库）。
- 修改既有 Schema、迁移历史 migration 文件、改动既有业务模块行为（除非数据接入验证所需的最小只读查询）。
- 连接历史生产库、修改生产环境、将历史真实账号与敏感配置导入目标系统。

## Milestone

Milestone: SurgeCart 历史电商数据迁移与集成验收（历史商品/分类/SKU/库存接入隔离测试环境，现有 API 可查询，迁移安全可重复可验证）

## Acceptance Criteria

- [ ] AC-001（原始 SQL 归档与安全隔离）：`shopping.sql` 从仓库根移动到 `storage/private/legacy/shopping.sql`，移动前后 SHA-256 一致；`.gitignore` 新增规则使该路径不被跟踪（`git check-ignore` 命中、`git status` 不再出现该文件）；`docs/legacy-data.md` 记录数据来源、用途、迁移范围与安全注意事项；`git ls-files` 与全历史（`git log --all --oneline -- storage/private/legacy/shopping.sql`）确认原始 SQL 未被跟踪/提交；日志、报告、测试样本中不出现真实私钥、第三方凭据或个人身份信息。
- [ ] AC-002（staging 与目标库隔离）：原始 SQL 只导入独立 staging 数据库，迁移工具与 SurgeCart 目标测试库通过独立连接/库名/权限隔离，工具无法对目标库直接执行原始 SQL；目标库不得通过 `SET FOREIGN_KEY_CHECKS=0` 等方式绕过外键/约束校验。
- [ ] AC-003（分类迁移与树结构）：历史 `store_category`（含软删除/上下架状态）按规则适配为 `categories`，`parent_id` 层级关系与历史 `pid` 一致；`GET /categories` 可见分类树且结构正确；软删除（`is_del=1`）与非法分类按规则处理并记录原因。
- [ ] AC-004（商品迁移、分类关联与金额精度）：历史 `store_product` 适配为 `products`：`store_name→name`、`category_id` 正确关联新分类、`price` 从元精确转整数分（如 19.90→1990，无浮点误差）、主图与轮播图拆分为 `product_images`；软删除/上下架状态正确映射到 `products.status`；`GET /products` 与 `GET /products/:id` 可查询到迁移商品。
- [ ] AC-005（规格→SKU 映射）：历史 `store_product_attr` + `store_product_attr_result` + `store_product_attr_value` 的规格组合正确映射为 `skus`（name 为规格组合、price 精确转分、status 映射）；多规格商品生成多个 SKU；重复规格、缺失 SKU 等异常数据按规则跳过并记录原因。
- [ ] AC-006（默认 SKU 有据判定）：是否创建默认 SKU 依据真实数据判断（如单规格商品存在「默认」规格行），不无依据补造业务数据；无法安全映射的 SKU 记录明确原因。
- [ ] AC-007（库存正确且不重复计算）：历史商品级 `stock` 与 SKU 级 `stock` 按明确规则去重（单规格用商品级、多规格用 SKU 级），导入后 `inventories` 与 `skus` 一对一、`quantity` 精确且不重复计算；`GET /admin/inventories/:sku_id` 可查询到正确库存。
- [ ] AC-008（ID 映射持久化与幂等）：旧 ID→新 ID 映射被持久化；重复执行迁移工具不新增重复商品/SKU/库存记录，也不重复增加库存。
- [ ] AC-009（约束一致性与不覆盖存量）：导入结果不违反 `categories` 唯一约束 `(parent_id,name)`、`products` 外键 `category_id`、`skus` 唯一约束 `(product_id,name)` 与外键、`inventories` 唯一约束 `(sku_id)`；不覆盖非迁移来源的现有业务数据。
- [ ] AC-010（Dry Run）：工具支持 Dry Run，仅完成解析、映射、约束校验并输出将执行的变更与校验结果，不写目标库。
- [ ] AC-011（失败原子性）：导入中途失败时，目标库不留下不一致的部分数据（事务回滚或安全重试），可安全再次执行。
- [ ] AC-012（结果统计真实）：工具输出真实执行记录的成功/跳过/失败数量及逐条失败原因；报告以真实执行结果为准，不编造。
- [ ] AC-013（API 集成验收）：在隔离测试环境真实导入后，通过现有 `GET /categories`、`GET /products`、`GET /products/:id`、`GET /admin/products/:id`、`GET /admin/inventories/:sku_id` 可查询到迁移后的分类树、商品（含图片与 SKU）、SKU 与库存，数据与迁移结果一致。
- [ ] AC-014（测试与回归）：字段映射、金额「元→分」转换、ID 映射、异常数据处理（缺 SKU/非法分类/重复规格/软删除/上下架）均有单元测试；有可重复执行的迁移集成测试或验收脚本；现有后端测试无回归。
- [ ] AC-015（安全边界）：原始 SQL 不进入 Docker 镜像（`.dockerignore`）、CI 产物或远程仓库；提交仓库的测试数据为脱敏/合成数据；不连接历史生产库、不修改生产环境、不导入历史真实账号与敏感配置。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1` → `internal/controller` → `internal/service` → `internal/logic`，无 `dao`/`model` 层，数据访问用 `g.DB().Model()`。迁移工具可复用现有 `internal/cmd` 子命令组织方式（`my-shop migrate ...`）。
- Schema Migration 机制已存在（db-migration 任务建立，golang-migrate v4）：迁移文件 `internal/migrations/sql/{14位时间戳}_{title}.up.sql`，`internal/migrations/migrations_test.go` 校验 latestMigrationVersion/businessTables/expectedSchema；`.agent/registry/migrations.md` 最新为 `20261001000022`（coupon，RESERVED）。本任务不改历史 migration 文件。
- 目标表结构（已核实 `internal/migrations/sql/`）：`categories(id,parent_id,name,sort,status,uk(parent_id,name))`；`products(id,name,brand,category_id,price INT UNSIGNED 分,main_image,detail,status 0=draft/1=on_shelf/2=off_shelf,fk→categories)`；`product_images(id,product_id,url,sort)`；`skus(id,product_id,name,price 分,status 1=enabled/0=disabled,uk(product_id,name),fk→products)`；`inventories(id,sku_id,quantity,uk(sku_id),fk→skus)`。目标模型只有 SKU 级库存，无商品级库存字段。
- 历史表结构（已核实，仅本次相关 5 表）：`store_category(pid,cate_name,sort,pic,is_show,is_del)`；`store_product(cate_id 字符串,price decimal(8,2),stock 商品级,is_show,is_del,spec_type 0单/1多,image,slider_image,store_name,store_info,description)`；`store_product_attr(product_id,attr_name,attr_values 逗号分隔)`；`store_product_attr_value(product_id,sku,stock,price decimal(8,2),image,unique)`；`store_product_attr_result(product_id,result JSON 含 sku/price/stock 明细)`。注意 `attr_result` JSON 与 `attr_value` 表对同一商品存在重叠/冗余，需 Analyst 确定权威来源与冲突优先级。
- API 已核实（`internal/cmd/routes_test.go`）：`GET /categories`、`GET /categories/:id`、`GET /products`、`GET /products/:id`、`GET /admin/products`、`GET /admin/products/:id`、`GET /admin/inventories/:sku_id`、`GET /admin/inventories/:sku_id/logs`。SKU 无独立公开查询路由，随商品详情返回。
- 数据规模（Owner 已完成只读审计，本任务不重复审计）：83 张表、商品 226、SKU 111、分类 57。
- Git 现状：当前分支 `feat/db-migration`，HEAD `554cd21a0d87386f8e9348e5780b98a3330f4470`；`shopping.sql` 为未跟踪新文件（`git status --short` → `?? shopping.sql`），尚未被 Git 跟踪。
- 错误码域：迁移工具为独立 CLI、不新增 HTTP 接口，预期无需新错误码域（`.agent/registry/error-codes.md`）；若工具确需稳定错误码语义由 Analyst 判断。

Assumption（合理但未经 Owner 确认，交 Analyst 核实）：

- 迁移工具采用独立 Go CLI（复用 `internal/cmd` 子命令或独立二进制），不引入新的重依赖。
- 「隔离测试环境」基于现有 `docker-compose.yml` 扩展独立 staging 库与目标测试库，具体编排由 Analyst/Coder 落地。
- 历史图片为 `/file/pic/...` 路径字符串，本任务按字符串落库到 `products.main_image`/`product_images.url`，不下载/重建图片文件；真实图片文件可用性属环境问题，不纳入本次数据映射正确性范畴。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 旧 ID→新 ID 持久映射的落点：若需在目标库新增映射表，则 Design Impact 升级为 NEW 且需按 Registry 机制预留 migration version（当前按 NONE 声明）；若映射仅存于 staging 库或工具侧文件，则无 Schema 变更。
- `store_product_attr_result` JSON 与 `store_product_attr_value` 表重叠/冲突时的权威来源。
- 商品级 vs SKU 级库存去重的精确规则（单规格 vs 多规格商品的判定与取值）。
- 「隔离测试环境」的具体形态（独立库名 vs 独立 compose service）。

## Verification

环境：需 MySQL 8.0（独立 staging 库 + 独立 SurgeCart 目标测试库）；集成验收需启动真实服务并走 `RegisterFrontendRoutes`/`RegisterAdminRoutes`。原始 SQL 只允许进入 staging 库。

- AC-001 → 文件系统 + Git + `sha256sum` 前后比对；`git check-ignore -v storage/private/legacy/shopping.sql`；`git log --all --oneline -- shopping.sql storage/private/legacy/shopping.sql` 无跟踪记录；扫描日志/报告/测试样本确认无敏感值。
- AC-002 → 代码/配置审查 + 实际执行：工具仅连接 staging 库；目标库外键校验开启、无 `FOREIGN_KEY_CHECKS=0`。
- AC-003~AC-008 → 真实导入后 SQL 断言（分类树/商品/SKU/库存行数与字段值）+ API 断言。
- AC-009 → 导入后无唯一/FK 冲突；比对迁移前存量数据未被覆盖。
- AC-010 → Dry Run 执行后目标库零写入（行数不变）。
- AC-011 → 注入导入失败，断言目标库无部分数据残留。
- AC-012 → 报告统计与库内实际行数一致。
- AC-013 → 真实服务 + API 断言迁移结果。
- AC-014 → `go test ./...`（单元测试）+ 可重复集成脚本；`go build ./...`、`go vet ./...`。
- AC-015 → `.dockerignore`/CI 配置审查；`git ls-files` 无原始 SQL。

## Complexity

COMPLEX

原因：涉及真实历史数据的适配映射（软删除/上下架/金额精度/规格→SKU/库存去重/异常数据）、事务与幂等一致性（可重复执行、不重复库存、失败回滚）、旧 ID→新 ID 持久映射的可能 Schema 变更，以及敏感数据安全隔离边界。多个现实方案（规格映射权威来源、库存去重规则、ID 映射落点、默认 SKU 判定）会产生不同数据结果与运维形态，需 Analyst 调查真实数据后固化 Contract 交 Owner 确认。

## Analyst Questions

1. 旧 ID→新 ID 映射持久化方案（目标库新表 vs staging 库 vs 工具侧文件）；若需目标库新表，涉及 Design Impact 升级与 migration version 预留。
2. 规格→SKU 的权威数据源：`store_product_attr_result` JSON vs `store_product_attr` vs `store_product_attr_value`，重叠/冲突时的优先级与校验。
3. 库存去重规则：单规格 vs 多规格商品判定，商品级 `stock` 与 SKU 级 `stock` 的取值与去重，避免重复计算。
4. 软删除（`is_del`）与上下架（`is_show`）到 `products.status`（0/1/2）与 `skus.status`（0/1）的映射规则；软删除数据是否导入。
5. 默认 SKU 判定：何种真实数据条件下可创建默认 SKU，何种条件下跳过并记录原因。
6. 金额 `decimal(8,2)` 元→整数分的精确转换与边界（NULL、0、异常值）。
7. 隔离测试环境形态与 staging/目标库编排。
8. 异常数据（缺 SKU、非法分类、重复规格、`cate_id` 引用不存在分类、缺图片）的跳过/失败语义与原因记录格式。
9. 错误码域：迁移工具是否需稳定错误码（倾向不需要，因非 HTTP）。
10. 图片处理：历史 `/file/pic/...` 路径是否直接落库字符串（不下载），真实图片可用性是否属验收范围。

## Review Baseline

- Base commit：`554cd21a0d87386f8e9348e5780b98a3330f4470`（分支 `feat/db-migration`）。
- 任务开始时已有修改：`shopping.sql` 为未跟踪新文件（`git status --short` → `?? shopping.sql`），这正是本任务归档与迁移的输入数据本身，无其他 Agent 的未提交修改。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/legacy-data-migration/`、`storage/private/legacy/shopping.sql`（归档）、`.gitignore`/`.dockerignore` 增补、`docs/legacy-data.md`、数据迁移 CLI 代码与测试；与既有 `db-migration` 任务的 Schema Migration 机制（`internal/migrations/`）无文件重叠，不改历史 migration 文件。

## Initial Route

交 Analyst（COMPLEX）
