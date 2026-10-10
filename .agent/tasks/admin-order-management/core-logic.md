# Owner 核心逻辑验证

本任务为「后台订单只读管理」，真正的正确性核心是两条：**授权隔离**（订单敏感交易数据的最小权限）与**历史快照自足**（改价不得污染已成交订单）。以下是 Owner 应亲自验证的验证卡。

## CL-001：后台订单只读授权隔离（order:list / order:view）

- Owner 需要理解：订单是敏感交易数据。若后台列表/统计/详情缺少 `order:list`/`order:view` 权限校验，任何已登录的普通管理员或普通用户都能读取全部订单，造成数据越权。不变量：无对应权限的非超管访问返回 403 且无任何 DB 写；普通用户 token（`type=user`）访问 `/admin/orders*` 返回 403；超管放行。
- 生产代码：`internal/cmd/routes_admin.go`（`require("order:list").GET("/admin/orders")`、`require("order:list").GET("/admin/orders/stats")`、`require("order:view").GET("/admin/orders/:id")`）；`internal/middleware/auth.go` 的 `RequirePermission`（`IsSuper` 放行 → `hasPermission` 查 `permissions.code` → 未命中 `CodeForbidden`）。
- 关键测试：`internal/cmd/order_admin_test.go` 的 `TestAdminOrderReadOnlyPermission`（真实路由 + 真实 MySQL/Redis，分别断言超管 200、无权限管理员 403、持权管理员 200、普通用户 403）。
- 基线验证：`go test -p 1 ./internal/cmd -run TestAdminOrderReadOnlyPermission`（预期 PASS）。
- 可选 Mutation：临时把 `routes_admin.go` 中 `GET /admin/orders` 的 `require("order:list")` 改为仅 `admin.GET(...)`（去掉 `RequirePermission`），即绕过列表读权限。
- 预期失败：`TestAdminOrderReadOnlyPermission` 中 `plain-list` 用例（无权限管理员访问列表期望 403）会因实际返回 200 而失败。
- 恢复确认：还原该行后再次 `go test -p 1 ./internal/cmd -run TestAdminOrderReadOnlyPermission`（预期 PASS）。

## CL-002：订单详情历史快照自足（改价不污染已成交订单）

- Owner 需要理解：订单一旦成交，金额、商品/SKU 名称、单价、数量必须来自下单时的快照（`orders`/`order_items`），绝不能重读当前 `skus.price`/商品售价，否则商品改价后历史订单金额会失真，破坏交易正确性。
- 生产代码：`internal/logic/order/order.go` 的 `AdminDetail` → `loadByIDOrder` → `toOrder`（读 `orderRow.TotalAmount`）→ `loadItems`（读 `order_items.price/sku_name/product_name/quantity`），全程不查 `skus`/`products` 当前价。
- 关键测试：`internal/cmd/order_admin_test.go` 的 `TestAdminOrderDetailSnapshot`（下单后把 `skus.price` 改成 9999，断言详情 `total_amount`/`items[].price`/`sku_name` 仍为快照）。
- 基线验证：`go test -p 1 ./internal/cmd -run TestAdminOrderDetailSnapshot`（预期 PASS）。
- 可选 Mutation：临时把 `loadItems` 中 `Price: r.Price` 改为重读 `skus.price`（如 `service.Sku().GetByID(...)` 取当前价）。
- 预期失败：`TestAdminOrderDetailSnapshot` 断言 `order.Items[0].Price == 3000`（快照价）会因实际返回 9999 而失败。
- 恢复确认：还原该行后再次 `go test -p 1 ./internal/cmd -run TestAdminOrderDetailSnapshot`（预期 PASS）。
