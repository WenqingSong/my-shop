# IAM 系统设计（JWT + Redis 有状态会话）

本文面向项目接手者，说明 IAM 的架构、数据模型、鉴权/登出/撤销流程与安全边界。协作过程文件（`task.md`、`contract.md`）与此文档职责不同，本文沉淀最终设计，随实现一同维护。

## 1. 概述

IAM 采用「无状态 JWT 签名 + 有状态 Redis 会话」的混合模型：

- **JWT（HS256）**：作为访问凭证，声明 `sub`（用户 id）、`iss`、`iat`、`exp` 与自定义声明 `sid`（会话 id）。JWT 本身不可撤销。
- **Redis 会话**：以 `sid` 为桥，在 Redis 中维护会话状态（存在性 + 撤销标记 + 用户绑定）。受保护接口在验签与校验 `exp` 之后，还必须通过 Redis 会话有效性校验才放行。
- **登出**：通过将对应会话逻辑标记为 `revoked` 实现 token 可撤销，session 记录保留至 TTL 自然过期，不物理删除。

单 access token，无 refresh token、无滑动续期、无会话列表/登出全部设备/强制下线。

本系统存在两个互相隔离的身份域：**前台用户**（`users` + `type=user` + `iam:session:{sid}` + `Auth`）与**后台管理员**（`admins` + `type=admin` + `iam:admin:session:{sid}` + `AdminAuth`）。两者使用独立凭据表、独立 token 类型、独立 Session Key 前缀与独立认证中间件，后端始终独立验证身份域（详见「5. 安全边界」）。

## 2. 架构与组件

```
客户端
  │
  ├─ POST /register  （公开）
  ├─ POST /login     （公开）
  │     校验凭据 → 生成 sid → 写 Redis session → 签发含 sid 的 JWT → 返回 token
  │
  ├─ GET /me         （受保护，Auth 中间件）
  │     验签+exp → 校验 Redis session（存在/未撤销/user_id 匹配）→ 注入 Principal
  │
  └─ POST /logout    （受保护，AuthSignatureOnly 中间件）
        仅验签+exp → 取 Principal.Sid → 原子撤销 session（幂等）
```

后台管理员域（独立于前台用户域）：

```
  ├─ POST /admin/v1/login     （公开）
  │     校验凭据 → 校验 status → 生成 sid → 写 Redis admin session → 签发 type=admin JWT
  │
  ├─ GET /admin/v1/me         （受保护，AdminAuth 中间件）
  │     验签+exp → type=admin → 校验 admin session → 校验 admins.status → 注入 AdminPrincipal
  │
  └─ POST /admin/v1/logout    （受保护，AdminAuthSignatureOnly 中间件）
        仅验签+exp+type=admin → 取 AdminPrincipal.Sid → 原子撤销 admin session（幂等）
```

事实来源：

| 组件 | 角色 |
| --- | --- |
| MySQL `users` | 前台用户身份事实来源（id/username/password_hash） |
| MySQL `admins` | 后台管理员身份事实来源（id/username/password_hash/status/is_super） |
| Redis `iam:session:{sid}` | 前台会话状态事实来源（存在性 + revoked + user_id 绑定） |
| Redis `iam:admin:session:{sid}` | 后台管理员会话状态事实来源（存在性 + revoked + admin_id 绑定） |
| JWT | `sub + type + sid + exp` 的签名凭证，不是事实来源 |

## 3. 数据模型

### 3.1 JWT 声明

| 声明 | 类型 | 说明 |
| --- | --- | --- |
| `sub` | string | 主体 id（十进制字符串）：前台为用户 id，后台为管理员 id |
| `type` | string | 身份域标识：`"user"`（前台用户）或 `"admin"`（后台管理员） |
| `iss` | string | 恒为 `surgecart` |
| `iat` / `exp` | numeric date | 签发/过期时间，`exp = iat + 3600` |
| `sid` | string | 会话 id（自定义声明，非标准 `jti`），与 Redis session 一一对应 |

签名算法限定 HS256；密钥来自 `auth.jwt.secret` / `AUTH_JWT_SECRET`，长度 ≥ 32 字节，启动时校验。

### 3.2 sid 生成

128-bit 密码学随机数（`crypto/rand` 16 字节），编码为 32 位小写 hex。碰撞概率可忽略，不做碰撞重试。

### 3.3 Redis 会话

- **Key**：`iam:session:{sid}`（单 key，无 user 维度索引）。
- **Value**：Hash，字段：
  - `user_id`：用户 id 的十进制字符串（用于与 JWT `sub` 交叉校验，纵深防御）。
  - `revoked`：`"0"`（未撤销）/ `"1"`（已撤销）。
- **TTL**：`auth.session.ttl` 秒，默认 3600，必须 > 0，可经 `AUTH_SESSION_TTL` 覆盖。
- **有效访问窗口** = `min(JWT exp, session TTL)`；任一到期访问受保护接口均返回 401。正常流程 session 在 `iat` 之后写入，Redis 过期略晚于 JWT exp，JWT exp 为主导失效点。

### 3.4 管理员会话

后台管理员会话与前台用户会话结构对称但完全隔离：

- **Key**：`iam:admin:session:{sid}`（独立前缀，不与前台 `iam:session:` 共享）。
- **Value**：Hash，字段：
  - `admin_id`：管理员 id 的十进制字符串（用于与 JWT `sub` 交叉校验）。
  - `revoked`：`"0"`（未撤销）/ `"1"`（已撤销）。
- **TTL**：复用 `auth.session.ttl`，默认 3600。
- 管理员鉴权除校验会话外，还每请求查询 `admins.status`（禁用/不存在→401），实现禁用即时失效。

## 4. 鉴权与登出流程

### 4.1 登录（同步）

```
校验 username/password（bcrypt，防枚举假哈希对齐耗时）
  → 生成 sid
  → 写 Redis session（HSet user_id + revoked=0，Expire TTL）
  → 签发含 sid 的 JWT
  → 返回 {access_token, token_type:"Bearer", expires_in:3600}
```

写 Redis session 失败 → 登录失败，返回 500（`1000 INTERNAL_ERROR`），不签发 token，保证「返回的 token 必有有效 session」。

### 4.2 鉴权（同步，每请求）

`GET /me` 挂载 `Auth` 中间件：

1. 提取 `Authorization: Bearer <token>`。
2. `auth.Parse`：验签 + `exp` 校验 + issuer + 限定 HS256。失败 → 401（不触达 Redis）。
3. 解析 `sub` 与 `sid`（`sid` 为空 → 401）。
4. `ValidateSession(sid, userID)`：查 Redis 会话，要求「存在、未 revoked、`user_id == sub`」。
5. 通过后注入 `Principal{UserID, Sid}`；`/me` 只信任 `Principal.UserID`，不信任请求自带身份。

### 4.3 登出（同步，幂等）

`POST /logout` 挂载 `AuthSignatureOnly` 中间件（仅验签 + `exp`，**不查 session**，保证重复登出即使 session 已撤销/缺失也能到达 handler）：

1. 从 `Principal.Sid` 取 sid（**不接受**请求体/参数中的 sid）。
2. 原子撤销（Lua）：仅当 key 存在时 `HSET revoked=1`，保持剩余 TTL，不创建新 key；key 不存在视为已登出。
3. 成功/重复登出/已撤销/缺失均返回 200/0（`data` 为 null）。

## 5. 安全边界

- **fail-closed**：鉴权中间件查询 Redis 失败（不可用/超时/类型错误）一律返回 401（`1002`），绝不放行可能已撤销的 token；底层错误仅记录服务端日志，不对外暴露内部状态。
- **错误码复用**：已撤销/缺失 session 复用 `1002 UNAUTHORIZED`（401），不新增独立错误码（客户端无法仅凭 code 区分「被登出」与「token 过期」，当前范围内撤销仅由客户端自身登出触发，区分价值低，属可接受取舍）。
- **密钥与凭据**：不硬编码 Redis 地址 / JWT 密钥；密码 bcrypt 存储；Token/密钥不进日志或错误响应。
- **身份信任**：服务端验证身份，不信任客户端提交的 `sid` 或用户身份；`Principal` 是唯一身份来源。

### 5.1 身份域隔离

前台用户与后台管理员是两个互相隔离的身份域。**两个前端不会主动互调 ≠ 调用者不能直接访问另一个 API**：前端是否互相调用只是客户端行为，后端不能据此假定某个 API 只会被某个前端调用。后端身份域隔离由以下四要素独立保证，与客户端无关：

1. **独立凭据表**：前台登录只查询 `users`（`internal/logic/iam/iam.go`），后台登录只查询 `admins`（`internal/logic/admin/admin.go`）；同一 username 允许同时存在于两张表，分别代表两个独立账号。
2. **Token type**：前台登录签发 `type=user`，后台登录签发 `type=admin`；`Auth` 拒绝 `type=admin`、`AdminAuth` 拒绝 `type=user`，二者「type 不符」均返回 403（`1003`）。
3. **独立 Session Key**：前台使用 `iam:session:{sid}`，后台使用 `iam:admin:session:{sid}`；双方的创建/校验/撤销只操作各自前缀，互不读取或撤销对方会话。
4. **独立认证中间件**：前台受保护接口挂 `Auth`/`AuthSignatureOnly`，后台受保护接口挂 `AdminAuth`/`AdminAuthSignatureOnly`（另叠加 `RequirePermission` 授权）。

后台判定顺序：验签+exp → 401；`type≠admin` → 403；会话无效 → 401；`admins` 不存在/禁用 → 401；权限不足 → 403。

## 6. 错误码与 HTTP 状态

沿用 `{code,message,data}` 与集中错误码体系（`internal/codes`）：

| 场景 | code | HTTP |
| --- | --- | --- |
| 成功（含 logout 幂等成功） | 0 | 200 |
| 登录写 session / 生成 sid / 签发失败 | 1000 | 500 |
| token 缺失/非法/签名无效/过期/sid 缺失/会话缺失/已撤销/Redis 故障 | 1002 | 401 |
| 凭据错误（登录） | 2002 | 401 |
| 用户名已存在（注册） | 2001 | 409 |

## 7. 失败与一致性语义

- 登录：写 Redis 失败 → 500，不返回 token；「Redis 已写、JWT 未签」的极窄窗口产生孤儿 session，由 TTL 到期自愈，无安全影响。
- 鉴权：JWT 无效/过期 → 401（不触达 Redis）；JWT 有效但 session 缺失/撤销/`user_id` 不匹配 → 401；Redis 错误 → 401（fail-closed）+ 服务端日志。
- logout：幂等；同一 token 并发登出为原子操作（Lua），结果一致；不同会话互不影响。
- 无 MQ、无异步、无跨系统事务。

## 8. 配置

| 配置键 | 环境变量 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `auth.jwt.secret` | `AUTH_JWT_SECRET` | dev 默认值（见 config.yaml） | HS256 密钥，≥32 字节 |
| `auth.session.ttl` | `AUTH_SESSION_TTL` | `3600` | 会话 TTL（秒），必须 > 0 |

## 9. 代码位置

| 模块 | 路径 |
| --- | --- |
| JWT 签发/校验（含 `type` 声明） | `internal/auth/jwt.go` |
| 会话管理（前台/管理员 sid 生成/TTL/建会话/校验/撤销） | `internal/auth/session.go` |
| 前台鉴权中间件 | `internal/middleware/auth.go`（`Auth` / `AuthSignatureOnly`） |
| 后台鉴权中间件 | `internal/middleware/auth.go`（`AdminAuth` / `AdminAuthSignatureOnly` / `RequirePermission`） |
| 身份注入 | `internal/middleware/principal.go` |
| 前台登录/登出业务 | `internal/logic/iam/iam.go` |
| 后台登录/登出与 RBAC 业务 | `internal/logic/admin/admin.go` |
| 前台路由 | `internal/cmd/routes_frontend.go` |
| 后台路由 | `internal/cmd/routes_admin.go` |
| 错误码 | `internal/codes/codes.go` |
