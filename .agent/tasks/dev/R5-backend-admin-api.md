# R5 开发记录：Go 后端管理端 API 能力补齐

> 仓库：`/root/projects/my-shop-integration`
> 开发分支：`feat/p2-r5-backend`（Worktree `/root/projects/my-shop-worktrees/r5-backend`）
> 基线：`ddfd5ab77ced617ae0b0020c46a8220396b52145`
> 范围：仅 Go 后端（`api/**`、`internal/**`、`openapi/**`、相关文档）。未修改 `admin/**`、`web/**`、`nginx/**`、部署配置、Migration。

## 1. 范围冻结结论（R5-01）

核查对象：`admin/src/api/**`、`views/**`、`router/**`、`store/**` + A5/A1 审查报告 + 后端源码。

| 能力 | 结论 | 依据 |
| --- | --- | --- |
| `/admin/me` `permissions[]` | 实现 | 前端已读取该字段（`store/modules/user.ts`、`utils/permission.ts`），缺失导致非超管全量 fail-closed |
| 角色已授权限查询 | 实现 | 权限抽屉明确"当前无法读取角色已拥有的权限"，属被阻断的 UI |
| 用户列表 / 详情 | 实现 | 用户页 el-alert/el-empty 声明"后端没有用户列表接口"，页面降级为按 ID 查询 |
| 管理员列表 | 不实现 | 页面明确"不展示列表"，按 ID 操作；需新增 `admin:list` 并扩大账号可枚举面 |
| 秒杀活动列表/详情 | 不实现 | 不存在列表/详情页面（页面明确"不提供活动列表"） |

详见 `docs/reports/PHASE2_R5_SCOPE.md`。

## 2. 实现要点

- `GET /admin/me`：`permissions[]` 来自 `permissions ⋈ role_permissions ⋈ admin_roles`，`GROUP BY p.code ORDER BY p.code`（去重、稳定）；与 `middleware.RequirePermission` 同一条匹配链，无第二套判定；查询失败 fail-closed 返回 1000，不降级为空集合；超管的"全量权限"由 `is_super` 表达。
- `GET /admin/roles/:id/permissions`：`role:list` 保护；先校验角色存在（404/2007），再按 `p.id` 升序返回既有 `Permission` 结构；查询失败 500，不伪装空列表。
- `GET /admin/users` / `GET /admin/users/:id`：`user:read` 保护；字段白名单 `id/username/status/created_at`（SQL 层不取 `password_hash`/`auth_epoch`）；分页 `page`/`size` 默认 1/20、上限 100，越界 400；排序 `id DESC`；详情不存在 404/2015。
- 未新增权限码（仍 73 个），未改 Migration，未改中间件。

## 3. 契约与门禁同步

- `internal/cmd/routes_test.go`：清单 +3 条，总数 210→213，分布 98/110/2→98/113/2。
- `tools/openapi-gen`：`gen.go` 期望基线、`opmeta.go`（`GET /admin/users` 严格分页）、`model.go`（`iam.AdminUser → AdminUser`）、`unused-components-allowlist.yaml`、`main_test.go` 同步；随后 `go run ./tools/openapi-gen -repo .` 重生成 `openapi/**` 与 `docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md`。
- `docs/design/rbac.md`、`docs/design/iam.md` 同步接口与权限语义。

## 4. 验证

- `go build ./...`、`go vet ./...`、`gofmt -l` 全部通过。
- `bash scripts/contract-check.sh` 全部通过（含 `-check`、定向路由测试 `go test ./internal/cmd -run 'TestRoute|...'`、Spectral、生成器单测）。
- 运行时（隔离 MySQL/Redis + `serve :18000`）：未认证 401、用户 token 403、超管 200、多角色合并去重、缺权限 403、分页边界 400、不存在 ID 404/2015/2007、RBAC 表故障时 500 fail-closed。
- 未执行 `go test ./...` / `make test-unit`（Owner 禁止）。

## 5. 已知遗留

- `scripts/contract-check.sh:97` 成功提示文案仍为"210 条常驻路由"（仅提示语；脚本不在本任务所有权内）。
- R5-05 / R5-06 缺口与建议方案见交付报告第 16 节。
