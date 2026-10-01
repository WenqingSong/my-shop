# Delivery Verification

## Milestone and Target
- Milestone：库存 V1 里程碑验收（普通库存：查询 / 初始化增加 / 条件扣减 / 防负库存 / 流水 / 并发扣减）。
- Delivery Target：HEAD `f9b08d0`（分支 `feat/product`），生产代码与 `2ada140` 一致。
- Cleaner Review Target：HEAD `2ada140`（`feat(inventory): 添加库存查询与增减接口`）。
- Target Match：YES —— 生产代码（`api/`、`internal/`）在 `2ada140` 与当前 HEAD 之间 diff hash 一致（`7c505ee`）；`f9b08d0` 仅追加 `.agent/tasks/inventory-v1/` 文档（core-logic.md、findings.md），不改生产代码。

## Environment
- OS：Linux x86_64
- 运行时：Go 1.24.1
- MySQL：8.0.46（docker `mysql:8.0`，`healthy`）
- Redis：7（docker `redis:7-alpine`，`healthy`）
- 迁移：`schema_migrations` 版本 `20261001000004`，`dirty=0`
- 配置来源：`manifest/config/config.yaml` + 环境变量覆盖（`ADMIN_SUPER_PASSWORD`、`SERVER_ADDRESS`）；Secret 值不记录。
- 测试数据与隔离：开发环境本地 docker MySQL/Redis；验收前清空业务表与 IAM 表以建立干净基线，验收后已清理（`inventories`/`inventory_logs`/`skus`/`products`/`categories` 均 0 行），验收服务进程已停止、端口 8000 已释放。

## Verification
| Check | Result | Evidence |
|---|---|---|
| go build ./... | PASS | exit 0，无输出 |
| go build -o bin/my-shop . | PASS | 产出 36,880,388 字节二进制 |
| go vet ./... | PASS | exit 0，无输出 |
| gofmt -l（改动文件） | PASS | 无输出 |
| go test -p 1 ./... | PASS | 全量 ok（含 inventory、migrations、boot、cmd、sku 等） |
| 并发 race 测试 | PASS | `go test -p 1 -race -count=1 ./internal/controller/inventory/` 11 个测试全 PASS，无 data race |
| 服务启动 + 健康检查 | PASS | `serve` 启动后 1s 内 `/health` 返回 200 `{"code":0,"status":"ok"}` |
| 迁移加载 | PASS | `schema_migrations` = `20261001000004`，`dirty=0`，`inventories`/`inventory_logs` 表已建 |

## Acceptance Evidence

端到端 HTTP Smoke（真实 `serve` 进程 + 真实 MySQL/Redis，SKU_ID=12、operator_admin_id=15）与数据库最终核对：

| AC / INV | Result | Evidence |
|---|---|---|
| AC-001 库存查询 | PASS | 新建 SKU 查询返回 `quantity=0`（惰性无记录=0）；不存在 SKU 查询 HTTP 404 / code 5001 |
| AC-002 初始化/增加 | PASS | increase 10 → 10；再 increase 5 → 15；两条「increase」流水 before/after 为 0→10→15 |
| AC-003 条件扣减成功 | PASS | deduct 4：15→11，「deduct」流水 before=15/after=11 |
| AC-004 条件扣减拒绝 | PASS | deduct 999：HTTP 409 / code 6001，库存保持 11，无新增流水 |
| AC-005 防负库存 | PASS | 条件更新 + RowsAffected；race 并发扣减最终 quantity=0（≥0） |
| AC-006 流水记录 | PASS | 3 条流水字段完整、operator_admin_id=15、before/after 与库存一致、id 倒序 |
| AC-007 并发扣减 | PASS | 20 goroutine 各扣 10（初始 100）：成功 10、失败(6001) 10、最终 0、流水 11 条 |
| INV-001 防负库存 | PASS | 同 AC-007 / AC-005 |
| INV-002 充足才成功 | PASS | 不足返回 409/6001，库存与流水均不变 |
| INV-003 流水与库存一致 | PASS | 每条流水 `after = before ± change`，与变更后当前库存一致、同事务 |
| INV-004 1:1 归属 | PASS | 单 SKU 单库存记录；并发增加 10×5=50 无丢失更新、无重复行 |
| INV-005 RBAC | PASS | 无 token 写 HTTP 401 / code 1002；超管放行；无权限管理员 403 / code 1003（本次 race 集成测试 TestInventoryAuthorization 验证，真实路由+DB） |
| INV-006 SKU 引用有效 | PASS | 不存在 SKU 查询/写均 404/5001 且无写入；有库存 SKU 删除 HTTP 409 / code 5005 |

数据库最终核对（与 HTTP 响应一致）：`inventories`（sku_id=12, quantity=11）；`inventory_logs` 3 行：id33 increase(10, 0→10)、id34 increase(5, 10→15)、id35 deduct(4, 15→11)，operator_admin_id=15。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| （无） | 全部 AC/INV 均有本次运行证据（HTTP smoke + race 集成测试 + DB 核对） | - |

说明：无权限管理员 403 场景未在独立 curl 中复现，但已由本次执行的 `TestInventoryAuthorization`（真实 HTTP 服务器 + 真实路由 + 真实 MySQL/Redis）覆盖并 PASS。

## Remaining Risks

- CLEAN-001（P3，Cleaner OPEN，Deliverer 不关闭）：`increase` upsert 使用 MySQL 已弃用的 `VALUES()` 语法（`ON DUPLICATE KEY UPDATE quantity = quantity + VALUES(quantity)`）。当前 MySQL 8.0.46 功能正常，属前向兼容/维护风险，不影响本里程碑；交由 Cleaner/Owner 跟踪。
- SKU 删除行为收紧（有库存记录或流水的 SKU 不可物理删除，返回 409/5005）：Owner 已在 Contract Owner Decision Record 确认接受。

## Result

PASS
