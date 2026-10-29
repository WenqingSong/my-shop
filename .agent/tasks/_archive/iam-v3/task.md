# Task: IAM V3（多设备会话管理与主动撤销）

## Goal

让用户能够查看并主动管理自己的所有登录会话（多设备）：查看会话列表、撤销指定设备、撤销其他设备、全部退出；同时明确 `token_version` 与 `jti` 单 Token 撤销是否需要引入，并据此落地相应设计与实现。

## Scope

- 会话列表：受保护接口返回当前用户（或经确认的身份域）的所有活跃会话，含区分设备所需的最小元数据，并标识当前会话。
- 撤销指定设备：撤销当前用户指定的一个会话（按会话标识），使其 token 立即失效，其他会话不受影响。
- 撤销其他设备：撤销当前用户除当前会话外的所有其他会话。
- 全部退出：撤销当前用户的所有会话（含当前）。
- 支撑上述能力的 user→sessions 索引（数据模型由 Analyst 决定，如 Redis Set 或其它结构），及会话元数据（如登录时间、设备信息等）的存储方案。
- `token_version` 决策：是否需要引入；若引入，落地对应字段/声明/校验与迁移。
- `jti` 单 Token 撤销决策：是否需要独立于 sid 的 jti 语义；若需要，落地对应设计。
- 更新 `docs/design/iam.md`，沉淀新的会话数据模型、撤销语义与安全边界（Design Impact = UPDATE）。

## Out of Scope

- refresh token、滑动续期、token 轮换（保持单 access token，沿用 IAM V2 已确认约束）。
- 管理员对「其他用户」的强制下线/跨用户撤销（本任务只处理「当前用户管理自己的会话」）。
- 会话的物理删除策略调整（撤销仍为逻辑标记，记录保留至 TTL 到期）。
- 登录限流、设备指纹风控、异地登录提醒等安全增强。
- 修改 `/register`、`/health` 的公开行为。

## Design Impact

Design Impact: UPDATE
Design Artifact: `docs/design/iam.md`（若 `token_version` 引入新列，另涉 `docs/design/migration.md`；若管理员域纳入范围，另涉 `docs/design/rbac.md`）

## Acceptance Criteria

- [ ] AC-001 用户以正确凭据登录后，系统为该次登录建立独立会话；同一用户多次登录产生多个可区分的独立会话（多设备并存）。
- [ ] AC-002 用户携带有效 token 访问会话列表接口，返回该用户所有未撤销会话的列表，每条至少含会话标识与登录时间，且明确标识「当前会话」；不返回其他用户的任何会话。
- [ ] AC-003 用户请求撤销自己名下指定的一个会话后，该会话被标记撤销，其 token 随后访问受保护接口返回 401，且该用户其他会话仍可正常访问。
- [ ] AC-004 用户请求撤销除当前会话外的其他全部会话后，其他所有会话被撤销（其 token 访问受保护接口 401），当前会话保持有效。
- [ ] AC-005 用户请求全部退出后，该用户所有会话（含当前）被撤销，所有旧 token 访问受保护接口均返回 401。
- [ ] AC-006 撤销指定设备的目标会话必须属于当前用户：传入非本人会话标识、非法标识或不存在的会话时，返回错误（语义由 Contract 定义），且不产生任何撤销；不泄露会话是否存在或归属。
- [ ] AC-007 撤销操作幂等：对已撤销或已不存在的会话重复撤销不产生系统错误（幂等语义由 Contract 约定）。
- [ ] AC-008 Redis 不可用/查询失败时，受保护接口按既有 fail-closed 语义拒绝（401），会话列表与撤销操作不 fail-open 放行，服务端记录底层错误日志。
- [ ] AC-009 会话列表与撤销能力不回归 IAM V2 既有行为：`/login`、`/logout`、`/me` 语义不变，`/logout` 仍只撤销当前会话。
- [ ] AC-010 更新 `docs/design/iam.md`，沉淀新的会话索引数据模型、撤销语义、身份域边界与安全边界，并与最终 Contract、实现一致。
- [ ] AC-011 对 `token_version` 与 `jti` 是否必要给出明确结论并由 Owner 确认；若结论为必要，则按其决定落地对应实现、迁移与设计更新（对应 AC 由 Analyst 在 Contract 中细化）。

## Relevant Context

### 已核实事实

- 技术栈 GoFrame v2（Go 1.23），模块 `cnb.cool/go-cloud-devops/my-shop`；响应统一 `{code,message,data}`，错误码集中在 `internal/codes/codes.go`。
- 当前会话模型（`internal/auth/session.go`、`docs/design/iam.md`）：
  - 前台用户：单 key `iam:session:{sid}`，Redis Hash 字段 `user_id`（十进制字符串）、`revoked`（"0"/"1"），TTL `auth.session.ttl` 默认 3600。
  - 后台管理员：单 key `iam:admin:session:{sid}`，字段 `admin_id`、`revoked`，对称但完全隔离。
  - **无任何 user/admin 维度的会话索引**，当前无法枚举某用户的全部会话。
- JWT（`internal/auth/jwt.go`）：`Claims{Sid, Type}` 内嵌 `gojwt.RegisteredClaims`（含标准 `ID`/jti 字段但未使用）；签发 `sub`/`iss=surgecart`/`iat`/`exp=iat+3600`；无 `token_version` 声明。
- 认证中间件（`internal/middleware/auth.go`）：`Auth`（验签+exp → 查 session 存在且未撤销且 `user_id==sub`）、`AuthSignatureOnly`（仅验签）、`AdminAuth`/`AdminAuthSignatureOnly`（管理员域，另叠加 `RequirePermission`）；注入 `Principal{UserID, Sid}`、`AdminPrincipal{AdminID, Sid, IsSuper}`。
- 登出（`internal/logic/iam/iam.go`、`internal/logic/admin/admin.go`）：`/logout`、`/admin/logout` 仅撤销当前 token 的 sid，幂等（`RevokeSession`/`RevokeAdminSession` 用 Lua 原子置 `revoked=1`）。
- 服务接口（`internal/service/iam.go` 的 `IIam`、`internal/service/admin.go` 的 `IAdmin`）：目前无会话列表/批量撤销方法。
- 迁移机制（`internal/migrations/`，golang-migrate）：14 位时间戳 `.up.sql`；`users`/`admins` 表结构见 baseline `20261001000001`（当前无 `token_version` 列）。
- 身份域隔离是既有强约束（`docs/design/iam.md` §5.1）：前台用户与后台管理员凭据表、token type、session key 前缀、认证中间件四要素独立。
- Git 基线：分支 `feat/iam-v3`，HEAD `3e61dc339adf6d3b731b4f69272392c26fd79cdf`，working tree clean。

### Assumption

- 「多设备与主动撤销」的默认主体为前台用户域（用户管理自己的登录设备）；后台管理员域是否同步纳入，由 Analyst 分析并交 Owner 确认（见 Analyst Questions）。
- 会话列表需要新增会话元数据（登录时间、设备信息等）才能满足「区分设备」的可观察要求；具体字段与存储由 Analyst 定。
- 「撤销其他设备」「全部退出」属于当前用户对自己会话的操作，不引入管理员跨用户强制下线。

### OPEN QUESTION（需 Analyst 分析、Owner 确认）

- 身份域覆盖范围：仅前台用户 / 前台 + 后台管理员。
- `token_version` 是否必要及其取舍（见 Analyst Questions）。
- `jti` 单 Token 撤销是否必要及其与 sid 的关系（见 Analyst Questions）。

## Verification

- AC-001 → 需 Redis：同一用户连续登录多次，确认每次产生不同 sid 且各自 session 均存在未撤销（`HGETALL iam:session:{sid}`）。
- AC-002 → 需 Redis：登录多个会话后调用列表接口，断言返回全部会话且含「当前会话」标识；换另一个用户 token 调用，断言列表不含对方会话。
- AC-003/004/005 → 需 Redis：分别触发撤销指定/撤销其他/全部退出，用 `redis-cli` 断言目标 session `revoked=1`（保留 key 与 TTL），并用对应旧 token 访问 `/me` 断言 401；保留的会话仍 200。
- AC-006 → 需 Redis：用用户 A 的 token 尝试撤销用户 B 的会话标识 / 非法标识，断言拒绝且 Redis 无变化。
- AC-007 → 需 Redis：对已撤销会话重复撤销，断言幂等成功（语义按 Contract）。
- AC-008 → 需 Redis：停 Redis 或注入错误，断言受保护接口 401（非 200/5xx），撤销/列表不 fail-open。
- AC-009 → 回归：登录/logout/me 正常路径与 IAM V2 一致；logout 只撤销当前会话。
- AC-010 → 确认 `docs/design/iam.md` 与最终 Contract、实现一致（由 Cleaner 审查一致性）。
- AC-011 → `token_version`/`jti` 结论写入 Contract 并由 Owner 确认；若实现，则按 Contract 补充验证。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；Redis 集成验证说明容器就绪；按风险对关键不变量运行 `go test -race`。

## Complexity

COMPLEX

原因：需要新增 user→sessions 索引这一数据模型，涉及索引与会话 key 的一致性、批量撤销的原子性与并发语义；`token_version` 与 `jti` 是两个会影响数据模型、JWT 声明与鉴权语义的架构取舍；撤销指定设备还存在「跨用户越权撤销」的安全边界，多个现实方案会产生不同业务与运维结果。

## Analyst Questions

1. user→sessions 索引的数据模型：Redis Set（`iam:user:{id}:sessions`）还是其它结构；如何与 session key 保持一致性、TTL 到期后如何清理；登录/撤销/全部退出时如何原子维护。
2. `token_version` 是否必要：引入它的收益（O(1) 全部失效、改密即全下线）与成本（新列迁移、JWT 新声明、每请求与 DB/会话版本比对）；若不引入，「全部退出」如何以索引枚举实现并保证一致性。
3. `jti` 单 Token 撤销是否必要：当前 sid 已等价于单 token 撤销（每登录 1 session 1 token）；是否需区分「会话（设备）」与「单个 token」（如未来 token 轮换/多 token），当前是否引入标准 `jti` 声明。
4. 身份域覆盖：会话管理能力仅前台用户域，还是同时覆盖后台管理员域（对称实现）。
5. 会话元数据：列表需展示哪些字段（登录时间、User-Agent、IP、设备名等），如何存储与清理；「当前会话」如何判定。
6. 撤销的失败语义与错误码：撤销非本人/不存在会话返回什么（404 vs 403 vs 幂等 200），是否新增独立错误码；批量撤销的原子性边界与部分失败处理。
7. 撤销指定设备的安全校验：如何在撤销前确认目标 sid 归属当前用户（读 session 比对 user_id），以及 Redis 查询失败时的 fail-closed 语义。

## Review Baseline

- Base commit：`3e61dc339adf6d3b731b4f69272392c26fd79cdf`（分支 `feat/iam-v3`）
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）
- 重叠修改的区分方式：不适用（无已有未提交修改）

## Initial Route

READY_FOR_ANALYST
