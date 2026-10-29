# Owner Core Logic 验证卡

本文只承载 Owner 需要理解并亲自验证的核心机制、因果说明与验证证据，不承担 Owner 决策状态权威（由 OwnerGate 在 Owner 明确决定后写 `state.owner`）。

## CL-001：公开列表可见性与排序（INV-001）

- Owner 需要理解：前台 `GET /banners` 只能返回 `status=1` 的轮播图，禁用项必须即时消失；顺序由 `sort` 升序、同值按 `id` 升序兜底。若过滤被破坏，禁用/下线内容会泄漏到首页，直接造成运营事故。
- 生产代码：`internal/logic/banner/banner.go` `List`（`g.DB().Model("banners").Where("status", 1).Order("sort", "id").Scan(...)`）
- 关键测试：`TestBannerPublicListEnabledAndSorted`（`internal/cmd/banner_test.go`），预置 3 启用 + 1 禁用（含 sort 同值），断言仅 3 项、顺序 B(sort=5)→A(sort=10,id小)→C(sort=10,id大)、字段完整。
- 基线验证：`go test ./internal/cmd -run TestBannerPublicListEnabledAndSorted -count=1` → 预期 PASS。
- 可选 Mutation：把 `List` 中 `Where("status", statusEnabled)` 去掉（不过滤状态）。
- 预期失败：`TestBannerPublicListEnabledAndSorted` 失败——公开列表会多出禁用项，`len(items)!=3` 变为 4。
- 恢复确认：恢复 `Where("status", statusEnabled)` 后再次运行同一命令 → PASS。

## CL-002：后台写权限边界（INV-002）

- Owner 需要理解：轮播图创建/更新/删除必须经 `AdminAuth` + 对应 `banner:*` 权限（超管 `IsSuper` 放行），未认证 401、无权限 403，且拒绝时绝不产生任何 DB 写入。若权限被旁路，任意登录管理员都可篡改首页内容。
- 生产代码：`internal/cmd/routes_admin.go` `require("banner:create/update/delete")` 挂载写路由；`internal/middleware/auth.go` `RequirePermission`（fail-closed，无权限 403、DB 查询失败 500）。
- 关键测试：`TestBannerAdminCreateAndPermission`（`internal/cmd/banner_test.go`），断言超管创建落库、普通管理员（无权限）403/1003、未认证 401/1002，且 `banners` 行数不增加。
- 基线验证：`go test ./internal/cmd -run TestBannerAdminCreateAndPermission -count=1` → 预期 PASS。
- 可选 Mutation：把 `require("banner:create").POST("/admin/banners", ...)` 改为 `admin.POST("/admin/banners", ...)`（去掉权限校验）。
- 预期失败：`TestBannerAdminCreateAndPermission` 失败——普通管理员创建从 403/1003 变为 200，`banner count` 断言失败。
- 恢复确认：恢复 `require("banner:create")` 后再次运行同一命令 → PASS。

## CL-003：更新三态语义（幂等更新不误报 404）

- Owner 需要理解：轮播图更新以「记录是否存在」判断存在性，不依赖 MySQL `UPDATE` 的 `RowsAffected`——因为「记录不存在」与「记录存在但提交值与原值完全相同」都会让 `RowsAffected=0`。若误用 `RowsAffected=0` 判不存在，幂等保存会被误报为 404，前端可能误判记录丢失。
- 生产代码：`internal/logic/banner/banner.go` `Update`（更新前 `findOne` 判存在 → `UPDATE`（不依赖 `RowsAffected`）→ 更新后 `findOne` 兜底并发删除）。
- 关键测试：`TestBannerUpdateRegression`（`internal/cmd/banner_test.go`），覆盖「不存在→404 / 有变化→成功 / 相同值幂等→200 / 无权限→403 无副作用」。
- 基线验证：`go test ./internal/cmd -run TestBannerUpdateRegression -count=1` → 预期 PASS。
- 可选 Mutation：在 `Update` 的 `UPDATE` 之后恢复 `if n, _ := result.RowsAffected(); n == 0 { return nil, codes.New(codes.CodeBannerNotFound) }`。
- 预期失败：`TestBannerUpdateRegression` 的「相同值幂等更新」断言失败——相同值重复 Update 返回 404/14001 而非 200/0。
- 恢复确认：移除该 `RowsAffected=0 → 404` 判断后再次运行同一命令 → PASS。
