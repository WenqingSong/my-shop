# IAM 模块知识（接手必读）

架构、会话模型与安全语义见 `docs/design/iam.md`；本文只记录代码入口、本次 Schema 变更与易踩的坑。

## 1. 当前实现状态

- 认证链路（注册/登录/refresh/登出/会话撤销/后台启用禁用）已完整，本次**未改任何认证代码与密码算法**。
- `users` 已增加基础资料列 `nickname` / `avatar_url` / `phone`（migration `20261110000002_user_profile`），
  但**尚无资料读写接口**：`GET /me` 仍只返回 `id`/`username`，注册也不接收资料字段。

## 2. 代码入口

| 关注点 | 位置 |
| --- | --- |
| 注册/登录/登出/ /me/会话列表与撤销 | `internal/logic/iam/iam.go` |
| refresh token 轮换与 family 血缘 | `internal/logic/iam/refresh.go` |
| 后台启用/禁用用户（`status` + `auth_epoch`） | `internal/logic/iam/user_status.go` |
| JWT 与会话（Redis） | `internal/auth/jwt.go`、`internal/auth/session.go` |
| 鉴权中间件 | `internal/middleware/auth.go`、`internal/middleware/principal.go` |
| 对外结构 | `api/iam/v1/iam.go` |
| 路由 | `internal/cmd/routes_frontend.go`、`internal/cmd/routes_admin.go` |
| 集成测试 | `internal/controller/iam/iam_test.go`、`internal/controller/iam/refresh_test.go` |
| 用户资料默认值/手机号唯一测试 | `internal/migrations/product_model_migration_test.go` |

## 3. `users` 数据约束（本次新增）

| 列 | 规则 |
| --- | --- |
| `nickname` | `VARCHAR(32) NOT NULL DEFAULT ''`；空串表示未设置 |
| `avatar_url` | `VARCHAR(512) NOT NULL DEFAULT ''`；空串表示未设置 |
| `phone` | `VARCHAR(20) NULL`；**未设置必须写 NULL，不能写空串** |

- `uk_phone` 是普通唯一索引，MySQL 允许多个 NULL → 「未设置」不受限；两个用户写同一手机号 → 1062。
- 手机号**不是**登录凭证，`username` 仍是唯一登录标识；不要把 `phone` 加进 JWT 声明或 `Principal`。
- 格式校验（大陆 11 位 `^1[3-9]\d{9}$`，与 `addresses.phone` 一致）放在应用层；DB 无 CHECK。

## 4. 不要改的部分

- `users.password_hash` 的 bcrypt 算法与长度（`VARCHAR(60)`）。
- `users.status` / `users.auth_epoch` 的语义：仅「启用→禁用」实际迁移时 `auth_epoch+1`；启用不递减。
- `refresh_tokens` 的哈希、family、`auth_epoch` 继承规则。
- 前台与后台身份域隔离（独立表、独立 token type、独立 session key、独立中间件）。

## 5. 后续实现资料接口时的注意点

- 更新手机号时把 1062 映射为业务冲突（例如“手机号已被占用”），不要返回 500；
- 空值统一写 NULL；不要把空串写进 `phone`（会占用唯一键）；
- 资料更新不得触碰 `status`/`auth_epoch`/`password_hash`，也不得让客户端提交 `user_id`；
- 头像 URL 只存地址（`VARCHAR(512)`），上传流程复用 `internal/logic/upload`。
