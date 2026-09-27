# Task: IAM V1（用户注册、登录与 JWT 认证）

## Goal

实现 IAM V1 基础认证能力：用户可用 username/password 注册与登录，密码以 bcrypt 哈希安全存储；登录成功后签发 JWT（`sub=user.id`、`iss=surgecart`、`iat=now`、`exp=now+1h`）；受保护接口 `/me` 经 Bearer Token 认证后返回当前用户 `id` 与 `username`。

## Scope

允许完成的内容：

- `users` 表：`id`、`username`、`password_hash`、`created_at`、`updated_at`，约束 `username` 唯一且非空、`password_hash` 非空；并提供在每日重置环境下可重复建表/迁移的方式。
- 三个接口的 API 定义与完整实现：
  - `POST /register`（公开）
  - `POST /login`（公开）
  - `GET /me`（受保护）
- 注册逻辑：校验 username（3~24 位大小写字母或数字）与 password（8~24 位）→ bcrypt 哈希 → 写入 users。
- 登录逻辑：按 username 查找用户 → 校验 bcrypt 哈希 → 签发 JWT。
- JWT 生成与校验（声明 `sub`/`iss`/`iat`/`exp`），签名密钥通过配置/环境变量注入，不硬编码。
- 认证中间件：解析 `Authorization: Bearer <access_token>`，校验签名与有效期，将 `Principal.UserID` 注入请求上下文；受保护路由 `/me` 使用该中间件。
- `/me` 逻辑：经认证后按 `Principal.UserID` 查询用户，返回 `id` + `username`。
- 输入校验、稳定业务错误码与响应格式（沿用现有 `{code,message,data}` 响应中间件）。
- 必要的测试：注册/登录/`/me` 的正常路径、关键错误路径与边界条件；涉及 MySQL 的部分需可运行集成验证。
- 本任务所需的新增依赖（JWT 库、`x/crypto/bcrypt` 等），具体库由 Contract 指定。

## Out of Scope

明确本次不处理：

- Token 刷新、登出、撤销、黑名单/失效机制。
- 角色/权限体系（RBAC）、管理员、多用户角色。
- 邮箱/手机验证、找回密码、修改/重置密码。
- 防暴力破解：登录速率限制、验证码、账号锁定。
- 第三方登录（OAuth/SSO）、多租户、审计日志。
- 密码策略之外的安全加固（如密码强度计、历史密码）。
- 现有 `/health` 及其它模块的改动。

## Acceptance Criteria

- [ ] AC-001 使用合法 username（3~24 位、仅大小写字母与数字）与 password（8~24 位）调用 `/register` 成功，用户被持久化到 `users` 表，`password_hash` 为 bcrypt 哈希而非明文，且两次相同密码的哈希不同（含随机盐）。
- [ ] AC-002 username 不满足长度或字符集规则、或 password 不满足长度规则时，`/register` 返回明确校验错误，且不写入任何用户记录。
- [ ] AC-003 使用已存在的 username 注册时返回冲突错误，且不覆盖或修改既有用户。
- [ ] AC-004 使用已注册且密码正确的 username/password 调用 `/login` 成功，返回 access_token；其 JWT 声明满足 `sub=user.id`、`iss=surgecart`、含 `iat` 与 `exp`，且 `exp` 约等于 `iat + 1 小时（3600 秒）`。
- [ ] AC-005 使用不存在的 username 或错误密码调用 `/login` 返回认证失败错误且不签发 token；对外不泄露"用户名是否存在"（具体错误语义以 Contract 为准）。
- [ ] AC-006 携带有效 Bearer token 访问 `/me` 返回该用户的 `id` 与 `username`，且返回的 id 与 token 的 `sub` 一致。
- [ ] AC-007 无 token、token 格式错误、签名无效或已过期时访问 `/me` 返回未授权错误，不返回任何用户数据。
- [ ] AC-008 `/register`、`/login` 为公开接口（无需 token 即可访问）；`/me` 必须经认证中间件通过后才能访问。
- [ ] AC-009 JWT 签名密钥不硬编码在代码中，通过配置/环境变量注入。

## Relevant Context

已核实的事实：

- 技术栈 GoFrame v2（Go 1.23），模块 `cnb.cool/go-cloud-devops/my-shop`。
- 现有分层：`api/<module>/v1`（请求/响应定义，`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + Register 模式）→ `internal/logic`（`init()` 注册实现）。当前无 `dao`、`model`、`middleware` 层，无数据库迁移机制。
- 路由在 `internal/cmd/cmd.go` 中 `s.Group("/")`，已挂 `ghttp.MiddlewareHandlerResponse` 并绑定 health 控制器；统一响应格式 `{"code":0,"message":"OK","data":...}`。
- 配置由 `boot.Bootstrap` 从 `manifest/config/config.yaml` 读取并支持环境变量覆盖（键 `.` → `_` 大写，如 `DATABASE_DEFAULT_HOST`）。
- 依赖 MySQL 8.0 与 Redis 7 由 `docker-compose.yml` 编排；运行环境为 CNB DinD，环境每日重置、数据不持久，因此 `users` 表必须能由仓库内定义可重复创建。
- 当前 `go.mod`/`go.sum` 不含 JWT 库与 `x/crypto/bcrypt`，本任务需新增依赖。

Assumption：

- JWT 采用对称签名 HS256，密钥由配置/环境变量注入（具体算法与密钥来源由 Analyst 提议、Owner 确认）。
- 密码哈希使用 `golang.org/x/crypto/bcrypt`，成本取合理默认值或由 Contract 指定。
- `/register` 成功返回成功状态（如新建用户的 id/username 或空成功响应），不自动签发 token。
- `/login` 成功响应至少包含 access_token（可附带 token 类型等基础信息），具体字段由 Contract 约定。

OPEN QUESTION（不阻塞创建，交由 Analyst 分析、Owner 确认）：

- `users` 表在每日重置环境下如何可重复创建：启动时自动建表 vs 迁移脚本/SQL 文件，项目当前无既有机制。
- 登录失败是否对"用户名不存在"与"密码错误"返回统一错误（防用户枚举）。
- JWT 签名算法与密钥来源、密钥长度/生成方式。
- 各失败场景的错误码与 HTTP 状态约定（注册冲突、参数校验失败、认证失败、未授权等）。

## Verification

- AC-001/002/003：需可连接 MySQL（`docker compose up -d` 后）；用 HTTP 集成测试分别覆盖成功注册、非法输入、重复用户名，并直接查库确认 `password_hash` 非明文且带盐。建议 `go test ./...` 中提供集成或单元测试覆盖。
- AC-004/005：需 MySQL；对 `/login` 覆盖正确凭据、错误密码、不存在用户三种情况，解码返回 token 的声明核对 sub/iss/iat/exp。
- AC-006/007：`/me` 覆盖有效 token、缺失 token、错误格式、无效签名、过期 token（可构造短期/已过期 token 或用测试密钥验证）。JWT 签发与中间件校验逻辑可在无 MySQL 时用单元测试覆盖。
- AC-008：通过路由配置/中间件挂载确认公开与受保护边界（可结合 HTTP 集成测试断言 401）。
- AC-009：代码检索确认无硬编码密钥，密钥来自配置/环境变量；用环境变量覆盖后重启验证生效。
- 通用命令：`go build ./...`、`go vet ./...`、`go test ./...`；涉及 MySQL 的集成验证需说明环境（依赖容器就绪）。

## Complexity

COMPLEX

原因：这是项目首个认证/授权安全边界组件，引入 JWT 签名与密钥管理、bcrypt 密码哈希、受保护路由与认证中间件，且存在多个会影响行为与运维的取舍（JWT 算法与密钥来源、`users` 表可重复迁移方式、登录失败防枚举的错误语义）。需 Analyst 定向分析并起草 `contract.md`，由 Owner 确认关键方案后再交 Coder 实现。

## Optional Analyst Questions

Analyst 应调查、Owner 应确认的重要问题：

1. JWT 签名算法与密钥来源：是否 HS256 + 配置/环境变量密钥；密钥如何生成、长度要求、是否需要区分开发/生产。
2. `users` 表的可重复创建方式：启动时自动建表（GoFrame schema）还是迁移脚本/SQL 文件，如何在每日重置环境下稳定重建。
3. 登录失败错误语义：是否统一返回"用户名或密码错误"以防水枚举；是否对不存在用户与密码错误做同响应与相近耗时。
4. 密码哈希成本（bcrypt cost）取值与依据。
5. 认证中间件失败语义：缺失/非法/过期 token 对应的 HTTP 状态码与业务错误码，以及 `Principal.UserID` 的注入方式与上下文约定。

## Review Baseline

- Base commit：`43567bf0ba3de796170e8942caad16a77d3f39bb`（分支 `feat/auth`）
- 任务开始时已有修改：无（working tree clean，`git status --short` 为空）
- 同文件既有修改的区分方式：不适用（无已有未提交修改）

## Initial Route

READY_FOR_ANALYST
