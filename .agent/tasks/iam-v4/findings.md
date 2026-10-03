# Cleaner Findings

## Review Target

- 分支 `feat/iam-v4`，当前 HEAD：`8f37bc1`（`feat(iam): 新增 refresh token 轮换与血缘撤销`），working tree 干净（`git status --short` 为空）。
- 任务基线：`task.md` 声明 base `1db733d`（无 IAM V3、无已有修改）。但 `contract.md`「Verified Current Behavior」已明确该基线信息过时：当前分支已包含 IAM V3（`0291413` 是 HEAD 祖先），`feat/iam-v4` 相对 `origin/develop` 领先 3、落后 0，无需再抉择基线（OPEN QUESTION 已消解）。本次以 `5541b4a`（task 文档提交，含 IAM V3）为变更前基准。
- 本任务变更 = 两个提交：
  - `3d185a9`（`docs(iam-v4): 添加 IAM V4 refresh token 技术契约`）：`.agent/tasks/iam-v4/contract.md`、`docs/design/iam.md`、`docs/design/migration.md`、`.agent/registry/migrations.md`。
  - `8f37bc1`（`feat(iam): 新增 refresh token 轮换与血缘撤销`）：`api/iam/v1/iam.go`、`internal/auth/refresh.go`（新增）、`internal/auth/refresh_test.go`（新增）、`internal/auth/session.go`、`internal/boot/boot_migration_test.go`、`internal/cmd/routes_frontend.go`、`internal/codes/codes.go`、`internal/controller/iam/iam.go`、`internal/controller/iam/refresh_test.go`（新增）、`internal/logic/iam/iam.go`、`internal/logic/iam/refresh.go`（新增）、`internal/migrations/migrations_test.go`、`internal/migrations/sql/20261001000007_refresh_tokens.up.sql`（新增）、`internal/service/iam.go`、`manifest/config/config.yaml`。
- 任务前已有修改：无（变更均已提交，无未提交重叠修改需要区分）。
- 关键全局资源：migration `20261001000007`（`refresh_tokens`，Registry `RESERVED`）；错误码 `2012/2013/2014`（IAM 域 2000-2999 `ACTIVE`）；配置 `auth.refresh.ttl`=2592000。
- 环境：MySQL 8.0 + Redis 7（docker compose 已就绪，`docker compose ps` 显示两者 healthy）。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestLoginIssuesRefreshToken`：login 返回 64 位 hex refresh token；DB `token_hash`=SHA-256 且 != 明文；明文哈希列不存在（明文不落库）。全量集成测试通过。 |
| AC-002 | PASS | `TestRefreshRotationAndLineage`：refresh 返回新双 token；旧 token 置 `revoked_reason='rotated'`；新后代 `parent_id`=旧 id、`generation`=1、同 sid、同 family。 |
| AC-003 | PASS | `TestRefreshReuseRevokesFamily`：重放已轮换 token → 401/2014；`activeFamilyCount`=0（family 全撤销）；最新后代再 refresh → 401。 |
| AC-004 | PASS | 同上测试覆盖被盗重放时序：攻击者重放旧 token 后，合法新 token 亦失效，攻击者无法换新 access。 |
| AC-005 | PASS | `TestRefreshExpired`：`expires_at` 置为过去后 refresh → 401/2013。常规过期路径通过（见 CLEAN-001 的边界竞态例外）。 |
| AC-006 | PASS | `TestRefreshInvalidAndUnknown`：空/非 hex/未知 64 位 token 统一 401/2012，`data=null` 不泄露存在性。 |
| AC-007 | PASS | `TestConcurrentRefreshSingleRotation`：10 并发提交同一 token，恰 1 成功 + 9 复用，family 仅 1 后代、0 未撤销行；`go test -race` 通过。 |
| AC-008 | PASS | 独立验证：临时集成测试删除 session 后 refresh → 200，同 sid 重建、新 access 可访问 `/me`（验证后已删除临时文件）。注：提交的 `TestRefreshAccessExpiryIndependent` 未真正删除 session（见 CLEAN-002）。 |
| AC-009 | PASS | 2012/2013/2014 均映射 401；响应沿用 `{code,message,data}`；错误 message 不含哈希/family 标识；测试断言各失败 code 与 data=null。 |
| AC-010 | PASS | `docs/design/iam.md`（§3.6 数据模型、§4.5 刷新流程、§6 错误码、§8 配置）与 `docs/design/migration.md`（清单新增 `20261001000007`）均已更新，与 Contract/实现一致（除 CLEAN-001 所述「重判」实现偏差）。 |
| AC-011 | PASS | 可逆 Mutation 验证：将 `revokeAllFamiliesByUser` 改为 no-op 后 `TestRefreshReuseRevokesFamily` 失败（`expected 0 active rows, got 1`），证明该测试能区分「撤销 family」与「不撤销 family」的错误实现；已恢复原实现并确认 `go build` 通过。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 无错误 |
| `go vet ./...` | PASS | 无告警 |
| `go test -p 1 ./...` | PASS | 全量测试通过（含 MySQL/Redis 集成测试；`internal/controller/iam` 12.812s、`internal/migrations` 10.524s） |
| `go test -race -p 1 -run 'TestConcurrentRefreshSingleRotation|TestRefreshReuseRevokesFamily|TestRefreshRotationAndLineage' ./internal/controller/iam/` | PASS | 无 data race |
| `bash scripts/check-registry.sh` | PASS | 无 Reservation 重复 / Registry↔实现漂移 |
| 全局资源三边一致性 | PASS | migration `20261001000007`：Registry RESERVED ↔ Contract RESERVED ↔ 实现 `20261001000007_refresh_tokens.up.sql` + `latestMigrationVersion`，一致；错误码 2012/2013/2014：Registry IAM 2000-2999 ACTIVE ↔ Contract ↔ `codes.go`，一致 |
| AC-011 Mutation | PASS | 见上；破坏 family 撤销后测试失败，恢复后通过 |

## Findings

### CLEAN-001：轮换事务中「过期」被误判为「reuse」并触发全量撤销

- Severity：P2
- Status：OPEN
- Location：`internal/logic/iam/refresh.go` `Refresh`（约 106-116 行）+ `rotateRefresh`（约 151-153 行）
- AC / Invariant：AC-005（过期须返回明确过期错误）、AC-009（失败场景按 code 判型）；Contract「Failure and Consistency Semantics」步骤③「失败（并发 reuse/过期）→ 回滚并返回对应错误」；`docs/design/iam.md` §4.5 步骤 3「RowsAffected==0 则回滚并按第 1 步重判」。
- Trigger：refresh token 在「第①步初始读取通过（未过期）」之后、事务内条件 `UPDATE ... WHERE revoked_at IS NULL AND expires_at > NOW()` 执行之前恰好过期。此时 UPDATE 影响 0 行，进入 `!rotated` 分支。
- Actual：`!rotated` 分支无条件调用 `revokeAllByUser`（撤销该用户全部 refresh families + 全部 access sessions）并返回 2014 REUSE，不区分「并发已轮换」与「恰逢过期」。
- Expected：0 行时重读该行并区分：`revoked_reason='rotated'` → 2014 REUSE + 全量撤销；`revoked_at IS NULL 且 expires_at <= NOW()` → 2013 EXPIRED，不做全量撤销；`revoked_reason='revoked'` → 2012 INVALID。
- Impact：一个即将过期的合法 refresh 请求会被误判为「被盗重放」，返回错误 code（2014 而非 2013），并把该用户**全部设备会话与全部 refresh family 一并撤销**（比单纯过期多出远超预期的破坏面），用户被强制全端重新登录。触发窗口较窄（初始读取到 UPDATE 之间，含一次 Redis session upsert 往返），但后果属核心语义偏差。
- Evidence：`rotateRefresh` 对 `RowsAffected==0` 统一返回 `errRefreshNotRotated`，`Refresh` 将 `!rotated` 一律映射为 reuse + `revokeAllByUser`；代码无任何「重读区分」逻辑。Contract 与 Design 均要求「返回对应错误/按第 1 步重判」，实现未遵循。
- Required Fix Boundary：修复必须恢复「0 行时按真实状态返回对应错误」，即过期 → 2013 且不触发全量撤销；并发已轮换 → 2014 + 全量撤销；已主动撤销 → 2012。不规定重读的具体实现方式（事务内 SELECT、或 UPDATE 后查 `expires_at`/`revoked_reason` 均可），但不得继续把过期统一按 reuse 处理。

### CLEAN-002：AC-008 的提交测试未真正模拟 session 过期，重建路径无回归保护

- Severity：P3
- Status：OPEN
- Location：`internal/controller/iam/refresh_test.go` `TestRefreshAccessExpiryIndependent`（约 403-419 行）
- AC / Invariant：AC-008（access 过期后 refresh 仍可换新 access 并访问受保护接口）
- Trigger：代码注释声称「模拟 access token 过期：直接删除对应 session」，但函数体内未执行任何删除，直接对仍有效的 session 调 refresh，走的是 `UpsertSessionAlive` 的「续期」分支而非「重建」分支。
- Actual：该测试未覆盖 session 重建路径；重建逻辑（`UpsertSessionAlive` → `createSessionHash` 不写索引）在提交的测试集中无直接回归保护。
- Expected：测试应真正删除 session（或置 TTL 到期）后再 refresh，断言同 sid 重建、新 access 可访问 `/me`。
- Impact：AC-008 的核心场景（access/session 已过期后重建）缺少可区分错误实现的回归测试；后续若重建逻辑被破坏，现有测试仍可能全绿。
- Evidence：`TestRefreshAccessExpiryIndependent` 代码（注释与实现不符）；Cleaner 已用临时集成测试独立验证重建路径当前工作正常（验证后删除），确认当前实现正确、仅测试保护不足。
- Required Fix Boundary：补充或修正测试以真实触发 session 重建路径，并断言 refresh 返回 200、新 access 可访问 `/me`、session 以同 sid 重建。不规定具体实现方式（删除 session Hash 或缩短 TTL 均可）。

## 备注（非阻塞）

- session 重建不写索引（Contract 明示「不新增会话列表条目」）：refresh 重建过期 session 后，该 sid 不在 `iam:user:{id}:sessions` 索引中，故 `GET /sessions` 不展示、`revoke-others/revoke-all` 无法经索引撤销该重建会话；refresh 凭据撤销仍以 MySQL family 为权威（reuse/revoke-all 均覆盖）。这是 Contract 已确认的设计取舍，仅在 access token 最长 1 小时窗口内存在，本审查记录为已知边界，不另行开 Finding。
