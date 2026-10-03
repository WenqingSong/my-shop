# Delivery Verification

## Milestone and Target

- Milestone：购物车 V1 完整交付验收（首个里程碑，覆盖全部 11 条 AC 与 7 条 INV）。
- Delivery Target：HEAD `749b8d2`（`docs(cart): 更新核心逻辑验证与复审findings`）。
- Cleaner Review Target：`1c36c3f`（`fix(cart): 修复相同值更新误判 404 及缺失 sku_id 返回 500`）。
- Target Match：YES —— `749b8d2` 相对 `1c36c3f` 仅改动 `.agent/tasks/cart-v1/core-logic.md` 与 `findings.md`（任务过程文档），生产代码、测试、迁移与配置完全一致；本次所有构建/测试/冒烟均针对该版本执行。

## Environment

- OS：Linux（amd64）。
- Go 工具链：`go1.24.1 linux/amd64`（`go.mod` 声明 `go 1.23.0`，Go 1.24 向后兼容构建通过）。
- GoFrame：v2.10.3；golang-migrate v4.19.0。
- MySQL：`mysql:8.0` 容器 `my-shop-mysql`（`Up (healthy)`，库 `my_shop`）。
- Redis：`redis:7-alpine` 容器 `my-shop-redis`（`Up (healthy)`）。
- 迁移版本：`20261001000005`，`dirty=false`（`my-shop migrate version` 实测）。
- 配置来源：`manifest/config/config.yaml` + 环境变量覆盖（`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD`、`SERVER_ADDRESS` 等）；本次未记录任何 Secret 明文值。
- 测试数据与清理：冒烟使用独立用户/分类/商品/SKU（`smokealice`、`smokeCat/smokeProd/smokeSku`，id 均 15/16），验证后已从 MySQL 删除；集成测试自行 `DELETE` 清理并恢复 baseline 迁移。

## Verification

| Check | Result | Evidence |
|---|---|---|
| 格式 gofmt | PASS | `gofmt -l` 对 cart/sku/product/codes/routes 相关文件无输出。 |
| 构建 go build ./... | PASS | 退出码 0。 |
| 静态检查 go vet ./... | PASS | 退出码 0。 |
| 二进制构建 | PASS | `go build -o bin/my-shop .` 退出码 0。 |
| 迁移状态 | PASS | `./bin/my-shop migrate version` → `version: 20261001000005 (dirty=false)`；`cart_items` 表结构与 Contract DDL 逐列一致（含 `uk_user_sku` 唯一键、`price_snapshot`、`selected` 默认 1）。 |
| 全量测试 go test -p 1 ./... | PASS | 全部 ok，含 `internal/controller/cart`（8.554s）、`internal/migrations`（9.773s）。 |
| 并发/竞态 go test -race ./internal/controller/cart/ | PASS | `ok ... 48.270s`，无 data race（含 `TestCartConcurrentAdd` 20×5=100 无丢失更新）。 |
| 服务启动 + 健康 | PASS | `./bin/my-shop serve` 启动，`GET /health` → `code:0`。 |
| 核心主链路 Smoke | PASS | 见下「Acceptance Evidence」。 |
| 最终数据 | PASS | 见下「Acceptance Evidence」。 |

## Acceptance Evidence

以下为 Deliverer 本次独立执行的证据（非 Coder/Cleaner 转述）：

- 认证与保护路由（AC-011）：无 token `GET /cart` → HTTP 401 / code 1002，无数据返回。
- 空列表（AC-001）：登录后 `GET /cart` → code 0、`items:[]`（非错误）。
- 真实注册登录（AC-010 前置）：`POST /register` 写入 `users` 表（id=15）；`POST /login` 签发 user token。
- 添加与累加（AC-002/003、INV-002）：`POST /cart/items {sku_id:16, quantity:2}` → code 0、quantity=2；再次 `{quantity:3}` → 同一条目 quantity=5（原子累加）；DB 仅 1 行 `cart_items(user_id=15, sku_id=16, quantity=5, price_snapshot=100, selected=1)`。
- 价格快照（INV-006）：加购返回 `price_snapshot=100`、`current_price=100`、`price_changed=false`，与 SKU 价一致。
- 异常可观察（AC-009/INV-005）：无库存记录时 `stock=0`、`insufficient=true`（quantity 5 > 0），不调整数量。
- 越权/隔离（AC-010/INV-001）：集成测试 `TestCartUserIsolation`（B 查/改/勾/删 A 条目均 404 且无写入）在 `-race` 下通过。
- 下架/禁用/改价/库存不足/SKU 删除软引用（AC-007/008/009、INV-005/006/007）：分别由 `TestCartUnavailable`、`TestCartPriceChanged`、`TestCartInsufficient`、`TestCartSkuDeleted` 在 `-race` 下通过。
- 数量边界与并发上限（AC-004、INV-002/003）：`TestCartQuantityBoundary`（0/负数/非整数/1000/累加超限均 400 且原值不变）、`TestCartConcurrentAdd`（20×5=100 无丢失更新）通过。

## Not Executed

（无关键检查未执行。恢复/回滚/性能检查不在本 Task/Contract 范围，未演练。）

## Remaining Risks

- Go 工具链 1.24.1 高于 `go.mod` 声明的 1.23.0：构建/测试/竞态均通过，非阻塞，建议在目标部署环境以 1.23 复核一次。
- 累加/改数量语义下客户端重试会重复累加（非幂等）：Contract 已声明为 V1 留白，Out of Scope。
- 列表无分页：Contract 已声明留白，条目量大时后续任务补。
- 未演练生产部署、重启恢复与回滚（本任务未要求）。

## Result

PASS
