# Owner Core Logic 验证卡

Cleaner 审查结论：CLEAN。以下两张验证卡面向 Owner，聚焦「权限/安全」与「幂等/一致性」两条最关键的因果链。Owner 无需逐行 Review 全部 Diff。

## CL-001：用户隔离与越权无写入（INV-001）

- Owner 需要理解：收藏只归属登录用户的 `Principal.UserID`；所有读写都按 `user_id` 过滤，身份只取自认证中间件注入的 Principal，不信任请求参数。若漏掉 `user_id` 过滤，任何用户都能查看/删除他人收藏，且会真实落库写坏数据。
- 生产代码：`internal/logic/favorite/favorite.go` 的 `List`/`Remove`/`Check`（均 `Where("user_id", userID)`）；`internal/controller/favorite/favorite.go` 的 `currentUserID`（`middleware.PrincipalFromContext(ctx)` 取身份，缺失返回 401）。
- 关键测试：`internal/cmd/favorite_test.go` 的 `TestFavoriteUserIsolation`。
- 基线验证：`go test -p 1 ./internal/cmd/ -run TestFavoriteUserIsolation -v` → 预期 PASS。
- 可选 Mutation：把 `Remove` 的 `Where("user_id", userID)` 去掉，只保留 `Where("product_id", productID)`。
- 预期失败：`TestFavoriteUserIsolation` 在「B 取消 A 的收藏」后断言 `favoriteCount(t) != 1` 必须失败（A 的记录被 B 误删，count 变成 0）。
- 恢复确认：恢复 `Where("user_id", userID)` 后重跑该测试 → 预期 PASS。

## CL-002：重复收藏幂等 + 唯一约束并发兜底（INV-002）

- Owner 需要理解：同一用户对同一商品至多一条收藏。并发重复收藏由 `uk_user_product (user_id, product_id)` 唯一约束兜底，写入撞 1062 时视为幂等成功（no-op），不产生第二条记录；若去掉唯一约束或错误地把 1062 当失败，要么数据重复，要么并发请求报错。
- 生产代码：`internal/logic/favorite/favorite.go` 的 `Add`（`isDuplicateKeyError` 识别 MySQL 1062 → 幂等成功）；`internal/migrations/sql/20261001000010_favorites.up.sql` 的 `UNIQUE KEY uk_user_product (user_id, product_id)`。
- 关键测试：`internal/cmd/favorite_test.go` 的 `TestFavoriteConcurrentDuplicateSingleRow`（8 并发）与 `TestFavoriteDuplicateIdempotent`（顺序重复）。
- 基线验证：`go test -p 1 -race ./internal/cmd/ -run TestFavoriteConcurrentDuplicateSingleRow -v` → 预期 PASS，且 `favoriteCount == 1`。
- 可选 Mutation：把迁移文件中 `UNIQUE KEY uk_user_product` 改为普通 `KEY`（去掉唯一性）并重建表。
- 预期失败：`TestFavoriteConcurrentDuplicateSingleRow` 断言 `favoriteCount(t) == 1` 必须失败（并发下产生 8 条记录）。
- 恢复确认：恢复唯一约束并重建表后重跑该测试 → 预期 PASS。
