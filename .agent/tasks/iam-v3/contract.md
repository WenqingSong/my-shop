# Technical Contract

## Decision Status
APPROVED

## Problem

用户需要查看并主动管理自己的全部登录会话（多设备）：查看会话列表、撤销指定设备、撤销其他设备、全部退出。当前会话模型只有「单 key 无 user 维度索引」，无法枚举某用户全部会话；且 `token_version` 与 `jti` 两个架构取舍尚未定论，身份域覆盖范围（仅前台 / 前台+后台）未确认。

## Verified Current Behavior

- VERIFIED：会话为 Redis Hash `iam:session:{sid}`，字段 `user_id`（十进制字符串）、`revoked`（"0"/"1"），TTL `auth.session.ttl` 默认 3600（`internal/auth/session.go`）。后台管理员对称但完全隔离：`iam:admin:session:{sid}` + `admin_id`。
- VERIFIED：**无任何 user/admin 维度会话索引**，无法枚举某用户全部会话（`internal/auth/session.go` 仅 `SessionKey(sid)` 单 key）。
- VERIFIED：JWT `Claims{Sid, Type}` 内嵌 `gojwt.RegisteredClaims`（含标准 `ID`/jti 字段但未使用）；签发 `sub/iss=surgecart/iat/exp=iat+3600`，无 `token_version` 声明（`internal/auth/jwt.go`）。
- VERIFIED：鉴权中间件 `Auth`（验签+exp → `ValidateSession` 存在且未撤销且 `user_id==sub`）；`AuthSignatureOnly`（仅验签，供 `/logout` 幂等撤销）；Redis 查询失败一律 fail-closed 401（`internal/middleware/auth.go`）。
- VERIFIED：`/logout` 仅撤销当前 token 的 sid（`Principal.Sid`），Lua 原子置 `revoked=1`，幂等，key 保留至 TTL（`internal/logic/iam/iam.go`、`internal/auth/session.go`）。
- VERIFIED：服务接口 `IIam` 无会话列表/批量撤销方法（`internal/service/iam.go`）。
- VERIFIED：`users`/`admins` 表无 `token_version` 列（baseline `20261001000001`）；迁移机制 14 位时间戳 `.up.sql`。
- VERIFIED：错误码集中 `internal/codes/codes.go`；2001-2010 已全部分配（2001/2002 用户域，2003-2010 管理员域）；通用码 1002=401、1003=403、1004=404 存在。
- VERIFIED：响应统一 `{code,message,data}`，成功 data 可空（`internal/middleware/response.go`）。
- UNKNOWN：会话元数据（登录时间、User-Agent、IP）当前不存储；「区分设备」所需字段需新增。
- UNKNOWN：Redis 客户端（goframe `g.Redis()`，底层 go-redis v9）是否直接暴露 ZADD/ZRANGE 便捷方法，实现时以 `Do` 或适配器方法落地（非阻塞，可验证）。

## Recommendation

RECOMMENDATION：仅覆盖前台用户域；不引入 `token_version` 与标准 `jti`；新增 user→sessions 的 **Redis ZSET 索引** `iam:user:{userID}:sessions`（member=sid，score=登录 unix 秒），并将会话元数据（`login_at`、`user_agent`、`ip`）写入 session Hash；撤销沿用「逻辑标记 `revoked=1`、保留 key 与 TTL」的既有语义。

关键取舍：

1. **身份域**：仅前台用户域（推荐）。任务 Goal/AC 均以「用户」为主体，后台管理员域已具备对称会话模型但引入它会扩大 Scope 且缺少明确 AC；可作为后续任务。若 Owner 选择「前台+后台」，需补充后台对称实现与 AC。
2. **不引入 `token_version`**：其收益（O(1) 全部失效、改密即全下线）在本任务无对应需求（改密不在 Scope），且代价明显——`users` 新列迁移、JWT 新声明、以及**前台 `Auth` 每请求新增一次 DB 查询**（当前前台鉴权只查 Redis，不查 DB，见 `internal/middleware/auth.go`）。更重要的是它引入与既有「撤销=逻辑标记 `revoked=1`」不一致的第二套失效机制，与 AC-003/004/005 的验证断言（Redis `revoked=1`）冲突。ZSET 索引 + 逐会话逻辑撤销即可完整实现「全部退出」，且与既有语义一致。
3. **不引入标准 `jti`**：当前 1 次登录 = 1 会话 = 1 token，`sid` 已等价于「单 token 撤销」的唯一标识；无 refresh/轮换（Out of Scope），`jti` 与 `sid` 冗余。保留 `sid` 作为唯一会话/token 标识；未来引入 token 轮换时再评估。
4. **撤销失败语义**（见下方 Owner Decision）：revoke-one 先按 session Hash `user_id` 校验归属；非本人/不存在统一返回 `404 SESSION_NOT_FOUND`（新码 `2011`），不泄露存在性与归属；已撤销的本人会话幂等返回 200。
5. **index 一致性**：session Hash 是「归属 + 撤销状态」的权威事实，index 是枚举优化；读取时以 Hash 为准过滤已撤销/已过期项（index 允许短暂含 stale 项，最终一致，无后台清理，TTL 自愈）。

该方案完整满足 Goal 与 AC-001~AC-010；AC-011 的 `token_version`/`jti` 结论为「均不引入」，据此写入 Contract 并交 Owner 确认。

## Selected Design

经 Owner 确认（2026-10-04），最终设计如下：

1. **身份域**：仅前台用户域。后台管理员域保持现有隔离模型，不扩展 Session 管理；后续如有需求另立任务。
2. **撤销语义**：
   - 本人 active session 撤销成功 → 200/0；
   - 本人已 revoked session 重复撤销 → 幂等 200/0；
   - session 不存在或不属于当前用户 → 统一 404/2011（`SESSION_NOT_FOUND`），不泄露资源存在性；
   - session TTL 到期后再次撤销视为不存在 → 404/2011。
3. **会话失效机制**：不引入 `token_version` 与标准 `jti`；继续以 `sid + Redis Session revoked` 为唯一失效机制。未来 Access/Refresh、Refresh Rotation、Reuse Detection 或单 Token 撤销出现真实需求时再引入。
4. **批量撤销语义**：
   - `POST /sessions/revoke-others` 不撤销当前请求的 sid；
   - `POST /sessions/revoke-all` 包含当前 sid，本次接口仍正常返回（200/0），后续请求开始鉴权失败。
5. **索引一致性**：`iam:user:{id}:sessions` ZSET 只是会话索引，Session Hash 才是权威事实；列表发现 ZSET 中 sid 对应 Hash 已过期/不存在时，视为 stale member 并允许清理，不得把索引残留当成有效 Session。

## Interfaces and Data

### 新增前台接口（均挂 `Auth` 中间件，复用 `/me` 的完整会话校验）

| 方法 | 路径 | 作用 | 成功响应 |
| --- | --- | --- | --- |
| GET | `/sessions` | 会话列表 | `data: {items: [{sid, login_at, user_agent, ip, current}]}` |
| DELETE | `/sessions/{sid}` | 撤销指定会话 | `data: null`（200/0） |
| POST | `/sessions/revoke-others` | 撤销除当前外的全部 | `data: null`（200/0） |
| POST | `/sessions/revoke-all` | 全部退出（含当前） | `data: null`（200/0） |

- `sid`（撤销目标）来自 URL 路径；「当前 sid」来自 `Principal.Sid`，**绝不接受请求体中的身份/sid**（与 `/logout` 一致，见 `internal/middleware/principal.go`）。
- `current` 判定 = `item.sid == Principal.Sid`。
- 会话元数据字段（写入 session Hash，追加到现有 `user_id`/`revoked`）：
  - `login_at`：登录 unix 秒（必需，AC-002 最低要求）。
  - `user_agent`、`ip`：设备信息（推荐，用于「区分设备」；由 Controller 从 HTTP 请求提取后传入 logic）。
- 登录写序：先 `HSET` session Hash（含 `user_id`、`revoked=0`、`login_at`、`user_agent`、`ip`）+ `EXPIRE`，再 `ZADD` 索引 + `EXPIRE` 索引 key（session 先行、索引为提交点）。

### Redis 数据结构

- `iam:session:{sid}`（Hash，现有）：新增 `login_at`、`user_agent`、`ip` 字段；`user_id`/`revoked` 不变。
- `iam:user:{userID}:sessions`（ZSET，新增）：member=sid，score=登录 unix 秒；每次登录 `ZADD` 后 `EXPIRE` 至 `auth.session.ttl`。
- 撤销仍为逻辑标记：`HSET revoked=1`，**不物理删除、不清 TTL**（沿用 IAM V2）。

### 服务接口扩展

`IIam`（`internal/service/iam.go`）新增：`ListSessions(ctx, userID, currentSid)`、`RevokeSessionByID(ctx, userID, targetSid)`、`RevokeOtherSessions(ctx, userID, currentSid)`、`RevokeAllSessions(ctx, userID)`。Controller 从 `Principal` 取 `UserID`/`Sid` 传入，logic 不处理 HTTP 细节。

### 错误码

- 新增 `CodeSessionNotFound = 2011` → 404「会话不存在」（IAM 用户域，沿用「域专用 404 码」惯例：2003/2007/2009）。
- 复用：Redis 故障 → 1002/401（鉴权 fail-closed）或 1000/500（数据操作失败，见失败语义）；sid 格式非法 → 1001/400。

## Business Invariants

- INV-001（列表归属隔离）：会话列表只返回当前用户自己的会话，绝不返回他人会话；每个 item 必须经「session Hash `user_id == current` 且未撤销且存在」校验。
- INV-002（越权撤销防护）：未授权/伪造 sid 不能撤销非本人会话；revoke-one 写前校验 `user_id` 归属，非本人/不存在 → 404 且无任何写入，不泄露会话存在性与归属。
- INV-003（撤销幂等且逻辑标记）：撤销仅置 `revoked=1` 并保留 key 与 TTL，不物理删除；重复撤销本人会话无副作用（200/0）。
- INV-004（身份域隔离不变）：本任务只改前台用户域；后台管理员域（`iam:admin:session:`、`AdminAuth`、`/admin/logout`）行为不变。
- INV-005（fail-closed）：Redis 故障时受保护接口 401；列表/撤销在数据操作阶段 Redis 失败返回 500 + 日志，绝不返回虚假空列表/虚假成功。
- INV-006（index 最终一致）：session Hash 是归属与撤销状态权威，index 为枚举优化；读取以 Hash 为准过滤失效项；ZSET 中 sid 对应 Hash 已过期/不存在视为 stale member 并允许清理，不得把索引残留当成有效 Session，允许 index 短暂 stale，TTL 自愈。

## Failure and Consistency Semantics

- 事实来源：MySQL `users`（身份）；Redis `iam:session:{sid}`（会话归属 + 撤销状态，权威）；Redis `iam:user:{userID}:sessions`（枚举索引，非权威）；JWT（签名凭证，非事实来源）。
- 登录成功 = 已写 session Hash + 已写 index + 已签发 token；写 Redis 任一步失败 → 500/1000，不签发 token（保持「返回的 token 必有有效 session」）。
- 列表成功 = 返回过滤后全部有效会话；Redis 枚举/读取失败 → 500/1000 + 日志（不返回空列表，防 fail-open）。
- 撤销成功 = 目标会话 `revoked=1`（含当前，若是 revoke-all）；Redis 写失败 → 500/1000 + 日志（不吞错误伪报成功）。
- 撤销幂等：revoke-one 对本人已撤销会话重复调用 → 200/0；对非本人/不存在 → 404/2011（无写入）。
- 批量撤销（revoke-others/revoke-all）：用 Lua 原子「读取 index → 逐个 `HSET revoked=1` → 清空 index」；Redis 单线程保证脚本内原子。与「并发登录」的边界：脚本执行后才完成的新登录（新 sid）自然幸存，属正确行为（登录发生在撤销之后）。
- 重复/乱序/重试：撤销操作天然幂等；无 MQ、无异步、无跨系统事务。

## Allowed / Forbidden Changes

- 允许：新增前台 4 个接口与 `IIam` 方法；新增 Redis ZSET 索引与会话元数据字段；新增错误码 2011；更新 `docs/design/iam.md`。
- 禁止：修改 `/register`、`/login`、`/logout`、`/me` 的既有公开行为与语义；改动后台管理员域（`/admin/*`、`iam:admin:session:`、`AdminAuth`）；引入 `token_version` 列/声明、标准 `jti` 声明；物理删除 session 或改变 TTL/撤销语义；引入管理员跨用户强制下线。

## Verification Requirements

- INV-001 → 需 Redis：同一用户登录多次后 `GET /sessions`，断言返回全部且 `current` 唯一正确；换另一用户 token 调用，断言列表不含对方会话。
- INV-002 → 需 Redis：用户 A 的 token `DELETE /sessions/{B的sid}` 与非法 sid，断言 404 且 Redis 无变化（`HGETALL` 目标 session `revoked` 不变）。
- INV-003 → 需 Redis：`DELETE /sessions/{sid}` 后断言 `revoked=1` 且 key/TTL 保留；重复撤销返回 200/0。
- INV-004 → 回归：`/admin/login`、`/admin/logout`、`/admin/me` 与后台会话隔离测试保持通过（`internal/cmd/identity_isolation_test.go`）。
- INV-005 → 需 Redis：停 Redis 或注入类型错误，断言受保护接口 401、列表/撤销不返回虚假成功（500/1000）。
- AC-004/005 → 需 Redis：分别触发 revoke-others/revoke-all，用旧 token 访问 `/me` 断言 401，保留会话仍 200；Redis 中断言各 session `revoked=1`。
- AC-011 → `token_version`/`jti` 结论「均不引入」写入 Contract 并由 Owner 确认。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；关键不变量（INV-002/INV-003）按风险跑 `go test -race`。

## Open Risks

- AC-006（「不存在 → 返回错误」）与 AC-007（「已不存在 → 幂等」）在「不存在会话」上的措辞张力，需 Owner 确认最终语义（见 Owner Decision 2）。
- index 含 stale 项的窗口期，列表需逐条过滤，实现须保证过滤逻辑不遗漏（否则泄露已过期会话或误标 current）。
- 若 Owner 选择「前台+后台」域，则本 Contract 需扩展后台对称实现与对应 AC，当前推荐未覆盖。

## Owner Decision Record

- 2026-10-04 Owner 确认：
  1. IAM V3 仅覆盖前台用户 Session 管理；后台管理员域保持现有隔离模型，后续如有需求另立任务。
  2. 撤销语义四档：本人 active → 200；本人已 revoked → 幂等 200；不存在或非本人 → 统一 404/2011、不泄露存在性；TTL 到期后再次撤销视为不存在 → 404。
  3. 不引入 `token_version` 与 `jti`；`sid + Redis Session revoked` 为唯一失效机制，未来有真实需求再引入。
  4. `revoke-others` 不撤销当前 sid；`revoke-all` 含当前 sid 且本次正常返回，后续请求鉴权失败。
  5. ZSET 仅会话索引非权威；Hash 过期/不存在的 sid 视为 stale member 并允许清理。
