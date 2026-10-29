# Cleaner Findings

## Review Target

- 任务基线（Base commit）：`727cf978976d63010ed8bb67a991109e251bc57f`（分支 `feature/like`，与 `origin/develop` 的 merge-base 一致）。
- Implementation Evidence Commit（C1，`review.target`）：`c926efef02acd993c28dae43fcae86b8327274b0`（`fix(like): 补齐 product_likes 迁移测试与清理清单并修正格式`，为 Coder 最终实现提交）。
- 复审时 HEAD：`a9d56975ce50c290126e2b90e36bf8e2ae539f4e`（`chore(state): 发起 product-like-v1 实现复审`，仅改 `state.yaml`，属 review-neutral metadata tail，非 C1）。
- 任务前已有修改：无。基线 `727cf97` 处 working tree clean；任务相关全部产物均由 `727cf97..c926efe` 引入。
- 审查对象（`727cf97..c926efe` 相对变更，含新增文件）：
  - `api/like/v1/like.go`（点赞 API 契约，4 个接口）
  - `internal/controller/like/like.go`（HTTP 适配，Auth 分组 + 公开计数）
  - `internal/service/like.go`（`ILike` 接口 + `RegisterLike`）
  - `internal/logic/like/like.go`（业务逻辑）
  - `internal/logic/logic.go`（blank import 注册）
  - `internal/codes/codes.go`（新增 `CodeLikeProductUnavailable=13001`）
  - `internal/cmd/routes_frontend.go`（路由：`POST /likes`、`DELETE /likes/:product_id`、`GET /likes/check` 走 Auth；`GET /likes/count` 公开）
  - `internal/migrations/sql/20261001000013_product_likes.up.sql`（点赞表迁移）
  - `internal/migrations/migrations_test.go`（`latestMigrationVersion`/`businessTables`/`expectedSchema` 同步）
  - `internal/boot/boot_migration_test.go`（清理清单加 `product_likes`）
  - `internal/cmd/like_test.go`（8 个集成测试）
  - `internal/cmd/routes_test.go`（路由表断言）
  - `docs/design/like.md`（长期设计，Design Impact = NEW）
  - `.agent/tasks/product-like-v1/contract.md`、`state.yaml`（Analyst/状态产物）
- 关键配置/迁移版本：错误码域 `13000-13999`（`13001`）；migration `20261001000013_product_likes`。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001（点赞） | PASS | `TestLikeAndCheck`：登录用户点赞 `on_shelf` 商品，断言 200、`liked=true`、`product_likes` 落库且 `user_id` 归属本人。 |
| AC-002（取消点赞） | PASS | `TestLikeCancelAndCheck`：点赞后取消 → 200、库中记录删除、`check=false`。 |
| AC-003（重复点赞） | PASS | `TestLikeDuplicateIdempotent`（串行重复幂等成功、仅 1 条）+ `TestLikeConcurrentDuplicateSingleRow`（`-race` 下 8 并发点赞均幂等成功、至多 1 条，`uk_user_product` 兜底）。 |
| AC-004（点赞可见性） | PASS | `TestLikeCountPublic`：公开 `GET /likes/count` 无需 token，点赞/取消后计数 0→1→2→1 正确变化，不存在商品为 0。 |
| AC-005（是否已点赞） | PASS | `TestLikeAndCheck`（点赞后 check=true）+ `TestLikeCancelAndCheck`（取消后 check=false）。 |
| AC-006（用户隔离） | PASS | `TestLikeUserIsolation`：B 对 A 的点赞 `check=false`；B 取消 A 的点赞幂等成功但 A 记录保留（`user_id` 仍为 owner），无跨用户写入。 |
| AC-007（商品校验） | PASS | `TestLikeProductValidation`：不存在商品 404/4001、`off_shelf`/`draft` 409/13001、`product_id≤0` 400/1001，均无写入。 |
| AC-008（必须登录） | PASS | `TestLikeRequiresAuth`：无 token / 非法 token 访问 `POST /likes`、`GET /likes/check`、`DELETE /likes/:product_id` 均 401/1002 且无写入；公开计数无需 token 正常 200。 |
| AC-009（数据模型与迁移） | PASS | `TestUpCreatesSchemaAndIsIdempotent`（版本 `20261001000013`、幂等）+ `TestSchemaStructureMatchesBaseline`（`product_likes` 结构快照：列/唯一约束/索引严格等价），`internal/boot` readiness 测试通过。 |
| AC-010（长期设计） | PASS | `docs/design/like.md` 与 APPROVED Contract、最终实现四者一致（见「四者一致性」）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 编译通过，无输出。 |
| `go vet ./...` | PASS | 静态检查通过。 |
| `go test -p 1 ./...` | PASS | 全量测试通过（含 `internal/cmd`、`internal/migrations`、`internal/boot` 等，MySQL 8.0 + Redis 7 容器已就绪）。 |
| `go test -race -run 'TestLike' ./internal/cmd/ -v` | PASS | 8 个点赞集成测试全部通过（含并发去重 `-race`），走真实 `RegisterFrontendRoutes` + `middleware.Auth` + 真实 MySQL/Redis。 |
| 迁移结构等价 | PASS | `TestSchemaStructureMatchesBaseline`、`TestUpCreatesSchemaAndIsIdempotent` 通过，`product_likes` 列/索引/引擎/字符集与迁移严格一致。 |
| 全局资源三边一致性 | PASS | Registry（`origin/develop`，权威）↔ Contract ↔ 实现一致：`origin/develop` 的 `d75ded2` 已 `RESERVED` `13000-13999`（product-like-v1）与 `20261001000013`（product_likes）；实现使用 `13001` 与 `20261001000013_product_likes`。 |
| 四者一致性（Task ↔ Contract ↔ Design ↔ 实现） | PASS | 数据模型（`product_likes` + `uk_user_product` + `idx_product_id`）、幂等点赞/取消、可点赞校验（存在且 `on_shelf`）、本人状态 `GET /likes/check`、公开计数 `GET /likes/count`（实时 COUNT）、错误码 `13001`/复用 `4001`/`1001`/`1002`、migration `20261001000013` 均一致。 |

### 说明（非 Finding）

- 本地 `scripts/check-registry.sh` 因 feature 分支基于 `727cf97`（早于 `origin/develop` 的预留提交 `d75ded2`）而报 `13001`/`20261001000013` 漂移，属「本地 registry 文件滞后于 develop」的分支同步事实，非真实漂移。权威校验 `workflow-check gate`（以 `origin/develop` 为 Registry 权威）与语义审查均确认资源已正确预留、三边一致。分支合并进 develop 时该差异自然消除，无需 Coder 改动。

## Findings

No actionable findings.

- 实现完整覆盖 Goal 与全部 AC，Scope 内、无越界改动；错误处理不泄漏内部信息（DB 技术错误统一 `1000`/500）；并发去重由 `uk_user_product` 唯一约束兜底；用户隔离由 `Principal.UserID` 服务端过滤；公开计数采用实时 COUNT，无冗余列、无双重写。
- 测试覆盖关键拒绝路径、边界与并发，断言验证最终业务结果（落库行数、归属、计数、状态码/错误码），无 Mock 绕过 MySQL/Auth/HTTP 边界。
