# Task: 商品分类 Categories V1（树形分类 CRUD）

## Goal

交付「商品分类」后端初步能力：用 `parent_id` 邻接表实现最多 3 级的树形分类；提供公开的树形/详情查询，以及需登录的创建、更新、删除；分类名在同级内唯一；删除仅物理删除且禁止删除有子分类的分类，并支持用 `status` 禁用作为软下线手段。本阶段不校验商品关联、不区分角色。

## Scope

允许完成的内容：

- `categories` 表：`id`、`parent_id`（0 表示顶级）、`name`、`sort`、`status`（1 启用/0 禁用）、`created_at`、`updated_at`；`parent_id` 默认 0，复合唯一键 `(parent_id, name)` 保证同级重名；沿用 `boot.Bootstrap` 幂等 `CREATE TABLE IF NOT EXISTS` 方式，在每日重置环境下可重复建表。
- 五个接口的 API 定义与完整实现：
  - `GET /categories`（公开）：树形返回，默认只返回启用项。
  - `GET /categories/:id`（公开）：单个分类详情。
  - `POST /categories`（需登录）：创建分类。
  - `PUT /categories/:id`（需登录）：更新分类（改名/改父/改排序/改状态）。
  - `DELETE /categories/:id`（需登录）：物理删除分类。
- 树构建：一次查全表，内存按 `parent_id` 组装嵌套树；查询固定 `ORDER BY parent_id, sort, id`。
- 循环引用防护：更新 `parent_id` 时禁止指向自身或自身后代。
- 层级限制：分类最多 3 级；创建或改父后会导致超过 3 级时拒绝。
- 删除语义：存在子分类时禁止删除；`status=0` 作为软下线（不物理删除、树接口不再展示）。
- 名称唯一性：仅依赖 DB 复合唯一键 `(parent_id, name)` 判定（MySQL 1062 → 3002），不在应用层先查再插。
- 错误码：新增分类域 3000-3999，沿用 `internal/codes` 集中定义 + HTTP 状态映射模式。
- 必要的测试：树形/详情/创建/更新/删除的正常路径、关键错误路径与边界（重名、循环引用、有子禁删、超层级、禁用软下线、未登录 401）；涉及 MySQL 的部分需可运行集成验证。

## Out of Scope

明确本次不处理：

- 权限/RBAC：写接口仅要求登录，不区分管理员/普通用户（遗留能力，见 AGENTS.md 第 11 条）。
- 商品关联：本次无商品表，删除分类不校验商品关联（遗留能力，见 AGENTS.md 第 11 条）。
- 分类的批量操作、排序拖拽、导入导出、前端展示。
- 分类与商品的绑定/解绑、商品按分类统计。
- 软删除（逻辑删除）机制：删除为物理删除，仅保留 `status` 禁用作为软下线。
- 缓存、MQ、跨系统一致性。
- 现有 `/health`、IAM 模块的改动。

## Acceptance Criteria

- [ ] AC-001 服务启动后（MySQL 就绪），`categories` 表存在，字段与约束符合定义，且含复合唯一键 `(parent_id, name)`；DDL 可重复执行（每日重置环境重启后表仍能重建）。
- [ ] AC-002 无 token 访问 `GET /categories` 成功返回树形分类：仅含 `status=1` 的启用项；同一级按 `sort` 升序、`sort` 相同按 `id` 升序；父子关系按 `parent_id` 正确嵌套（顶级 `parent_id=0`）。
- [ ] AC-003 无 token 访问 `GET /categories/:id` 成功返回该分类完整字段（id、parent_id、name、sort、status、created_at、updated_at）；id 不存在时返回「分类不存在」且 HTTP 404、`data:null`。
- [ ] AC-004 无 token 调用 `POST /categories` 返回未授权（HTTP 401）；携带有效 token 且输入合法时创建成功并返回新分类（含新 id），且持久化到 `categories` 表。
- [ ] AC-005 创建或改名为「同级已存在」的同名分类时返回「同级重名」错误（HTTP 409）且不产生写入/覆盖；并发下同名仅一个成功（依赖 DB 复合唯一键，非应用层先查）。
- [ ] AC-006 无 token 调用 `PUT /categories/:id` 返回未授权（HTTP 401）；携带有效 token 时能分别成功改名、改父、改排序、改状态；id 不存在时返回「分类不存在」（HTTP 404）。
- [ ] AC-007 更新 `parent_id` 指向自身或自身后代时返回「父分类非法」错误（HTTP 400），且数据不被修改、不产生循环引用。
- [ ] AC-008 创建或改父后会导致分类层级超过 3 级时返回「父分类非法」（HTTP 400），且不产生第 4 级分类。
- [ ] AC-009 无 token 调用 `DELETE /categories/:id` 返回未授权（HTTP 401）；目标分类存在子分类时返回「有子分类不能删除」（HTTP 409）且分类及其子分类数据不变；无子分类时物理删除成功，且删除后详情返回 404。
- [ ] AC-010 将分类 `status` 置为 0（禁用）后：分类记录仍存在于数据库、不物理删除；`GET /categories` 树不再展示该分类（及其子树）；`GET /categories/:id` 详情仍可访问。

## Relevant Context

已核实的事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`。
- 现有分层：`api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + Register 模式）→ `internal/logic`（`init()` 注册）；数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。
- 路由在 `internal/cmd/cmd.go` 的 `s.Group("/")` 下，已挂 `middleware.Response`；IAM 已演示公开路由与 `middleware.Auth` 保护路由的写法。
- 统一响应 `{code,message,data}`，由 `internal/middleware/response.go` 依据业务错误码映射 HTTP 状态；客户端靠 code 判型。
- 错误码集中定义于 `internal/codes`，现有通用域 1000-1005、IAM 域 2000-2999，3000+ 已预留供后续模块按域扩展（见 iam-v1 contract.md Error Semantics）。
- 建表沿用 `boot.Bootstrap` 中的幂等 `CREATE TABLE IF NOT EXISTS`（现有 `users` 表示例），在依赖校验通过后执行。
- 认证中间件 `middleware.Auth` 注入 `Principal{UserID}`；读接口公开、写接口挂该中间件即可实现「写需登录」。
- 环境每日重置（CNB DinD）、数据不持久，表必须由仓库内定义可重复创建。
- `internal/logic/iam/iam.go` 已有 `isDuplicateKeyError`（MySQL 1062 → 业务错误码）先例，分类重名可复用同模式。
- Git 基线：HEAD `8e4ff4d`，分支 `feat/category`；工作区已有未提交修改 `agents.md`（新增第 11 条「遗留设计与未完成能力」，与本次 Scope 直接相关，须保留）。

Assumption：

- 树形返回的嵌套字段名用 `children`（子节点数组）；单分类详情返回上述完整字段；具体 JSON 结构以 Coder 实现为准，Cleaner 按 AC 的可观察行为审查。
- `GET /categories` 当前不提供查询参数，始终只返回启用项；「默认只返回启用项」中「默认」暗示的「包含禁用项」能力不在本次实现。
- 「只返回启用项」的语义：禁用父分类后，其子树（即使含启用子分类）也不再出现在树结果中。
- 分类名 `name` 非空，长度上限取合理值（如 64 字符，trim 后校验）；`sort` 默认 0；`status` 默认 1（启用）。
- 错误码映射（沿用 IAM 模式，Owner 未逐条指定 HTTP 状态）：
  - 3001 分类不存在 → 404
  - 3002 同级重名 → 409
  - 3003 有子分类不能删除 → 409
  - 3004 父分类非法（父不存在/指向自身或后代/超 3 级）→ 400
- 「最多 3 级」需在创建/改父时强制校验：新分类的层级 = 目标父分类层级 + 1，超过 3 即拒绝；顶级分类层级为 1。
- 循环引用防护与「有子禁删」采用应用层 check-then-act（单表、低并发、每日重置环境，无强并发一致性要求）；唯一性由 DB 复合唯一键兜底。

OPEN QUESTION（不阻塞创建，Coder 实现时可自决或 Owner 后续确认）：

- 树形返回的嵌套字段名（`children`）与详情字段集合是否需要与前端/后续商品模块对齐；本次按 Assumption 实现，若 Owner 有既定约定请补充。

## Verification

- AC-001 → 需可连接 MySQL（`docker compose up -d` 后）；启动服务后 `SHOW CREATE TABLE categories`（或 `SHOW INDEX`）确认字段、`UNIQUE KEY (parent_id, name)`；重启服务再次执行 DDL 不报错。
- AC-002 → 无 token 请求 `GET /categories`，断言 HTTP 200、code=0，核对树结构（层级、同级排序 `sort`/`id` 升序、仅启用项）。需 MySQL + 预置多级分类数据。
- AC-003 → 请求已存在与不存在的 id，断言 200 与 404/3001、`data:null`。
- AC-004 → 无 token 创建断言 401/1002；带 token 创建断言 200/0 且查库确认新行存在。
- AC-005 → 同一 `parent_id` 下重复创建/改名同名断言 409/3002 且查库无重复行；并发同名创建集成测试（复用 IAM 并发注册模式）。
- AC-006 → 无 token 更新断言 401/1002；带 token 分别覆盖改名/改父/改排序/改状态，查库确认字段变化；更新不存在 id 断言 404/3001。
- AC-007 → 构造 A→B→A 或 A 的父指向 A 自身/A 的子，断言 400/3004 且查库父子关系不变。
- AC-008 → 构造三层分类后在其下创建子分类（或把某分类改为四级父），断言 400/3004 且不产生第 4 级。
- AC-009 → 无 token 删除断言 401/1002；删除有子分类断言 409/3003 且父子数据不变；删除叶子分类断言成功且详情 404。
- AC-010 → 置 `status=0` 后：查库记录仍在；`GET /categories` 树不含该分类及其子树；`GET /categories/:id` 仍 200。
- 通用命令：`go build ./...`、`go vet ./...`、`go test ./...`；涉及 MySQL 的集成验证需说明容器就绪。

## Complexity

NORMAL

原因：Owner 已明确数据模型（邻接表 parent_id、字段、复合唯一键）、接口清单、鉴权方式（写需登录）、删除语义（有子禁删 + status 软下线）、循环引用防护、层级上限、排序与错误码域。核心业务规则无重大未决设计选择；唯一性由 DB 约束兜底（与 IAM 的 uk_username 同构），单表、无跨系统一致性、无 MQ、无新安全边界（RBAC 已明确 Out of Scope）。树形组装与校验均为确定性逻辑，无需 Analyst 权衡多个会改变业务结果的方案。

## Review Baseline

- Base commit：`8e4ff4df6536383a4720630c7b62e03bd0e5689f`（分支 `feat/category`）
- 任务开始时已有修改：`agents.md`（未提交，新增第 11 条「遗留设计与未完成能力」，属规范文档，与本任务 Scope 直接相关，需保留、不覆盖、不归入本任务产出）
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/categories-v1/`、`api/categories/`、`internal/controller/categories/`、`internal/logic/categories/`、`internal/service` 分类接口、`internal/codes` 分类域扩展、`internal/boot/boot.go` 建表扩展、`internal/cmd/cmd.go` 路由扩展及对应测试；与 `agents.md` 的既有修改无文件重叠，Cleaner 以 `git diff` + 新增未跟踪文件审查本任务变更，不把 `agents.md` 的修改算作本任务成果。

## Initial Route

READY_FOR_CODER
