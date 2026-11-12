# IAM 模块知识（接手必读）

架构、会话模型与安全语义见 `docs/design/iam.md`；本文只记录代码入口、本次 Schema 变更与易踩的坑。

## 1. 当前实现状态

- 认证链路（注册/登录/refresh/登出/会话撤销/后台启用禁用）已完整，**未改任何认证代码与密码算法**。
- `users` 基础资料列 `nickname` / `avatar_url` / `phone`（migration `20261110000002_user_profile`）已由
  B1-04-01 接入读写接口；`birthday DATE NULL` 由 `20261110000015_profile_region_address` 新增。
- `profile` 是独立模块（`api/profile/v1`、`internal/controller/profile`、`internal/logic/profile`），
  数据仍落在 `users`；**不存在 `user_profiles` 表**。
- `GET /me` 保持原契约（只返回 `id`/`username`），资料读取走 `GET /profile`；注册仍不接收资料字段。

## 2. 代码入口

| 关注点 | 位置 |
| --- | --- |
| 注册/登录/登出/ /me/会话列表与撤销 | `internal/logic/iam/iam.go` |
| refresh token 轮换与 family 血缘 | `internal/logic/iam/refresh.go` |
| 后台启用/禁用用户（`status` + `auth_epoch`） | `internal/logic/iam/user_status.go` |
| 用户资料读写（昵称/头像/手机号/生日） | `internal/logic/profile/profile.go` |
| 用户资料 API/Controller | `api/profile/v1/profile.go`、`internal/controller/profile/profile.go` |
| 资料服务接口与注册 | `internal/service/profile.go` |
| JWT 与会话（Redis） | `internal/auth/jwt.go`、`internal/auth/session.go` |
| 鉴权中间件 | `internal/middleware/auth.go`、`internal/middleware/principal.go` |
| 对外结构 | `api/iam/v1/iam.go`、`api/profile/v1/profile.go` |
| 路由 | `internal/cmd/routes_frontend.go`（`// B1-04-01` 锚点）、`internal/cmd/routes_admin.go` |
| 集成测试 | `internal/controller/iam/iam_test.go`、`internal/controller/iam/refresh_test.go`、`internal/controller/profile/profile_test.go` |
| 资料字段纯校验单测 | `internal/logic/profile/profile_internal_test.go` |
| 用户资料默认值/手机号唯一测试 | `internal/migrations/product_model_migration_test.go` |

## 3. `users` 数据约束

| 列 | 规则 |
| --- | --- |
| `nickname` | `VARCHAR(32) NOT NULL DEFAULT ''`；空串表示未设置 |
| `avatar_url` | `VARCHAR(512) NOT NULL DEFAULT ''`；空串表示未设置 |
| `phone` | `VARCHAR(20) NULL`；**未设置必须写 NULL，不能写空串** |
| `birthday` | `DATE NULL`；应用层严格 `YYYY-MM-DD`，NULL 表示未设置 |

- `uk_phone` 是普通唯一索引，MySQL 允许多个 NULL → 「未设置」不受限；两个用户写同一手机号 → 1062 → `2016`。
- 手机号**不是**登录凭证，`username` 仍是唯一登录标识；不要把 `phone` 加进 JWT 声明或 `Principal`。
- 格式校验（大陆 11 位 `^1[3-9]\d{9}$`，与 `addresses.phone` 一致）放在应用层；DB 无 CHECK。
- 生日 `DATE` 列无时区；Go 端用标准库 `time.Parse("2006-01-02")` 严格校验（拒绝未补零、`02-30` 溢出）。

## 4. 资料更新语义（易错点）

- DTO 全部用指针：`nil`=未提供（不修改），`""`=主动清空。不要用「空串等于未提供」的实现，否则无法清空。
- 清空 `phone`/`birthday` 时写 `nil`（SQL NULL），不要写空串（会占 `uk_phone`）。
- 手机号冲突只映射 1062 → `2016`；不要做「先查 phone 是否被占用」的判定（并发下不可靠）。
- 更新只写 `nickname`/`avatar_url`/`phone`/`birthday` 四列；**绝不触碰** `status`/`auth_epoch`/`password_hash`/`username`。
- 用户 id 只从 `middleware.PrincipalFromContext(ctx)` 取；请求体若带 `user_id` 应被忽略（当前 DTO 不声明该字段）。
- 头像非空时校验 `http`/`https` 绝对 URL（≤512 字符），拒绝 `javascript:`/`data:`。

## 5. 不要改的部分

- `users.password_hash` 的 bcrypt 算法与长度（`VARCHAR(60)`）。
- `users.status` / `users.auth_epoch` 的语义：仅「启用→禁用」实际迁移时 `auth_epoch+1`；启用不递减。
- `refresh_tokens` 的哈希、family、`auth_epoch` 继承规则。
- 前台与后台身份域隔离（独立表、独立 token type、独立 session key、独立中间件）。

## 6. 测试方式

破坏性测试必须在独立测试库执行，且测试内部会核实真实连接库名：

```bash
mysql -h127.0.0.1 -uroot -p -e "CREATE DATABASE IF NOT EXISTS my_shop_b104_test"
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root go run . migrate up
DATABASE_DEFAULT_NAME=my_shop_b104_test DATABASE_DEFAULT_PASS=root \
  go test -count=1 ./internal/controller/profile/ ./internal/logic/profile/
```

`my_shop_b10304_verify` 保存完整历史迁移数据，**禁止**在其上执行 DELETE / DROP / 回滚。
