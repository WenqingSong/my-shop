# Task: 商品评价（Product Review）V1

## Goal

交付「商品评价」核心闭环：买家对「自己已完成（或已收货，最终以 Contract 为准）订单中的商品」提交星级 + 文字评价，评价由服务端绑定订单项与商品、每个已购项最多评价一次；商品详情页公开展示已发布评价及汇总（平均分 + 条数），汇总随评价增删正确变化；买家只能操作自己的评价，管理员可审核/下架违规评价，所有越权与重复评价均不产生数据库写入。

## Scope

- 评价数据模型（`reviews` 表），经既有 golang-migrate 机制新增迁移文件（schema 由 Analyst 固化），并同步更新 `internal/migrations/migrations_test.go` 的 `latestMigrationVersion` 与 `businessTables`。
- 提交评价（登录用户，作用于 `Principal.UserID`）：服务端校验购买资格（订单项归属 + 订单状态），`user_id`/`product_id`/`sku_id`/`order_item_id` 由服务端推导，不信任客户端提交的归属信息。
- 重复评价防护：同一买家对同一订单项最多一条评价，DB 唯一约束兜底并发。
- 公开查询：商品维度评价列表（分页）+ 汇总（平均分 + 条数）。
- 归属与隔离：买家仅能查看/修改/删除自己的评价；越权与不存在统一稳定拒绝。
- 管理员审核/下架：违规评价下架后不再出现在公开列表。
- 错误码：新增评价错误码域（语义：商品评价），具体编号由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。
- 权限 code：若含管理员审核，新增 `review:*` 权限并在 `internal/boot/seed.go` 登记（权限 code 属 B 类 namespace，不纳入 Registry）。
- 长期设计：新增 `docs/design/review.md`（Design Impact = NEW），沉淀评价数据模型、购买资格校验、汇总一致性方案、权限边界、错误码域与权限 code。
- 必要测试：提交（正常/资格拒绝/重复）、归属隔离、公开列表与汇总、管理员审核、非法输入、并发重复评价只落一条。

## Out of Scope

- 评价图片/视频等富媒体上传与对象存储（本次仅文本评价；如需图片仅预留 URL 字段，上传能力另议）。
- 评价点赞、回复、追评（追加评价）、举报流程。
- 评价排序/推荐/置顶算法（本次仅满足基础列表与最新/默认排序）。
- 优惠券、积分、会员等激励评价机制。
- 修改既有订单/商品/SKU/库存/身份模块行为（除评价资格校验所需的最小只读引用）。
- 前端页面改造（`frotend_web`/`frotend_manage` 为未接入本后端的模板工程）。

## Design Impact

Design Impact: NEW
Design Artifact: docs/design/review.md

## Acceptance Criteria

- [ ] AC-001（提交评价·购买资格）：登录买家可对自己已完成（或已收货，状态以 Contract 为准）订单中的商品提交评价，成功后评价可见且归属该买家。
- [ ] AC-002（资格拒绝）：未登录、未购买该商品、或订单未达可评价状态的提交被拒绝（稳定错误），且不产生任何评价记录。
- [ ] AC-003（服务端归属绑定）：评价的 `user_id`/`product_id`/`sku_id`/`order_item_id` 由服务端从 `Principal.UserID` 与订单项推导，不信任客户端提交的归属信息。
- [ ] AC-004（每个已购项最多一次）：同一买家对同一订单项重复提交评价被拒绝，不产生第二条评价（DB 唯一约束兜底并发）。
- [ ] AC-005（公开列表与汇总）：商品详情页公开返回该商品已发布评价列表（分页）与汇总（平均分 + 条数），汇总随新增/删除正确更新。
- [ ] AC-006（归属隔离）：买家只能查看/修改/删除自己的评价；操作他人或不存在的评价返回稳定错误（404，防枚举）且无写入。
- [ ] AC-007（管理员审核/下架）：管理员（含超管，持 `review:*` 权限）可下架/删除违规评价，被下架评价不再出现在公开列表；无权限访问返回 403 且无数据库副作用。
- [ ] AC-008（非法输入拒绝）：星级越界、内容为空或超长等非法评价提交被拒绝且无写入。
- [ ] AC-009（数据模型与迁移）：评价表经迁移正确建表，`migrations_test.go` 的版本与表清单同步更新，迁移可重复/幂等。
- [ ] AC-010（长期设计）：新增 `docs/design/review.md`，沉淀评价数据模型、购买资格校验、汇总一致性方案、权限边界、错误码域与权限 code。

## Relevant Context

已核实事实：

- 技术栈 GoFrame v2（Go 1.23+），模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无 `dao`/`model` 层。评价模块应沿用此结构。
- 身份域：前台用户经 `middleware.Auth` 注入 `Principal{UserID, Sid}`；后台管理员经 `AdminAuth` + `RequirePermission(code)`（`IsSuper` 放行、fail-closed）。前台受保护路由挂 `routes_frontend.go` 的 `Auth` 分组，后台写路由挂 `routes_admin.go` 的 `require(code)` 分组。
- 商品/SKU：`products`（`status` 0=draft/1=on_shelf/2=off_shelf）、`skus`（`product_id`、`price`、`status`）；前台公开详情 `GET /products/:id`。
- 订单：`orders`（`user_id` 取自 `Principal.UserID`，`status` 状态机 10 待支付/20 已支付/30 已发货/40 已收货/50 已完成/60 已取消/70 已退款）+ `order_items`（`order_id`、`sku_id`、`product_id`、`product_name`、`product_main_image`、`price`、`quantity`，均为下单时快照）。`order_items` 是评价资格与「每个已购项最多一次」的自然锚点。
- 错误码集中在 `internal/codes/codes.go`：通用 1000、IAM 2000、分类 3000、商品 4000、SKU 5000、库存 6000、地址 7000、购物车 8000、订单 9000（已用至域序 9）。评价需新增一个错误码域，具体域号由 Analyst 读 `.agent/registry/error-codes.md` 派生。
- 迁移机制 golang-migrate v4：`internal/migrations/sql/{14位时间戳}_{title}.up.sql`，当前最新为 `refresh_tokens`（IAM V4）。评价表需新增 1 个 migration，具体 version 由 Analyst 读 `.agent/registry/migrations.md` 派生；需同步更新 `migrations_test.go`。
- RBAC seed：`internal/boot/seed.go` 的 `seedPermissionList` 现含 25 个权限（`category/product/sku/inventory/admin/role/permission` 各动作）；订单域写权限 `order:ship`/`order:refund` 已登记。若评价含管理员审核，需新增并登记 `review:*`。
- 事实来源为单一 MySQL；Redis 仅会话；无 MQ（订单仅有一个超时取消 scanner goroutine）。评价为同步写、无异步。
- 后端当前无任何评价/评论表、无评价模块；`frotend_web`/`frotend_manage` 为模板工程，未接入本后端。

Assumption（合理但未经 Owner 确认，交 Analyst 核实并向 Owner 确认）：

- 评价与订单关联：只有「已购买（对应 `order_items`）且订单达到可评价状态」的买家才能评价，非购买者不能评价。
- 评价粒度：按 `order_items`（订单项/SKU 粒度），每个订单项最多一条评价。
- 星级为 1–5 星整数，文字内容必填（有长度上限），图片为可选（本次不实现上传）。

OPEN QUESTION（不阻塞任务创建，交 Analyst 分析、Owner 确认）：

- 可评价的订单状态：`已收货(40)` 还是 `已完成(50)`，还是二者皆可；是否限制评价有效期（如收货后 N 天）。
- 评价是否可修改/删除（次数与时效）；删除后汇总是否回退。
- 汇总一致性：平均分与条数采用「实时聚合」还是「冗余字段 + 事务更新」，前者读放大、后者需保证与评价增删一致。
- 审核机制：评价「先发后审」还是「先审后发」；下架/删除的权限粒度与是否保留记录。
- 图片等富媒体评价是否需要进入本次 Scope（需对象存储能力，当前无）。

## Verification

环境：需可连接的 MySQL 8.0 与 Redis 7（`docker compose up -d`）；集成测试必须走真实 `RegisterFrontendRoutes` + `middleware.Auth` 与 `RegisterAdminRoutes` + `AdminAuth/RequirePermission`，用真实用户/管理员账号断言。

- AC-001 → 需 MySQL + Redis：登录买家对已完成订单项提交评价，断言评价落库且归属该买家。
- AC-002 → 需 MySQL + Redis：未购买该商品的用户、或订单未达可评价状态时提交，断言稳定拒绝且无评价记录。
- AC-003 → 需 MySQL：提交含伪造 `product_id`/`user_id` 的评价，断言服务端以 `Principal` 与订单项为准，伪造值被忽略/拒绝。
- AC-004 → 需 MySQL（+ `-race`）：同一订单项并发/重复提交，断言至多一条评价（唯一约束兜底）。
- AC-005 → 需 MySQL：提交/删除评价后查商品详情，断言评价列表与平均分、条数正确变化。
- AC-006 → 需 MySQL + Redis：用户 B 修改/删除用户 A 的评价，断言稳定拒绝（404 防枚举）且无写入。
- AC-007 → 需 MySQL + Redis：无权限管理员（及普通用户 token）下架评价，断言 403 且无写入；超管/有权限管理员成功后评价从公开列表消失。
- AC-008 → 需 MySQL：星级越界/内容为空或超长，断言被拒且无写入。
- AC-009 → 需 MySQL：执行迁移断言评价表结构正确、`migrations_test.go` 通过、迁移可重复执行。
- AC-010 → 文档审查：`docs/design/review.md` 与 APPROVED Contract、最终实现一致。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；涉及 MySQL/Redis 的集成验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及新数据模型（`reviews` 与订单项/商品/用户的关系）、新公开协议（评价 API + 错误码域）、购买资格校验（评价 ↔ 订单项的跨模块校验）、每个已购项最多一条的并发去重、汇总（平均分/条数）的一致性方案（实时聚合 vs 冗余字段），以及管理员审核的权限边界；多个现实方案会产生不同的数据模型、接口形态与一致性/运维结果，需 Analyst 固化 Contract 后由 Owner 确认。

## Analyst Questions

1. 评价数据模型与引用语义：`reviews` schema；`order_item_id`/`sku_id`/`product_id`/`user_id` 的引用方式（快照软引用 vs 强 FK）；SKU/商品被删后评价是否仍可读；星级与内容字段的类型与约束。
2. 购买资格校验路径：如何用 `orders` + `order_items` 判定「该用户已购买该商品且订单达可评价状态」；可评价状态与有效期。
3. 每个已购项最多一条的并发保证：唯一约束的列组合与兜底；重复提交的返回语义。
4. 汇总一致性：平均分 + 条数的计算与存储方案（实时聚合 vs 冗余字段 + 事务更新），删除/下架后的回退语义。
5. 审核与权限：审核/下架的触发者与权限 code 命名、seed 登记；先发后审 vs 先审后发；下架后公开列表的可见性。
6. 错误语义：前台评价路径与后台审核路径；越权/不存在/重复评价/非法输入的稳定错误码与 HTTP 状态。
7. 全局资源：新增评价错误码域（语义：商品评价）、新增 1 个 migration（`reviews` 表）、新增 `review:*` 权限 code；具体域号/version 由 Analyst 读 `.agent/registry/*` 派生并写入 Contract。

## Review Baseline

- Base commit：`e0a48e9f023809d279e61fe7a0052a7cff461095`（分支 `feat/comment`）。
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）。
- 重叠修改的区分方式：本任务新增产物为 `.agent/tasks/product-review-v1/`、`api/review*/`（或等价评价 API 包）、`internal/controller/review*/`、`internal/logic/review*/`、`internal/service` 的 `IReview` 接口、`internal/codes` 评价域扩展、migration 文件（`reviews`）、`internal/boot/seed.go` 的 `review:*` 权限 seed、`internal/cmd` 路由扩展及对应测试；`docs/design/review.md` 由 Analyst 写入。当前工作区干净，无既有未提交修改。

## Initial Route

READY_FOR_ANALYST
