# Technical Contract

## Decision Status
APPROVED

## Problem

在现有 IAM 体系上，让管理员能够安全禁用/启用普通用户账号并查询其状态；被禁用用户无法登录、无法刷新 Token，且已签发的未过期 JWT 也不能继续访问受保护接口；状态变更持久化并具备可追溯审计。复用现有 JWT、Redis 会话、Admin RBAC 与 GoFrame 分层，不重造 IAM。

核心难点：禁用即时失效（不依赖 JWT 过期、不只在登录时检查）、重新启用后旧凭证不得复活，以及跨系统一致性（MySQL `users.status`/`auth_epoch` 与 Redis 会话撤销/refresh family 撤销之间无跨系统事务）。

## Verified Current Behavior

- VERIFIED：`users` 表（`internal/migrations/sql/20261001000001_baseline.up.sql`）仅有 `id/username/password_hash/created_at/updated_at`，无 `status` 字段；`admins` 表有 `status TINYINT NOT NULL DEFAULT 1`（`1=启用`/`0=禁用`），是状态枚举先例。
- VERIFIED：`Auth` 中间件（`internal/middleware/auth.go`）验签+exp 后调用 `auth.ValidateSession`（`iam:session:{sid}` 存在、未撤销、`user_id==sub`），**不校验 `users.status`**；`AdminAuth` 在会话校验后每请求查 `admins.status`（禁用/不存在→401），是「每请求查库」先例。
- VERIFIED：前台 `Login`（`internal/logic/iam/iam.go`）与 `Refresh`（`internal/logic/iam/refresh.go`）当前均不校验用户 status；`Refresh` 在 `findRefreshByHash` 后已持有 `row.UserID` 与 `row.Sid`。
- VERIFIED：管理员登录先例（`internal/logic/admin/admin.go`）：禁用管理员（凭据正确）返回 `1002 UNAUTHORIZED`（401），在 bcrypt 之后判定。
- VERIFIED：会话/refresh 撤销能力已存在：`auth.RevokeAllSessions(userID)`（Lua）、`iam:user:{id}:sessions` ZSET 索引；`revokeAllFamiliesByUser(userID)`。既有 `revokeAllByUser` 先例：family 撤销（MySQL）权威、session 撤销（Redis）best-effort。
- VERIFIED：`Refresh` 轮换用事务内条件 `UPDATE refresh_tokens ... WHERE token_hash=? AND revoked_at IS NULL AND expires_at > NOW()`（`rotateRefresh`），`RowsAffected==0` 时经 `errRefreshNotRotated` 重读判定，是「刷新与撤销并发」的既有串行化点。
- VERIFIED：`refresh_tokens` 表（`20261001000008`）无 `auth_epoch` 字段；会话 Hash（`iam:session:{sid}`）无 `auth_epoch` 字段。
- VERIFIED：后台路由先例（`internal/cmd/routes_admin.go`）：`require("admin:disable").PUT("/admin/admins/:id/status")`；权限 code 为「资源:动作」，RBAC seed 在 `internal/boot/seed.go`。
- VERIFIED：审计先例（`20261001000018_flash_sale_request_audits.up.sql`）：append-only，`operator_admin_id`/`operator_username`（快照）/`action`/`before_status`/`after_status`/`reason`/`created_at`，软引用无 FK。
- VERIFIED：错误码 IAM 域 2000-2999 已 ACTIVE，已用 `2001-2014`；迁移 Registry 与磁盘最新 version 均为 `20261001000018`。
- UNKNOWN：无阻塞性未知项。

## Recommendation

RECOMMENDATION：在「每请求校验 `users.status` + 禁用时撤销会话/refresh family」之外，引入**每用户持久化认证版本 `users.auth_epoch`**，作为「重新启用不复活旧凭证」与「禁用即时失效（fail-closed）」的最终保证。

关键取舍：

- 每请求校验 `users.status` 保证「禁用后被拒绝」；`auth_epoch` 保证「重新启用后旧凭证仍被拒绝」（即便 Redis 撤销失败留下未撤销会话，其绑定版本 < 当前版本，仍 401）。代价是每受保护前台请求新增一次 DB 读（与 `AdminAuth` 一致），以及一处新字段。
- 版本仅在**实际禁用迁移**时单调递增，启用不递减、不重签，因此旧凭证一旦落后即永久失效，必须重新登录（满足 AC-007）。
- DB（`users.status` + `auth_epoch` 更新 + 审计插入 + refresh family 撤销）在单个 MySQL 事务内原子完成；Redis 会话撤销在事务提交后 best-effort（失败仅日志，由 status/epoch 校验兜底）。

## Selected Design

采用「每请求校验 `users.status` + `users.auth_epoch` 版本匹配（fail-closed）＋ 禁用时在同一 MySQL 事务内递增 `auth_epoch`、更新状态、撤销 refresh family、写审计，事务后 best-effort 撤销 Redis 会话」的组合方案。

- 数据模型：`users.status`（启用/禁用）+ `users.auth_epoch`（仅实际禁用迁移 +1）+ `refresh_tokens.auth_epoch`（随 family 血缘继承）+ `user_status_audits`（append-only，仅实际迁移落审计）。
- 权限拆分：`user:read`（查询）、`user:status`（禁用/启用）两个 code。
- 状态更新：事务内 `SELECT ... FOR UPDATE` 锁定目标行，区分「不存在→404 / 已一致→幂等 / 实际迁移→更新+审计(+禁用撤销)」三态。
- 并发与失败语义详见下文对应章节。

## Interfaces and Data

### 数据模型（migration `20261001000019`，单文件）

1. `users` 新增：
   - `status TINYINT NOT NULL DEFAULT 1`（`1=启用`/`0=禁用`，对齐 `admins.status`）。
   - `auth_epoch BIGINT UNSIGNED NOT NULL DEFAULT 0`（认证版本，仅在「启用→禁用」实际迁移时 +1）。
   - 不新增「状态变更时间」字段（变更时间/原因由审计表承载）。

2. `refresh_tokens` 新增：`auth_epoch BIGINT UNSIGNED NOT NULL DEFAULT 0`（签发时绑定的认证版本，随 family 血缘继承）。

3. 新增审计表 `user_status_audits`（append-only，无 UPDATE/DELETE 接口）：

   | 字段 | 类型 | 说明 |
   | --- | --- | --- |
   | `id` | BIGINT UNSIGNED | 主键自增 |
   | `target_user_id` | BIGINT UNSIGNED | 目标普通用户 id（软引用无 FK） |
   | `operator_admin_id` | BIGINT UNSIGNED | 操作管理员 id（软引用无 FK） |
   | `operator_username` | VARCHAR(64) | 操作时管理员用户名快照 |
   | `action` | VARCHAR(16) | `enable` / `disable` |
   | `before_status` | TINYINT | 原始状态 |
   | `after_status` | TINYINT | 目标状态 |
   | `reason` | VARCHAR(255) | 变更原因 |
   | `result` | TINYINT | `1=成功`（仅记录成功变更） |
   | `created_at` | DATETIME | 默认 CURRENT_TIMESTAMP |

   索引：`idx_target_user(target_user_id)`、`idx_operator(operator_admin_id)`。

### API 契约（后台域，`AdminAuth` + `RequirePermission`）

- `GET /admin/users/:id/status`：查询指定用户状态，响应 `{id, status}`，挂 `user:read`。
- `PUT /admin/users/:id/status`：禁用/启用，请求体 `{status: 0|1, reason: string}`（`reason` 必填，trim 后非空、≤255，非法→`1001`），响应无业务字段，挂 `user:status`。
- 目标用户 id 一律来自 URL 路径（`in:"path"`），服务端不信任请求体身份。

### 权限与 seed

- 新增两个权限 code 并纳入 `seedPermissionList`：`user:read`（查询状态）、`user:status`（禁用/启用）。只读管理员仅授予 `user:read`，不自动获得改变账号访问资格的能力。

### 错误码（IAM 域内，无需新增域 Reservation）

- 新增 `2015 USER_NOT_FOUND`（404）：状态管理接口目标用户不存在。
- 禁用登录/刷新/鉴权与版本不匹配复用 `1002 UNAUTHORIZED`（401）；非法 `status`/`reason` 复用 `1001 INVALID_ARGUMENT`（400）。

### 认证链改造落点

- `Login`：`findUserByUsername` 增读 `status`/`auth_epoch`；bcrypt 通过后校验 `status==1`（否则 `1002`），并以**本次读到的 `auth_epoch`** 写入会话 Hash 与 refresh 根（绑定当前版本，不重读）。
- `Refresh`：`findRefreshByHash` 行含 `auth_epoch`（记为 E）；`respondRefreshRow` 判定可轮换后，读取 `users`（`status`、`auth_epoch`），校验 `status==1` 且 `E == users.auth_epoch`（不符→`1002`，无任何副作用）；`UpsertSessionAlive` 重建会话时以 `meta.AuthEpoch = E`（继承）；`rotateRefresh` 插入新后代 `auth_epoch = E`（继承）。**不得**直接读取最新 `users.auth_epoch` 来签发新凭证。
- `Auth` 中间件：`ValidateSession` 通过后新增每请求查 `users`（`status`、`auth_epoch`），校验 `status==1` 且 `session.auth_epoch == users.auth_epoch`；用户不存在/禁用/版本不匹配/Redis 失败均 fail-closed 401。会话 Hash 增加 `auth_epoch` 字段（`CreateSession`/重建时写入），`ValidateSession` 同一次读返回该字段（避免额外 Redis 读）。

### 服务落点

- 用户状态管理业务在 `internal/logic/iam`（新增文件），经 `service.Iam()` 暴露 `GetUserStatus` / `UpdateUserStatus`；复用 `auth.RevokeAllSessions` 与 `revokeAllFamiliesByUser`。操作管理员 id 取自已认证 `AdminPrincipal.AdminID`，`operator_username` 为操作时 `admins.username` 快照。

## Business Invariants

- INV-001：被禁用用户（`users.status=0`）无法通过 `Login`/`Refresh` 获得或续用访问能力；其已签发且未过期的 JWT 在受保护接口一律 401（status 或版本不匹配），不依赖 JWT 过期、不只在登录时检查。
- INV-002：未持有对应权限的非超管与普通用户 token（`type=user`）调用用户状态管理接口一律 403/401，且不产生任何 `users.status`/`auth_epoch` 写入、审计写入或会话撤销。
- INV-003：同一用户禁用后重新启用，旧会话与旧 refresh family 不自动恢复（其绑定 `auth_epoch` 落后于当前版本，永久失效），需重新登录；即使禁用时 Redis 撤销失败亦然。
- INV-004：目标用户 id 只能来自已认证上下文（URL path），响应不泄露 `password_hash`/token/Redis 会话凭据。
- INV-005：仅「启用→禁用」实际迁移递增 `auth_epoch`；但「启用→禁用」与「禁用→启用」的实际迁移**均**写审计（`before_status`/`after_status` 反映真实迁移）；幂等 no-op（重复禁用/重复启用）不递增版本、不写审计、不重复撤销。

## Failure and Consistency Semantics

- 事实来源：`users.status` + `users.auth_epoch`（MySQL）为账号状态与认证版本的权威事实；`refresh_tokens`（含 `auth_epoch`）为 refresh 撤销/版本血缘的权威事实；Redis `iam:session:{sid}`（含 `auth_epoch`）为前台会话状态事实来源；审计表为状态变更追溯事实。
- 禁用（同步）：单个 MySQL 事务内 `UPDATE users SET status=0, auth_epoch=auth_epoch+1` + 插入审计 + `UPDATE refresh_tokens`（撤销该用户全部未撤销 family）。事务提交后 best-effort `auth.RevokeAllSessions(userID)`（Redis），失败仅日志、不使禁用失败——status/版本校验兜底保证被禁用用户仍被拒绝。
- 启用（同步）：单个 MySQL 事务内 `UPDATE users SET status=1` + 插入审计；不递增 `auth_epoch`、不撤销会话/refresh family（旧凭证因版本落后仍不可用）。
- 状态更新与并发规则：在单个 MySQL 事务内以 `SELECT ... FOR UPDATE` 锁定目标 `users` 行，串行化并发状态变更，并据锁定读到的真实状态区分三态（避免「先查再写」的 TOCTOU）：
  - 无行 → `2015 USER_NOT_FOUND`（404，无任何写入）；
  - `status == 目标` → 幂等成功（no-op，不写审计、不递增版本、不撤销）；
  - `status != 目标` → 实际迁移：更新 `status`（禁用时同时 `auth_epoch=auth_epoch+1`）+ 写审计 +（禁用场景）撤销 refresh family。
  三态在同一事务内原子判定与执行；`auth_epoch` 仅在实际「启用→禁用」迁移时递增。
- 刷新与禁用并发：禁用事务的 family 撤销与 `rotateRefresh` 的条件 UPDATE 竞争同一 refresh 行，由 InnoDB 行锁串行化——禁用先提交则刷新 `RowsAffected==0` 拒绝（不产生新凭证）；刷新先提交则其新后代随后被禁用撤销，且新 access token 的会话版本落后，鉴权仍 401。
- 登录与禁用并发：登录以开始时读到的 `auth_epoch` 绑定凭证；若禁用在其后提交（版本递增），新凭证版本落后，鉴权/刷新均 401（不产生禁用后仍可用的凭证）。
- 事务失败：status/版本/审计/family 撤销在同一事务内全有或全无，不留下部分变更；Redis 撤销在事务外 best-effort，其失败不影响事务结果。
- 「立即失效」语义边界：承诺「禁用事务提交之后的鉴权请求被拒绝」，不承诺撤回「禁用提交前已通过中间件、正在执行的请求」（标准可接受窗口）。
- 幂等清理接口例外：`POST /logout`（`AuthSignatureOnly`）与 `POST /admin/logout`（`AdminAuthSignatureOnly`）仅验签、不查会话/状态/版本，禁用用户仍可调用以清理自身会话；无访问收益、幂等，属有意例外，不修改。
- 旧凭证升级兼容：迁移给 `users.auth_epoch` 与 `refresh_tokens.auth_epoch` 设 `DEFAULT 0`；已存在会话 Hash 缺 `auth_epoch` 字段按 `0` 解读，与 `users.auth_epoch=0` 匹配，迁移后既有会话/refresh token 仍有效至自然 TTL 到期或首次禁用，不强制重新登录。首次禁用后版本递增，所有 epoch 0 凭证永久失效。
- 无 MQ、无异步；MySQL 与 Redis 间为顺序写，无跨系统事务。

## Allowed / Forbidden Changes

- 允许：新增 `users.status`/`users.auth_epoch`、`refresh_tokens.auth_epoch` 与 `user_status_audits` 表迁移；新增 `2015` 错误码；新增 `user:read`/`user:status` 权限 seed；`Auth`/`Login`/`Refresh` 增加 status/版本校验与会话/refresh 版本绑定；新增两个后台 API 与对应 service/controller。
- 禁止：修改 `users.id`、JWT `type` 声明、`iam:session:`/`iam:admin:session:` key 前缀与既有身份域隔离模型；修改已落地错误码（`2001-2014` 等）取值/HTTP/message；对管理员账号禁用/启用重复建设；新建 Token 黑名单或重造 IAM；删除用户及其历史业务数据；启用时递减或重签 `auth_epoch`。

## Verification Requirements

- INV-001 → MySQL+Redis 集成：登录成功→禁用→用原 access token 访问 `/me` 断言 401；禁用后 `/login`、`/refresh` 断言 401 + `1002`，且无新 session/refresh 行写入。
- INV-003（重点）→ 禁用时模拟 Redis 撤销失败，随后重新启用：旧 access/旧 refresh token 仍 401（版本落后）；重新登录成功并可访问受保护接口。
- INV-002 → HTTP 集成：普通用户 token、无权限管理员、仅有 `user:read` 的管理员调 `PUT /status`、持有 `user:status` 管理员、超管分别调用，断言 403/403/403/成功/成功，并查库确认拒绝路径无写入。
- INV-005 → 重复禁用/启用断言成功，且审计行数、`auth_epoch`、会话撤销次数不重复增加。
- 并发 → 登录/刷新与禁用并发压测（或可逆 Mutation）：断言不产生禁用后仍可用的 access/refresh 凭证；禁用与启用并发最终 `status`/`auth_epoch` 一致、审计与迁移一一对应。
- 事务失败 → 注入审计/撤销失败，断言 status/版本/审计/family 无部分变更。
- AC-011 → 不存在的 id 断言 404 无写入。
- AC-014 → 模拟 `users.status`/`auth_epoch` 查询失败或 Redis 失败，断言 fail-closed（拒绝放行）且禁用操作语义正确。
- AC-015 → 运行既有 IAM/RBAC 回归测试，`type=user`/`type=admin` 隔离不变。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`，并发关键路径按风险 `go test -race`；MySQL/Redis 验证需说明容器就绪。

## Open Risks

- 每请求查 `users.status`/`auth_epoch` 增加前台受保护请求一次 DB 读；与 `AdminAuth` 相同取舍，不引入缓存（避免失效窗口）。
- 审计表 `result` 字段在「仅记录成功变更」语义下恒为 `1`，保留以备未来记录失败尝试。
- 禁用时 Redis 撤销失败会残留未撤销会话至 TTL，但无安全影响（status/版本校验兜底）。
- 认证链基础设施故障的返回码取舍：`Auth` 中间件查询 Redis（`ValidateSession`）或 MySQL（`users.status`/`auth_epoch`）失败时**统一返回 401**（`1002`），与既有 `Auth`（Redis 失败→401）与 `AdminAuth`（`findAdmin` DB 失败→401）保持一致，fail-closed 绝不放行。**不选 503**：虽能更精确表达「服务不可用」，但会引入新的错误分类、与现有认证链 401 语义不一致、且需客户端额外处理；底层错误仅记录服务端日志，不暴露内部基础设施状态。该取舍的代价是 DB/Redis 整体故障时客户端会把「未授权」与「系统故障」混为 401，但安全目标（绝不放行禁用用户）优先，且客户端本就依赖稳定 code 而非 message 区分。

## 全局资源清单（Global Resource Reservation）

- `migration_version`：候选 `20261001000019`（Registry `next = max(已记录 18) + 1`），拥有方 `iam-v5`。**Contract 正式 APPROVED 后**，由 Analyst 从最新 `origin/develop` Registry 重新读取并独占预留（Registry-only commit 落 `RESERVED`）；批准前不推定可用。
- `error_code_domain`：无需新增域（IAM 域 2000-2999 已 ACTIVE，`2015` 属域内编号，无跨任务冲突）。

## Owner Decision Record

- 决定：Owner 认可 IAM V5 整体技术设计（`auth_epoch` 持久化版本、Refresh 版本继承、MySQL 事务 + Redis best-effort 撤销组合），并批准进入 APPROVED 阶段。
- 修订要求（已落实）：① `RowsAffected==0` 必须区分「用户不存在（404）／状态已一致（幂等）」并保证并发正确——改为事务内 `SELECT ... FOR UPDATE` 三态判定；② 实际禁用与实际启用均写审计，仅实际禁用递增 `auth_epoch`；③ 在 Open Risks 说明认证链 DB/Redis 故障统一 401（fail-closed）而非 503 的取舍。
- 适用范围：本任务范围内；不新增架构组件、不扩大 IAM V5 功能范围。`Design Impact = UPDATE`（`iam.md`/`rbac.md`/`migration.md`，`error-codes.md` 视新增错误码而定）。
- 记录者：Analyst（File Writer）；Decision Authority：Owner。
