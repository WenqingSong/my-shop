# Cleaner Findings

## Review Target

- Task：`.agent/tasks/shipping-address-v1/task.md`（Goal/Scope/AC，Review Baseline 已修正为 base `1db733d`）。
- Contract：`.agent/tasks/shipping-address-v1/contract.md`（`APPROVED`，含 2026-10-04 CONTRACT_REVISION，Design Impact = NEW）。
- 当前版本：HEAD `a7aad28fd96953a1abfee50776f0865219ac7947`（分支 `develop`，working tree clean）。
  - `1db733d`：任务基线（clean）。
  - `37353a4`：地址实现 + contract + design + 首次 Cleaner findings。
  - `a7aad28`：复审修复（仅文档，STORED→VIRTUAL）。
- 相关文件：
  - 新增：`api/address/v1/address.go`、`internal/controller/address/address.go`、`internal/controller/address/address_test.go`、`internal/logic/address/address.go`、`internal/service/address.go`、`internal/migrations/sql/20261001000005_addresses.up.sql`、`docs/design/address.md`、`.agent/tasks/shipping-address-v1/contract.md`。
  - 修改：`internal/cmd/routes_frontend.go`、`internal/cmd/routes_test.go`、`internal/codes/codes.go`、`internal/logic/logic.go`、`internal/migrations/migrations_test.go`、`internal/boot/boot_migration_test.go`、`.agent/tasks/shipping-address-v1/task.md`。
- 迁移版本：latest = `20261001000005`。
- 区分方式：任务基线 working tree clean；本任务产物全部落在 Contract「Allowed Changes」范围内，无 Scope 外功能、无无关重构、无公开行为变化。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（创建） | PASS | `TestAddressCrudAndDefaultSwitch`：创建返回 `id`，`dbAddressUserID(a1.Id)==userID`；首条自动默认。 |
| AC-002（列表） | PASS | 列表 `len==2` 且仅含本人，无他人地址。 |
| AC-003（详情） | PASS | 本人详情 200；`TestAddressIsolation`：他人/不存在统一 404/7001，不泄露存在性/归属。 |
| AC-004（更新） | PASS | 本人更新成功；他人更新 404/7001 且原地址未变。 |
| AC-005（删除） | PASS | 本人删除成功；他人删除 404/7001 且无写入。 |
| AC-006（隔离） | PASS | `TestAddressIsolation`：两真实用户 HTTP 路由，B 无法读/改/删 A 地址，B 名下 0 条，A 列表仅 1 条。 |
| AC-007（默认唯一） | PASS | 首条自动默认、切换取消旧默认、删除默认后无默认（`TestAddressCrudAndDefaultSwitch`）；并发设默认终态 `count==1` 且码仅 0/7002（`TestAddressDefaultConcurrency` + `-race`）。 |
| AC-008（未登录） | PASS | `TestAddressUnauthenticated`：5 接口无 token 均 401/1002，`addresses` 表 0 行。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 无输出，退出码 0。 |
| `go vet ./...` | PASS | 无输出，退出码 0。 |
| `gofmt -l`（相关文件） | PASS | 空输出。 |
| `go test -p 1 ./...` | PASS | 全部包 ok（含 address/migrations/boot/cmd）。 |
| `go test -p 1 -race ./internal/controller/address/...` | PASS | ok（并发默认唯一在 Race 下通过）。 |
| MySQL 8.0 生成列实测 | PASS | STORED+FK 报 1215；VIRTUAL+FK 正常；确认实现用 VIRTUAL 正确。 |
| 四者一致性（复审） | PASS | Task（中性）↔ Contract（VIRTUAL）↔ docs/design/*（address.md VIRTUAL、migration.md 含 00005）↔ 实现（VIRTUAL）一致。 |

## Findings

### CLEAN-001：`default_key` 生成列类型 STORED→VIRTUAL 漂移

- Severity：P2
- Status：CLOSED
- Location：`.agent/tasks/shipping-address-v1/contract.md`、`docs/design/address.md`、`internal/migrations/sql/20261001000005_addresses.up.sql`
- 复审结论：修复提交 `a7aad28` 将 contract.md（第 47/52/109 行）与 docs/design/address.md（第 26/31/32 行）的 `default_key` 由 STORED 更正为 VIRTUAL，并补注 MySQL 8.0 下 STORED 引用外键列报 1215 的取舍；新增 CONTRACT_REVISION 记录且 Owner 已确认（第 169-173 行），Contract 状态 `APPROVED`。实现（VIRTUAL）未改。四者一致，关闭。

### CLEAN-002：`docs/design/migration.md` 迁移清单缺 addresses

- Severity：P3
- Status：CLOSED
- Location：`docs/design/migration.md` §2.1
- 复审结论：修复提交 `a7aad28` 在 §2.1 清单补 `20261001000005_addresses` 一行，§7 跨模块关系补 `addresses`。关闭。

无开放 P0/P1/P2/P3 问题。
