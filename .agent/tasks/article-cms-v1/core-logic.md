# Owner 核心逻辑验证

> 以下验证卡仅承载「值得 Owner 理解的核心机制、因果与验证证据」，不承载 Owner 决策状态。Owner 是否接受由 OwnerGate 在 Owner 明确决定后写入 `state.owner`。

## CL-001：服务端归属绑定 + 越权隔离（作者身份不可伪造）

- Owner 需要理解：文章作者身份唯一来源是登录上下文 `Principal.UserID`，请求体不暴露 `author_id`/`user_id`；非作者修改/删除他人文章统一返回 404（16001，防枚举）且零写入。若归属判断被破坏，任意登录用户可篡改/删除他人文章。
- 生产代码：
  - `internal/controller/article/article.go:136-142`（`currentUserID` 从 `Principal` 取 `UserID`）
  - `internal/logic/article/article.go:89-116`（`Create`：`author_id=userID`，忽略客户端身份字段）
  - `internal/logic/article/article.go:118-149`（`Update`：条件 `WHERE id AND author_id` + `RowsAffected` 核对）
  - `internal/logic/article/article.go:151-174`（`Delete`：条件删除 + 事务 + `RowsAffected` 核对）
- 关键测试：
  - `TestArticleCreateDetailOwnership`（`internal/cmd/article_test.go:109`）
  - `TestArticleUpdateOwnership`（`internal/cmd/article_test.go:218`）
  - `TestArticleDeleteOwnershipAndCleanup`（`internal/cmd/article_test.go:257`）
- 基线验证：`go test -race -run 'TestArticle(CreateDetailOwnership|UpdateOwnership|DeleteOwnershipAndCleanup)' ./internal/cmd/...` → 应 PASS
- 可选 Mutation：删除 `internal/logic/article/article.go` `Update` 内的 `Where("author_id", userID)` 过滤
- 预期失败：`TestArticleUpdateOwnership` 必须以 `intruder update: status=200 code=0 want 404/16001` 失败（已实测：移除后该测试 FAIL）
- 恢复确认：恢复该行后重新运行 `go test -run TestArticleUpdateOwnership ./internal/cmd/...` → 恢复 PASS

## CL-002：删除事务清理 + 幂等唯一约束（无孤儿残留 / 重复不落多条）

- Owner 需要理解：删除文章在同一事务内先清 `article_likes`→`article_favorites`→再条件删除文章本体，任一步失败整体回滚（无半删/孤儿）；点赞/收藏靠 `uk_user_article(user_id, article_id)` 唯一约束兜底并发，重复操作幂等成功（命中 1062 视为成功）。
- 生产代码：
  - `internal/logic/article/article.go:151-174`（`Delete` 单事务清理）
  - `internal/logic/article/article.go:197-215` / `:257-275`（`Like`/`Favorite`，1062 幂等）
  - `internal/migrations/sql/20261001000017_articles.up.sql:22-42`（`article_likes`/`article_favorites` 的 `uk_user_article` 唯一约束）
- 关键测试：
  - `TestArticleDeleteOwnershipAndCleanup`（删除后三表 0 行、计数回落）
  - `TestArticleLikeConcurrentDuplicateSingleRow`（`-race`，8 并发仅落一条）
- 基线验证：`go test -race -run 'TestArticle(DeleteOwnershipAndCleanup|LikeConcurrentDuplicateSingleRow)' ./internal/cmd/...` → 应 PASS
- 可选 Mutation：删除 `Delete` 内 `article_likes`/`article_favorites` 的清理语句
- 预期失败：`TestArticleDeleteOwnershipAndCleanup` 会因 `article_likes`/`article_favorites` 残留（`count want 0`）而失败
- 恢复确认：恢复清理语句后重新运行该测试 → 恢复 PASS
