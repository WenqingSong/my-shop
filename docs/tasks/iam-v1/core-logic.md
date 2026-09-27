# Core Logic Review

> 以下为最终实现中真正承载业务结果与安全边界的代码，Owner 应重点理解、质疑与验证。

## CL-001：认证中间件与 Principal 注入

### Location

internal/middleware/auth.go:17-38

### What It Does

解析 `Authorization: Bearer <token>`，校验签名与 `exp`，成功后将 `Principal{UserID}` 注入请求上下文；缺失/格式非法/签名无效/过期/`sub` 非法统一返回 401 `UNAUTHORIZED`，不进入 handler。

### Business Invariant

只有持有服务端密钥正确签名、未过期且 `sub` 为合法正整数的 token，才能通过受保护路由；`/me` 只信任中间件注入的 `Principal.UserID`，不信任请求自带的任何身份信息（INV-003）。

### Why Owner Should Understand It

这是本任务唯一的授权边界。若此处解析或校验有误（如接受 `alg:none`、忽略 `exp`、信任客户端提交的 userID），会导致未授权访问或冒充他人。

### Relevant Tests

- TestIAMEndToEnd（AC-007：缺失/坏格式/坏签名/过期/错误 issuer 均 401 且无数据）
- TestParseRejectsWrongIssuer / TestParseRejectsExpiredToken / TestParseRejectsMissingExp（`internal/auth/jwt_test.go`）

### Suggested Mutation

临时让 `Auth` 跳过签名校验（例如直接信任 header 里的任意 token 或注释 `auth.Parse` 调用），运行 `TestIAMEndToEnd` 中 `/me` 的 401 场景。

### Expected Result

测试必须失败（原本应 401 的场景会通过并返回用户数据）；随后恢复原实现并重新确认测试通过。

---

## CL-002：JWT 签发与校验（密钥管理、声明与失效）

### Location

internal/auth/jwt.go:31-99

### What It Does

HS256 签发 `sub`(用户 id 十进制字符串)/`iss=surgecart`/`iat`/`exp=iat+3600`；密钥来自 `auth.jwt.secret`（环境变量 `AUTH_JWT_SECRET` 覆盖），长度 ≥32 字节，启动 `boot.Bootstrap` 时 fail-fast 校验；解析侧强制 `WithExpirationRequired` 与 `WithIssuer`、限定 `HS256`。

### Business Invariant

密钥不硬编码于代码（来自配置/环境变量）；签发与校验的声明约定（sub/iss/iat/exp）一致；无效签名、错误 issuer、缺失或过期 exp 一律拒绝（INV-005、INV-003）。

### Why Owner Should Understand It

密钥来源与启动校验决定系统在空/弱密钥下是显式失败还是带着安全隐患运行；`iss`/`exp` 校验防止跨系统 token 复用与过期 token 长期有效。

### Relevant Tests

- TestGenerateClaims（sub/iss/iat/exp 及 exp-iat=3600）
- TestParseRejectsWrongSecret / TestParseRejectsWrongIssuer / TestParseRejectsExpiredToken / TestParseRejectsMissingExp
- TestSecretFromEnv / TestSecretRejectsTooShort

### Suggested Mutation

临时移除 `ParseWithSecret` 中的 `gojwt.WithIssuer(Issuer)`，运行 `TestParseRejectsWrongIssuer`。

### Expected Result

测试必须失败（错误 issuer 的 token 会被接受）；随后恢复并重新确认。

---

## CL-003：登录防用户名枚举

### Location

internal/logic/iam/iam.go:80-112（`Login`）；internal/codes/codes.go:28（`CodeInvalidCredentials`）

### What It Does

不存在用户与密码错误统一返回 401 `INVALID_CREDENTIALS`（2002）；对不存在用户执行一次 `dummyPasswordHash` 的假 bcrypt 比对对齐耗时，避免通过响应时间区分用户是否存在（INV-004）。

### Why Owner Should Understand It

统一 code/message/status 只防「内容枚举」，耗时对齐防「时序枚举」。这是登录接口的安全纵深，需理解其双重要求。

### Relevant Tests

- TestIAMEndToEnd（`iam_test.go:227-235`：错误密码与不存在用户 code/message/status 一致）

### Suggested Mutation

临时注释 `_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, ...)` 一行，运行 `TestIAMEndToEnd`。

### Expected Result

测试**仍会通过**（现有测试仅断言 code/message/status，未断言耗时）。这本身揭示了「耗时相近」未被测试保护的已知轻量缺口（见 findings.md CLEAN-002），而非任务失败。Owner 可据此决定是否补一条宽松的耗时同量级断言。

---

## CL-004：用户名唯一性与并发注册

### Location

internal/boot/boot.go:33-43（`users` 表 `UNIQUE KEY uk_username`）；internal/logic/iam/iam.go:64-73、177-184（`isDuplicateKeyError`）

### What It Does

用户唯一性由数据库 `uk_username`（utf8mb4_unicode_ci，大小写不敏感）唯一约束保证，不在应用层先查再插；并发同名注册由 MySQL 1062 唯一冲突判定映射为 409 `USERNAME_EXISTS`（INV-001）。

### Why Owner Should Understand It

这是「一人一名」并发安全的关键：先查后插会在并发下重复写入，而唯一约束 + 1062→409 是存储层保证。判断 `*mysql.MySQLError` 的 1062 是正确映射的前提。

### Relevant Tests

- TestConcurrentRegisterSameUsername（10 并发同名仅 1 成功、9 冲突、查库仅 1 行）
- TestIAMEndToEnd（AC-003 重复注册 409 且不覆盖）

### Suggested Mutation

临时把 `isDuplicateKeyError` 改成只返回 `false`（不再识别 1062），运行 `TestConcurrentRegisterSameUsername`。

### Expected Result

测试必须失败（冲突请求会被误报为 500/内部错误，或 success/conflict 计数不符）；随后恢复。

---

## CL-005：密码 bcrypt 哈希存储

### Location

internal/logic/iam/iam.go:59（`bcrypt.GenerateFromPassword(..., bcrypt.DefaultCost)`）

### What It Does

注册时用 bcrypt（cost=10）哈希密码后写入 `users.password_hash`（VARCHAR(60)）；登录用 `bcrypt.CompareHashAndPassword` 校验；任何路径不落明文（INV-002）。

### Why Owner Should Understand It

密码安全取决于哈希算法与随机盐；`VARCHAR(60)` 与 bcrypt 输出长度对应，改算法或 cost 会改变该约束。

### Relevant Tests

- TestIAMEndToEnd（`password_hash` 长度 60 且 != 明文；同密码两次哈希不同）
- TestConcurrentRegisterSameUsername（间接覆盖写入路径）

### Suggested Mutation

（可选）临时将 cost 提升到极高值或改为 `bcrypt.MinCost`，观察注册/登录耗时与测试稳定性变化，确认 cost 对延迟的实际影响。

---

## CL-006：错误码体系与统一响应映射

### Location

internal/codes/codes.go:15-86；internal/middleware/response.go:12-45

### What It Does

业务错误码集中定义（成功 0、通用 1000-1005、IAM 2001-2002），每个 code 绑定 HTTP 状态与用户安全 message；项目级 `Response` 中间件统一把 code→HTTP 状态并保持 `{code,message,data}`，失败时 `data:null` 且不泄漏底层错误（保留错误链仅用于日志）。

### Why Owner Should Understand It

这是对外 API 的稳定契约：客户端靠 code 判型不靠 message 文本；HTTP 状态与业务码职责分离；错误响应不得泄漏数据库/Redis/堆栈等内部细节。

### Relevant Tests

- TestIAMEndToEnd（400/409/401/500 各场景 code 与 status 均被断言）
- internal/controller/health/health_test.go（成功路径格式）

### Suggested Mutation

（可选）临时在 `Response` 失败分支把 `WriteHeader` 去掉或写死 200，运行 `TestIAMEndToEnd` 中 400/409/401 断言。

### Expected Result

测试必须失败（HTTP 状态码不符合约定）；随后恢复。
