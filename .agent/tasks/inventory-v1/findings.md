# Cleaner Findings

## Review Target

- 任务基线（base commit）：`90f05bdf74e2305d0d6a6ccbef1bb3b8a4584404`（分支 `feat/product`，HEAD 为「docs(sku): 补充 SKU V1 交付验收报告」，working tree clean）。
- 当前版本：HEAD = `2ada140`（`feat(inventory): 添加库存查询与增减接口`），其上依次为 `cb09ed3`（contract）、`e9bba55`（task）。工作区无未提交修改（`git status --short` 为空），全部变更已提交。
- 审查范围：`git diff 90f05bd..HEAD` 共 20 个文件，含新增 `api/inventory/v1`、`internal/controller/inventory`、`internal/logic/inventory`、`internal/service/inventory.go`、migration `20261001000004_inventory.up.sql`，以及修改的 `internal/codes/codes.go`、`internal/boot/seed.go`、`internal/cmd/routes_admin.go`、`internal/logic/sku/sku.go`、`internal/service/sku.go`、`internal/logic/logic.go` 与对应测试。
- 任务前已有修改区分：基线 `git status --short` 为空，故上述 diff 全部为本任务产物，无重叠历史修改需要剥离。
- 关键配置/迁移版本：MySQL 8.0.46（`mysql:8.0`，支持 CHECK 但本任务未用）、Redis 7（`redis:7-alpine`）；迁移 latest = `20261001000004`。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（库存查询） | PASS | `internal/logic/inventory/inventory.go:57` `Get` 先 `ensureSkuExists`（不存在 5001/404），无记录返回 `Quantity=0`。测试 `TestInventoryGetLazyZeroAndNotFound` 断言惰性 0 与 404/5001，`-race` 通过。 |
| AC-002（初始化/增加） | PASS | `Increase` 原子 upsert（`ON DUPLICATE KEY UPDATE quantity = quantity + VALUES(quantity)`），首次建立记录、再次累加；同事务写「增加」流水。`TestInventoryIncreaseInitAndAccumulate` 断言 0→10→25 与两条 before/after 一致的流水，通过。 |
| AC-003（条件扣减成功） | PASS | `Deduct` 条件更新 `UPDATE ... WHERE quantity >= ?` + 核对 `RowsAffected`。`TestInventoryDeductSuccess` 断言 30→18 与「扣减」流水（before=30/after=18），通过。 |
| AC-004（条件扣减拒绝） | PASS | `RowsAffected==0` 返回 `6001`（409），事务回滚、不写流水。`TestInventoryDeductInsufficient` 断言 409/6001、库存不变、仅剩 1 条增加流水，通过。 |
| AC-005（防负库存） | PASS | 条件更新为主 + `quantity INT UNSIGNED` 类型兜底。`TestInventoryConcurrentDeduct` 断言最终 `quantity==0 ≥ 0`；`TestInventoryDeductInsufficient` 断言不足时不变，`-race` 通过。 |
| AC-006（流水记录） | PASS | `inventory_logs` 含 sku_id/change_type/change_qty/before_qty/after_qty/operator_admin_id/created_at；`insertLog` 与库存变更同事务。`TestInventoryLogsOrdering` 断言字段完整、`operator_admin_id` 非空、id 倒序，通过。 |
| AC-007（并发扣减） | PASS | `TestInventoryConcurrentDeduct`（20 goroutine 各扣 10，初始 100）断言成功恰好 10 次、失败 10 次、最终 0、流水 11 条；`-race` 通过。该测试可区分「条件更新」与「先查再写」（后者会超卖或计数错误）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| go build ./... | PASS | 无输出，退出码 0。 |
| go vet ./... | PASS | 无输出，退出码 0。 |
| gofmt | PASS | 对全部改动文件 `gofmt -l` 无输出。 |
| go test -p 1 ./... | PASS | 全量通过（`internal/controller/inventory`、`internal/migrations`、`internal/boot`、`internal/cmd`、`internal/controller/sku` 等全部 ok）。`-p 1` 串行是仓库既定约束（`scripts/test.sh`），因各集成测试共享同一 MySQL/Redis。 |
| go test ./internal/controller/inventory/ -race -count=1 | PASS | 11 个测试全部通过（含并发扣减/并发增加），无 data race 报告。 |
| 环境 | 就绪 | MySQL 8.0.46、Redis 7 均 `Up (healthy)`；迁移已应用至 `20261001000004`。 |

## Findings

### CLEAN-001：扣减/增加 upsert 使用已弃用的 `VALUES()` 语法

- Severity：P3
- Status：OPEN
- Location：`internal/logic/inventory/inventory.go:85`（`INSERT ... ON DUPLICATE KEY UPDATE quantity = quantity + VALUES(quantity)`）
- AC / Invariant：INV-004（1:1 唯一 + 无丢失更新）
- Trigger：MySQL 升级到移除 `VALUES()` 语法的版本后，`increase` 首次/累加路径可能报语法错误。
- Actual：当前 MySQL 8.0.46 下工作正常（集成测试全绿），但 `VALUES()` 在 `ON DUPLICATE KEY UPDATE` 中自 MySQL 8.0.20 起已标记弃用并计划移除。
- Expected：改用行别名语法（`INSERT ... VALUES (?, ?) AS new ON DUPLICATE KEY UPDATE quantity = quantity + new.quantity`）或等价写法，消除前向兼容隐患。
- Impact：仅维护/前向兼容风险，当前无功能影响。
- Evidence：docker `mysql:8.0` 实际版本 8.0.46；`go test ./internal/controller/inventory/` 当前通过，说明语法暂未失效。
- Required Fix Boundary：保证 `increase` 的原子 upsert 语义不变（首次建记录、并发累加不丢增量、不产生重复行）的前提下替换弃用语法；不改动接口、错误码或流水语义。

（无 P0/P1/P2 问题。）
