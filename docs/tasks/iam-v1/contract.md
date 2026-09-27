# Technical Contract

## Decision Status

APPROVED

## Problem

实现 IAM V1 基础认证：username/password 注册与登录（bcrypt 存储），登录签发 JWT（HS256，声明 sub/iss/iat/exp），受保护接口 `/me` 经 Bearer Token 认证后返回当前用户 id/username。本任务引入项目首个认证安全边界，涉及 JWT 密钥管理、users 表可重复建表、认证中间件与 Principal 上下文约定，以及稳定错误码与 HTTP 状态约定。

## Verified Current Behavior

- 技术栈 GoFrame v2.10.3 / Go 1.23，模块 `cnb.cool/go-cloud-devops/my-shop`。
- 现有分层：`api/health/v1`（`g.Meta` 声明）→ `internal/controller/health` → `internal/service`（接口 + Register 模式）→ `internal/logic/health`（`init()` 注册）。无 `dao`/`model`/`middleware` 层，无迁移机制。
- 路由 `internal/cmd/cmd.go`：`s.Group("/")` + `ghttp.MiddlewareHandlerResponse` + `health.NewV1()`。
- 统一响应 `{code,message,data}`；`ghttp.MiddlewareHandlerResponse` 出错时**不设置 HTTP 状态码**（默认 200），仅把错误码写入 body（源码 `ghttp_middleware_handler_response.go`）。
- 配置 `manifest/config/config.yaml`，经 `boot.Bootstrap` 用 `g.Cfg().GetEffective` 读取，支持环境变量覆盖（`.`→`_` 大写，如 `DATABASE_DEFAULT_HOST`）。当前无 `auth.jwt.*` 配置。
- 启动依赖校验：`boot.Bootstrap` 依次 Ping MySQL 与 Redis 后才启动 HTTP；IAM V1 本身不使用 Redis。
- `go.mod` 无 JWT 库与 `x/crypto/bcrypt`。
- 环境每日重置（CNB DinD）、数据不持久；`scripts/*.sh` 仅管理依赖容器与应用生命周期，不建表。
- Git：working tree clean，分支 `feat/auth`，HEAD `ba7560f`（任务基线 `43567bf` 之后仅文档提交）。

## Root Cause / Design Constraint

- 无既有 users 表与迁移机制 → 需引入可重复、幂等的建表方式。
- 无 JWT / bcrypt 依赖 → 需新增依赖。
- 默认响应中间件不产出 HTTP 4xx/5xx → 为满足 AC-008「断言 401」等，需约定错误码→HTTP 状态映射。
- 无认证中间件与 Principal 上下文约定 → 需新建。

## Analyst Recommendation

RECOMMENDATION（已由 Owner 确认，见 Selected Design）：

1. **依赖**：`github.com/golang-jwt/jwt/v5`（HS256）+ `golang.org/x/crypto/bcrypt`。
2. **users 建表**：幂等 DDL（`CREATE TABLE IF NOT EXISTS users`）在启动依赖校验成功后执行一次；单表场景推荐以 Go 常量定义 DDL（或同包 `go:embed` SQL 文件），不引入迁移框架。
3. **JWT**：HS256；密钥来自配置 `auth.jwt.secret` / 环境变量 `AUTH_JWT_SECRET`，长度 ≥32 字节，启动校验。
4. **bcrypt**：cost = 10（`bcrypt.DefaultCost`）。
5. **登录防枚举**：统一错误 + 假哈希比对对齐耗时。
6. **错误码→HTTP 状态**：集中映射，保持 `{code,message,data}` 格式。
7. **认证中间件**：校验签名与 `exp`，注入 `Principal{UserID}`。

## Selected Design

Owner 已确认，采用以下方案：

1. **JWT**：HS256；密钥来自配置 `auth.jwt.secret` / 环境变量 `AUTH_JWT_SECRET`，长度 ≥32 字节，启动校验（空或过短 fail-fast）。claims：`sub`=用户 id 十进制字符串、`iss=surgecart`、`iat`/`exp` 为 Unix 秒、`exp=iat+3600`（exp 固定 1h，代码常量）。
2. **开发默认密钥**（dev-only，已随机生成，须配标准注释）：`c49d0fea5cf77761ca6d0275545b9501686dd40cc313b072c36972d6128fc403`。仅用于本地/CI 每日重置环境，生产必须用 `AUTH_JWT_SECRET` 覆盖。
3. **users 建表**：启动时幂等 `CREATE TABLE IF NOT EXISTS`（Go 常量或同包 `go:embed` SQL），不引入迁移框架。
4. **bcrypt**：cost = 10。
5. **登录防枚举**：不存在用户与密码错误统一返回「用户名或密码错误」，对不存在用户执行一次假哈希 bcrypt 比较对齐耗时。
6. **错误码体系**：标准成熟、集中定义、按域分段（见 Error Semantics）；项目级响应中间件统一映射错误码→HTTP 状态，保持 `{code,message,data}`。
7. **认证中间件**：解析 `Authorization: Bearer`，校验签名与 `exp`，失败 401 + UNAUTHORIZED；成功注入 `Principal{UserID}`；`/me` 只信任 Principal.UserID。

### 配置片段（Coder 按此写入 `manifest/config/config.yaml`）

```yaml
auth:
  jwt:
    # JWT 签名密钥（HS256）。
    # ⚠️ 开发环境默认值，仅用于本地/CI 每日重置环境。
    # 生产环境必须通过环境变量 AUTH_JWT_SECRET 注入真实密钥，禁止使用此默认值。
    secret: "c49d0fea5cf77761ca6d0275545b9501686dd40cc313b072c36972d6128fc403"
```

对应环境变量：`AUTH_JWT_SECRET`（键 `auth.jwt.secret`）。

## Interfaces

- `POST /register`（公开）：body `{username,password}` → `data:{id,username}`（不自动签发 token）。
- `POST /login`（公开）：body `{username,password}` → `data:{access_token, token_type:"Bearer", expires_in:3600}`。
- `GET /me`（受保护）：`Authorization: Bearer <token>` → `data:{id,username}`。
- 新增 `api/iam/v1`、`internal/controller/iam`、`internal/service`（IIam）、`internal/logic/iam`，遵循现有分层与 Register 模式。
- 新增 `internal/middleware`：认证中间件 + `Principal` 定义 + 上下文读取 helper + 项目级响应中间件（错误码→HTTP 状态映射，保持 `{code,message,data}`）。
- 新增 `internal/codes`（错误码集中定义，见 Error Semantics）。

## Data Model

```sql
CREATE TABLE IF NOT EXISTS users (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  username      VARCHAR(24)     NOT NULL,
  password_hash VARCHAR(60)     NOT NULL,
  created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY uk_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

- `uk_username`（utf8mb4_unicode_ci，大小写不敏感）是用户名唯一的存储层保证；注册冲突依赖该约束判定（MySQL 1062 → USERNAME_EXISTS），不依赖应用层先查。

## Business Invariants

- INV-001：用户名唯一由 DB `UNIQUE` 约束保证；并发注册同名用户最多一个成功，其余返回 409，不覆盖、不重复。
- INV-002：密码只以 bcrypt 哈希存储；任何路径不落明文/可逆形式；相同密码两次哈希结果不同（含盐）。
- INV-003：仅持有服务端密钥正确签名且未过期 token 的请求能通过 `/me`；无法通过伪造 sub 冒充他人；`/me` 返回的 id 必须等于 token 的 sub。
- INV-004：登录失败不泄露用户名是否存在（不存在用户与密码错误返回相同 code/message/status，耗时相近）。
- INV-005：JWT 密钥不硬编码在代码中，来自配置/环境变量，长度 ≥32 字节，启动时校验。

## Consistency Semantics

- 事实来源：MySQL `users` 表是本任务唯一事实来源；无 Redis/MQ 参与，无跨系统一致性要求。
- 注册/登录为同步写/读；成功响应代表已提交到 MySQL。
- `/me` 为只读查询；按 Principal.UserID 查询不到用户时按 401 处理（不泄露存在性）。

## Error Semantics

### 错误码体系（标准成熟、集中定义、按域分段）

错误码用 `int` 常量集中定义于 `internal/codes`，每个 code 绑定 HTTP 状态与用户安全 message；响应中间件统一把 code→HTTP 状态，body 保持 `{code,message,data}`。客户端靠 code 判型，不依赖 message 文本。

| 域 | 范围 | 说明 |
| --- | --- | --- |
| 成功 | `0` | OK |
| 通用/系统 | `1000-1999` | 与 HTTP 语义对齐，全项目复用 |
| 认证/用户域（IAM） | `2000-2999` | 本任务业务错误 |
| （预留） | `3000+` | 后续模块按域扩展 |

通用/系统：

| code | 语义 | HTTP |
| --- | --- | --- |
| 1000 | INTERNAL_ERROR | 500 |
| 1001 | INVALID_ARGUMENT | 400 |
| 1002 | UNAUTHORIZED | 401 |
| 1003 | FORBIDDEN | 403 |
| 1004 | NOT_FOUND | 404 |
| 1005 | SERVICE_UNAVAILABLE | 503 |

认证/用户域（IAM）：

| code | 语义 | HTTP |
| --- | --- | --- |
| 2001 | USERNAME_EXISTS | 409 |
| 2002 | INVALID_CREDENTIALS | 401 |

### 本任务错误场景映射

| 场景 | 业务 code | HTTP |
| --- | --- | --- |
| 参数校验失败（username/password 规则） | 1001 INVALID_ARGUMENT | 400 |
| 用户名已存在 | 2001 USERNAME_EXISTS | 409 |
| 登录失败（不存在或密码错） | 2002 INVALID_CREDENTIALS | 401 |
| 缺失/非法/过期/签名无效 token | 1002 UNAUTHORIZED | 401 |
| 内部错误 | 1000 INTERNAL_ERROR | 500 |

## Concurrency Semantics

- 并发注册同名用户：靠 `uk_username` 唯一约束，只有一个成功，其余映射 1062 → 409。
- 无共享可变状态、无缓存/MQ，无其他并发不变量。

## Allowed Changes

- 新增 IAM 相关 API/controller/service/logic/middleware/codes 与 users 建表。
- 新增 `github.com/golang-jwt/jwt/v5`、`golang.org/x/crypto/bcrypt` 依赖。
- 新增项目级响应中间件（保持 `{code,message,data}`）以映射错误码→HTTP 状态。
- 扩展 `boot.Bootstrap` 执行幂等建表；新增 `auth.jwt.secret` 配置（dev 默认值 + `AUTH_JWT_SECRET` 环境变量覆盖）。

## Forbidden Changes

- 不改动 `/health` 行为与响应格式。
- 不引入 token 刷新/登出/撤销/RBAC/限流/邮箱验证等（Out of Scope）。
- 不硬编码 JWT 密钥（dev 默认值属配置而非代码硬编码）、不存明文密码、不泄露用户存在性。
- 不引入迁移框架或 Redis/MQ 依赖（本任务无此需要）。

## Verification Requirements

- INV-001 → 集成测试并发注册同名用户：仅一个成功、其余 409；查库确认无重复行。
- INV-002 → 注册后查库：password_hash 为 60 字符 bcrypt 且 != 明文；两次同密码注册哈希不同。
- INV-003 → `/me`：有效 token 返回正确 id/username（id==sub）；无 token/坏格式/坏签名/过期 token 均 401 且无用户数据。
- INV-004 → `/login`：不存在用户与错误密码返回相同 code/message/status。
- INV-005 → 代码检索无硬编码密钥；`AUTH_JWT_SECRET` 覆盖后重启生效；空/过短密钥启动失败。
- 通用：`go build ./...`、`go vet ./...`、`go test ./...`；MySQL 集成验证说明容器就绪。

## Open Risks

- 开发默认密钥已提交仓库（dev-only）：生产环境必须覆盖，属运维/发布纪律，非阻塞。
- bcrypt cost=10 在更高安全要求下可上调（会增加登录延迟），非阻塞。
- username 大小写不敏感唯一性（utf8mb4_unicode_ci）已确认采用 ci 语义。

## Owner Decision Record

Owner 于 2026-09-27 确认以下决定（对应 Analyst 待决问题 1-5）：

1. **JWT 方案**：认可 HS256 + `auth.jwt.secret`/`AUTH_JWT_SECRET`（≥32 字节、启动校验）；密钥由 Analyst 随机生成 → `c49d0fea5cf77761ca6d0275545b9501686dd40cc313b072c36972d6128fc403`（dev-only）。
2. **users 建表**：认可启动时幂等 DDL（非迁移框架）。
3. **登录防枚举**：认可统一「用户名或密码错误」+ 对齐耗时。
4. **错误码/HTTP 状态**：认可 400/409/401/500 映射，并要求建立标准成熟错误码体系（已落地为按域分段、集中定义，见 Error Semantics）。
5. **开发默认密钥**：提交 dev 默认密钥到 config.yaml，并加标准注释（配置片段见 Selected Design）。

适用范围：仅 IAM V1 本任务；后续模块新增错误码时沿用本错误码体系按域扩展。关键问题均已解决，Contract 标记 APPROVED，可交 Coder 实现。
