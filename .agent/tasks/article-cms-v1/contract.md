# Technical Contract

## Decision Status
APPROVED

## Problem

交付「用户文章」核心闭环：登录用户发布/修改/删除自己的文章；任何人公开浏览列表与详情；登录用户点赞/收藏（幂等、公开计数、独立鉴权状态）。作者身份唯一来源 `Principal.UserID`；非作者不能改删他人文章；「我的文章」只返回本人。点赞/收藏复用既有**机制**（幂等 + 唯一约束 + 公开计数 + 独立鉴权状态查询），但**不得破坏既有商品点赞/收藏**（`product_likes`/`favorites` 及其 API/错误语义保持不变）。

Owner 已决定两处关键选择（D1/D2），并补充：创建文章的请求参数不暴露 `author_id`/`user_id`，作者身份直接以 `Principal.UserID` 为唯一来源。

## Verified Current Behavior

- VERIFIED：技术栈 GoFrame v2，模块 `cnb.cool/go-cloud-devops/my-shop`；分层 `api/<module>/v1`（`g.Meta` 声明 path/method）→ `internal/controller` → `internal/service`（接口 + `Register`）→ `internal/logic`（`init()` 注册），数据访问用 `g.DB().Model()`，无独立 dao/model 层（`internal/logic/like/like.go`、`internal/logic/favorite/favorite.go`、`internal/logic/review/review.go`）。
- VERIFIED：身份经 `middleware.Auth` 注入 `Principal{UserID, Sid}`，`middleware.PrincipalFromContext` 读取（`internal/middleware/principal.go`）；受保护路由挂 `internal/cmd/routes_frontend.go` 的 `Auth` 分组。
- VERIFIED：既有 `product_likes`（`user_id`+`product_id`，`uk_user_product`，`idx_product_id`）与 `favorites`（`user_id`+`product_id`，`uk_user_product`）字段为 `product_id`，**非通用 `target_type/target_id`**，不能直接对文章复用同一张表（`internal/migrations/sql/20261001000013_product_likes.up.sql`、`20261001000010_favorites.up.sql`）。
- VERIFIED：作者越权既有模式为条件更新/删除 `WHERE id AND user_id` + 核对 `RowsAffected`，=0 → 404 防枚举（`internal/logic/review/review.go` 的 `Update`/`Delete`）。
- VERIFIED：既有「是否已点赞/收藏」用独立鉴权接口 `GET /likes/check`、`GET /favorites/check`；公开点赞数用独立公开接口 `GET /likes/count`（无需 token）；商品详情 `GET /products/:id` 保持公开无 token（`internal/cmd/routes_frontend.go`）。
- VERIFIED：`users` 表仅 `id`/`username`/`password_hash`/时间戳，无昵称/头像（`internal/migrations/migrations_test.go` 的 `expectedSchema` users 表）。
- VERIFIED：错误码集中在 `internal/codes/codes.go`；`.agent/registry/error-codes.md` 最新（`origin/develop`）max 域序 = 15（recommendation-v1 已并发预留 15000-15999）→ 文章新域序 = 16（区间 `16000-16999`）。
- VERIFIED：迁移为 golang-migrate v4；`.agent/registry/migrations.md` 最新（`origin/develop`）max version = `20261001000016`（recommendation-v1 已并发预留）→ 文章 migration version = `20261001000017`。
- VERIFIED：`internal/migrations/migrations_test.go` 的 `latestMigrationVersion` 现为 `20261001000014`；`TestUpAppliesOnlyPendingMigration` / `TestUpFailsFastAndMarksDirty` 用 `20261001000015` 作为「探针 next version」。文章迁移为 17 后，这些探针版本必须上移至 `20261001000018`，否则断言失败。
- VERIFIED：事实来源单一 MySQL；Redis 仅会话；无 MQ、无异步；文章为同步写。

## Recommendation

RECOMMENDATION：新建独立 `article` 模块与独立三表 `articles` / `article_likes` / `article_favorites`；文章**硬删 + 单事务清理**点赞/收藏关联数据；详情点赞/收藏态用**独立鉴权接口**承载（详情保持公开无 token、不引入 Optional Auth）；公开点赞数用独立公开接口；收藏数不公开（与既有收藏一致）；创建请求不暴露作者身份字段。

关键取舍：独立表 + 独立接口完全规避对商品点赞/收藏的破坏，代价是新增一套结构而非泛化复用；硬删+事务清理满足「不残留孤儿数据」且无需新增 `status`/`deleted_at` 列，代价是删除不可恢复、无审计（V1 可接受）。

不改变既有行为：不修改 `product_likes`/`favorites` 及其 API/错误语义；不修改 `products`/`review`/`like`/`favorite` 模块；不新增权限 code、不改 `seed.go`、不涉及 `AdminAuth`/`RequirePermission`。

失败表现：认证失败 401、参数非法 400、文章不存在/非作者 404（防枚举）、点赞/收藏目标文章不存在 404，均无写入；DB 技术错误统一 1000（500）。事务任一步失败整体回滚，不产生半删状态。

## Selected Design

（Owner 已批准）

- **D1（删除语义）= 硬删除 + 事务清理**：`DELETE` 文章并清理 `article_likes`/`article_favorites` 在同一事务内完成，任一步失败整体回滚，无孤儿残留、无半删状态。
- **D2（详情点赞/收藏态）= 独立鉴权接口**：文章详情 `GET /articles/:id` 保持公开无 token，不引入 Optional Auth；点赞/收藏状态继续走独立鉴权 check 接口（`GET /articles/:id/like/check`、`GET /articles/:id/favorite/check`）。
- **作者身份**：创建文章请求体仅含 `title`/`content`，不暴露 `author_id`/`user_id`；作者身份以 `Principal.UserID` 为唯一来源，客户端提交的任何身份字段被忽略。
- 模块/表结构/错误码/迁移/校验按本 Contract「Interfaces and Data」执行。

## Interfaces and Data

### 模块与分层（新建，全部为 `article` 单模块）
- `api/article/v1`（请求/响应契约，`g.Meta` 声明完整 path）；`internal/controller/article`；`internal/service/article.go`（`IArticle` 接口 + `RegisterArticle`）；`internal/logic/article`（`init()` 注册）。点赞/收藏作为 `article` 模块子能力，同一 `IArticle` 接口承载（因删除需在同一事务内清理三表）。

### API 面（最终 path 与 `g.Meta` 一致）
公开（无需 token）：
- `GET /articles` → 公开列表（分页，`ORDER BY id DESC`）
- `GET /articles/:id` → 公开详情
- `GET /articles/:id/like/count` → 公开点赞数 `{count}`

登录（`Auth`，作用于 `Principal.UserID`）：
- `POST /articles` → 发布（body 仅 `title`/`content`，作者取 `Principal.UserID`，忽略任何客户端身份字段）
- `PUT /articles/:id` → 修改本人文章
- `DELETE /articles/:id` → 删除本人文章 + 事务清理点赞/收藏
- `GET /my/articles` → 我的文章列表（分页）
- `POST /articles/:id/like` → 点赞（幂等）
- `DELETE /articles/:id/like` → 取消点赞（幂等）
- `GET /articles/:id/like/check` → 本人是否已点赞 `{liked}`
- `POST /articles/:id/favorite` → 收藏（幂等）
- `DELETE /articles/:id/favorite` → 取消收藏（幂等）
- `GET /articles/:id/favorite/check` → 本人是否已收藏 `{favorited}`
- `GET /my/articles/favorites` → 我的收藏列表（分页）

响应字段：文章条目含 `id`、`author_id`、`author_username`（LEFT JOIN `users.username`）、`title`、`content`（详情含、列表可省略）、`created_at`、`updated_at`。

### 数据模型（migration `20261001000017_articles.up.sql`，单文件三表）
`articles`：
| 字段 | 类型 | 约束 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | PK 自增 |
| `author_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK），取 `Principal.UserID` |
| `title` | VARCHAR(64) | 非空 |
| `content` | TEXT | 非空 |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |
| `updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`idx_author_id(author_id)`（支撑「我的文章」）。

`article_likes`（对齐 `product_likes`）：
- `id` BIGINT UNSIGNED PK 自增；`user_id`/`article_id` BIGINT UNSIGNED 非空（软引用，无 FK）；`created_at` DATETIME 默认 `CURRENT_TIMESTAMP`。
- 索引：`PRIMARY(id)`、`uk_user_article(user_id, article_id)` 唯一、`idx_article_id(article_id)`（支撑公开计数）。
- 无 `updated_at`（二元关系）。

`article_favorites`（对齐 `favorites`）：
- `id` BIGINT UNSIGNED PK 自增；`user_id`/`article_id` BIGINT UNSIGNED 非空（软引用，无 FK）；`created_at` DATETIME 默认 `CURRENT_TIMESTAMP`。
- 索引：`PRIMARY(id)`、`uk_user_article(user_id, article_id)` 唯一（前导列 `user_id` 兼作「我的收藏」查询）。
- 无 `updated_at`；无 `idx_article_id`（删除按 `article_id` 的扫描为低频可接受，与 `favorites` 一致）。

`internal/migrations/migrations_test.go` 同步更新：`latestMigrationVersion = 20261001000017`；`businessTables` 增 `articles`/`article_likes`/`article_favorites`（无 FK，DROP 顺序无额外约束）；`expectedSchema` 增三张表精确快照；测试探针版本 `20261001000015` → `20261001000018`（共 4 处：`TestUpAppliesOnlyPendingMigration` 的 map key 与断言、`TestUpFailsFastAndMarksDirty` 的 broken map key 与 `Force` 值）。

### 输入校验（Analyst 固化）
- `title`：trim 后非空且 `RuneCount ≤ 64`；`content`：trim 后非空且 `RuneCount ≤ 10000`；非法 → `16002`（400）。

### 全局资源清单（已派生，待落 Registry RESERVED）
| kind | candidate | 语义/拥有方 | 依据 |
| --- | --- | --- | --- |
| `error_code_domain` | 域序 16 → `16000-16999` | article（article-cms-v1） | `max(已记录域序=15)+1`（recommendation-v1 已占 15） |
| `migration_version` | `20261001000017` | articles（article-cms-v1） | `max(已记录 version=20261001000016)+1`（recommendation-v1 已占 16） |

域内错误码（写入 `internal/codes/codes.go` 与 `codeTable`）：
| code | 语义 | HTTP |
| --- | --- | --- |
| 16001 | ARTICLE_NOT_FOUND（文章不存在或非本人，防枚举；点赞/收藏目标不存在亦复用） | 404 |
| 16002 | ARTICLE_INVALID_INPUT（标题/内容为空或超长） | 400 |

复用：`1001`（400）、`1002`（401）；点赞/收藏无「已点赞/已收藏」错误码（幂等 no-op），无「标题重复」错误码（标题不唯一）。

## Business Invariants

- INV-001（服务端归属绑定）：`articles.author_id` 与 `article_likes/article_favorites.user_id` 全部取自 `Principal.UserID`；客户端提交的任何身份字段被忽略，不可伪造作者或伪造点赞/收藏归属。
- INV-002（越权隔离 + 防枚举）：非作者修改/删除他人文章 → 404（16001）且无写入；「我的文章」/「我的收藏」仅返回 `user_id == Principal.UserID` 的记录。
- INV-003（同用户同文章唯一 + 幂等）：同一用户对同一文章至多一条点赞、至多一条收藏，由 `uk_user_article` 唯一约束兜底并发；重复点赞/收藏/取消均幂等成功（no-op，命中 1062 视为成功）。
- INV-004（删除关联清理）：删除文章后 `article_likes`/`article_favorites` 无残留（事务内清理），公开计数/列表正确回落。
- INV-005（鉴权边界）：公开读（列表/详情/公开计数）无需登录；所有写与本人状态查询需有效凭证（无/非法 token → 401 且无写入）；既有商品点赞/收藏行为不受影响。

## Failure and Consistency Semantics

- 事实来源单一 MySQL：`articles`（文章事实）、`article_likes`/`article_favorites`（交互事实）、`users`（只读，供 author_username 联查）。Redis 仅会话，不参与；无 MQ、无异步、无跨系统事务。
- 发布成功 = 单条 `INSERT articles`（作者服务端绑定）；修改/删除成功 = 条件更新/删除 + `RowsAffected` 核对（=0 → 16001）。
- 删除 = 单事务：`DELETE article_likes WHERE article_id=?` → `DELETE article_favorites WHERE article_id=?` → `DELETE articles WHERE id=? AND author_id=?`（核对 `RowsAffected`，=0 回滚并返回 16001）；任一步失败整体回滚，无半删/孤儿。
- 点赞/收藏 = 单条 `INSERT`，命中 `uk_user_article`（1062）视为幂等成功；取消 = `DELETE WHERE user_id=? AND article_id=?`，`RowsAffected=0` 视为幂等成功（no-op）。点赞/收藏前校验文章存在（不存在 → 16001）；校验与写入间 TOCTOU 允许（与既有商品点赞/收藏同立场）。
- 公开点赞数 = `article_likes` 按 `article_id` 实时 `COUNT(*)`，恒准确、无冗余列、无双重写。
- 删除与并发点赞的极小竞态（删除事务提交边界处插入新点赞导致悬空引用）属可接受 TOCTOU 窗口，见 Open Risks。

## Allowed / Forbidden Changes

- 允许：新增 `api/article/v1`、`internal/controller/article`、`internal/service/article.go`、`internal/logic/article`、`internal/codes` 文章域扩展、`20261001000017_articles.up.sql`、`internal/cmd/routes_frontend.go` 文章路由扩展与对应测试；`internal/migrations/migrations_test.go` 同步更新。
- 禁止：修改 `product_likes`/`favorites` 表结构、其 API 契约与错误语义；修改 `products`/`review`/`like`/`favorite` 既有模块行为；修改 `users` 表；新增 `article:*` 权限 code、改 `seed.go`、涉及 `AdminAuth`/`RequirePermission`；复用或泛化既有 `product_likes`/`favorites` 为 `target_type/target_id`；改既有公开接口与错误码取值。

## Verification Requirements

- INV-001 → 需 MySQL+Redis：登录用户发布文章，断言落库 `author_id == Principal.UserID`；提交伪造 `author_id` 被忽略；创建请求契约不含 `author_id`/`user_id` 字段。
- INV-002 → 需 MySQL+Redis：作者修改成功；另一用户修改/删除返回 404 且内容不变/无删除；`GET /my/articles` 仅含本人文章。
- INV-003 → 需 MySQL（`-race`）：重复点赞/收藏幂等、唯一约束兜底并发（并发重复只落一条）；取消幂等；商品点赞/收藏行为不受影响（回归既有测试通过）。
- INV-004 → 需 MySQL：删除文章后 `article_likes`/`article_favorites` 无残留、公开计数回落。
- INV-005 → 需 MySQL+Redis：无/非法 token 访问写接口 401 且无写入；公开读/公开计数无需 token 可访问。
- AC-012 → 需 MySQL：执行迁移断言三表结构正确、`migrations_test.go`（含探针版本 18）通过、迁移可重复执行。
- AC-013 → 文档审查：`docs/design/article.md` 与 APPROVED Contract、最终实现一致（Design Impact = NEW，已由 Analyst 在 Coder 前写入）。
- 通用命令：`gofmt`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`。

## Open Risks

- 删除事务与并发点赞的边界竞态：删除事务提交前，若有并发点赞已校验文章存在但尚未 INSERT，事务提交后该 INSERT 可能产生指向已删文章的悬空引用。概率极低、与既有商品点赞/收藏的 TOCTOU 立场一致；如需彻底消除需引入行锁或软删，属后续 Contract Revision。
- 文章硬删不可恢复、无审计；若未来需「回收站/管理员审计/软删」，需改数据模型（新增 `status`/`deleted_at`），属 Contract Revision。
- `author_username` 联查依赖 `users` 存在；用户被删除（当前 `users` 无删除接口）时用户名悬空，需 `LEFT JOIN` 容忍并返回空（与 `favorites` 联查 `products` 的容忍一致）。
- migration version 15（flash-sale-v3）、16（recommendation-v1）为并行任务已 RESERVED、文章为 17；`migrations_test.go` 的 `latestMigrationVersion` 在文章分支置 17（跳过 15/16 的空隙，golang-migrate 容忍不连续 version）。并行分支合并到 `develop` 时 `latestMigrationVersion`/探针版本可能需由集成方按合并顺序协调，属 Owner 集成关注，非本任务阻塞。

## Owner Decision Record

- D1（删除语义）：**APPROVED —— 硬删除 + 事务清理**（`DELETE` 文章并清理 `article_likes`/`article_favorites` 同一事务，失败整体回滚）。
- D2（详情点赞/收藏态）：**APPROVED —— 独立鉴权接口**（详情保持公开无 token，不引入 Optional Auth）。
- 补充约束：**创建文章请求不暴露 `author_id`/`user_id`**，作者身份以 `Principal.UserID` 为唯一来源。
- 决策范围：适用于本任务（article-cms-v1）全部实现与测试；不改变 Task 的 Goal/Scope/AC。
