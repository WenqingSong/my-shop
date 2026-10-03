# Technical Contract

## Decision Status
APPROVED

## Problem

在现有「无状态 JWT access token + Redis 有状态会话」之上引入长期 Refresh Token：登录同时签发 access token 与长期 refresh token（仅哈希落库）；刷新执行轮换（rotation），以 Token Family 追踪血缘；检测到「已轮换 token 被重放」时撤销整个 family；并发刷新仅一次轮换。本 Contract 固化 Owner 已确认的存储介质、family 血缘表示、reuse 撤销范围、身份域范围、Access Session 与 Refresh Family 关系及并发一致性语义。

## Verified Current Behavior

- VERIFIED：当前 HEAD `5541b4a`（`docs(iam-v4): 添加 IAM V4 任务文档`），`feat/iam-v4` 相对 `origin/develop` 领先 1、落后 0；IAM V3（`0291413`）是当前 HEAD 祖先。task.md 中「版本基线分歧/OPEN QUESTION/Review Baseline」已过时，无需抉择基线。
- VERIFIED：单 access token（JWT HS256，`Claims{Sid,Type}` + `sub/iss=surgecart/iat/exp=iat+3600`），无 refresh/family/reuse（`internal/auth/jwt.go`）。
- VERIFIED：Redis 会话（`internal/auth/session.go`）：前台 `iam:session:{sid}` Hash（user_id/revoked/login_at/user_agent/ip）、管理员 `iam:admin:session:{sid}`；TTL `auth.session.ttl` 默认 3600；前台 `iam:user:{id}:sessions` ZSET 索引 + `RevokeSession/RevokeOtherSessions/RevokeAllSessions` 均已具备。
- VERIFIED：登录（`internal/logic/iam/iam.go` `Login`）：bcrypt → `NewSid` → `CreateSession` → `Generate` → `{access_token, token_type, expires_in:3600}`；写 session 失败→500。
- VERIFIED：身份域四要素隔离（`docs/design/iam.md` §5.1）；IAM V3 多设备会话仅前台用户域。
- VERIFIED：迁移 golang-migrate v4 嵌入，Registry 最新 `20261001000006`（ACTIVE）；错误码 IAM 域 2000-2999 已用 2001-2011，2012 起空闲；IAM 域在 Registry 为 ACTIVE。
- VERIFIED：配置 `auth.jwt.secret`/`auth.session.ttl`；无 refresh 配置。

## Recommendation

RECOMMENDATION：MySQL 持久化 `refresh_tokens`（仅存 SHA-256 哈希）+ 每次登录独立 Token Family（`family_id`+`parent_id`+`generation`）+ 轮换用「事务内条件 UPDATE + InnoDB 行锁」保证并发仅一次轮换 + 已轮换 token 重放统一判定 reuse 并全量撤销，仅覆盖前台用户域。已由 Owner 确认并固化为下述 Selected Design。

## Selected Design

（已由 Owner 批准）

1. **身份域范围**：refresh 仅覆盖前台用户域（`type=user`），不扩展后台管理员域；`/refresh` 为公开前台接口，不挂 Auth，签发的新 access token 恒为 `type=user`。
2. **并发语义**：不引入 grace period。同一 refresh token 并发提交，首个成功请求完成 rotation；后到请求因条件 UPDATE 影响 0 行（旧 token 已 rotated）统一按 reuse detection 处理。客户端必须避免并发复用同一旧 refresh token。
3. **撤销联动**（区分三种场景，不接受统一为「撤销全部 access sessions」）：
   - 普通 logout：撤销当前 access session + 当前登录对应的 refresh family，不影响其它设备的 session/family。
   - revoke-all：撤销该用户全部 access sessions + 该用户全部 refresh families。
   - refresh token reuse：撤销该用户全部 refresh families + 该用户全部 access sessions，强制重新认证（真正「全部重新登录」）。
4. **Access Session 与 Refresh Family 关系**：`一次登录 = 一个 sid 会话 + 一个 refresh family`，一一对应。Refresh rotation 只轮换 refresh credential 并签发新 access token，不创建新的「设备登录 Session」；同一登录生命周期内 `family_id` 不变；通过 `refresh_tokens.sid` 建立「当前 session → 对应 refresh family」的追踪关系，支撑 logout / revoke-all / reuse 的准确撤销。
5. **不引入** `token_version` 与 `jti`。

其余推荐默认已确认：MySQL 仅存 SHA-256 哈希、refresh TTL 30 天、错误码 2012/2013/2014、migration `20261001000007`。

## Interfaces and Data

### 新增表 `refresh_tokens`（migration `20261001000007`）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键自增 |
| `token_hash` | CHAR(64) | SHA-256(明文) hex，`uk_token_hash` 唯一 |
| `family_id` | VARCHAR(32) | 登录时生成的 16 字节 hex；同 family 共享、登录生命周期内不变 |
| `user_id` | BIGINT UNSIGNED | 前台用户 id（仅前台域） |
| `parent_id` | BIGINT UNSIGNED NULL | 父 token id，根为 NULL |
| `generation` | INT NOT NULL DEFAULT 0 | 代际计数（根=0，每次轮换 +1） |
| `sid` | VARCHAR(32) | 本次登录绑定的 access session sid（family 内所有行同值；session→family 追踪键） |
| `expires_at` | DATETIME NOT NULL | 绝对过期时间（签发时刻 + 30 天） |
| `revoked_at` | DATETIME NULL | 轮换/撤销时间，NULL=有效 |
| `revoked_reason` | VARCHAR(16) NULL | `rotated`（被轮换）/`revoked`（被主动撤销），NULL=有效 |
| `created_at`/`updated_at` | DATETIME | 默认 CURRENT_TIMESTAMP |

索引：`uk_token_hash(token_hash)` 唯一；`idx_family(family_id)`；`idx_user(user_id)`；`idx_sid(sid)`。引擎/字符集沿用基线（InnoDB/utf8mb4_unicode_ci），无外键。

### 公开接口

- `POST /login` 响应扩展：新增 `refresh_token` 字段（`{access_token, refresh_token, token_type:"Bearer", expires_in:3600}`）。
- 新增 `POST /refresh`（公开，无 Auth）：请求 `{refresh_token}`；成功 `{access_token, refresh_token, token_type:"Bearer", expires_in:3600}`。
- `/logout`、`DELETE /sessions/:sid`、`/sessions/revoke-others`、`/sessions/revoke-all` 行为升级：在撤销 access session 之外联动撤销被撤销 session 对应的 refresh family（公开响应契约不变）。

### refresh token 格式与哈希

- 明文：`crypto/rand` 32 字节 → hex 64 字符；仅本次响应出现一次，不进日志/错误/持久介质。
- 落库：`token_hash = SHA-256(明文)` hex 64 字符（`crypto/sha256`，高熵随机串用快速哈希）。

### 配置

- 新增 `auth.refresh.ttl` / `AUTH_REFRESH_TTL`（默认 2592000 秒 = 30 天，必须 > 0）。access token 有效期保持 3600 不变。

## Business Invariants

- INV-001（明文仅一次）：refresh token 明文仅出现在签发响应一次；持久化仅存 SHA-256 哈希；日志/错误/任何持久介质不出现明文。
- INV-002（并发仅一次轮换）：同一 refresh token 并发/重复提交至多一次轮换成功（事务内条件 UPDATE + `uk_token_hash` 唯一兜底），family 内任意时刻至多一个「未撤销且未过期」的有效后代。
- INV-003（reuse 全量撤销）：已轮换（`reason='rotated'`）token 再次提交 → reuse，撤销该用户全部 refresh families + 全部 access sessions，强制重新认证，不产生任何新 token。
- INV-004（身份不可伪造）：refresh 只按 `token_hash` 对应 `user_id` 签发 `type=user` 新 access token，不信任请求体身份；不能用 refresh 换取他人身份或 `type=admin` token。
- INV-005（身份域隔离）：refresh 仅覆盖前台用户域；`type=admin` token 不能用于 `/refresh`，前台 refresh 不能换取 admin 凭据。
- INV-006（一次登录一个 family）：一次登录 = 一个 sid 会话 + 一个 refresh family；refresh rotation 复用同一 sid、不新增「设备登录 Session」；`family_id` 在登录生命周期内不变，`sid` 恒可追踪到该 family。

## Failure and Consistency Semantics

- 事实来源：`refresh_tokens`（哈希、血缘、revoked/过期）以 MySQL 为权威；access 会话有效性以 Redis session 为权威（沿用现状）；JWT 为签名凭证、非事实来源。
- refresh 成功 = 旧 token 已 rotated（事务内）+ 新 access token / 新 refresh token 已签发 + 对应 sid 的 session 已确保存活（upsert）。
- refresh 处理顺序（避免「已轮换却报失败」导致重试误判 reuse）：① 读 `refresh_tokens` 判定（invalid/expired/reuse/有效）→ ② 有效则 session upsert（Redis）→ ③ 事务内条件 UPDATE 旧 token（`WHERE token_hash=? AND revoked_at IS NULL AND expires_at > NOW()`）+ INSERT 新后代（同 sid、同 family_id、parent_id=旧 id、generation+1）→ ④ 签新 access token（同 sid）→ ⑤ 返回。步骤 ② 失败 → 500（未轮换，可重试）；步骤 ③ 失败（并发 reuse/过期）→ 回滚并返回对应错误。
- session upsert：session 存在且未 revoked → 仅续期 TTL 至 `auth.session.ttl`（保留 login_at/UA/IP）；session 不存在（已过期）→ 以同 sid 重建（login_at=当前时间，UA/IP=本次请求），不新增会话列表条目；session 已 revoked（防御性，理论不出现）→ 拒绝 refresh（2012）。
- 并发：InnoDB 行锁串行化同一 `token_hash` 的条件 UPDATE，后到事务在首事务提交后重估条件 → 影响 0 行 → 判定 reuse（INV-003）。
- 判定顺序：hash 不存在 → `2012 INVALID`（不泄露存在性）；`reason='rotated'` → `2014 REUSE`（撤销全部 families + 全部 access sessions）；`revoked_at` 非空（`reason='revoked'`）→ `2012 INVALID`；`expires_at < NOW()` → `2013 EXPIRED`；否则轮换。
- MySQL 写失败 → 500 `1000`，不签发 token、不伪造成功。
- 撤销联动（均以「sid→family_id」追踪）：
  - logout / DELETE `/sessions/:sid`：撤销目标 sid 的 session + 该 sid 对应 family（全部未撤销成员置 `revoked`）。
  - revoke-others：撤销除当前外的各 sid 的 session + 各自 family。
  - revoke-all：撤销该用户全部 sid 的 session + 该用户全部 families。
  - reuse：撤销该用户全部 sid 的 session + 该用户全部 families。
  - Redis session 撤销沿用现状 Lua 原子；family 撤销为 MySQL 更新（幂等：已 revoked 成员跳过）。

## Allowed / Forbidden Changes

- 允许：新增 `refresh_tokens` 表迁移（`20261001000007`）；新增 `POST /refresh`、错误码 2012/2013/2014、`auth.refresh.ttl` 配置；`/login` 响应新增 `refresh_token`；`/logout`、`DELETE /sessions/:sid`、`revoke-others`、`revoke-all` 联动撤销对应 family；更新 `docs/design/iam.md` 与 `docs/design/migration.md`。
- 禁止：改变 `/register`、`/health`、`/me` 既有行为；改变 access token 现有 1h 有效期与 JWT 声明契约；改变既有错误码取值/HTTP 状态（已落地码 immutable）；引入滑动续期、token_version、jti、管理员域 refresh、RBAC/限流/验证码；把 refresh 明文落库/写日志/写错误响应；改变两域隔离四要素；refresh 时创建新的设备登录 Session（新 sid）。

## Verification Requirements

- INV-001 → 集成测试：login 后查 `refresh_tokens` 断言 `token_hash` 为 64 hex 且 != 明文；检索日志/错误响应无明文。
- INV-002 → 并发测试：多 goroutine 提交同一 refresh token，断言仅一个成功、其余 reuse，family 无多个未撤销后代；关键场景 `go test -race`。
- INV-003 → 专项测试（AC-003/004/011）：轮换后重放旧 token，断言该用户全部 refresh families 全部 revoked、最新后代失效、该用户全部 access sessions 被撤销（`/me` 401），且可逆 Mutation 证明「不撤销 family」的错误实现失败。
- INV-004/005 → 测试：A 的 refresh 不能换 `sub=B` 或 `type=admin`；`type=admin` token 调 `/refresh` 被拒；前台 refresh 换的 access token `type=user`。
- INV-006 → 测试：refresh 后 `sid` 不变、会话列表仍为一条目、`family_id` 不变、新后代继承 family。
- 撤销联动 → 测试：logout 仅撤销当前 session+family 不影响其它；revoke-all 撤销全部 sessions+families；DELETE `/sessions/:sid` 撤销指定 session+family。
- AC-005/006 → 测试：过期 → `2013`；无效/篡改/未知 → 统一 `2012` 不泄露存在性。
- AC-010 → Cleaner 核对 `docs/design/iam.md`/`migration.md` 与 APPROVED Contract、实现四者一致。

## Open Risks

- 并发刷新同一 token 触发 reuse 全量撤销（安全优先），客户端需「每次刷新即用新 token、不并发复用旧 token」。
- refresh 复用同一 sid + session upsert：access token 有效窗口仍受 `min(JWT exp, session TTL)` 约束，refresh 时 session 不存在则重建（login_at 更新为 refresh 时刻，属可接受边界）。
- 管理员域本期不纳入 refresh；后续若需要，长期 refresh 与「禁用即时失效」的语义张力需单独设计。

## Global Resources

- migration version：`20261001000007`（`refresh_tokens`）——RESERVED（`next = max(Registry) + 1`），需经 Registry commit 生效。
- 错误码：IAM 域（2000-2999，ACTIVE）内新增 `2012`/`2013`/`2014`，无新域 Reservation。

## Owner Decision Record

- 决定 1：仅覆盖前台用户域，不扩展后台管理员域。APPROVED。
- 决定 2：并发刷新不引入 grace period，后到者统一按 reuse detection。APPROVED。
- 决定 3：撤销联动区分 logout（当前 session+family）/ revoke-all（全部 sessions+families）/ reuse（全部 families + 全部 sessions，强制重登）。APPROVED。
- 决定 4：一次登录 = 一个 sid 会话 + 一个 refresh family；refresh 复用 sid 不新增设备登录 Session；family_id 不变；session→family 可追踪。APPROVED。
- 决定 5：不引入 token_version 与 jti。APPROVED。
- 其余推荐默认（MySQL 仅存 SHA-256、refresh TTL 30 天、2012/2013/2014、migration `20261001000007`）。APPROVED。
