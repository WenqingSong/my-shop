# Technical Contract

## Decision Status

APPROVED

## Problem

把 IAM 从无状态 JWT 升级为「有状态会话」：登录在 Redis 建立 `iam:session:{sid}`，受保护接口在验签 + 校验 exp 后追加 session 有效性校验，新增 `POST /logout` 使 token 可撤销。核心设计问题集中在：session 数据模型与 TTL、sid 生成与 JWT 承载方式、Redis 故障时的安全边界（fail-open / fail-closed）、logout 与「已撤销 token」的失败语义与错误码。

## Verified Current Behavior

- VERIFIED：JWT 为 HS256 无状态。`internal/auth/jwt.go` 中 `Claims` 内嵌 `gojwt.RegisteredClaims`（已含标准 `ID`/jti 字段但未使用），签发 `sub`（用户 id 十进制字符串）/`iss=surgecart`/`iat`/`exp=iat+3600`；密钥来自 `auth.jwt.secret` / `AUTH_JWT_SECRET`，≥32 字节，启动校验。
- VERIFIED：认证中间件 `internal/middleware/auth.go` 解析 `Authorization: Bearer` → `auth.Parse`（验签 + `WithExpirationRequired` + `WithIssuer` + 限定 HS256）→ 解析 sub 为 UserID → 注入 `Principal{UserID}`；`internal/middleware/principal.go` 的 `Principal` 当前仅 `UserID int64`。
- VERIFIED：登录 `internal/logic/iam/iam.go` 校验凭据后 `auth.Generate(ctx, user.ID)` 签发 token，返回 `LoginRes{AccessToken, TokenType:"Bearer", ExpiresIn:3600}`；`internal/service/iam.go` 的 `IIam` 仅 `Register`/`Login`/`Me`。
- VERIFIED：错误码 `internal/codes/codes.go` 集中定义 0 / 通用 1000-1005 / IAM 2001-2002，`internal/middleware/response.go` 统一映射 code→HTTP 状态并保持 `{code,message,data}`；当前无「已撤销/session 失效」专用码。
- VERIFIED：Redis 已接入 `internal/boot/boot.go`（`redis.default.*` 配置 + 启动 `PING`），`go.mod` 已含 `gogf/gf/contrib/nosql/redis/v2` 与 `github.com/redis/go-redis/v9`（indirect）；`github.com/google/uuid v1.6.0` 为 indirect 依赖。IAM V1 逻辑本身不使用 Redis。
- VERIFIED：路由 `internal/cmd/cmd.go`：`/register`、`/login` 公开，`/me` 挂 `middleware.Auth`。
- VERIFIED：`boot.checkDependencies` 启动即 `PING` Redis，故现有集成测试（`internal/controller/iam/iam_test.go` 走 `boot.Bootstrap`）本就要求 Redis 就绪；`docker-compose.yml` 已编排 redis:7-alpine（独立 volume）。
- INFERENCE：CI/CNB DinD 环境按现有测试基线已提供 Redis（与 MySQL 同源就绪）；未直接验证 CI 是否对 Redis 做独立健康检查。

## Recommendation

RECOMMENDATION（关键取舍已由 Owner 确认，见 Selected Design）：

1. **sid 生成**：128-bit 密码学随机，`crypto/rand` 16 字节 → 32 位小写 hex。碰撞概率可忽略，不引入新依赖，不做碰撞重试。
2. **JWT 承载 sid**：`Claims` 新增自定义声明 `Sid string`（JSON 键 `sid`），不用标准 `jti`（sid 语义为会话 id，且自定义字段自文档化；Owner 已授权 Analyst 定字段名）。`auth.Generate`/`GenerateWithSecret` 增加 `sid` 参数。
3. **session 数据模型**：单 key `iam:session:{sid}`，值为 Redis Hash，字段 `user_id`（int64 字符串）与 `revoked`（"0"/"1"）。写入 `user_id` 用于与 JWT `sub` 交叉校验（纵深防御）。无 user 维度索引、无物理删除。
4. **TTL 与对齐**：session TTL 与 JWT 生命周期一致，默认 `3600` 秒，可经 `auth.session.ttl` / `AUTH_SESSION_TTL` 覆盖（须 >0）。无滑动续期（保持单 access token）。有效访问窗口 = min(JWT exp, session TTL)；任一到期访问受保护接口均 401。因 session 在 iat 之后写入，正常流程 Redis 过期略晚于 JWT exp，JWT exp 为主导失效点。
5. **鉴权中间件**：拆为两层——`Auth`（完整：验签+exp → 查 session 存在且未 revoked 且 `user_id==sub` → 注入 `Principal{UserID, Sid}`）用于 `/me`；另设仅验签的轻量中间件（验签+exp → 注入 `Principal{UserID, Sid}`，不查 session）用于 `/logout`，保证重复登出的幂等。
6. **Redis 故障语义**：鉴权中间件查 session 失败（Redis 不可用/超时）→ **fail-closed 返回 401**（code 1002），绝不放行，服务端记录底层错误日志；登录写 session 失败 → 视为登录失败，返回 500（1000 INTERNAL_ERROR，与现有 DB 失败处理一致）。
7. **错误码**：复用 `1002 UNAUTHORIZED`（401）覆盖「已撤销 token / session 缺失 / Redis 不可用」等所有鉴权拒绝场景，不新增错误码（安全统一、不回归 V1、不泄露内部状态）。`logout` 成功/重复登出/已撤销/session 缺失均返回 200/0（幂等）。

关键取舍：安全优先——Redis 故障时宁可让受保护接口全部拒绝（401 fail-closed），也不 fail-open 放行可能已撤销的 token；代价是 Redis 短时故障会暂时中断所有鉴权访问（可观测性通过服务端日志弥补，而非对外返回 5xx）。

## Selected Design

Owner 已确认（2026-09-27）：

1. **sid 生成**：`crypto/rand` 128-bit（16 字节）→ 32 位小写 hex；碰撞可忽略，不做碰撞重试。
2. **JWT 承载**：`Claims` 新增自定义声明 `Sid string`（JSON 键 `sid`，非标准 `jti`）；`auth.Generate(ctx, userID int64, sid string)` 签发。
3. **session 数据模型**：单 key `iam:session:{sid}`，Redis Hash 字段 `user_id`（int64 十进制字符串）、`revoked`（`"0"`/`"1"`）。
4. **TTL**：`auth.session.ttl` 秒，默认 3600，须 >0，环境变量 `AUTH_SESSION_TTL` 覆盖；无滑动续期；有效访问窗口 = min(JWT exp, session TTL)。
5. **鉴权中间件**：`Auth`（验签+exp → 查 session 存在且未 revoked 且 `user_id==sub` → 注入 `Principal{UserID, Sid}`）用于 `/me`；另设仅验签轻量中间件（验签+exp → 注入 `Principal{UserID, Sid}`，不查 session）用于 `/logout`，保证重复登出幂等。
6. **Redis 故障语义**：鉴权查询失败 fail-closed 返回 401（1002），服务端记录底层错误日志；登录写 session 失败 → 登录失败返回 500，不签发 token。
7. **错误码**：已撤销/缺失 session 复用 `1002 UNAUTHORIZED`（401），不新增独立错误码。
8. **logout**：受保护（仅验签中间件），从当前 token/Principal 取 sid，原子撤销（key 存在才置 `revoked=1`、保持剩余 TTL、不创建新 key；缺失视为已登出），成功/重复登出/已撤销/缺失均返回 200/0。

## Interfaces and Data

### 接口

- `POST /login`（公开）：成功后写 Redis session → 签发含 `sid` 的 JWT → 返回 `data:{access_token, token_type:"Bearer", expires_in:3600}`（响应字段不变，不新增 `sid` 字段；`sid` 仅在 JWT 声明内）。单 token 约束不变。
- `POST /logout`（受保护，仅验签轻量中间件）：body 无 sid 参数；从当前 token/Principal 取 sid，撤销对应 session，返回 200/0（data 为 null）。重复登出幂等。
- `GET /me`（受保护，完整 `Auth`）：返回 `data:{id,username}`，id 与 token `sub` 一致（行为不变，仅增加 session 门槛）。
- 代码接口变更：
  - `auth.Claims` 新增 `Sid string`（`json:"sid,omitempty"`）。
  - `auth.Generate(ctx, userID int64, sid string)` / `auth.GenerateWithSecret(secret []byte, userID int64, sid string)`。
  - `Principal` 新增 `Sid string`。
  - `service.IIam` 新增 `Logout(ctx context.Context, sid string) (*v1.LogoutRes, error)`。
  - `api/iam/v1` 新增 `LogoutReq`/`LogoutRes`。
  - session 的 key 格式、value 字段、TTL 与 创建/校验/撤销逻辑集中在单一模块（建议 `internal/auth/session.go`），中间件与 login/logout 均通过该模块访问，禁止散落 `iam:session:` 字面量。

### 数据

- Redis key：`iam:session:{sid}`，Hash：
  - `user_id`：用户 id 的十进制字符串。
  - `revoked`：`"0"`（未撤销）/ `"1"`（已撤销）。
- TTL：`auth.session.ttl` 秒，默认 3600，必须 >0。
- 无 `user:*` 维度的 sessions 索引；无物理删除。

## Business Invariants

- INV-001：每次登录成功产生的 token，其 `sid` 与 Redis 实际写入的 `iam:session:{sid}` 一致，且该 session 的 `user_id` 等于 token 的 `sub`（sid↔user 绑定一致）。
- INV-002：只有「验签通过、未过期、且 session 存在且未撤销」的请求能访问 `/me`；`/me` 返回的 id 必须等于 token 的 `sub`（升级不回归 V1，新增 session 门槛）。
- INV-003：撤销不可逆且保留至 TTL——`revoked` 仅 0→1；logout 后该 token 立即失效（后续访问 401）；session key 不被物理删除、保留至 TTL 自然过期。
- INV-004：登出只撤销当前 sid，不影响同一用户或其它用户的其它 sid（本次不登出全部设备）。
- INV-005：登出的目标 sid 只能来自验签后的 token/Principal，不接受请求体/参数指定；`/me` 只信任 `Principal.UserID`，不信任请求自带身份。
- INV-006：Redis 不可用/查询失败时，鉴权中间件绝不 fail-open，受保护请求一律 401。

## Failure and Consistency Semantics

- 事实来源：MySQL `users` 是用户身份（id/username/password_hash）的事实来源；Redis `iam:session:{sid}` 是会话状态（存在性 + revoked + user_id 绑定）的事实来源；JWT 是「sub + sid + exp」的签名凭证，不是事实来源。
- 登录（同步）：校验凭据 → 生成 sid → 写 Redis session（含 TTL）→ 签 JWT → 返回。写 Redis 失败 → 登录失败 500，不返回 token（保证「返回的 token 必有有效 session」）。若写 Redis 后签 JWT 失败（本地操作，几乎不可能），遗留孤儿 session 由 TTL 到期自愈，无安全影响。
- 鉴权（同步，每请求）：JWT 无效/过期 → 401（不触达 Redis）；JWT 有效但 session 缺失/已撤销/`user_id` 不匹配 → 401；Redis 错误 → 401（fail-closed）+ 服务端日志。
- logout（同步，幂等）：验签通过取 sid → 原子撤销（key 存在才置 `revoked=1`，保持剩余 TTL，不创建新 key；key 不存在视为已登出，直接成功）。重复登出 / 已撤销 / session 缺失均返回 200/0。
- 重试/重复：logout 幂等；登录重试产生新 sid、新 session（多会话并存，符合 AC-007）。
- 并发：同一用户多次登录 → 多个独立 sid，无冲突（无 user 索引）；并发 logout 同一 token → 撤销为原子操作（建议 Lua），结果一致。
- 部分完成：仅登录存在「Redis 已写、JWT 未签」的极窄窗口，后果为 TTL 自愈的孤儿 session，无正确性/安全问题。
- 无 MQ、无异步、无跨系统事务。

## Allowed / Forbidden Changes

允许：
- 新增 `POST /logout` 及 `api/iam/v1`、controller、service（`IIam.Logout`）、logic 对应改动。
- 修改 `auth.Claims`/`Generate` 签名、`Principal`、鉴权中间件（拆两层）、`internal/cmd/cmd.go` 路由（挂 logout）。
- 新增 `auth.session.*` 配置（默认值 + 环境变量覆盖）；集中维护 session 的 Redis 读写模块。
- 更新既有调用点与测试（`auth.Generate` 签名变化、登录/鉴权集成测试补 session 与 logout 用例、测试环境需清理 Redis）。

禁止：
- 不改变 `/register`、`/health` 的公开行为与响应格式（登录/鉴权升级必需除外）。
- 不引入 refresh token、token 自动续期、session 列表、登出全部设备、管理员强制下线、user 维度索引。
- 不对已撤销 session 物理删除（保留至 TTL 到期）。
- 不硬编码 Redis 地址或 JWT 密钥；不在任何路径 fail-open 放行未通过 session 校验的请求。
- 不新增客户端可提交 sid 的 logout 参数；不让 logout 依赖请求体中的身份信息。

## Verification Requirements

- INV-001 → Redis 就绪下登录成功，解码 token 取 sid，`redis-cli HGETALL iam:session:{sid}` 断言 `user_id==sub` 且 `revoked=0`、TTL>0。
- INV-002 → 有效 token 访问 `/me` 返回 200 且 id==sub；缺失/坏格式/坏签名/过期/错误 issuer/session 缺失/已撤销 均 401 且 data 为 null。
- INV-003 → 登录 → logout → `HGET` 断言 `revoked=1` 且 key 仍在、TTL 未清零；再用同 token 访问 `/me` 断言 401；短 TTL 场景确认到期后 key 消失且 `/me` 仍 401。
- INV-004 → 两次独立登录得两个 sid，登出其一，另一 token 访问 `/me` 仍 200。
- INV-005 → 无 token 访问 `/logout` 断言 401；构造带 sid 参数的请求体，确认 logout 忽略外部 sid（只撤销当前 token 的 sid）。
- INV-006 → 模拟 Redis 不可用（停容器或注入错误），访问 `/me` 断言 401（而非 200 或 5xx）；登录在 Redis 不可用时返回 500 且不签发 token。
- AC-008 → 断言 logout 成功/重复登出/已撤销访问 `/me` 的 `code` 与 HTTP 状态（均为 200/0 或 401/1002），body 保持 `{code,message,data}`。
- AC-009 → 代码检索无硬编码 Redis 地址/JWT 密钥；`AUTH_SESSION_TTL` 覆盖后重启生效。
- AC-010 → 实现落定且 Cleaner 通过后，交付 `docs/design/iam.md`（由 Coder 起草，Cleaner 审查与 Contract/实现一致）。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test ./...`；Redis 集成验证说明容器就绪；按风险对关键不变量运行 `go test -race`。

## Open Risks

- 复用 1002 覆盖「已撤销/缺失/Redis 故障」意味着客户端无法仅凭 code 区分「被登出」与「token 过期」；当前范围内撤销仅由客户端自身登出触发，区分价值低，属可接受取舍（若需区分，可新增 2003 类码，需 Owner 决定）。
- fail-closed 在 Redis 短时故障时中断全部鉴权访问，可用性下降，靠服务端日志与监控兜底，不对外暴露内部状态。
- 开发默认 `auth.jwt.secret` 已入库（沿用 V1，dev-only），生产须覆盖，属运维纪律。

## Owner Decision Record

Owner 于 2026-09-27 确认以下三项关键决定：

1. **Redis 故障语义**：鉴权中间件按 sid 查询失败时 fail-closed 返回 401（非 5xx）；登录写 session 失败视为登录失败。
2. **错误码**：已撤销/缺失 session 复用 `1002 UNAUTHORIZED`，不新增独立错误码。
3. **logout 幂等**：已撤销/缺失 session 时 logout 返回 200/0（幂等成功）。

其余设计细节（sid 字段名 `sid`、sid 生成方式、session TTL 默认值等）由 task「已确认约束」#1 委托 Analyst 决定，或属不改变 Scope/AC/公开行为的实现细节，已按 Recommendation 落地。

适用范围：仅 IAM V2 本任务；关键问题均已解决，Contract 标记 APPROVED，可交 Coder 实现。
