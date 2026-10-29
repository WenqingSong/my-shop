# Core Logic（Owner 核心验证）

## CL-001：金额与销量的「有效订单状态」口径

- Owner 需要理解：`today_order_amount`（今日订单金额）与商品销量 TOP 榜的 `sales` 都**只统计「当前状态」属于 `{20,30,40,50}`（已支付/已发货/已收货/已完成）的订单**，排除待支付(10)/已取消(60)/已退款(70)。这是「当前快照」口径：一笔订单后续退款（20→70）后，它会从**历史日期**的有效金额/销量中消失。金额按 `orders.total_amount`（整数分）求和，销量按 `order_items.quantity` 求和，二者绝不混用 `view_count` 或库存变化。
- 生产代码：`internal/logic/dashboard/dashboard.go` —— `validOrderStatuses`（第 34 行）、`sumOrderAmount`（金额）、`TopProducts`（销量）
- 关键测试：`internal/cmd/dashboard_test.go` 的 `TestDashboardOverviewMetrics`（金额=3000，排除 10/60/70）、`TestDashboardProductsTop`（取消/待支付明细不计销量）
- 基线验证：`go test -count=1 -run 'TestDashboardOverviewMetrics|TestDashboardProductsTop' ./internal/cmd/` → PASS
- 可选 Mutation：把 `validOrderStatuses` 改成 `[]int{20,30,40,50,60}`（把「已取消」也当作有效）
- 预期失败：`TestDashboardOverviewMetrics` 断言 `today_order_amount=3000` 会变成含取消订单的更大值；`TestDashboardProductsTop` 销量排序/计数随之改变，测试必须失败
- 恢复确认：还原 `validOrderStatuses` 后重跑上述测试，PASS 恢复

## CL-002：Dashboard 的权限边界（AdminAuth + dashboard:view）

- Owner 需要理解：5 个 Dashboard 接口是**只读但敏感**的运营数据，只能由 `type=admin` 且持有 `dashboard:view` 权限（或超级管理员）访问；普通用户 token → 403、无/失效 token → 401、无 `dashboard:view` 的普通管理员 → 403；任何拒绝路径不产生数据库写入。`dashboard:view` 是新增的 RBAC 权限 code（B 类 namespace），seed 于权限表，超管 `IsSuper` 直接放行。
- 生产代码：`internal/cmd/routes_admin.go`（第 153-158 行 `require("dashboard:view")` 挂载 5 条路由）+ `internal/boot/seed.go`（`dashboard:view` seed，第 67 行）
- 关键测试：`internal/cmd/dashboard_test.go` 的 `TestDashboardPermissionBoundary`（超管 200 / 无权限管理员 403 / 普通用户 403 / 无 token 401，且拒绝路径计数不变）
- 基线验证：`go test -count=1 -run 'TestDashboardPermissionBoundary' ./internal/cmd/` → PASS
- 可选 Mutation：把某条 Dashboard 路由的 `require("dashboard:view")` 改成 `admin.GET`（去掉权限校验，仅保留认证）
- 预期失败：`TestDashboardPermissionBoundary` 中「无 dashboard:view 管理员」请求该接口本应 403，会变成 200/code=0，测试必须失败
- 恢复确认：还原 `require("dashboard:view")` 后重跑上述测试，PASS 恢复
