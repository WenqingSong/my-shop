# Task: IAM V5（用户账号状态管理与禁用会话控制）

## Goal

在现有 IAM 体系上，让管理员能够安全地禁用、启用普通用户账号并查询其状态；被禁用用户无法登录、无法刷新 Token，且已签发的未过期 JWT 也无法继续访问受保护接口；状态变更持久化并具备可追溯审计。复用现有 JWT、Redis 会话、Admin RBAC 与 GoFrame 分层，不重造 IAM。

## Scope

- 为前台 `users` 表新增账号状态字段（沿用 `admins.status` 的 `TINYINT` 枚举语义：`1=启用`、`0=禁用`），旧用户升级后默认启用，维持既有登录语义。
- 管理员用户状态管理 API（后台域，`AdminAuth` + `RequirePermission` 保护）：
  - 查询指定普通用户账号状态；
  - 禁用指定普通用户；
  - 启用指定普通用户。
- 认证链路改造，使禁用用户无法继续获得访问能力：
  - `Login`：禁用用户禁止登录；
  - `Refresh`：禁用用户禁止刷新 Token；
  - `Auth` 中间件：禁用用户已有 Session 不能通过受保护接口鉴权（最迟在受保护请求的认证校验时生效，不依赖 JWT 过期，不只在登录时检查）。
- 禁用时联动撤销该用户已有会话（复用现有 `iam:user:{id}:sessions` 索引与按用户批量撤销能力，不另起 Token 黑名单）。
- 账号状态变更操作审计（append-only），记录操作管理员、目标用户、原始状态、目标状态、原因、时间、结果。
- 新增用户状态管理权限 code 并纳入 RBAC seed（对齐现有「资源:动作」命名）。
- 更新项目级设计文档：`docs/design/iam.md`、`docs/design/rbac.md`、`docs/design/migration.md`（如新增迁移）、`docs/design/error-codes.md`（如新增错误码）。

## Out of Scope

- 前端管理页面。
- 自动风控 / 自动封禁、多级冻结、定时解封。
- IP 黑名单、设备指纹、登录异常检测、用户信用分。
- 删除用户账号（不删除用户及其历史业务数据，不影响订单、秒杀、评价等数据）。
- 修改普通用户与管理员的身份隔离模型；修改 `users.id`、JWT `type` 声明与既有身份模型。
- 管理员账号的禁用/启用（`admins.status` 已由 `admin:disable` 实现，本任务不重复建设）。
- 审计数据的后台查询/列表接口（本任务只负责“可靠落库”，查询界面不做）。
- 重构整个 IAM。

## Design Impact

Design Impact: UPDATE
Design Artifact: `docs/design/iam.md`（用户状态字段与认证/会话撤销语义）；`docs/design/rbac.md`（新增用户状态管理权限）；`docs/design/migration.md`（新增迁移）；`docs/design/error-codes.md`（如新增错误码）

## Acceptance Criteria

- [ ] AC-001：给定已启用的普通用户以正确凭据调用 `/login`，登录成功并返回 access/refresh token；用返回的 access token 访问 `/me` 等受保护接口返回 200。
- [ ] AC-002：给定被禁用的普通用户以正确凭据调用 `/login`，系统拒绝登录（401，稳定业务错误码），不签发任何 token、不写会话。
- [ ] AC-003：给定被禁用用户的仍有效 refresh token 调用 `/refresh`，系统拒绝刷新（401），不签发新 access token、不轮换产生新 refresh token。
- [ ] AC-004：给定用户在禁用前已签发的未过期 access token，禁用生效后访问受保护接口返回 401；即使客户端仍持有未过期 JWT 也不能继续访问（账号状态校验不依赖 JWT 过期、不只在登录时检查）。
- [ ] AC-005：给定同一用户多设备（多个有效会话）登录后被禁用，其全部已有会话在禁用后均无法通过受保护接口鉴权。
- [ ] AC-006：给定被禁用用户被重新启用后，可用正确凭据重新登录并访问受保护接口。
- [ ] AC-007：给定用户被禁用（其旧会话被撤销）后重新启用，其旧会话不会被自动恢复；旧 JWT 仍不可用，需重新登录。
- [ ] AC-008：给定普通用户 token（`type=user`）调用用户状态管理接口，系统返回无权限（403），且不产生任何状态写入。
- [ ] AC-009：给定无用户状态管理权限的管理员（含超级管理员之外、未持有该权限）调用用户状态管理接口，系统返回 403，且不产生任何写入；持有该权限的管理员可正常操作；超级管理员默认放行。
- [ ] AC-010：给定已禁用用户再次禁用、已启用用户再次启用（幂等），系统返回成功，不产生错误状态、不重复撤销会话、不产生重复副作用。
- [ ] AC-011：给定目标用户不存在，禁用/启用/查询返回稳定错误（404），不产生写入。
- [ ] AC-012：用户状态管理接口通过 `AdminAuth` + `RequirePermission` 鉴权；目标用户 id 来自 URL/请求路径，服务端不信任请求体中的身份；接口不返回 `password_hash`、token、Redis 会话凭据等敏感信息。
- [ ] AC-013：状态变更持久化到数据库，并产生对应审计记录（含操作管理员、目标用户、原始状态、目标状态、原因、时间、结果），审计记录不包含明文密码/JWT/Redis 凭据。
- [ ] AC-014：认证/会话故障（如 Redis 查询失败、用户状态查询失败）时 fail-closed，绝不因故障放行禁用用户；底层错误仅记录日志，不暴露内部状态。
- [ ] AC-015：回归：现有正常用户登录、`/refresh`、`/logout`、会话列表与撤销、管理员登录/RBAC 判定均保持既有行为（`type=user`/`type=admin` 隔离不变）。

## Relevant Context

### 已核实事实

- 分支 `feat/iam-user-status`，HEAD `f0bf42c`，与 `origin/feat/iam-user-status`、`origin/develop` 均一致（`0 0`），working tree clean。
- `users` 表（`internal/migrations/sql/20261001000001_baseline.up.sql`）仅有 `id/username/password_hash/created_at/updated_at`，**无 status 字段**；`admins` 表有 `status TINYINT NOT NULL DEFAULT 1`（`1=启用`/`0=禁用`），是本次状态枚举与语义的既有先例（`docs/design/rbac.md` §2.1）。
- 前台认证链（`internal/middleware/auth.go`）：`Auth` 中间件验签+exp 后调用 `auth.ValidateSession`（查 `iam:session:{sid}` 存在、未撤销、`user_id==sub`），**不校验用户 status**；`AdminAuth` 在会话校验后每请求查 `admins.status`（禁用→401），即「每请求查库实现禁用即时失效」的现成先例。
- 登录（`internal/logic/iam/iam.go` `Login`）与刷新（`internal/logic/iam/refresh.go` `Refresh`）当前均不校验用户 status；`Refresh` 在 `findRefreshByHash` 后已持有 `row.UserID`，便于加状态校验。
- 会话撤销能力（`internal/auth/session.go`）：已有 `RevokeAllSessions(userID)` / `RevokeOtherSessions`（Lua 原子批量撤销）、`iam:user:{id}:sessions` ZSET 索引、`RevokeSession(sid)`；refresh family 撤销函数（`revokeAllFamiliesByUser` 等）在 `internal/logic/iam/refresh.go`。
- 后台管理 API 与 RBAC 先例（`internal/cmd/routes_admin.go` + `internal/boot/seed.go`）：`PUT /admin/admins/:id/status` 挂 `require("admin:disable")`；权限 code 为「资源:动作」，`RequirePermission(code)` 对非超管查 `admins → admin_roles → role_permissions → permissions`，超管放行。
- 审计先例（`internal/migrations/sql/20261001000018_flash_sale_request_audits.up.sql`）：append-only 表，`operator_admin_id`/`operator_username`（快照）/`action`/`before_status`/`after_status`/`reason`/`created_at`，软引用无 FK。
- 错误码：`internal/codes/codes.go`，IAM 域 2000-2999 已用 `2001-2014`；`1002 UNAUTHORIZED`(401)/`1003 FORBIDDEN`(403)/`1004 NOT_FOUND`(404) 等通用码可用；错误码域分配以 `.agent/registry/error-codes.md` 为权威（IAM 域 2000-2999 已 ACTIVE）。
- 迁移机制：`internal/migrations/sql/{version}_{title}.up.sql`，14 位时间戳 version，仅 up；`docs/design/migration.md` 维护迁移清单。

### Assumption

- 用户状态字段沿用 `admins.status` 的 `TINYINT`（`1=启用`/`0=禁用`），默认 `1`；具体字段名与是否需要额外记录「状态变更时间」由 Analyst 定。
- 认证侧采用与 `AdminAuth` 一致的「每请求校验用户 status」作为禁用即时失效的兜底，禁用时再联动批量撤销会话；具体以 Contract 为准。

### OPEN QUESTION

- 无阻塞性问题。下列技术选型与一致性语义交 Analyst 分析、Owner 确认（见 Analyst Questions）。

## Verification

- AC-001 → MySQL + Redis 集成环境；注册用户后 `/login` 成功，携带 access token 访问 `/me` 断言 200。
- AC-002 → 同上；禁用用户后 `/login` 断言 401 + 稳定错误码，并确认无新会话写入。
- AC-003 → 同上；禁用后调用 `/refresh` 断言 401，且 `refresh_tokens` 无新后代行。
- AC-004 → 同上；登录取得 access token → 禁用用户 → 用原 token 访问 `/me` 断言 401（token 未过期仍拒绝）。
- AC-005 → 同上；多设备登录后禁用，用各设备 token 访问受保护接口均 401。
- AC-006 / AC-007 → 同上；启用后重新登录成功；旧 token/旧 refresh token 仍不可用。
- AC-008 / AC-009 / AC-012 → HTTP 集成测试：分别用普通用户 token、无权限管理员、有权限管理员、超管调用用户状态管理接口，断言 403/403/成功/成功，并查库确认拒绝路径无写入、响应不含敏感字段。
- AC-010 / AC-011 → 幂等与不存在场景：重复禁用/启用断言成功且无重复副作用；不存在的 id 断言 404 无写入。
- AC-013 → 状态变更后查库断言状态已更新且审计表新增对应记录，字段完整、不含敏感信息。
- AC-014 → 通过可逆 Mutation 或测试桩模拟 Redis/状态查询失败，断言 fail-closed 返回 401 且不放行禁用用户。
- AC-015 → 运行既有 IAM/RBAC 回归测试。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；涉及并发的关键路径按风险 `go test -race`。涉及 MySQL/Redis 的验证需说明容器就绪。

## Complexity

COMPLEX

原因：涉及重要安全边界（禁用用户的认证放行与 fail-closed）与跨系统一致性（MySQL 状态变更与 Redis 会话撤销/refresh family 撤销之间非跨系统事务），并有多个现实方案（每请求校验 status vs 禁用时批量撤销会话 vs 二者结合）会直接决定安全与运维结果。需 Analyst 定向调查并起草 `contract.md`，Owner 确认关键方案后交 Coder。

## Analyst Questions

1. 用户状态字段设计：`users` 新增字段名/类型/默认值（是否严格对齐 `admins.status`）；是否额外记录「状态变更时间」字段，还是仅依赖审计表记录时间与原因。
2. 禁用即时生效的实现与一致性：采用「每请求查 `users.status`」（对齐 `AdminAuth`）为主、禁用时批量撤销会话为辅，还是仅靠会话撤销？DB 状态更新与 Redis 会话撤销、refresh family 撤销之间如何定义失败处理与一致性语义（Redis 失败时是否回滚状态更新、或状态已改但会话靠 TTL/每请求校验兜底）。
3. 重新启用语义：启用是否恢复或重签会话；明确「禁用已撤销的旧会话不因启用自动恢复」（是否需要保留会话撤销标记）。
4. 权限 code 与粒度：用户状态管理的权限 code 命名（如 `user:disable`/`user:status`），禁用与启用是否共用一个 code（对齐 `admin:disable` 先例）；是否新增查询权限。
5. 用户状态管理 API 路径与请求/响应契约：查询/禁用/启用的路径（对齐 `/admin/admins/:id/status` 与 `/admin/admins` 风格）、请求体字段、响应结构；是否新增 `GET /admin/users/:id/status`。
6. 审计表结构：新增独立审计表（对齐 `flash_sale_request_audits` 先例）还是复用；字段集合（operator_admin_id、operator_username 快照、target_user_id、before/after status、reason、result、时间）；与状态变更的落库顺序/一致性。
7. 错误码分配：新增错误码（用户不存在、状态非法/非法转换等）在 IAM 域 2000-2999 内的具体取值与 HTTP 状态；禁用登录/刷新是否复用 `1002`。
8. 全局资源：需新增的 migration 数量与内容边界（status 字段迁移、审计表迁移）；错误码若新增是否需在 Registry 预留（IAM 域已 ACTIVE，域内编号由 Analyst 逐个列出并写入 Contract）。
9. 认证侧改造落点：`Auth` 中间件加 status 校验的准确位置与查询方式（是否与 `ValidateSession` 合并、避免每请求额外查库的取舍）；`Refresh` 加状态校验的时机（`findRefreshByHash` 后、session upsert 前）。

## Review Baseline

- Base commit：`f0bf42cbf8592215a76173ef75d2eac5078960e2`（分支 `feat/iam-user-status`）
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空；`origin/develop...HEAD` 为 `0 0`）
- 重叠修改的区分方式：不适用（无已有未提交修改，分支与 `origin/develop` 一致）

## Initial Route

交 Analyst（COMPLEX）
