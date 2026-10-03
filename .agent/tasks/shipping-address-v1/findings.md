# Cleaner Findings

## Review Target

- Task：`.agent/tasks/shipping-address-v1/task.md`（Goal/Scope/AC，Review Baseline 已由 Task Builder 修正为 base `1db733d`）。
- Contract：`.agent/tasks/shipping-address-v1/contract.md`（`APPROVED`，含 Owner Decision Record 2026-10-04，Design Impact = NEW）。
- Base commit：`1db733d454ae967702bc87f080a03195123cdc02`（分支 `develop`，与当前 HEAD 一致）。
- 工作区变更（基线后，无其它无关修改）：
  - 新增：`api/address/v1/address.go`、`internal/controller/address/address.go`、`internal/controller/address/address_test.go`、`internal/logic/address/address.go`、`internal/service/address.go`、`internal/migrations/sql/20261001000005_addresses.up.sql`、`docs/design/address.md`、`.agent/tasks/shipping-address-v1/contract.md`。
  - 修改：`internal/cmd/routes_frontend.go`、`internal/cmd/routes_test.go`、`internal/codes/codes.go`、`internal/logic/logic.go`、`internal/migrations/migrations_test.go`、`internal/boot/boot_migration_test.go`、`.agent/tasks/shipping-address-v1/task.md`（仅 Review Baseline 修正）。
- 迁移版本：latest = `20261001000005`（addresses 紧随 inventory `00004` 之后）。
- 区分方式：任务基线为 working tree clean；本任务产物全部落在 Contract「Allowed Changes」声明范围内，无 Scope 外功能、无无关重构、无公开行为变化。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（创建） | PASS | `TestAddressCrudAndDefaultSwitch`：创建返回 `id`，且 `dbAddressUserID(a1.Id) == userID`；首条自动默认。 |
| AC-002（列表） | PASS | 同一测试：列表 `len==2`，仅含本人两条，无他人地址。 |
| AC-003（详情） | PASS | 本人详情 200；`TestAddressIsolation`：他人地址与不存在地址统一 404/7001，不泄露存在性/归属。 |
| AC-004（更新） | PASS | 本人更新成功；`TestAddressIsolation`：他人更新 404/7001 且原地址未变。 |
| AC-005（删除） | PASS | 本人删除成功；`TestAddressIsolation`：他人删除 404/7001 且无写入。 |
| AC-006（隔离） | PASS | `TestAddressIsolation`：用户 A/B 真实 HTTP 路由，B 无法读/改/删 A 的地址，B 名下 0 条，A 列表仅 1 条。 |
| AC-007（默认唯一） | PASS | 首条自动默认、切换取消旧默认、删除默认后无默认（`TestAddressCrudAndDefaultSwitch`）；并发设默认终态 `count==1` 且结果码仅 0/7002（`TestAddressDefaultConcurrency` + `-race`）。 |
| AC-008（未登录） | PASS | `TestAddressUnauthenticated`：5 个接口无 token 均 401/1002，`addresses` 表 0 行。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 无输出，退出码 0。 |
| `go vet ./...` | PASS | 无输出，退出码 0。 |
| `gofmt -l`（相关文件） | PASS | 空输出（全部已格式化）。 |
| `go test -p 1 ./...` | PASS | 全部包 ok（含 address/migrations/boot/cmd）。 |
| `go test -p 1 -race ./internal/controller/address/...` | PASS | ok（并发默认唯一在 Race 下通过）。 |
| MySQL 8.0 生成列实测 | PASS | `STORED` 生成列 + FK（引用 `users.id`）报 `ERROR 1215`；`VIRTUAL` 生成列 + FK 正常；确认迁移改用 `VIRTUAL` 正确。 |

说明：单独并行跑 `internal/migrations` 与 `internal/controller/address` 两个包会因争用同一 MySQL 库（迁移测试 `DROP ALL TABLES`）互相污染，这是 `-p 1` 串行前提下的已知约束，非实现缺陷；按任务约定 `go test -p 1 ./...` 串行执行全部通过。

## Findings

### CLEAN-001：`default_key` 生成列类型在 Contract/Design 标注为 STORED，实现为 VIRTUAL，四者不一致

- Severity：P2
- Status：OPEN
- Location：
  - `.agent/tasks/shipping-address-v1/contract.md`「数据模型」表 `default_key` 行（STORED 生成列）；
  - `docs/design/address.md` §2.1 表（`default_key` 行）与 §2.1 说明（“`default_key` 为 STORED 生成列”）；
  - 对比 `internal/migrations/sql/20261001000005_addresses.up.sql`（`... VIRTUAL`，注释第 9-11 行说明改用 VIRTUAL 的原因）。
- AC / Invariant：Design Impact = NEW 的「Task ↔ Contract ↔ docs/design/* ↔ 最终实现」四者一致要求；INV-002（默认地址唯一）本身不受影响。
- Trigger：阅读 Contract/Design 的数据模型描述，与迁移 SQL 比对。
- Actual：Contract 与 `docs/design/address.md` 均将 `default_key` 描述为 “STORED 生成列”；最终迁移实现使用 `VIRTUAL`。
- Expected：四者一致。因 MySQL 8.0 下 STORED 生成列引用「同时作为外键列」的 `user_id` 会报 1215（已实测复现），实现采用 VIRTUAL 是正确的，应由 Design/Contract 更正为 VIRTUAL 并说明原因，而非把实现改回 STORED。
- Impact：破坏 Design Impact=NEW 的四者一致性；文档与真实 schema 不符，未来接手者可能依据 Design 把 VIRTUAL “纠正”回 STORED，导致迁移失败（1215）；维护风险。
- Evidence：
  - 迁移文件注释明确自述了 STORED→VIRTUAL 的偏离及原因；
  - `docker exec my-shop-mysql mysql ...`：`STORED` + FK 报 `ERROR 1215`，`VIRTUAL` + FK 正常；
  - `go test -p 1 ./...`、`go test -p 1 -race ./internal/controller/address/...` 全部通过（实现功能正确，唯一索引语义不变）。
- Required Fix Boundary：将 `contract.md` 与 `docs/design/address.md` 中 `default_key` 的 “STORED 生成列” 更正为 “VIRTUAL 生成列”，并保留/补注「STORED 生成列引用外键列在 MySQL 8.0 报 1215」的取舍说明；不得改动实现（VIRTUAL 正确）与 `uk_user_default` 唯一索引语义。

### CLEAN-002：`docs/design/migration.md` 迁移清单未收录 addresses 迁移

- Severity：P3
- Status：OPEN
- Location：`docs/design/migration.md` §2.1「当前迁移清单」表格。
- AC / Invariant：Design Impact = NEW 的项目级设计事实同步。
- Trigger：阅读 migration.md 迁移清单，与实际迁移文件对比。
- Actual：清单仅到 `20261001000004`（inventory），缺少新增的 `20261001000005`（addresses）。
- Expected：清单包含 `20261001000005_addresses` 一行（含标题/内容说明）。
- Impact：项目级迁移清单过期，接手者查询迁移时不完整（低风险）。
- Evidence：`docs/design/migration.md` §2.1 表格共 4 行；新增 `internal/migrations/sql/20261001000005_addresses.up.sql`。
- Required Fix Boundary：在 `docs/design/migration.md` §2.1 清单补 `20261001000005` addresses 一行；不改动迁移机制语义。
