# Task: IAM V2（JWT + Redis 有状态会话与登出）

## Goal

把 IAM 的无状态 JWT 升级为「有状态」：登录成功后在 Redis 建立 server session（以 sid 为 JWT 与 Redis 之间的桥梁），受保护接口在验签与校验 exp 后追加 session 有效性校验，并新增 `POST /logout` 使 token 可被登出/撤销。同时交付一份面向项目接手者的 IAM 系统设计文档。

## Scope

- 登录流程升级：`POST /login` 成功后生成 sid → 写 Redis session（带 TTL，初始未撤销）→ 签发含 sid 的 JWT → 返回 token。
- 鉴权中间件升级：验签 + 校验 exp 通过后，按 JWT 内的 sid 查 Redis，确认 session 存在且未 revoked 才放行，并注入 `Principal`（含 sid）。
- 新增受保护接口 `POST /logout`：将当前 token 对应 sid 的 session 标记为 revoked，使该 token 之后访问受保护接口返回 401。
- 会话数据模型：仅 `iam:session:{sid}` 单一 key；撤销为逻辑标记（`revoked=true`），记录保留至 TTL 到期，不物理删除；不引入 user 维度的 sessions 索引。
- 错误码/HTTP 状态：沿用现有 `{code,message,data}` 与集中错误码体系，登出与「已撤销 token」等新增失败场景的语义由 Contract 定义并落地。
- 必要测试：登录建 session、有效/失效/撤销/session 缺失等鉴权路径，logout 正常与幂等，以及 IAM V1 行为回归。
- 独立交付物：一份 IAM 系统设计文档（见下）。

### 设计文档（独立交付物）

- 位置与命名：`docs/design/iam.md`（IAM 系统设计文档，面向项目接手者，区别于 `task.md` / `contract.md` 等协作文件）。
- 内容：JWT + Redis Session 的架构、数据模型、鉴权/登出/撤销流程与安全边界。
- 产出时机：在 Contract 经 Owner 确认（APPROVED）且实现落定（Cleaner 审查通过）后，基于最终 Contract 与实际实现整理定稿，随本任务一并交付（由 Coder 起草，Cleaner 审查其与 Contract/实现的一致性）。

## Out of Scope

- refresh token、token 自动续期（保持单 access token）。
- session 列表查询、登出全部设备、管理员强制下线（需 user→sessions 索引，属完整版）。
- user 维度的 sessions 索引（本次仅保留 `iam:session:{sid}` 单一 key）。
- 对已撤销 session 的物理删除（保留至 TTL 到期）。
- RBAC/角色权限、限流、验证码、第三方登录等（同 IAM V1 Out of Scope）。
- 修改 `/register`、`/health` 的既有公开行为（登录与鉴权路径的必需升级除外）。

## Acceptance Criteria

- [ ] AC-001 给定已注册用户以正确凭据调用 `/login`，当登录成功，则系统生成唯一 sid，向 Redis 写入 session 记录（key 为 `iam:session:{sid}`、带 TTL、初始未撤销），返回的 `access_token` 其 JWT 声明包含该 sid，且解码 token 得到的 sid 与 Redis 中实际写入的 key 一致。
- [ ] AC-002 给定用户登录成功，当 Redis 中对应 session 存在且未撤销，则携带该 token 访问 `/me` 返回该用户 `id` 与 `username`，且返回 id 与 token 标识的用户一致。
- [ ] AC-003 给定有效 token，调用 `POST /logout`（受保护）成功，则 Redis 中对应 session 被标记 `revoked=true`；随后用同一 token 访问 `/me` 返回 401 未授权，且不返回任何用户数据。
- [ ] AC-004 登出后 session 记录仍保留在 Redis（不被物理删除），并在 TTL 到期前持续使 token 失效；TTL 到期后 key 自然消失，访问受保护接口仍返回 401。
- [ ] AC-005 缺失、格式非法、签名无效、已过期（exp 已过）的 token，以及「验签与 exp 通过但 sid 在 Redis 中不存在」（如 session TTL 已到期、Redis 被清空）的 token，访问 `/me` 均返回 401 未授权（沿用 IAM V1 语义，升级不回归）。
- [ ] AC-006 `POST /logout` 为受保护接口，需有效 token 才能访问；登出的目标 sid 来自当前 token/Principal，不接受客户端在请求体或参数中另行指定 sid；对同一 token 重复登出不产生系统错误（幂等/已撤销语义由 Contract 约定）。
- [ ] AC-007 登出某会话后，其他独立会话（另一用户，或同一用户再次登录产生的不同 sid）仍可正常访问受保护接口（本次不登出全部设备）。
- [ ] AC-008 logout 与「已撤销 token」等新增失败场景使用稳定业务错误码与 HTTP 状态（沿用 `{code,message,data}` 与集中错误码体系），客户端可按 code 判型，具体 code/状态由 Contract 定义并落地。
- [ ] AC-009 会话能力复用现有 Redis 配置（`redis.default.*`）与 JWT 密钥注入（`auth.jwt.secret` / `AUTH_JWT_SECRET`）；不硬编码 Redis 地址或密钥；如新增会话 TTL 等配置，须说明用途、默认值与必填条件，并可经环境变量覆盖。
- [ ] AC-010 交付一份 IAM 系统设计文档，沉淀 JWT + Redis Session 的架构、数据模型、鉴权/登出/撤销流程与安全边界，且内容与最终 Contract 及实现一致。

## Relevant Context

### 已核实事实（代码/配置/基线）

- IAM V1 已实现并合并入 `feat/auth-v2`，当前 HEAD `8e4ff4df6536383a4720630c7b62e03bd0e5689f`，working tree clean。
- 当前 JWT 为 HS256 无状态：`internal/auth/jwt.go` 签发 `sub`（用户 id 十进制字符串）/`iss=surgecart`/`iat`/`exp=iat+3600`；`Claims` 内嵌 `gojwt.RegisteredClaims`（含 `ID` 即 jti 字段，当前未写入 sid）。密钥来自 `auth.jwt.secret` / `AUTH_JWT_SECRET`，≥32 字节，启动校验。
- 认证中间件 `internal/middleware/auth.go`：解析 `Authorization: Bearer` → `auth.Parse`（验签 + `WithExpirationRequired` + `WithIssuer` + 限定 HS256）→ 解析 sub 为 UserID → 注入 `Principal{UserID}`。`internal/middleware/principal.go` 的 `Principal` 当前仅 `UserID int64`。
- 登录 `internal/logic/iam/iam.go`：校验凭据后 `auth.Generate(ctx, user.ID)` 签发 token；`internal/service/iam.go` 的 `IIam` 接口当前仅 `Register`/`Login`/`Me`。
- 错误码体系 `internal/codes/codes.go`：0 / 通用 1000-1005 / IAM 2001-2002，集中绑定 HTTP 状态；`internal/middleware/response.go` 统一映射并保持 `{code,message,data}`。
- Redis 已接入：`internal/boot/boot.go` 的 `applyRedisConfig`（`redis.default.*`）与 `checkDependencies`（PING）已存在；依赖 `gogf/gf/contrib/nosql/redis/v2` + `github.com/redis/go-redis/v9` 已在 `go.mod`。IAM V1 本身不使用 Redis。
- 路由 `internal/cmd/cmd.go`：`/register`、`/login` 公开；`/me` 挂 `middleware.Auth`。
- 运行环境为 CNB DinD、每日重置、数据不持久；MySQL 8.0 / Redis 7 由 `docker-compose.yml` 编排（Redis 已有独立 volume）。
- 现有测试：`internal/controller/iam/iam_test.go`（真实 MySQL 集成 `TestIAMEndToEnd`、`TestConcurrentRegisterSameUsername`）、`internal/auth/jwt_test.go`、`internal/logic/iam/iam_test.go`、`internal/boot/boot_test.go`。
- 现存开放 P3 Finding（非阻塞，见 `docs/tasks/iam-v1/findings.md`）：`CLEAN-001`（`/health` message 空串→"OK"）、`CLEAN-002`（登录防枚举耗时对齐未被测试保护）。

### 已确认约束（Owner 已决定，Analyst 无需再向 Owner 提问）

1. sid 写入 JWT 声明内（自定义 claim 或标准 `jti`，具体字段名交 Analyst 定）。
2. 本版本不做 refresh token，保持单 access token。
3. 撤销采用逻辑标记：Redis 内 session 记录标记 `revoked=true`，保留至 TTL 到期，不做物理删除。
4. Session 管理仅最小版：仅 logout（登出当前设备）；不做 session 列表、登出全部设备、管理员强制下线。
5. 最小数据边界：仅 `iam:session:{sid}` 单一 key，不引入 user 维度的 sessions 索引。

因此以下问题**不需要** Analyst 再向 Owner 提问：是否引入 refresh token（否）、撤销用物理删除还是逻辑标记（逻辑标记）、是否做登出全部设备/会话列表/强制下线（否）、sid 是否写入 JWT 声明（是）、是否引入 user 维度索引（否）。

### Assumption

- 登录成功后仍返回单一 `access_token`（`token_type=Bearer`），响应字段是否变化（如新增 `sid` 或 `expires_in` 语义调整）由 Contract 约定，不改变「单 token」这一已确认约束。

## Verification

- AC-001 → 需 Redis 可连接（`docker compose up -d` 后）；对 `/login` 发起真实请求，解码返回 token 核对 sid 声明；用 `redis-cli`（或 `g.Redis()`）查 `iam:session:{sid}` 确认存在、TTL>0 且未撤销。建议在 `go test ./...` 中提供 Redis 集成测试。
- AC-002 → 需 Redis；登录后携带 token 访问 `/me`，断言 200/0 且 `data.id`、`data.username` 正确（id 与 token 用户一致）。
- AC-003 → 需 Redis；登录 → `POST /logout` → 查 Redis 确认 `revoked=true` → 再 `/me` 断言 401 且无用户数据。
- AC-004 → 需 Redis；登出后确认 key 仍存在且 TTL 未清零（未被 DEL）；用短 TTL 测试场景确认到期后 key 消失且 `/me` 仍 401。
- AC-005 → 需 Redis + 可构造 token；覆盖缺失/坏格式/坏签名/过期/错误 issuer 五类（V1 回归），以及「验签通过但 Redis 无对应 key」（删除 key 或改用不存在的 sid）→ `/me` 401。
- AC-006 → 需 Redis；无 token 访问 `/logout` 断言 401；重复登出同一 token，按 Contract 断言幂等语义；确认 logout 不使用客户端提交的 sid（可审查请求体为空/忽略外部 sid 并配合测试）。
- AC-007 → 需 Redis；两次独立登录得到两个 sid，登出其一，另一 token 仍可 `/me` 200。
- AC-008 → 按 Contract 断言 logout 与已撤销 token 场景的 `code` 与 HTTP 状态，且 body 保持 `{code,message,data}`。
- AC-009 → 代码检索确认无硬编码 Redis 地址/JWT 密钥；会话 TTL 等新增配置有默认值、说明与环境变量覆盖；用环境变量覆盖后重启验证生效。
- AC-010 → 确认 `docs/design/iam.md` 存在并覆盖架构/数据模型/鉴权·登出·撤销流程/安全边界，且与最终 Contract、实现一致（由 Cleaner 审查一致性）。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；涉及 Redis 的集成验证需说明环境（依赖容器就绪）。并发相关按风险运行 `go test -race` 覆盖关键不变量。

## Complexity

COMPLEX

原因：这是把无状态 JWT 升级为有状态会话，涉及 JWT 声明变更、Redis session 数据模型与 TTL 一致性、登出/撤销语义，以及 Redis 不可用时鉴权中间件的安全边界（fail-open vs fail-closed）。多个现实方案会产生不同的安全、可靠性与运维结果，需 Analyst 定向调查并起草 `contract.md`，由 Owner 确认关键方案后再交 Coder 实现。

## Analyst Questions

Analyst 应调查并由 Owner 确认的关键问题：

1. Redis session 的 key/value 结构与 TTL：key 采用 `iam:session:{sid}`（已确认），但 value 需哪些字段（如 `user_id`、`revoked`、`created_at` 等）、TTL 取值与是否刷新。
2. sid 的生成方式：格式（UUID v4 / 随机串等）、长度、唯一性来源与碰撞处理。
3. session TTL 与 JWT exp 的关系：两者是否对齐、由谁决定；两者过期窗口不一致时（如 JWT 未过期但 session 已到期）的鉴权行为。
4. JWT 中承载 sid 的声明字段：自定义 claim 还是标准 `jti`，字段名。
5. session value 是否包含 `user_id`：鉴权中间件如何重建 `Principal{UserID, Sid}`（JWT 继续携带 `sub`、session 仅作「存在且未撤销」标记，还是由 session 存储 user_id）。
6. logout 与「已撤销 token」的失败语义与错误码/HTTP 状态：是否新增独立错误码（区别于 1002 UNAUTHORIZED）；登出成功、重复登出、已撤销 token 访问受保护接口各自返回什么。
7. Redis 不可用/超时时的安全语义：鉴权中间件按 sid 查询失败时 fail-closed（401）还是返回 5xx；登录时写 Redis session 失败是否视为登录失败。

## Review Baseline

- Base commit：`8e4ff4df6536383a4720630c7b62e03bd0e5689f`（分支 `feat/auth-v2`）
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）
- 重叠修改的区分方式：不适用（无已有未提交修改）
- 说明：现行协作规范约定任务目录为 `.agent/tasks/<task-slug>/`（见 `docs/agent/AgentCollaborationSpecification.md`）；历史任务位于 `docs/tasks/`。本任务按现行规范创建于 `.agent/tasks/iam-v2/`。

## Initial Route

READY_FOR_ANALYST
