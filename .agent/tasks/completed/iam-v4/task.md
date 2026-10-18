# Task: IAM V4（Refresh Token 轮换、Token Family 与重用检测）

## Goal

在现有「无状态 JWT access token + Redis 有状态会话」之上引入长期 Refresh Token：登录同时签发短期 access token 与长期 refresh token，refresh token 仅以哈希落库；刷新时执行轮换（rotation），以 Token Family 追踪血缘，检测到已轮换 token 被重放时撤销整个 family；并发刷新保证仅一次轮换；并提供「被盗 refresh token 重放」的专项测试与对应设计文档更新。

## Scope

- 登录升级：`POST /login` 成功后在签发 access token 之外，生成一次性高熵 refresh token，仅将其哈希持久化，明文仅出现在本次登录响应中；刷新能力沿用现有身份域隔离约束（前台用户域，管理员域是否纳入由 Owner 决定，见 Analyst Questions）。
- 新增刷新接口 `POST /refresh`：凭有效 refresh token 换取新 access token 与新 refresh token（轮换），并立即使被使用的旧 refresh token 失效。
- Token Family 数据模型：以可持久化存储记录 refresh token 的哈希、所属 family 与血缘（父子/代际），用于轮换与重用检测；具体存储介质与表结构由 Analyst 决定（见 Analyst Questions）。
- Reuse Detection：检测「已轮换（已使用）的 refresh token 被再次提交」，并按约定撤销整个 family（含最新后代）。
- 刷新并发处理：同一 refresh token 被并发提交时，保证仅一次轮换成功，不产生多个并行有效后代。
- 错误码/HTTP 状态：refresh 无效、过期、已轮换（reuse）等失败场景使用稳定业务错误码与 HTTP 状态（沿用 `{code,message,data}` 与集中错误码体系），客户端按 code 判型，不泄露哈希或内部状态。
- 必要测试：登录双 token 签发与哈希落库、refresh 轮换、family 血缘、reuse 撤销 family、过期/无效/未知 refresh、并发刷新、以及「被盗 refresh token 重放」专项测试；IAM V2 行为回归。
- 更新 `docs/design/iam.md`（refresh token 数据模型、轮换/family/reuse 状态机、并发与安全边界）；若新增表迁移，同步更新 `docs/design/migration.md` 的迁移清单与数据模型。

## Out of Scope

- 滑动续期（refresh token 绝对有效期内的自动顺延，是否引入由 Analyst/Owner 决定，默认不做）。
- 会话列表、登出全部设备、管理员强制下线（IAM V3 范畴，见「Relevant Context」的版本基线说明）。
- RBAC/角色权限、限流、验证码、第三方登录、设备指纹风控。
- 修改 `/register`、`/health`、`/logout`、`/me` 的既有公开行为（登录与刷新路径的必需升级除外）。
- 对 refresh token 的物理删除策略（保留至过期/撤销，具体清理策略由 Contract 定义）。

## Design Impact

Design Impact: UPDATE
Design Artifact: `docs/design/iam.md`（新增 refresh_tokens 表迁移另涉 `docs/design/migration.md`）

## Acceptance Criteria

- [ ] AC-001 给定已注册用户以正确凭据调用 `/login`，登录成功时系统同时返回短期 access token 与长期 refresh token；refresh token 为高熵一次性随机值，数据库中仅保存其哈希，不保存明文；明文仅在本次登录响应中出现一次，不出现在日志、错误响应或任何持久化介质中。
- [ ] AC-002 给定有效 refresh token 调用 `/refresh`，系统返回新的 access token 与新的 refresh token，并立即使被使用的旧 refresh token 失效（此后旧 refresh token 再提交不再能换取新 token）。
- [ ] AC-003 给定同一 token family 中「已轮换（已使用过）」的旧 refresh token 再次提交，系统识别为 reuse 并撤销该 token family 的全部 refresh token（含最新后代），使它们全部失效；返回稳定业务错误码，不产生任何新 token。
- [ ] AC-004 被盗重放场景：攻击者持有被盗的旧 refresh token、合法用户持有轮换后的新 refresh token；攻击者重放旧 token 后，整个 family 被撤销，合法用户的新 refresh token 也随之失效（需重新登录），攻击者无法用旧 token 换取新 access token。
- [ ] AC-005 给定已过期的 refresh token 调用 `/refresh`，系统返回明确的过期错误，不产生新 token；refresh token 的有效期显著长于 access token。
- [ ] AC-006 给定无效、被篡改或未知的 refresh token 调用 `/refresh`，系统拒绝且不泄露该 token 是否存在（统一错误语义，由 Contract 约定）。
- [ ] AC-007 给定同一 refresh token 被并发提交多次，系统保证仅一个请求成功轮换，其余请求按 Contract 约定语义处理（幂等返回同一结果或明确拒绝），不产生多个并行有效后代，不出现数据竞争导致的重复换新。
- [ ] AC-008 access token 过期后，客户端可用仍有效的 refresh token 换取新 access token 并继续访问受保护接口；refresh token 的生命周期独立于 access token。
- [ ] AC-009 refresh 相关的所有失败场景（无效/过期/已轮换/reuse）使用稳定业务错误码与 HTTP 状态，客户端可按 code 判型；响应体沿用 `{code,message,data}`，不向客户端暴露哈希值、family 内部标识或 Redis/DB 内部状态。
- [ ] AC-010 更新 `docs/design/iam.md`（及涉及新表迁移的 `docs/design/migration.md`），沉淀 refresh token 数据模型、轮换/family/reuse 状态机、并发语义与安全边界，且内容与最终 Contract 及实现一致。
- [ ] AC-011 提供「被盗 refresh token 重放」专项测试：先证明重放会撤销 family、使合法新 token 失效，再证明测试能在错误实现（如不撤销 family、或并发下多次轮换成功）下失败，能区分正确与错误实现。

## Relevant Context

### 已核实事实（代码/配置/基线）

- 技术栈 GoFrame v2.10.3 / Go 1.23.0，模块 `cnb.cool/go-cloud-devops/my-shop`；响应统一 `{code,message,data}`，错误码集中在 `internal/codes/codes.go`（IAM 域 2000-2999 已用 2001-2010）。
- 当前 token 模型（`internal/auth/jwt.go`、`internal/auth/session.go`）：单 access token，JWT HS256（`Claims{Sid, Type}` 内嵌 `RegisteredClaims`，`sub/iss=surgecart/iat/exp=iat+3600`）；Redis 会话单 key `iam:session:{sid}`（Hash 字段 `user_id`/`revoked`，TTL `auth.session.ttl` 默认 3600）。**无 refresh token、无轮换、无 token family、无重用检测。**
- 登录（`internal/logic/iam/iam.go` `Login`）：校验 bcrypt → 生成 sid → `auth.CreateSession` 写 Redis → `auth.Generate` 签发 access token → 返回 `{access_token, token_type:"Bearer", expires_in:3600}`。
- 认证中间件（`internal/middleware/auth.go`）：`Auth`（验签+exp → 校验 session 存在且未撤销且 `user_id==sub` → 注入 `Principal{UserID,Sid}`）；`AuthSignatureOnly`（仅验签，用于 `/logout` 幂等撤销）。
- 身份域隔离（`docs/design/iam.md` §5.1 既有强约束）：前台用户（`users` + `Auth`）与后台管理员（`admins` + `AdminAuth` + `RequirePermission`）凭据表、token type、session key 前缀、认证中间件四要素独立。
- 迁移机制（`internal/migrations/`，golang-migrate v4）：14 位时间戳 `.up.sql`、仅 up、`//go:embed` 内嵌；当前迁移 `baseline`（含 `users`/`admins`）/`products`/`skus`/`inventory`；新表须新增迁移文件并更新 `docs/design/migration.md` 迁移清单。
- 依赖（`go.mod`）：`golang.org/x/crypto`（bcrypt）、`github.com/golang-jwt/jwt/v5` 已存在；高熵随机串哈希可用标准库 `crypto/sha256`。
- Git 基线：分支 `feat/iam-v4`，HEAD `1db733d454ae967702bc87f080a03195123cdc02`，working tree clean（`git status --short` 为空）。

### 版本基线分歧（重要，需 Owner 关注）

- 当前 HEAD `1db733d` 是 `origin/develop` 的祖先，落后 32 个提交。
- IAM V3（「多设备会话管理与主动撤销」，提交 `0291413`，其任务 `.agent/tasks/iam-v3/`）已合并进 `develop`，但**不在当前分支 `feat/iam-v4` 中**：当前工作区的 `internal/auth/session.go` 仍是单 key 会话模型（无 user→sessions 索引），`docs/design/iam.md` 仍写明「无会话列表/登出全部设备」。
- IAM V3 在其 Out of Scope 中明确把「refresh token、滑动续期、token 轮换」留到了后续任务，本任务正是承接该项。

### Assumption

- refresh token 需跨 Redis 重置长期有效（CNB 环境每日重置、数据不持久），因此默认采用 MySQL 持久化存储哈希；具体介质仍交 Analyst 在 Contract 中确认（见 Analyst Questions）。
- 高熵随机 refresh token 使用快速哈希（如 SHA-256）而非 bcrypt（bcrypt 用于低熵密码，不适用于高熵随机串）；具体算法交 Analyst 确认。
- 本任务默认不改变 access token 现有 1 小时有效期；是否缩短（Owner 所述「短期」）交 Analyst/Owner 确认。

### OPEN QUESTION

- 本任务是否应基于含 IAM V3 的 `develop` 建立基线，还是基于当前 HEAD（不含 IAM V3）继续：两者会影响「reuse 撤销 family 是否联动登出所有设备」的实现基础与后续合并。此问题需 Owner 在开始前明确（见「Review Baseline」与「Analyst Questions」第 11 条）。

## Verification

- AC-001 → 需 MySQL + Redis（`docker compose up -d` 后）；调用 `/login`，断言响应含 access_token 与 refresh_token；查库确认 refresh token 对应的存储值为哈希（长度/格式符合哈希约定）而非明文，且明文仅在响应中出现一次、日志/错误中不出现。
- AC-002 → 需 MySQL + Redis；用 refresh token 调 `/refresh`，断言返回新 access + 新 refresh；再用旧 refresh token 调 `/refresh` 断言失败（已失效）。
- AC-003 → 需 MySQL + Redis；构造同 family 内「已轮换」旧 token 再次提交，断言返回 reuse 错误，且查库确认该 family 全部记录被撤销、任一后代 token 后续均无法换取新 token。
- AC-004 → 需 MySQL + Redis；按「被盗重放」时序构造攻击者旧 token + 合法用户新 token，重放旧 token 后断言整个 family 撤销、合法用户新 token 亦失效（需重新登录），攻击者无法换取新 access。
- AC-005 → 需 MySQL；用过期 refresh token 调 `/refresh` 断言过期错误；用短 TTL 测试场景验证 access 与 refresh 生命周期独立。
- AC-006 → 需 MySQL + Redis；无效/篡改/未知 refresh token 调 `/refresh`，断言统一拒绝语义且不泄露存在性。
- AC-007 → 需 MySQL + Redis；并发提交同一 refresh token（多 goroutine 或并发请求），断言仅一次轮换成功、无重复换新；关键并发场景按风险运行 `go test -race`。
- AC-008 → 需 MySQL + Redis；构造 access 过期但 refresh 有效，用 refresh 换取新 access 后访问受保护接口 200。
- AC-009 → 按 Contract 断言各 refresh 失败场景的 `code` 与 HTTP 状态，body 保持 `{code,message,data}`，且不返回哈希/family 标识。
- AC-010 → 确认 `docs/design/iam.md`、`docs/design/migration.md` 覆盖 refresh 数据模型/状态机/并发/安全边界，并与 APPROVED Contract、实现一致（由 Cleaner 审查）。
- AC-011 → 用可逆 Mutation 或专项测试证明「不撤销 family」或「并发多次轮换」的错误实现会使该测试失败。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；涉及 MySQL/Redis 的集成验证需说明环境（依赖容器就绪）。

## Complexity

COMPLEX

原因：引入新数据模型（refresh token 哈希 + token family 血缘）、新公开协议（`/refresh` 与登录响应扩展）与重要安全边界（轮换、重用检测、被盗检测），并涉及并发一致性（同一 refresh token 并发刷新仅一次轮换）。存储介质、family 血缘表示、reuse 撤销范围、身份域范围等多个现实方案会产生不同安全与运维结果，需 Analyst 定向调查并起草 `contract.md`，由 Owner 确认关键方案后再交 Coder。

## Analyst Questions

Analyst 应调查并由 Owner 确认的关键问题：

1. refresh token 的存储介质与表结构：MySQL（跨 Redis 重置存活）还是 Redis；`refresh_tokens` 表字段（token_hash、family_id、父/代际、user_id/type、expires_at、revoked 等）与唯一索引/约束。
2. refresh token 的格式、熵与长度（crypto/rand 生成、编码）；哈希算法（高熵随机串用 SHA-256 还是其他）。
3. token family 的血缘表示：family_id + 父子链 vs family_id + generation 计数；reuse 判定如何定位「已轮换的旧后代」。
4. reuse detection 的撤销范围：识别到重放时撤销整个 family（含最新后代）还是仅该旧 token；是否联动登出所有相关会话。
5. access token 有效期是否调整（保持 1h 还是缩短），refresh token 有效期取值（如 30 天）与是否绝对过期。
6. 身份域范围：refresh token 是否同时覆盖前台用户域与后台管理员域，还是仅前台用户域（两域当前完全隔离）。
7. `/refresh` 契约：公开接口、请求体携带 refresh token 的字段、响应字段（access_token/refresh_token/expires_in）。
8. 并发刷新的一致性机制（条件 UPDATE / 事务 / Redis Lua）与结果语义：仅一个成功轮换，其余幂等返回同一新 token 还是明确拒绝。
9. `/logout` 与 refresh 的关系：登出是否同时撤销 refresh token family；access 会话撤销与 refresh family 是否独立生命周期。
10. 错误码分配：refresh 无效/过期/已轮换（reuse）等新错误码（沿用 IAM 2000 段扩展还是新开段），及其 HTTP 状态。
11. 版本基线：本任务基于当前 HEAD（不含 IAM V3 多设备会话）还是基于已合入 `develop` 的 IAM V3（见「版本基线分歧」与 OPEN QUESTION），该决定影响第 4 条的实现基础。

## Review Baseline

- Base commit：`1db733d454ae967702bc87f080a03195123cdc02`（分支 `feat/iam-v4`）
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）
- 重叠修改的区分方式：不适用（无已有未提交修改）
- 版本基线分歧：当前 HEAD 落后 `origin/develop` 32 个提交，且**不含** IAM V3（多设备会话管理，`.agent/tasks/iam-v3/`、提交 `0291413`）；如 Owner 决定基于 `develop`（含 IAM V3）开展，则需先合并/变基，并重新确认基线与本任务与 IAM V3 的重叠边界。

## Initial Route

READY_FOR_ANALYST
