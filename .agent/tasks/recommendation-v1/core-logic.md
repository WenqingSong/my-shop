# Cleaner Core Logic 验证卡

本文只承载 Owner 应理解并亲自验证的核心机制（因果说明 + 验证证据），不承担 Owner 决策状态权威。Owner 决策状态由 OwnerGate 在 Owner 明确决定后持久化到 `state.owner`。

## CL-001：前台只返回「启用位中的可售商品」，防止下架/draft 商品泄漏到公开接口

- Owner 需要理解：`GET /recommendations/:code` 是**无 token 的公开接口**。若过滤条件漏掉 `products.status=on_shelf`，任何 draft（未上架）/off_shelf（已下架）商品都会直接暴露给匿名访问者，这是公开数据泄漏。反过来，推荐关系本身永不因商品下架被物理删除（下架只是让前台暂时看不到）。
- 生产代码：`internal/logic/recommendation/recommendation.go` `Frontend`（L79-119）。关键两处过滤：L88 `Where("status", statusEnabled)`（推荐位必须启用）、L98-103 `InnerJoin("products p", "p.id = ri.product_id")` + `Where("p.status", productStatusOnShelf)`（商品必须 on_shelf），且 `Order("ri.sort","ri.id")` 稳定排序。
- 关键测试：`TestRecommendationFrontendOnlyOnShelfAndSorted`（`internal/cmd/recommendation_test.go` L197-269）：预置 on_shelf×3、off_shelf×1、draft×1，断言前台恰好 3 条、顺序 `sort,id` 稳定，并断言禁用位/不存在位返回空。
- 基线验证：`go test ./internal/cmd -run TestRecommendationFrontendOnlyOnShelfAndSorted -count=1` → 预期 PASS（需 MySQL/Redis 就绪）。
- 可选 Mutation：临时删除 L101 的 `Where("p.status", productStatusOnShelf)`（或把 `productStatusOnShelf` 改成 0）。
- 预期失败：上述测试会失败——`len(fd.Items)` 从 3 变成 5（off_shelf/draft 商品被错误返回），顺序断言也随之破坏。
- 恢复确认：恢复该行后重新运行同一命令，应重新 PASS。

## CL-002：同一推荐位同一商品至多一条关系，DB 唯一约束是并发重复的最后兜底

- Owner 需要理解：如果「去重」只靠应用层「先查再写」，两个并发添加请求会同时通过检查、各自插入，产生重复关系记录，前台出现同一商品重复展示。因此正确性必须由数据库唯一约束 `uk_position_product(position_id, product_id)` 保证，应用层只负责把 DB 冲突（MySQL 1062）翻译成稳定错误码 15005 返回给调用方。
- 生产代码：`internal/migrations/sql/20261001000016_recommend.up.sql` L31 `UNIQUE KEY uk_position_product (position_id, product_id)`；`internal/logic/recommendation/recommendation.go` `AddItem`（L277-287 插入 + L283-285 `isDuplicateKeyError`→15005）与 `isDuplicateKeyError`（L469-472，`errors.As` 识别 `mysql.MySQLError.Number==1062`）。
- 关键测试：`TestRecommendationDuplicateRejected`（`internal/cmd/recommendation_test.go` L468-489，重复添加→409/15005 且关系仅 1 条）+ `TestSchemaStructureMatchesBaseline`（`internal/migrations/migrations_test.go`，断言 `uk_position_product` 唯一索引真实存在）。
- 基线验证：`go test ./internal/cmd -run TestRecommendationDuplicateRejected -count=1` → 预期 PASS。
- 可选 Mutation：临时把 `AddItem` 里 `if isDuplicateKeyError(err) { return 15005 }` 这段去掉（让 1062 落进 `CodeInternalError`）。
- 预期失败：`TestRecommendationDuplicateRejected` 会失败——第二次添加期望 409/15005，实际返回 500（或内部错误码），证明「错误码翻译」是关键行为而非无关样板。
- 恢复确认：恢复该段后重新运行同一命令，应重新 PASS。并发场景下「唯一约束兜底」的机制由 `uk_position_product` 索引存在性（`TestSchemaStructureMatchesBaseline`）间接证明。

## CL-003：调整排序必须覆盖全部已加入商品（全量重排），缺漏被拒

- Owner 需要理解：`PUT /admin/recommend-positions/:id/items/sort` 提交的有序 `product_ids` 必须**恰好覆盖该推荐位全部已加入商品**。若允许「部分列表」，未提交的商品会保留旧 `sort`，与新赋值的 `sort=0..n-1` 交错，导致排序语义不完整、运营预期混乱。因此缺漏被拒（15006）、含未加入商品被拒（15004）、重复/空被拒（15003），只有完整列表才原子写入全部 `sort`。
- 生产代码：`internal/logic/recommendation/recommendation.go` `UpdateSort`（L320-389）。缺漏校验在 L357-362（`for _, r := range existing { if !seen[r.ProductId] → 15006 }`）；多余校验在 L352-356（`15004`）；重复/空在 L328-340（`15003`）；全部校验通过后 L364-375 事务内按序写入。错误码 `15006 CodeRecommendItemSortMismatch`（`internal/codes/codes.go`，409）。
- 关键测试：`TestRecommendationUpdateSortIncomplete`（`internal/cmd/recommendation_test.go`）：缺漏 `[p1]`→409/15006、多余 `[p1,999999]`→404/15004、完整 `[p2,p1]`→200。
- 基线验证：`go test ./internal/cmd -run TestRecommendationUpdateSortIncomplete -count=1` → 预期 PASS。
- 可选 Mutation：临时删除 L357-362 的缺漏校验循环（`for _, r := range existing { ... }`）。
- 预期失败：`TestRecommendationUpdateSortIncomplete` 的缺漏用例会失败——提交 `[p1]` 期望 409/15006，实际会通过校验并返回 200，证明「缺漏拒绝」是关键业务规则而非无关样板。
- 恢复确认：恢复该循环后重新运行同一命令，应重新 PASS。
