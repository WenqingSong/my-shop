# Cleaner Findings

## Review Target

- 分支 `feat/iam-v4`，当前 HEAD：`bbe0229`（`fix(iam): 区分 refresh 轮换中过期与 reuse`），working tree 干净（`git status --short` 为空）。
- 任务基线：`task.md` 声明 base `1db733d`（无 IAM V3、无已有修改）。但 `contract.md`「Verified Current Behavior」已明确该基线信息过时：当前分支已包含 IAM V3（`0291413` 是 HEAD 祖先），无需再抉择基线（OPEN QUESTION 已消解）。本次以 `5541b4a`（task 文档提交，含 IAM V3）为变更前基准。
- 本任务变更 = 三个提交：
  - `3d185a9`（`docs(iam-v4): 添加 IAM V4 refresh token 技术契约`）：`.agent/tasks/iam-v4/contract.md`、`docs/design/iam.md`、`docs/design/migration.md`、`.agent/registry/migrations.md`。
  - `8f37bc1`（`feat(iam): 新增 refresh token 轮换与血缘撤销`）：生产代码 + 测试 + 迁移 SQL + 配置（15 文件）。
  - `bbe0229`（`fix(iam): 区分 refresh 轮换中过期与 reuse`）：修复 CLEAN-001/CLEAN-002，新增 `internal/logic/iam/refresh_test.go`，改动 `internal/logic/iam/refresh.go`、`internal/controller/iam/refresh_test.go`。
- 任务前已有修改：无（变更均已提交，无未提交重叠修改需要区分）。
- 关键全局资源：migration `20261001000007`（`refresh_tokens`，Registry `RESERVED`）；错误码 `2012/2013/2014`（IAM 域 2000-2999 `ACTIVE`）；配置 `auth.refresh.ttl`=2592000。
- 环境：MySQL 8.0 + Redis 7（docker compose 已就绪，`docker compose ps` 显示两者 healthy）。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestLoginIssuesRefreshToken`：login 返回 64 位 hex refresh token；DB `token_hash`=SHA-256 且 != 明文；明文哈希列不存在（明文不落库）。 |
| AC-002 | PASS | `TestRefreshRotationAndLineage`：refresh 返回新双 token；旧 token 置 `revoked_reason='rotated'`；新后代 `parent_id`=旧 id、`generation`=1、同 sid、同 family。 |
| AC-003 | PASS | `TestRefreshReuseRevokesFamily`：重放已轮换 token → 401/2014；`activeFamilyCount`=0（family 全撤销）；最新后代再 refresh → 401。 |
| AC-004 | PASS | 同上测试覆盖被盗重放时序：攻击者重放旧 token 后，合法新 token 亦失效，攻击者无法换新 access。 |
| AC-005 | PASS | `TestRefreshExpired`（常规过期 → 2013）+ `TestHandleRefreshNotRotatedExpired`（0 行 UPDATE 时恰逢过期 → 2013 且不撤销，见 CLEAN-001 关闭记录）。 |
| AC-006 | PASS | `TestRefreshInvalidAndUnknown`：空/非 hex/未知 64 位 token 统一 401/2012，`data=null` 不泄露存在性。 |
| AC-007 | PASS | `TestConcurrentRefreshSingleRotation`：10 并发提交同一 token，恰 1 成功 + 9 复用，family 仅 1 后代、0 未撤销行；`go test -race` 通过。 |
| AC-008 | PASS | `TestRefreshAccessExpiryIndependent`（已修复）：删除 session 后 refresh → 200，同 sid 重建、新 access 可访问 `/me`。 |
| AC-009 | PASS | 2012/2013/2014 均映射 401；响应沿用 `{code,message,data}`；错误 message 不含哈希/family 标识；测试断言各失败 code 与 data=null。 |
| AC-010 | PASS | `docs/design/iam.md`（§3.6 数据模型、§4.5 刷新流程、§6 错误码、§8 配置）与 `docs/design/migration.md`（清单新增 `20261001000007`）均已更新，与 Contract/实现一致。 |
| AC-011 | PASS | 可逆 Mutation 验证：将 `revokeAllFamiliesByUser` 改为 no-op 后 `TestRefreshReuseRevokesFamily` 失败（`expected 0 active rows, got 1`）；另将 `handleRefreshNotRotated` 改回「统一 reuse」后 `TestHandleRefreshNotRotatedExpired` 失败（`expected EXPIRED(2013), got 2014`），两处均证明测试能区分正确/错误实现，均已恢复原实现。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` / `go vet ./...` | PASS | 无错误、无告警 |
| `go test -p 1 ./...` | PASS | 全量测试通过（含 MySQL/Redis 集成测试；`internal/controller/iam` 12.438s、`internal/logic/iam` 0.141s、`internal/migrations` 10.430s） |
| `go test -race -p 1 -run 'TestConcurrentRefreshSingleRotation|TestRefreshReuseRevokesFamily|TestRefreshAccessExpiryIndependent' ./internal/controller/iam/` | PASS | 无 data race |
| `bash scripts/check-registry.sh` | PASS | 无 Reservation 重复 / Registry↔实现漂移 |
| 全局资源三边一致性 | PASS | migration `20261001000007`：Registry RESERVED ↔ Contract RESERVED ↔ 实现 `.up.sql` + `latestMigrationVersion`，一致；错误码 2012/2013/2014：Registry IAM 2000-2999 ACTIVE ↔ Contract ↔ `codes.go`，一致 |
| CLEAN-001 回归 Mutation | PASS | 把 `handleRefreshNotRotated` 改回「统一 reuse」后 `TestHandleRefreshNotRotatedExpired` 失败（got 2014，want 2013），证明回归测试可捕获原 bug；已恢复 |
| CLEAN-002 重建路径 | PASS | `TestRefreshAccessExpiryIndependent` 已真正 `Del` session 并断言 sid 不变 + 重建 + `/me` 200，覆盖重建路径 |

## Findings

### CLEAN-001：轮换事务中「过期」被误判为「reuse」并触发全量撤销

- Severity：P2
- Status：CLOSED
- Location：`internal/logic/iam/refresh.go` `Refresh` / `rotateRefresh`（修复后由 `classifyRefreshRow` + `respondRefreshRow` + `handleRefreshNotRotated` 承载）
- AC / Invariant：AC-005、AC-009；Contract「步骤③失败（并发 reuse/过期）→ 返回对应错误」；`docs/design/iam.md` §4.5「RowsAffected==0 则重判」。
- 复审验证：`bbe0229` 将判定抽为 `classifyRefreshRow`（rotated→REUSE / revoked_at 非空→INVALID / 过期→EXPIRED / 否则 OK），初始读取与「0 行 UPDATE 后重读」两条路径共用 `respondRefreshRow` 执行副作用；`handleRefreshNotRotated` 重读该行后返回对应错误，过期不再触发全量撤销。新增 `TestClassifyRefreshRow`（分类单元测试）与 `TestHandleRefreshNotRotatedExpired`（过期 token 返回 2013 且有效 token B 未被撤销）通过；Mutation（改回统一 reuse）使回归测试失败，证明其可区分错误实现。
- 结论：修复正确，关闭。

### CLEAN-002：AC-008 的提交测试未真正模拟 session 过期，重建路径无回归保护

- Severity：P3
- Status：CLOSED
- Location：`internal/controller/iam/refresh_test.go` `TestRefreshAccessExpiryIndependent`
- AC / Invariant：AC-008
- 复审验证：`bbe0229` 中该测试已真正 `g.Redis().Del(session)`，并新增断言：新 access 复用同 sid、session Hash 以同 sid 重建（revoked=0 且 user_id 匹配）、新 access 可访问 `/me`。测试通过，覆盖 session 重建路径。
- 结论：修复正确，关闭。

## 备注（非阻塞）

- session 重建不写索引（Contract 明示「不新增会话列表条目」）：refresh 重建过期 session 后，该 sid 不在 `iam:user:{id}:sessions` 索引中，故 `GET /sessions` 不展示、`revoke-others/revoke-all` 无法经索引撤销该重建会话；refresh 凭据撤销仍以 MySQL family 为权威（reuse/revoke-all 均覆盖）。这是 Contract 已确认的设计取舍，仅在 access token 最长 1 小时窗口内存在，本审查记录为已知边界，不另行开 Finding。
