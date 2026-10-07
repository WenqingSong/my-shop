# 文件上传与对象存储设计（Storage / Qiniu）

本文面向项目接手者，说明「文件上传 / 对象存储（七牛云）」核心闭环的上传模型、配置模型、公开 API 契约、安全边界、错误语义与一致性模型。事实来源为 `object-storage-upload` 的 APPROVED Contract 与最终实现。错误码域的权威分配状态以 `.agent/registry/error-codes.md`（`develop` 上）为准。

## 1. 职责与边界

文件上传回答「客户端怎样把文件放进对象存储并取得可访问地址」，V1 采用**客户端直传**模型：后端仅签发七牛云上传凭证（upload token），文件本体由客户端直传七牛，后端不承载上传流量、不感知上传最终是否成功。

边界（V1）：

- 仅七牛云一种对象存储，不接 MinIO/OSS/S3。
- 无 CDN、图片处理（裁剪/压缩/水印/格式转换/缩略图）。
- **不落库**：无 `files`/`uploads` 表，不跟踪上传结果、不关联具体业务实体（商品图/文章图/轮播图等）。
- 无上传进度、分片上传/断点续传/大文件直传优化。
- 不改造前端模板工程 `frotend_web`/`frotend_manage`（其 `qiniu.js` 的 `GET /qiniu/upload/token` 为占位假地址）。
- 与现有 `internal/storage` 的 `LocalStorage`（轮播图本地静态服务 `/storage/banners`）是**两个不同关注点**：LocalStorage 负责本地静态资源服务，本次上传能力负责签发七牛直传凭证；二者并存、互不替换。

事实来源为单一 MySQL；Redis 仅会话；无 MQ。上传凭证与配置来自 `manifest/config`（七牛 AK/SK/bucket/domain），无 DB 写入、无 Redis 参与。

## 2. 上传模型（客户端直传）

1. 客户端（已认证）请求后端签发接口，提交可选的 `filename`（含扩展名）与 `content_type`（声明 MIME）。
2. 后端校验声明（扩展名/MIME 白名单、大小上限），用配置中的七牛 AK/SK 经官方 `qiniu/go-sdk` 生成 upload token（`PutPolicy` 携带 `scope=bucket:key`、`deadline`、`mimeLimit`、`fsizeLimit`），并预生成存储 key。
3. 客户端用返回 token 向七牛 upload host 直传，上传成功后访问地址 = `domain + "/" + key`。

关键点：

- key 由后端预生成（`upload/{yyyyMMdd}/{随机}.{ext}`）并写入 token scope，客户端**不能**任意指定 key，避免覆盖与越界。
- 大小上限（`fsizeLimit`）与 MIME 白名单（`mimeLimit`）固化进 token，由七牛服务端在上传时执行。
- 每次签发独立、幂等生成新 token + 新 key。

## 3. 配置模型（`manifest/config/config.yaml` 的 `qiniu` 段）

| 字段 | 环境变量 | 类型/默认 | 说明 |
| --- | --- | --- | --- |
| `access_key` | `QINIU_ACCESS_KEY` | string，config.yaml 空值 | 凭据标识，非机密，**仅环境变量注入**（与 secret_key 成对） |
| `secret_key` | `QINIU_SECRET_KEY` | string，config.yaml 空值 | **机密**，仅环境变量、不进日志/响应/仓库 |
| `bucket` | `QINIU_BUCKET` | string，无默认 | 非敏感，可写 config.yaml 或经环境变量覆盖 |
| `domain` | `QINIU_DOMAIN` | string，无默认 | 非敏感，可写 config.yaml 或经环境变量覆盖 |
| `region` | `QINIU_REGION` | string，默认 `z2` | 七牛区域，映射 upload host |
| `token_ttl` | `QINIU_TOKEN_TTL` | int，默认 3600（秒） | 凭证有效期，必须 > 0 |
| `max_file_size` | `QINIU_MAX_FILE_SIZE` | int，默认 10485760（10MB） | 单文件大小上限（字节），必须 > 0 |
| `allowed_extensions` | `QINIU_ALLOWED_EXTENSIONS` | []string | 后端扩展名预校验白名单 |
| `allowed_mime_types` | `QINIU_ALLOWED_MIME_TYPES` | []string | 固化进 token `mimeLimit` |

配置校验语义：分两类。① 启动 fail-fast（纯结构、有安全默认值）：`region` 合法、`token_ttl>0`、`max_file_size>0`、白名单非空。② 签发时校验（返回 17002，依赖外部七牛环境、无通用默认值）：`access_key`/`secret_key`/`bucket`/`domain` 缺失/非法。`access_key`/`secret_key` 仅环境变量注入、config.yaml 保持空值；`bucket`/`domain` 非敏感，可写 config.yaml 或经环境变量覆盖，不要求 env-only。服务启动不因无七牛凭据/桶/域名而 fail-fast（保证无七牛凭据的 CI/开发环境可启动、其它模块测试不受阻）。

Secret 注入模型（项目级统一）：`.env.example` 保存变量名/安全示例（可提交），`.env` 保存本地真实值（gitignore、绝不提交）；本地经 `scripts/lib.sh` 加载，CI/生产经平台 Secret/环境变量注入；不引入 dotenv，沿用 `g.Cfg().GetEffective` 环境变量覆盖机制。

## 4. 公开 API 契约

- `GET /admin/qiniu/upload/token`（`AdminAuth`）、`GET /qiniu/upload/token`（`Auth`）。
- 请求（query，均可选）：`filename`（含扩展名）、`content_type`（声明 MIME）。
- 响应 `{code:0,message:"OK",data:{token, key, upload_url, domain, final_url, expires_at}}`：
  - `token`：七牛 upload token（客户端直传用）；
  - `key`：后端预生成的存储 key；
  - `upload_url`：上传主机（按 region 映射）；
  - `domain`：对外访问域名；
  - `final_url`：`domain + "/" + key`（可直接访问）；
  - `expires_at`：token 过期时间（= 签发时间 + ttl）。

## 5. 安全与权限边界

- 后台端点：`GET /admin/qiniu/upload/token` 需 `AdminAuth`（所有已启用管理员，含超管 `IsSuper` 放行）；不新增独立权限 code（签发凭证为低敏感操作，scope 已限制）。
- 前台端点：`GET /qiniu/upload/token` 需 `Auth`（登录用户）。
- 未认证 401、token type 不符 403，均不签发、不产生上传。
- 凭据安全：`access_key`/`secret_key` 仅经环境变量注入、config.yaml 保持空值、不提交仓库、不进日志/响应；凭据或 bucket/domain 缺失/非法时签发接口返回稳定 17002，不泄漏任何凭据细节。

## 6. 业务不变量

- INV-001（授权边界）：未认证 401、type 不符 403，不签发、不产生上传与 DB 写入。
- INV-002（文件边界）：声明扩展名/MIME 不在白名单、或大小超上限 → 400（17001），不签发；最终文件级强制由 token 的 `mimeLimit`/`fsizeLimit` 由七牛服务端执行。
- INV-003（凭据安全）：`secret_key` 不落默认值、不进日志/响应/仓库，缺失/非法返回稳定 17002。
- INV-004（key 唯一可控）：key 由后端预生成并写入 token scope，客户端不可任意指定。

## 7. 一致性模型与失败语义

- 事实来源：单一配置源（`manifest/config` 的七牛段），无 DB 写入、无 Redis 参与、无 MQ。
- 签发成功 = 返回一份受 scope 约束、带有效期（ttl）的 upload token 与对应 key/URL；**不代表文件已上传**，仅代表「获得在限制内直传的资格」。
- 失败语义：未认证 401、type 不符 403、非法扩展名/类型/大小 400（17001）、配置缺失/非法 500（17002）、签名/签发内部错误 500（17003）；均不产生上传与写入，且不泄漏凭据/内部路径/堆栈。
- 无跨系统事务、无并发写入、无幂等键、无重试语义。

## 8. 错误码域（17000-17999）

| code | 语义 | HTTP |
| --- | --- | --- |
| 17001 | UPLOAD_INVALID_INPUT（扩展名/MIME/参数非法） | 400 |
| 17002 | UPLOAD_CONFIG_INVALID（七牛配置缺失/非法，安全语义失败） | 500 |
| 17003 | UPLOAD_TOKEN_FAILED（签发凭证失败） | 500 |

复用：`1002`（401）、`1003`（403）、`1001`（参数格式错误兜底）、`1000`（500）。

## 9. 跨模块关系

- 上传能力为独立新增模块，不修改既有商品/分类/文章/轮播图/IAM 等模块行为。
- 与 `internal/storage` 的 `LocalStorage`（轮播图静态服务）并存、互不替换。
- 错误码域 `17000-17999` 的编码模型与分配规则见 `error-codes.md`，分配状态以 `.agent/registry/error-codes.md` 为权威。
- 官方 `qiniu/go-sdk` 为新增第三方依赖（`go.mod`）。

## 10. Deferred / 已知留白

- 文件记录落库（`files`/`uploads` 表）与业务实体关联，属后续任务（需七牛回调或客户端回传 key 才能可靠追踪直传结果）。
- 服务端强制内容校验：直传模型下后端无法在签发时强制校验真实文件内容；`mimeLimit` 依赖七牛对客户端上报 Content-Type 的执行，客户端可伪造声明。若需服务端强制校验，转后端代理上传或七牛回调，属后续 Contract Revision。
- CDN、图片处理、分片/断点续传、大文件直传优化。
- 其它对象存储（MinIO/OSS/S3）。
