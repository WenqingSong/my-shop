# Technical Contract

## Decision Status
APPROVED

## Problem

交付后端「文件上传 / 对象存储（七牛云）V1」能力：客户端经后端接口取得七牛云上传凭证后，可将文件直传至七牛并获得可公开访问的 URL。上传受身份/权限、文件类型与大小约束，错误可稳定区分，凭据经配置安全注入。

本任务存在多个会改变业务、安全与运维形态的关键方案（上传模型、上传主体、是否落库、是否引入 SDK），已固化 Contract 并由 Owner 确认（见 Owner Decision Record），Coder 据此实现。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2.10.3，模块 `cnb.cool/go-cloud-devops/my-shop`，分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问 `g.DB().Model()`，无 `dao`/`model` 层（见 `api/banner/v1/banner.go`、`internal/logic/banner/banner.go`）。
- VERIFIED：认证中间件现成——前台 `middleware.Auth` 注入 `Principal{UserID,Sid}`；后台 `middleware.AdminAuth` + `RequirePermission(code)`（`IsSuper` 放行、fail-closed），后台权限体系完整（`admins/roles/permissions/admin_roles/role_permissions` 五表 + `internal/boot/seed.go` seed 权限）。前台公开路由挂 `routes_frontend.go`，后台写路由挂 `routes_admin.go` 的 `require(code)` 分组。
- VERIFIED：现状存储边界 `internal/storage/storage.go` 仅 `Storage` 接口 + `LocalStorage`，只实现 `PrepareBannerPlaceholders`；**无 HTTP 上传接口、无对象存储接入**。`cmd.go` 将 banner 目录映射为静态路由 `/storage/banners`。
- VERIFIED：前端两个模板工程 `frotend_web` 与 `frotend_manage` 的 `src/api/qiniu.js` 均引用 `GET /qiniu/upload/token`（注释「假地址 自行替换」），暗示前端期望后端签发七牛上传 token（直传模型）。
- VERIFIED：配置机制 `manifest/config/config.yaml` + `g.Cfg().GetEffective(ctx, key, def)` 支持环境变量覆盖；敏感 secret 注入先例 `auth.Secret()` 读 `auth.jwt.secret`（有开发默认值 + 启动 fail-fast 长度校验）。现有 `storage.local.root` 段，无任何对象存储配置。
- VERIFIED：错误码集中 `internal/codes/codes.go`（域区间 + `codeTable` 绑定 HTTP 状态与安全 message）；`.agent/registry/error-codes.md` 最新分配 `16000-16999`（article-cms-v1，RESERVED，域序 16），下一空闲域序 = 17 → 区间 `17000-17999`。
- VERIFIED：迁移 golang-migrate v4，`internal/migrations/sql/{14位时间戳}_{title}.up.sql`；`.agent/registry/migrations.md` 最新 `20261001000017`（articles，RESERVED）。
- VERIFIED：`.agent/workflow.yaml` 已声明两种 Shared Resource Kind：`migration_version`（`.agent/registry/migrations.md`）与 `error_code_domain`（`.agent/registry/error-codes.md`）。
- VERIFIED：事实来源单一 MySQL；Redis 仅会话；无 MQ。`go.mod` 无任何七牛 SDK 依赖（D3 决定引入官方 SDK 后由 Coder 添加）。
- UNKNOWN：真实七牛凭据当前不可得，AC-002（实际上传取得可访问 URL）需 Owner 注入真实 token 后才能验证，否则 NOT_VERIFIED。

## Recommendation

RECOMMENDATION（已获批）：**后端签发七牛 upload token、客户端直传；上传主体为后台管理员（`AdminAuth`）+ 前台登录用户（`Auth`）两个端点共用同一签发核心；V1 不落库、仅无状态签发凭证；引入官方 `qiniu/go-sdk` 签发 token；配置懒加载校验、签发时明确错误码。**

具体要点：

1. **上传模型 = 直传**。后端签发接口用配置中的 AK/SK 生成七牛 upload token（`PutPolicy` 携带 `scope=bucket:key`、`deadline`、`mimeLimit`、`fsizeLimit`），客户端用该 token 直传七牛，访问 URL = `domain + "/" + key`。最贴合前端 `qiniu.js` 预期、后端不承载上传流量、与 AC-001/002 完全对齐。
2. **上传主体 = 后台管理员 + 前台登录用户**。`GET /admin/qiniu/upload/token`（`AdminAuth`）与 `GET /qiniu/upload/token`（`Auth`）两个端点，共用同一签发核心；未认证 401、token type 不符 403，均不签发、不产生上传。不新增独立权限 code（签发凭证为低敏感操作，scope 已限制；前台仅需登录）。
3. **不落库**。直传模型下后端在签发时刻无法感知最终上传结果；落库需七牛回调或客户端回传 key，属后续任务。V1 仅无状态签发，**不新增 migration**。
4. **引入官方 `qiniu/go-sdk`**。上传签名属第三方认证协议，使用官方 SDK 签发 upload token（`auth.Credentials` + `storage.PutPolicy`），降低协议实现与安全维护风险，不自行实现签名细节（Owner D3 决定）。
5. **配置懒加载校验**。`secret_key` 仅环境变量注入、无默认值；签发时校验缺失/非法并返回稳定错误码（17002），服务启动不因无七牛凭据而 fail-fast（保证无凭据的 CI/开发环境可启动、其它模块测试不受阻）。非机密结构配置（bucket/domain/ttl/大小/白名单）若格式非法，启动时 fail-fast。

关键取舍：**直传模型下，后端在签发时刻无法强制校验真实文件内容**——大小与 MIME 上限只能「后端基于客户端声明参数预校验（400 拒绝）+ 固化进 token scope 由七牛服务端执行」，真实的文件级强制依赖七牛与客户端协作，这是用「后端不承载流量、贴合前端」换来的固有边界（详见 Open Risks）。

## Selected Design

Owner 已确认（D1/D2/D3）：

- D1（上传主体）：支持后台管理员（`AdminAuth`）+ 前台登录用户（`Auth`）两个端点，共用同一签发核心。
- D2（落库）：V1 不落库，仅无状态签发上传凭证，不新增 migration。
- D3（七牛依赖）：改用官方 `qiniu/go-sdk`，不自行实现上传 Token 签名协议。

其余设计（直传模型、key 预生成、类型/大小白名单、错误码域 17000-17999、配置模型）按本 Contract 执行。

## Interfaces and Data

### API 契约（新建 `api/upload/v1`，等价于 Task 声明的 `api/upload*`）

- `GET /admin/qiniu/upload/token`（`AdminAuth`）、`GET /qiniu/upload/token`（`Auth`）。
- 请求（query，均可选）：`filename`（含扩展名，用于扩展名预校验 + 生成 key）、`content_type`（客户端声明 MIME，用于预校验）。
- 响应 `{code:0,message:"OK",data:{token, key, upload_url, domain, final_url, expires_at}}`：
  - `token`：七牛 upload token（客户端直传用）；
  - `key`：后端预生成的存储 key（写入 token scope，客户端必须用该 key 直传）；
  - `upload_url`：上传主机（按 region 映射，如 `https://up-z2.qiniup.com`）；
  - `domain`：对外访问域名；
  - `final_url`：`domain + "/" + key`（可直接访问）；
  - `expires_at`：token 过期时间（= 签发时间 + ttl）。

### 配置模型（`manifest/config/config.yaml` 新增 `qiniu` 段）

| 字段 | 环境变量 | 类型/默认 | 说明 |
| --- | --- | --- | --- |
| `access_key` | `QINIU_ACCESS_KEY` | string，无默认 | 非机密，建议环境变量注入 |
| `secret_key` | `QINIU_SECRET_KEY` | string，无默认 | **机密**，仅环境变量、不进日志/响应/仓库 |
| `bucket` | `QINIU_BUCKET` | string，无默认 | 必填 |
| `domain` | `QINIU_DOMAIN` | string，无默认 | 对外访问域名（拼接 final_url） |
| `region` | `QINIU_REGION` | string，默认 `z2` | 七牛区域，映射 upload host |
| `token_ttl` | `QINIU_TOKEN_TTL` | int，默认 3600（秒） | 凭证有效期，必须 > 0 |
| `max_file_size` | `QINIU_MAX_FILE_SIZE` | int，默认 10485760（10MB） | 单文件大小上限（字节），必须 > 0 |
| `allowed_extensions` | `QINIU_ALLOWED_EXTENSIONS` | []string | 后端扩展名预校验白名单 |
| `allowed_mime_types` | `QINIU_ALLOWED_MIME_TYPES` | []string | 固化进 token `mimeLimit` |

### 全局资源预留

- `error_code_domain`：域序 17 → 区间 **`17000-17999`**（`domain_seq_next = max(16) + 1`），owner = `object-storage-upload`。域内编号（Contract 逐个列出，无跨任务冲突）：
  - `17001` UPLOAD_INVALID_INPUT（扩展名/MIME/参数非法）→ HTTP 400
  - `17002` UPLOAD_CONFIG_INVALID（七牛配置缺失/非法，安全语义失败）→ HTTP 500
  - `17003` UPLOAD_TOKEN_FAILED（签发凭证失败）→ HTTP 500
- `migration_version`：**不预留**（V1 不落库，无新增 migration）。

## Business Invariants

- INV-001（授权边界）：未认证请求访问任一签发接口返回 401，token type 不符返回 403，均不签发凭证、不产生任何上传与 DB 写入。
- INV-002（文件边界）：签发请求中声明的扩展名/MIME 不在白名单内、或大小超上限，返回 400（17001），不签发凭证；最终文件级强制由 token 的 `mimeLimit`/`fsizeLimit` 由七牛服务端执行。
- INV-003（凭据安全）：`secret_key` 不落 config 默认值、不提交仓库、不进日志/响应；凭据缺失/非法时签发接口返回稳定 17002，不泄漏任何凭据细节。
- INV-004（key 唯一可控）：上传 key 由后端预生成（`upload/{yyyyMMdd}/{随机}.{ext}`）并写入 token scope，客户端不能任意指定 key，避免覆盖与越界。

## Failure and Consistency Semantics

- 事实来源：上传凭证与配置来自 `manifest/config`（七牛 AK/SK/bucket/domain），无 DB 写入、无 Redis 参与、无 MQ。直传本身发生在七牛侧，后端不感知上传是否最终成功。
- 签发成功 = 返回一份受 scope 约束、带有效期（ttl）的七牛 upload token 与对应 key/URL；**不代表文件已上传**，仅代表「获得在限制内直传的资格」。
- 失败语义：未认证 401、type 不符 403、非法扩展名/类型/大小 400（17001）、配置缺失/非法 500（17002）、签名/签发内部错误 500（17003）；均不产生上传与写入，且不泄漏凭据/内部路径/堆栈。
- 无跨系统事务、无并发写入、无幂等键、无重试语义（每次签发独立、幂等生成新 token + 新 key）。

## Allowed / Forbidden Changes

- 允许：新建 `api/upload*`、`internal/controller/upload*`、`internal/logic/upload*`、`internal/service` 的 `IUpload` 接口、`internal/codes` 上传/存储域（17000-17999）、`manifest/config/config.yaml` 的 `qiniu` 段、`internal/boot` 配置校验接线、`internal/cmd` 路由注册、`docs/design/storage.md`（Design Impact=NEW）、对应测试；引入官方 `qiniu/go-sdk` 依赖。
- 禁止：修改既有模块（商品/分类/文章/轮播图/IAM 等）行为；改造前端模板工程 `frotend_web`/`frotend_manage`；新增任何 DB 表或 migration（V1 不落库）；硬编码任何七牛凭据或生产地址；改变既有错误码取值、HTTP 状态映射、message 或统一响应 `{code,message,data}` 格式。

## Verification Requirements

- INV-001 → 启动服务：无 token 请求 → 401；user token 访问 `/admin/qiniu/upload/token` 或 admin token 访问 `/qiniu/upload/token` → 403；断言不签发、无 DB 写入。
- INV-002 → 启动服务：`filename=evil.exe` / 非法 `content_type` / 超大小声明 → 400（17001），不签发；断言 token scope 含 `mimeLimit`/`fsizeLimit` 与配置一致。
- INV-003 → 启动服务：缺 `QINIU_SECRET_KEY` 时签发 → 500（17002），响应/日志不含凭据；`secret_key` 不在 config 默认值、不在 git 变更中。
- INV-004 → 启动服务：连续两次签发返回不同 key；key 符合 `upload/{yyyyMMdd}/{随机}.{ext}` 且被写入 token scope。
- AC-001 → 需启动服务：已认证客户端请求签发接口，断言返回可用凭证及 bucket/域名/key/expires_at 等最小必要信息，token 过期时间 = 签发时间 + ttl。
- AC-002 → 需真实七牛凭据：用返回凭证实际上传小文件，断言取得可访问 URL、HTTP 200、Content-Type 一致；无凭据则 NOT_VERIFIED 并说明影响。
- AC-006 → 错误码域 17000-17999 经 registry 分配并在 `internal/codes` 落地；不新增 migration（无落库），`migrations_test.go` 无需新增表断言。
- AC-007 → 文档审查：`docs/design/storage.md` 与 APPROVED Contract、最终实现一致。
- 通用：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Open Risks

- 直传固有边界：后端无法在签发时强制校验真实文件内容；`mimeLimit` 依赖七牛对客户端上报 Content-Type 的执行，客户端可伪造声明。若未来需要服务端强制内容校验，需转后端代理上传或七牛回调，属 Contract Revision。
- 真实七牛凭据缺失：AC-002 无法自验，需 Owner 注入真实 token 后由 Deliverer 验证，否则 NOT_VERIFIED。
- 引入官方 `qiniu/go-sdk` 增加一个第三方依赖（构建/维护/安全面），为 Owner D3 明确接受的代价。
- `region` → upload host 映射需按七牛当前区域表实现，存在随七牛服务演进而需维护的可能。

## Owner Decision Record

- 2026-10-07：Owner 确认 D1（上传主体 = 后台管理员 + 前台登录用户）、D2（V1 不落库、仅无状态签发）、D3（改用官方 `qiniu/go-sdk`，不自行实现上传 Token 签名协议，理由：签名属第三方认证协议，优先用官方 SDK 降低协议实现与安全维护风险）。其余设计按 Contract 执行。
