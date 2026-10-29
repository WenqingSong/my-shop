# Cleaner Findings

## Review Target

- Task：`banner-v1`
- Design Impact：NEW（`docs/design/banner.md`）
- **本次复审固化对象（C1，Cleaner 审定的 immutable implementation Evidence Commit）：`27db2fc38beb7505dd1528ef083080c19e2592c0`**
- 复审时 feature HEAD（review-neutral tail）：`4dbc0a73c9199c80bd44e27097391ab8461584a3`（仅 state.yaml rebind）
- 相关提交链（`git log`）：
  - `4493810` fix(banner)：修复无变化更新误报 404（生产代码 + 回归测试）
  - `bb817ae` chore(state)：发起复审（review PENDING，target 4493810）
  - `225fa60` chore(review)：CLEAN-001 复审 BLOCKED（Contract Revision 缺失）
  - `ced8231` docs(banner)：Contract REV-001 修订 Update 三态语义 + 同步 `docs/design/banner.md` §5 + 调整 contract 状态
  - `27db2fc` docs(contract)：REV-001 Owner ACCEPT（contract.md 定稿）
  - `4dbc0a7` chore(state)：REV-001 contract approved（rebind contract.target = 27db2fc）
- Contract target（APPROVED，REV-001）：`27db2fc38beb7505dd1528ef083080c19e2592c0`
- 原 CLEAN review target（已失效）：`0c04cc0931f91b3f90857c3fcfc1a6234e0db8d9`
- 说明：`review.target` 由 `4493810` 前移至 `27db2fc` 的机械原因——Contract Revision（`contract.md`、`docs/design/banner.md`）为实质变更（不在 review-neutral 白名单），必须纳入被审快照，否则 `review.target..HEAD` 的 tail 含实质变更、先前 CLEAN 无法成立。`27db2fc` 是包含「代码修复 + Contract 修订 + Design 同步」全部实质变更的最新 commit，其后的 `4dbc0a7` 仅 state.yaml（review-neutral）。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001（公开列表） | PASS | 代码未变；`TestBannerPublicListEnabledAndSorted` 通过 |
| AC-002（图片可访问） | PASS | `TestBannerPlaceholderImagesAccessible` 通过 |
| AC-003（后台创建与权限） | PASS | `TestBannerAdminCreateAndPermission` 通过 |
| AC-004（后台更新与排序） | PASS | `Update` 三态语义 + `TestBannerAdminUpdateAndSort` + `TestBannerUpdateRegression` 通过 |
| AC-005（后台删除） | PASS | `TestBannerAdminDelete` 通过 |
| AC-006（状态过滤） | PASS | `TestBannerPublicListEnabledAndSorted` / `TestBannerAdminUpdateAndSort` 通过 |
| AC-007（数据模型与迁移） | PASS | 迁移测试全通过（`internal/migrations`） |
| AC-008（四边一致） | PASS | `docs/design/banner.md` §5 已随 REV-001 同步，与 APPROVED Contract 及实现一致 |

## Verification

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| 四边一致（Task↔Contract↔Design↔实现） | PASS | REV-001 已修订 `Failure and Consistency Semantics` 三态语义，`docs/design/banner.md` §5 同步，`banner.go` `Update` 一致 |
| 全局资源三边一致（Registry↔Contract↔实现） | PASS | 错误码域/迁移 version 不受本次变更影响，仍与 origin/develop Registry 一致 |
| `go build ./...` | PASS | 无错误 |
| `go vet ./...` | PASS | 无告警 |
| `go test -p 1 ./...` | PASS | 全包通过 |

## Findings

### CLEAN-001：无变化更新误报 404

- Severity：P3
- Status：CLOSED
- 处理链路：Cleaner 发现（P3）→ Owner 决定修复 → 最小 Contract Revision（REV-001，`ced8231` + `27db2fc` Owner ACCEPT）→ 实现 `4493810` 与回归测试 `TestBannerUpdateRegression` → 本轮复审验证三态语义一致。
- 修复代码：`internal/logic/banner/banner.go` `Update` 移除「`RowsAffected=0 → 404`」，改以更新前 `findOne` 判存在、更新后 `findOne` 兜底并发删除；不存在 → 404、有变化 → 成功、无变化 → 幂等成功。
- 回归证据：`TestBannerUpdateRegression` 覆盖「不存在→404 / 有变化→成功 / 相同值幂等→200/0 / 无权限→403 无副作用」，`go test -p 1 ./...` 全包通过。
- 关闭依据：代码、Contract（REV-001 APPROVED）、Design 三者已对齐，且独立验证通过。

No actionable findings.
