# Cleaner Findings

## Review Target

- Task：`banner-v1`
- Design Impact：NEW（`docs/design/banner.md`）
- **本次复审对象（CLEAN-001 修复实现 C1）：`4493810dd5266ae3fb6ecfdb3591f94fea8815d3`**
- 复审时 feature HEAD（C2，Coder metadata commit）：`bb817aeba6fb8d74c86c7324bdf62c2c4350e09f`
- 原 CLEAN review target（因生产代码变更已客观失效）：`0c04cc0931f91b3f90857c3fcfc1a6234e0db8d9`
- Contract target（APPROVED，**未修订**）：`558221a52bab4e9b27428690d44b57efe5937eec`
- 变更面：`git diff 0c04cc0..4493810` 仅 `internal/logic/banner/banner.go` 与 `internal/cmd/banner_test.go`（不含任何 `contract.md` / `docs/design/banner.md` 修订）

## Result

BLOCKED

## 阻塞事实

Owner 已就 CLEAN-001 决定修复，并明确指令「先做最小 Contract Revision → Owner 确认 → 再进 Coder」。但当前 Feature Branch 上**未发生 Contract Revision**：

- `contract.md` 未改，`contract.status` 仍为 APPROVED 且 `contract.target` 仍指向原 `558221a`；
- `docs/design/banner.md` §5 未同步；
- Coder 直接落地生产代码修复，实现语义与仍 APPROVED 的 Contract 冲突。

具体漂移：

- 实现（`banner.go` `Update`）已不依据 `RowsAffected` 判存在性（无变化更新 → 幂等成功）；
- Contract「Failure and Consistency Semantics」仍写「更新/删除 = 条件更新/删除并核对 RowsAffected（RowsAffected=0 → 14001 404）」；
- `docs/design/banner.md` §5 同句未改。

这是「Owner 要求的前置 Contract Revision 缺失」的前置事实缺失，四边一致（Task↔Contract↔Design↔Implementation）被破坏。按角色边界，Contract Revision 属 Analyst 职责（起草后交 Owner 确认），Cleaner 无权改 `contract.md`/`docs/design/*`，也无法对一份与实现冲突的 APPROVED Contract 形成可信 CLEAN，故输出 BLOCKED。

## 复审技术结论（代码本身正确，供 Analyst/Owner 参考）

- `Update` 改为：更新前 `findOne` 判不存在 → 404；执行 `UPDATE` 后不再用 `RowsAffected=0` 判不存在；更新后再次 `findOne`，若 nil（并发删除兜底）→ 404。三态语义正确。
- 回归测试 `TestBannerUpdateRegression` 覆盖「不存在→404」「有变化→成功」「相同值幂等→200/0」「无权限→403 且无副作用」。
- `go build ./...`、`go vet ./...`、`go test -p 1 ./...` 均通过（全包 OK）。

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 ~ AC-007 | PASS | 相关代码未变，`go test -p 1 ./...` 全包通过 |
| AC-008（四边一致） | FAIL | `docs/design/banner.md` §5 与 Contract 未随 Owner 决定的语义修订，与实现漂移 |

## Verification

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| `go build ./...` | PASS | — |
| `go vet ./...` | PASS | — |
| `go test -p 1 ./...` | PASS | 全包通过 |
| 四边一致（Task↔Contract↔Design↔实现） | FAIL | Contract/Design 未修订，仍写「RowsAffected=0 → 404」 |
| 全局资源三边一致（Registry↔Contract↔实现） | PASS | 错误码域/迁移 version 不受本次变更影响 |

## Findings

### CLEAN-001：无变化更新误报 404

- Severity：P3
- Status：OPEN（代码已修复，但 Owner 要求的最小 Contract Revision 未执行，Contract/Design 未同步，见 Result=BLOCKED）
- Location：`internal/logic/banner/banner.go` `Update`
- 修复代码：移除 `RowsAffected=0 → 404`，改以 `findOne` 存在性判断；回归测试 `TestBannerUpdateRegression`
- 阻塞项：需 Analyst 就「Update 不再以 RowsAffected=0 判不存在；不存在→404、存在（含值未变）→成功」发起最小 Contract Revision，并同步 `docs/design/banner.md` §5，交 Owner 确认后再进入 Cleaner 复审
