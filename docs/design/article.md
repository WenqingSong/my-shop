# 用户文章设计（Article）

本文面向项目接手者，说明「用户文章」（用户侧 CMS）的架构、数据模型、作者归属、越权语义、点赞/收藏复用机制、关联清理语义与错误码域。事实来源为 `article-cms-v1` 最终 APPROVED Contract 与最终实现。

## 1. 职责与边界

用户文章回答「登录用户发布并管理自己的文章、任何人公开阅读、登录用户点赞/收藏文章」，承载文章主数据（`articles`）与文章交互数据（`article_likes`/`article_favorites`），实现：发布、修改、删除自己的文章，公开列表与详情，我的文章列表，点赞/取消点赞（幂等 + 公开计数 + 独立鉴权状态），收藏/取消收藏（幂等 + 独立鉴权状态 + 我的收藏列表）。

边界：纯用户域，**不涉及管理员、不新增权限 code、不改 `seed.go`、不涉及 `AdminAuth`/`RequirePermission`**；作者身份唯一来源 `Principal.UserID`；文章为同步写、无异步。事实来源单一 MySQL；Redis 仅会话；无 MQ、无缓存、无幂等键。

与商品点赞/收藏的关系：文章点赞/收藏**复用既有机制**（幂等 + 唯一约束 + 公开计数 + 独立鉴权状态查询），但使用**独立表** `article_likes`/`article_favorites`，不改动既有 `product_likes`/`favorites` 表结构、API 契约或错误语义（既有点赞/收藏字段为 `product_id`，非通用 `target_type/target_id`）。

## 2. 数据模型

### 2.1 `articles`（文章主数据）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `author_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK），取自 `Principal.UserID` |
| `title` | VARCHAR(64) | 非空，trim 后非空且 ≤64 字符 |
| `content` | TEXT | 非空，trim 后非空且 ≤10000 字符 |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |
| `updated_at` | DATETIME | 默认 `CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP` |

索引：`PRIMARY(id)`、`idx_author_id(author_id)`（支撑「我的文章」列表）。

无 `status`/`deleted_at`：文章为硬删除（见 §3），无状态机、无软删字段。

### 2.2 `article_likes`（文章点赞）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK），取自 `Principal.UserID` |
| `article_id` | BIGINT UNSIGNED | 非空，软引用 `articles.id`（无 FK），点赞时校验存在 |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

唯一约束：`uk_user_article(user_id, article_id)`（同一用户同一文章至多一条，兼作按用户查询索引）。

索引：`idx_article_id(article_id)`（支撑公开点赞数实时 COUNT）。

无 `updated_at`（二元关系，无字段可变）。

### 2.3 `article_favorites`（文章收藏）

| 字段 | 类型 | 约束/说明 |
| --- | --- | --- |
| `id` | BIGINT UNSIGNED | 主键，自增 |
| `user_id` | BIGINT UNSIGNED | 非空，软引用 `users.id`（无 FK），取自 `Principal.UserID` |
| `article_id` | BIGINT UNSIGNED | 非空，软引用 `articles.id`（无 FK），收藏时校验存在 |
| `created_at` | DATETIME | 默认 `CURRENT_TIMESTAMP` |

唯一约束：`uk_user_article(user_id, article_id)`（前导列 `user_id` 兼作「我的收藏」查询索引）。

无 `updated_at`（二元关系）。

## 3. 生命周期与语义

文章无状态机（无持久化状态字段），生命周期为「发布 → 修改 → 删除」，均以作者本人为操作主体：

- **发布**：`POST /articles`，请求体仅 `title`/`content`；`author_id` 由服务端取自 `Principal.UserID`，客户端提交的任何身份字段被忽略。
- **修改**：`PUT /articles/:id`，条件更新 `WHERE id AND author_id`，核对 `RowsAffected`，=0 → 404（16001，防枚举）。
- **删除**：`DELETE /articles/:id`，**硬删除 + 单事务清理**：同一事务内 `DELETE article_likes` → `DELETE article_favorites` → `DELETE articles WHERE id AND author_id`（核对 `RowsAffected`，=0 回滚返回 404）；任一步失败整体回滚，无孤儿残留、无半删状态。
- **公开列表/详情**：无需登录，`GET /articles`（分页 `ORDER BY id DESC`）、`GET /articles/:id`。
- **点赞/收藏**：幂等；重复点赞/收藏/取消均 no-op 成功，由 `uk_user_article` 唯一约束兜底并发。
- **公开点赞数**：`GET /articles/:id/like/count`（无需 token），`article_likes` 按 `article_id` 实时 `COUNT(*)`；收藏数不公开（与商品收藏一致）。
- **本人状态**：`GET /articles/:id/like/check`、`GET /articles/:id/favorite/check`（独立鉴权接口），详情保持公开、不引入 Optional Auth。

## 4. 业务不变量

- INV-001（服务端归属绑定）：`articles.author_id` 与 `article_likes/article_favorites.user_id` 全部取自 `Principal.UserID`；客户端提交的身份字段被忽略，不可伪造。
- INV-002（越权隔离 + 防枚举）：非作者修改/删除他人文章 → 404（16001）且无写入；「我的文章」/「我的收藏」仅返回本人记录。
- INV-003（同用户同文章唯一 + 幂等）：同一用户对同一文章至多一条点赞/收藏，由 `uk_user_article` 兜底并发；重复操作幂等成功。
- INV-004（删除关联清理）：删除文章后 `article_likes`/`article_favorites` 无残留，公开计数/列表正确回落。
- INV-005（鉴权边界）：公开读无需登录；写与本人状态查询需有效凭证（401 无写入）；既有商品点赞/收藏行为不受影响。

## 5. 一致性模型与失败语义

- 事实来源单一 MySQL：`articles`（文章事实）、`article_likes`/`article_favorites`（交互事实）、`users`（只读，供 `author_username` 联查）。Redis 仅会话；无 MQ、无异步、无跨系统事务。
- 发布成功 = 单条 `INSERT articles`；修改/删除成功 = 条件更新/删除 + `RowsAffected` 核对（=0 → 16001）。
- 删除 = 单事务（三表），任一步失败整体回滚。
- 点赞/收藏 = 单条 `INSERT`，命中 `uk_user_article`（1062）视为幂等成功；取消 = `DELETE WHERE user_id AND article_id`，`RowsAffected=0` 视为幂等成功。点赞/收藏前校验文章存在（不存在 → 16001）；校验与写入间 TOCTOU 允许（与商品点赞/收藏同立场）。
- 公开点赞数 = 实时 `COUNT(*)`，恒准确、无冗余列、无双重写。
- 失败语义：认证 401、参数非法 400、文章不存在/非作者 404（16001），均无写入；DB 技术错误统一 1000（500）。

## 6. 安全与权限边界

- 前台公开接口（无 token）：`GET /articles`、`GET /articles/:id`、`GET /articles/:id/like/count`。
- 前台用户接口（`Auth`，作用于 `Principal.UserID`）：`POST /articles`、`PUT /articles/:id`、`DELETE /articles/:id`、`GET /my/articles`、`POST /articles/:id/like`、`DELETE /articles/:id/like`、`GET /articles/:id/like/check`、`POST /articles/:id/favorite`、`DELETE /articles/:id/favorite`、`GET /articles/:id/favorite/check`、`GET /my/articles/favorites`。
- 身份信任：`Principal.UserID` 是唯一身份来源；不信任请求自带身份。
- 越权语义：非作者越权与文章不存在统一 404（16001，防枚举）；无管理员权限体系参与。

## 7. 错误码域（16000-16999）

| code | 语义 | HTTP |
| --- | --- | --- |
| 16001 | ARTICLE_NOT_FOUND（文章不存在或非本人，防枚举；点赞/收藏目标不存在亦复用） | 404 |
| 16002 | ARTICLE_INVALID_INPUT（标题/内容为空或超长） | 400 |

复用：`1001`（参数格式错误 → 400）、`1002`（未认证 → 401）。

## 8. 跨模块关系

- `articles.author_id` → `users.id`（软引用，无 FK）：取自 `Principal.UserID`；公开列表/详情经 `LEFT JOIN users` 取 `username` 作为 `author_username` 展示名。
- `article_likes.article_id` / `article_favorites.article_id` → `articles.id`（软引用，无 FK）：点赞/收藏时校验文章存在；删除文章时在事务内清理。
- `article_likes.user_id` / `article_favorites.user_id` → `users.id`（软引用，无 FK）：取自 `Principal.UserID`。
- 建表经 golang-migrate 迁移（`20261001000017_articles.up.sql`），见 `migration.md`。
- 错误码域经全局资源预留（域序 16），权威分配状态见 `.agent/registry/error-codes.md`。
- 与商品点赞/收藏（`like.md`/`favorite.md`）的边界：文章点赞/收藏为独立表、独立域、独立 API 面，复用机制但不共享表、不改动商品点赞/收藏。

## 9. Deferred / 已知留白

- 评论、关注/粉丝、标签/分类、推荐/热榜、全文搜索、审核工作流、草稿/定时发布：Out of Scope。
- 独立后台 CMS（后台管理/下架文章）：Out of Scope，不新增 `article:*` 权限 code。
- 文章软删/回收站/管理员审计：V1 为硬删除；如需软删需新增 `status`/`deleted_at`（属 Contract Revision）。
- 详情内联点赞/收藏态：V1 用独立鉴权接口，不引入 Optional Auth；若未来要求详情内联，需改详情为 auth 感知（属 Contract Revision）。
