# Delivery Verification

## Milestone and Target

- Milestone：IAM V5 用户账号状态管理与禁用会话控制（禁用即时失效 + 重新启用不复活旧凭证）
- Delivery Target：`feat/iam-user-status` HEAD `fac813cd37fd70b22db59ee358216eabdee71f2f`
- Cleaner Review Target：`60a28775c52754f6d7ad69bf54eeabafd1753d90`
- Target Match：YES（`fac813c` 相对 `60a2877` 仅新增 review / owner 元数据提交 `57406b6`/`7fd7579`/`b09bcd2`/`2278eab`/`fac813c`，生产代码、测试、迁移、设计文档未变）

## Environment

- OS：Linux（容器内执行）
- Go：1.24.1（`go version` 实测）
- MySQL：8.0（docker 容器 `my-shop-mysql`，healthy，端口 3306）
- Redis：7-alpine（docker 容器 `my-shop-redis`，healthy，端口 6379）
- 服务启动方式：测试通过真实 HTTP server（`g.Server().Start()` 随机端口 + `RegisterFrontendRoutes`/`RegisterAdminRoutes` 与生产 serve 同源）+ 真实 MySQL/Redis；`serve` 命令用于验证 Bootstrap 链路（依赖连通 + schema 就绪 + seed）
- 配置来源：`manifest/config/config.yaml` 开发默认值；`AUTH_JWT_SECRET`/`ADMIN_SUPER_PASSWORD` 以运行时 dev 值注入（未写入 .env，未记录生产 Secret）；七牛凭据缺失（见 Not Executed）
- 测试数据：测试自建隔离用户（`alice` 等）、自建超管并逐用例 flush Redis / 清空身份表，cleanup 恢复 baseline

## Verification

| Check | Result | Evidence |
|---|---|---|
| `delivery-start` Gate | PASS | `.agent/bin/workflow-check gate delivery-start .agent/tasks/iam-v5` → exit 0 |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go test -p 1 ./...` | PASS | 全量串行通过（含 `internal/cmd` 59s、`internal/migrations` 21s、`internal/controller/iam` 14s 等），exit 0 |
| `TestUserStatus*`（6 用例） | PASS | `go test -count=1 -v ./internal/cmd -run 'TestUserStatus'` 6/6 PASS |
| fail-closed（AC-014） | PASS | `TestAuthDBQueryFailClosed`（DB 侧）、`TestRedisWrongTypeFailClosed`（Redis 侧）均 PASS |
| 并发 race test | PASS | `go test -race -count=1 ./internal/cmd -run 'TestUserStatus'` exit 0，无数据竞争 |
| `migrate version` | PASS | `version: 20261001000019 (dirty=false)`，latest 已是最新 |
| `migrate up` 幂等 | PASS | 输出「没有待执行的 migration」，exit 0 |
| 权限 seed | PASS | `permissions` 表含 `user:read`、`user:status` |
| 审计落库 | PASS | `user_status_audits` 表存在且有真实审计行（测试运行写入） |
| Bootstrap 启动链路 | PASS | `serve` 命令实测：`all dependencies are reachable` → `schema 已就绪（20261001000019）` → 超管 seed → 权限 seed 全部通过 |
| 集成视图（develop_base + feature_head） | PASS | 临时 worktree merge 仅 `seed.go` 一处冲突（权限列表相邻插入），解决后 `go build`/`go vet`/`TestUserStatus*`/fail-closed 测试全通过 |

## Acceptance Evidence

- AC-001~007（登录/禁用/刷新/鉴权/多设备/重新启用/旧凭证不复活）：PASS——`TestUserStatusDisableRevokesAccessRefreshAndReEnable` 通过真实 HTTP 断言禁用后旧 access/登录/refresh 均 401、多设备全失效、重新启用后重登成功但旧凭证仍 401。
- AC-002/003（禁用用户登录/刷新拒绝且无 token）：PASS——断言 401/1002（登录）、401（refresh）且 `Data=nil`。
- AC-004/005（已签发未过期 JWT 禁用后 401、多设备全失效）：PASS——旧 access1/access2 禁用后访问 `/me` 均 401/1002。
- AC-006/007（重新启用后重登、旧凭证不复活）：PASS——重登成功 200，旧 access/旧 refresh 仍 401。
- AC-008/009/012（权限控制、id 来自 URL path、响应不含敏感字段）：PASS——`TestUserStatusAuthorizationAndNotFound` 覆盖前台用户 403、无权限/仅读管理员 403、持 `user:status` 与超管放行，id 取 `in:"path"`。
- AC-010（幂等重复禁用/启用）：PASS——重复禁用 `auth_epoch` 不递增、审计不重复；`TestUserStatusConcurrentDisableIdempotent` 8 并发禁用后 `auth_epoch=1`、审计仅 1 条。
- AC-011（目标不存在 404）：PASS——断言 404/2015 无写入。
- AC-013（状态持久化 + 审计字段完整）：PASS——`TestUserStatusAuditFields` 逐字段断言；`users.status`/`auth_epoch` 落库、`user_status_audits` 真实写入。
- AC-014（fail-closed）：PASS——DB 侧（RENAME users 触发 1146→401/1002）、Redis 侧（WRONGTYPE→401/1002）均不放行。
- AC-015（既有 IAM/RBAC 回归）：PASS——`go test -p 1 ./...` 全量通过，`type=user`/`type=admin` 隔离回归正常。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 完整 `serve` 启动 + `/health` HTTP smoke | 七牛凭据（`QINIU_ACCESS_KEY` 等）缺失，`serve` 在 `ValidateConfig` 阶段 fail-fast（`qiniu.access_key 未配置`） | 低——七牛与 iam-v5 交付范围无关；核心 API 主链路已通过测试中的真实 HTTP server + 真实路由 + 真实 MySQL/Redis 完整验证，Bootstrap（依赖/schema/seed/权限）已通过 `serve` 命令实测 |

## Remaining Risks

1. develop 已从任务基线 `f0bf42c` 前进至 `1a06ddf`（合并 dashboard-v1 等），feature 与 develop 在 `internal/boot/seed.go` 存在权限列表相邻插入冲突（iam-v5 加 `user:read`/`user:status`，dashboard-v1 加 `dashboard:view`）；`internal/cmd/routes_admin.go` 可自动合并。已在临时 worktree 验证：保留三条权限后集成视图可构建、可 vet、iam-v5 核心测试通过。Owner merge 时需解决该处冲突（解决方式已明确）。
2. 七牛凭据缺失使完整 `serve` 启动无法在本环境完成（ENVIRONMENT_GAP）；如需端到端 `/health` smoke，需 Owner 注入七牛凭据后补验。

## Result

PASS
