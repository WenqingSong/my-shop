# Delivery Verification

## Milestone and Target
- Milestone：IAM V4（Refresh Token 轮换、Token Family 与重用检测）
- Delivery Target：分支 `feat/iam-v4`，生产代码 HEAD `bbe0229`
- Cleaner Review Target：`bbe0229`（`fix(iam): 区分 refresh 轮换中过期与 reuse`）
- Target Match：YES（当前 HEAD `111ca12` 仅修改 `.agent/tasks/iam-v4/*` 文档，生产代码未变，与 Cleaner 审查对象一致）

## Environment
- OS：Linux
- Go：1.24.1（task 声明 1.23.0，README 开发用 1.24，无影响）
- MySQL：8.0（docker compose，healthy，127.0.0.1:3306）
- Redis：7-alpine（docker compose，healthy，127.0.0.1:6379）
- Docker Compose v2
- 服务：`bin/my-shop serve`，监听 `:8000`
- 配置来源：`manifest/config/config.yaml` + 环境变量覆盖；启动需注入 `ADMIN_SUPER_PASSWORD`（值不记录，见 Remaining Risks）
- 测试数据：唯一前缀用户名 `dv<ts>` / `exp<ts>` / `conc<ts>`，本地开发库，隔离且无需清理

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build | PASS | `go build ./...` exit 0 |
| Vet | PASS | `go vet ./...` exit 0 |
| Test | PASS | `go test -p 1 ./...` 全包 ok（`internal/controller/iam` 12.6s、`internal/migrations` 11.5s） |
| Race | PASS | `go test -race -p 1 -run 'TestConcurrentRefreshSingleRotation|TestRefreshReuseRevokesFamily|TestRefreshAccessExpiryIndependent' ./internal/controller/iam/` exit 0，无 data race |
| Migration | PASS | `schema_migrations` version=`20261001000007` dirty=0；`refresh_tokens` 表结构与 Contract 一致（`token_hash` CHAR(64) UNIQUE、`family_id`/`parent_id`/`generation`/`sid`/`revoked_reason`） |
| Startup | PASS | `bash scripts/up.sh` 成功，`GET /health` 200 `{"code":0,"message":"OK","data":{...}}` |
| Core Flow | PASS | 见 Acceptance Evidence |
| Data | PASS | SHA-256 匹配、明文不落库、family 血缘、revoked 状态、Redis `revoked=1` |

## Acceptance Evidence

| AC/INV | 本次运行结果 |
|---|---|
| AC-001 | 登录 200，返回 access（239 字节 JWT）+ refresh（64 hex）；DB `token_hash` = SHA-256(明文) 匹配，`plaintext_in_db: NO` |
| AC-002 | refresh 200 返回新双 token；旧行 `revoked_reason='rotated'`；新后代 `parent_id`=旧 id、`generation`=1、同 `sid` |
| AC-003 | 重放已轮换 token → 401/2014；该用户 family 全部 revoked（`rotated` + `revoked`） |
| AC-004 | 重放旧 token 后，合法新 token 再 refresh → 401/2012（已失效，需重新登录） |
| AC-005 | 手动将 `expires_at` 改为过去 → 401/2013；family 未被撤销（`revoked_reason` 仍 NULL，验证「过期不误判 reuse」） |
| AC-006 | 未知 64 位 token → 401/2012，`data=null`（不泄露存在性） |
| AC-007 | 10 并发提交同一 refresh token：恰 1 成功（code 0）+ 9 reuse（2014）；family 仅 1 个新后代 |
| AC-008 | refresh 后新 access 可用；session 重建路径由 `TestRefreshAccessExpiryIndependent` 覆盖（Cleaner 复审 CLOSED） |
| AC-009 | 2012/2013/2014 均映射 401；响应沿用 `{code,message,data}`；错误 message 不含哈希/family 标识 |
| AC-010 | `docs/design/iam.md` / `migration.md` 四者一致（Cleaner 审查 PASS，Deliverer 不重复 Design 审查） |
| AC-011 | 可逆 Mutation 已验证测试可区分错误实现（Cleaner 审查 PASS，Deliverer 不重复 Mutation） |
| INV-003 | reuse 后 Redis `iam:session:{sid}` 的 `revoked=1`，`GET /me` 用旧 access → 401/1002（access 会话已撤销） |

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| AC-011 可逆 Mutation 重做 | Cleaner 已执行并记录两处 Mutation 验证；Deliverer 不重复 Cleaner 的 Mutation 审查 | 无 |
| 全量 `-race ./...` | 仅对关键并发路径跑 race；其余包无并发敏感变更 | 低 |
| 性能/负载压测 | Task/Contract 未要求 | 无 |

## Remaining Risks

- 服务启动存在全局前置：super admin seed 在未配置 `ADMIN_SUPER_PASSWORD` 时 fail-fast（`internal/boot/seed.go`）。属 IAM V3 引入的启动逻辑，非本任务（前台 refresh）缺陷；开发/验收环境需注入该变量，生产由环境变量管理。
- Redis access session 全量撤销为 best-effort：Redis 撤销失败时 access token 最长可残留有效至 session TTL（Owner 已接受的安全降级边界，`core-logic.md` CL-002 已 ACCEPTED）。

## Result

PASS
