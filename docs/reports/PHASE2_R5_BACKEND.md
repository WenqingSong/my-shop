# PHASE2 R5 交付报告：Go 后端管理端 API 能力补齐

- 任务：R5（Wave 1，R5-01 ~ R5-06）
- 仓库：`/root/projects/my-shop-integration`
- 集成分支：`feat/phase2-ecommerce-business`
- 开发基线 SHA：`ddfd5ab77ced617ae0b0020c46a8220396b52145`
- 开发分支：`feat/p2-r5-backend`
- Worktree：`/root/projects/my-shop-worktrees/r5-backend`
- 范围冻结记录：`docs/reports/PHASE2_R5_SCOPE.md`

## 1. 原始问题

管理端（`admin/src/**`）已完成业务页面，但 Go 后端缺少若干管理端查询能力，导致页面只能按 ID 操作或 fail-closed：

1. `GET /admin/me` 不返回 `permissions[]`：前端 `store/modules/user.ts` 与 `utils/permission.ts` 已就绪，但字段缺失使 `permissions === null`，**所有非超管的菜单、路由守卫与 21 处 `hasPerms()` 按钮全部 fail-closed**。
2. 角色权限抽屉没有"查询角色已有权限"接口，页面明确注释"当前无法读取角色已拥有的权限"，只能提供授予/移除两个盲写操作。
3. 只有 `GET/PUT /admin/users/:id/status`，没有用户列表与详情，用户页因此降级为"输入用户 ID 查询状态"，并在 el-alert / el-empty 中声明"后端没有用户列表接口"。

按 R5-01 的核查规则（前端**实际读取**该字段，或已有页面存在**被缺失接口直接阻断的 UI 区域**），本轮实现 R5-02 / R5-03 / R5-04；R5-05 / R5-06 判定为不满足触发条件，登记为缺口（见第 16 节）。逐条证据见 `docs/reports/PHASE2_R5_SCOPE.md`。

## 2. 本次实际实现的接口

| # | 方法 | 路径 | 说明 | 保护 | 新增路由 |
| --- | --- | --- | --- | --- | --- |
| R5-02 | GET | `/admin/me` | 响应新增 `permissions[]`（身份字段全部保留） | `AdminAuth`（既有） | 否（仅响应扩展） |
| R5-03 | GET | `/admin/roles/:id/permissions` | 查询角色已授权限 | `AdminAuth` + `RequirePermission("role:list")` | 是 |
| R5-04 | GET | `/admin/users` | 后台用户分页列表 | `AdminAuth` + `RequirePermission("user:read")` | 是 |
| R5-04 | GET | `/admin/users/:id` | 后台用户详情 | `AdminAuth` + `RequirePermission("user:read")` | 是 |

常驻 JSON API 路由：**210 → 213**（后台 110 → 113，前台 98，健康检查 2）。
RBAC 权限码：**73 → 73**（未新增权限码；`user:read`、`role:list` 均为既有码）。

## 3. 请求 / 响应 DTO

### 3.1 `GET /admin/me`

请求：无参数（`MeReq`）。身份只取自已认证的 `AdminPrincipal.AdminID`，**不存在任何接受管理员 id 的入参**。

```go
type MeRes struct {
    Id          int64    `json:"id"`
    Username    string   `json:"username"`
    IsSuper     bool     `json:"is_super"`
    Roles       []string `json:"roles"`
    Permissions []string `json:"permissions"` // 新增：去重、按 code 升序；始终为数组，不返回 null
}
```

### 3.2 `GET /admin/roles/:id/permissions`

```go
type ListRolePermissionReq struct {
    Id int64 `json:"id" in:"path" v:"required"` // 角色 id
}

type ListRolePermissionRes struct {
    Items []*Permission `json:"items"` // 按权限 id 升序；无授权时为空数组
}

type Permission struct { // 复用既有结构，与 POST/DELETE 授权接口一致
    Id          int64  `json:"id"`
    Code        string `json:"code"`
    Name        string `json:"name"`
    Description string `json:"description"`
}
```

### 3.3 `GET /admin/users`

```go
type ListUserReq struct {
    Page *int `json:"page"` // 默认 1；<1 或非整数 → 400/1001
    Size *int `json:"size"` // 默认 20，上限 100；越界或非整数 → 400/1001
}

type ListUserRes struct {
    Items []*AdminUser `json:"items"`
    Total int          `json:"total"`
    Page  int          `json:"page"`
    Size  int          `json:"size"`
}
```

### 3.4 `GET /admin/users/:id`

```go
type GetUserReq struct {
    Id int64 `json:"id" in:"path" v:"required"` // 目标用户 id（URL 路径）
}

type GetUserRes struct {
    AdminUser // id / username / status / created_at
}
```

### 3.5 用户视图 `AdminUser`（列表项与详情共用）

```go
type AdminUser struct {
    Id        int64       `json:"id"`
    Username  string      `json:"username"`
    Status    int         `json:"status"`     // 1 启用 / 0 禁用
    CreatedAt *gtime.Time `json:"created_at"` // 数据库墙钟时间
}
```

## 4. API 路由及权限保护

`internal/cmd/routes_admin.go`（生产入口与契约工具共用同一注册函数）：

```go
require("user:read").GET("/admin/users", iamCtrl.ListUser)
require("user:read").GET("/admin/users/:id", iamCtrl.GetUser)
require("user:read").GET("/admin/users/:id/status", iamCtrl.GetUserStatus)   // 既有
require("user:status").PUT("/admin/users/:id/status", iamCtrl.UpdateUserStatus) // 既有

require("role:list").GET("/admin/roles/:id/permissions", adminCtrl.ListRolePermission)
```

- 保护全部在后端执行：`AdminAuth`（验签/exp/type=admin/管理员会话/admins.status=1）+ `RequirePermission(code)`。前端菜单隐藏不构成任何安全边界。
- `GET /admin/me` 仍只挂 `AdminAuth`（返回"自己的"信息，不需要额外权限码）。
- 未新增权限码，因此非超管不会因本次改动自动获得任何访问能力：`role:list`、`user:read` 需由管理员显式授予。
- 路由无歧义：`/admin/users/:id`、`/admin/users/:id/status`、`/admin/users` 为不同深度节点；运行时验证 `GET /admin/users/:id/status` 仍返回 200。

## 5. RBAC 权限集合计算方式

`GET /admin/me` 的 `permissions[]` 由 `internal/logic/admin` 的 `listPermissionCodes` 计算：

```sql
SELECT p.code
FROM permissions p
  INNER JOIN role_permissions rp ON rp.permission_id = p.id
  INNER JOIN admin_roles ar      ON ar.role_id = rp.role_id
WHERE ar.admin_id = ?
GROUP BY p.code
ORDER BY p.code
```

- **与中间件同一条匹配链**：`middleware.RequirePermission → hasPermission` 用的也是 `admin_roles → role_permissions → permissions` 按 `p.code` 命中；这里没有第二套权限判定逻辑，也没有角色名 → 权限码的本地映射。
- **多角色合并 + 去重**：`GROUP BY p.code` 保证同一 code 被多个角色授予时只出现一次（运行时用两个角色同时授予 `user:read` 验证，返回 `["user:read"]`）。
- **无无效权限码**：只返回 `permissions` 表中真实存在的行，不推导、不补全。
- **fail-closed**：`listPermissionCodes` 与 `listRoleNames` 任一查询失败都向上返回 `1000`（500），**绝不降级为空集合或伪造权限集合**。运行时通过临时重命名 `role_permissions` 制造故障验证：`/admin/me` 返回 `500 {"code":1000}`，恢复后返回正常数据。
- **超级管理员**：`is_super=1` 的"全量权限"由 `is_super` 表达（`RequirePermission` 对超管直通，前端 `resolvePermissions` 亦把超管映射为 `["*"]`）；该字段仍只反映角色授权事实，不伪造"被授予全部权限码"，避免与真实授权数据不一致。
- **不接受前端传入管理员 ID**：`Me` 的入参只有 controller 从 `AdminPrincipal` 取出的 `adminID`，请求结构体无 id 字段。

## 6. 数据库查询与敏感字段保护

| 接口 | 查询 | 说明 |
| --- | --- | --- |
| `/admin/me` | `permissions` ⋈ `role_permissions` ⋈ `admin_roles`；`roles` ⋈ `admin_roles`（既有） | 只读，无写入 |
| `/admin/roles/:id/permissions` | 先 `roles` 存在性校验，再 `permissions ⋈ role_permissions` | 角色不存在 404/2007 |
| `/admin/users` | `SELECT COUNT(*)` + `SELECT id, username, status, created_at FROM users ORDER BY id DESC LIMIT ...` | 固定排序 `id DESC`（唯一键，翻页稳定） |
| `/admin/users/:id` | `SELECT id, username, status, created_at FROM users WHERE id = ?` | 不存在 404/2015 |

敏感字段保护：

- 所有用户查询使用 `Fields("id","username","status","created_at")` **显式白名单**，SQL 层即不取 `password_hash`、`auth_epoch`；不返回 refresh/access token、会话、审计等认证材料。
- `nickname/phone/birthday` 属资料类 PII 且管理端页面当前无对应控件，本轮不外扩（页面仍显示"后端未提供该字段"）。
- 错误响应不含 SQL、表名或内部字段名；`glog` 中的底层错误只落服务端日志。
- 未引入写操作、未新增审计表、未放宽任何既有约束。

## 7. 修改文件清单

代码：

| 文件 | 变更 |
| --- | --- |
| `api/admin/v1/admin.go` | `MeRes.Permissions`；`ListRolePermissionReq/Res` |
| `api/iam/v1/iam.go` | `AdminUser`、`ListUserReq/Res`、`GetUserReq/Res` |
| `internal/service/admin.go` | `IAdmin` 新增 `ListRolePermission`；`Me` 注释更新 |
| `internal/service/iam.go` | `IIam` 新增 `ListUser`、`GetUser` |
| `internal/logic/admin/admin.go` | `Me` 返回权限码；新增 `ListRolePermission`、`listPermissionCodes` |
| `internal/logic/iam/user_status.go` | 新增 `ListUser`、`GetUser`、分页参数校验、`userAdminRow` |
| `internal/controller/admin/admin.go` | 新增 `ListRolePermission` |
| `internal/controller/iam/iam.go` | 新增 `ListUser`、`GetUser` |
| `internal/cmd/routes_admin.go` | 注册 3 条新路由并注明权限 |
| `internal/cmd/routes_test.go` | 权威路由清单 +3 条；总数 210→213；分布 98/110/2→98/113/2 |

契约与文档：

| 文件 | 变更 |
| --- | --- |
| `openapi/surgecart.openapi.yaml` | 生成器重生成（3 条新 operation + `AdminMeResponse.permissions`） |
| `openapi/paths/admin.yaml` | 同上（分域视图） |
| `openapi/components/schemas.yaml` | 新增 `AdminUser`、`UserListUserRequest/Response`、`UserGetUserRequest/Response`、`AdminListRolePermissionRequest/Response` |
| `docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md` | 由 `tools/openapi-gen` 自动重生成（DTO 覆盖清单） |
| `docs/design/rbac.md` | 权限码语义、`/admin/me` `permissions[]` 语义、用户/角色查询接口与权限挂载 |
| `docs/design/iam.md` | §4.6 增加用户列表/详情的字段白名单与分页语义 |
| `docs/reports/PHASE2_R5_SCOPE.md` | 新增（R5-01 范围冻结） |
| `docs/reports/PHASE2_R5_BACKEND.md` | 新增（本报告） |
| `.agent/tasks/dev/R5-backend-admin-api.md` | 新增（R5 开发记录） |

契约工具链同步（`openapi` 由生成器产出，手工编辑被禁止；不同步则契约门禁失败）：

| 文件 | 变更 |
| --- | --- |
| `tools/openapi-gen/gen.go` | 期望分布基线 210/98/110/2 → 213/98/113/2 |
| `tools/openapi-gen/opmeta.go` | 登记 `GET /admin/users` 为 `Pagination: "strict"` |
| `tools/openapi-gen/model.go` | 登记实体稳定 Schema 名 `iam.AdminUser → AdminUser` |
| `tools/openapi-gen/unused-components-allowlist.yaml` | 登记 3 个新增「完整请求 DTO」保留项 |
| `tools/openapi-gen/main_test.go` | 路由解析数期望 208 → 211（213 减 2 条自动绑定 health） |

未修改：`web/**`、`admin/**`、`nginx/**`、部署配置、`internal/middleware/**`、`internal/boot/seed.go`、`internal/migrations/**`、其他 Agent 的开发文件。

## 8. 是否涉及 Migration

**否**。未新增/修改任何 Migration，未改表结构、索引、约束或认证模型；本轮全部为既有表的只读查询。`git status` 中 `internal/migrations/` 与 `internal/boot/` 无变更。

## 9. `go build` 结果

```text
$ go build ./...
（无输出，exit 0）
```

## 10. `go vet` 结果

```text
$ go vet ./...
（无输出，exit 0）
```

补充静态检查（未运行被明令禁止的 `go test ./...` / `make test-unit`）：

| 检查 | 结果 |
| --- | --- |
| `gofmt -l api internal tools` | 无输出（全部已格式化） |
| `bash scripts/contract-check.sh`（项目契约门禁） | **全部通过** |
| └ 生成结果一致性（生成前→生成→生成后） | PASS 无漂移 |
| └ `go run ./tools/openapi-gen -repo . -check` | PASS：213 条常驻 API + 4 条 planned、497 个 Schema、DTO↔Schema、错误码↔codes.go、**权限码↔路由↔seed 双向一致** |
| └ `go test ./internal/cmd/ -run 'TestRoute\|TestContractPlannedNotRouted' -count=1` | PASS（路由表 213 条、分布 98/113/2、与 OpenAPI 双向一致） |
| └ Spectral lint | PASS（无 error 级问题） |
| └ `go test ./tools/openapi-gen/ -count=1` | PASS |

## 11. 运行时验证结果

环境：隔离容器 MySQL 8.0.46（`127.0.0.1:13306`）+ Redis 7（`127.0.0.1:16379`）；`go run . migrate up` 建库；`go run . serve` 监听 `:18000`。

**启动方式说明（如实披露）**：本地无七牛云凭据，`serve` 启动期的 `service.Upload().ValidateConfig` 会 fail-fast。为完成运行时验证，临时在本机把该调用降级为告警打印（`internal/cmd/cmd.go`，仅本地、**未提交**），验证后立即 `git checkout --` 还原；`git diff internal/cmd/cmd.go` 为空。该临时改动不影响任何被验证的接口语义。

| 验证项 | 请求 | 结果 |
| --- | --- | --- |
| 未认证拒绝 | `GET /admin/me` | 401 / 1002 |
| 未认证拒绝 | `GET /admin/users` | 401 / 1002 |
| 未认证拒绝 | `GET /admin/roles/1/permissions` | 401 / 1002 |
| 普通用户 token 不能访问管理端 | `GET /admin/users`（type=user token） | 403 / 1003 |
| 普通用户 token 不能访问管理端 | `GET /admin/me`（type=user token） | 403 / 1003 |
| 超管 `/admin/me` | `GET /admin/me` | 200 `{id:1,username:"admin",is_super:true,roles:[],permissions:[]}` |
| 用户列表分页 | `GET /admin/users?page=1&size=2` | 200，`items` id=4,3，`total=4`，字段仅 id/username/status/created_at |
| 用户列表第二页 | `GET /admin/users?page=2&size=2` | 200，`items` id=2,1（无重复/遗漏） |
| 用户列表默认分页 | `GET /admin/users` | 200，默认 page=1 size=20 |
| 分页越界 | `?page=0` | 400 / 1001 |
| 分页越界 | `?size=101` | 400 / 1001 |
| 分页类型非法 | `?page=abc` | 400 / 1001 |
| 用户不存在 | `GET /admin/users/999999` | 404 / 2015 |
| 用户详情 | `GET /admin/users/1` | 200 `{id,username,status,created_at}` |
| 既有路由未受影响 | `GET /admin/users/1/status` | 200 `{id:1,status:1}` |
| 角色不存在 | `GET /admin/roles/999999/permissions` | 404 / 2007 |
| 角色已授权限 | `GET /admin/roles/1/permissions` | 200 `items:[{id:48,code:"user:read",name:"查看用户状态"}]` |
| 角色无授权 | `GET /admin/roles/3/permissions` | 200 `items:[]` |
| 多角色合并去重 | 非超管 `r5admin` 同属 `r5-reader`+`r5-reader2`（均授 `user:read`）→ `GET /admin/me` | 200 `roles:["r5-reader","r5-reader2"]`，`permissions:["user:read"]`（去重） |
| 非超管有权限时放行 | 同上 `GET /admin/users?page=1&size=1` | 200 |
| 非超管缺少权限被拒绝 | 同上 `GET /admin/roles/1/permissions`（缺 `role:list`） | 403 / 1003 |
| 非超管缺少权限被拒绝 | 同上 `PUT /admin/users/1/status`（缺 `user:status`） | 403 / 1003 |
| RBAC 查询失败 fail-closed | 临时 `RENAME TABLE role_permissions` → `GET /admin/me` | 500 / 1000（**未**返回空或伪造权限集合） |
| RBAC 查询失败 fail-closed | 同上 → `GET /admin/roles/1/permissions` | 500 / 1000（**未**伪装为空列表） |
| 故障恢复 | 还原表名后重放两个请求 | 200，数据与故障前一致 |
| 用户列表不泄露密码哈希 | 检查全部响应体 | 仅 id/username/status/created_at，无 `password_hash`/`auth_epoch`/令牌 |
| 就绪探针 | `GET /ready` | 200 |

结论：R5-02 / R5-03 / R5-04 的鉴权、权限判定、分页边界、错误码、字段最小化、fail-closed 均已**运行时验证通过**。

## 12. 未验证内容（NOT_VERIFIED）

1. 未执行 `go test ./...`、`go test -p1 ./...`、`make test-unit`（Owner 明令禁止）。除上述定向契约测试外，应用层单元/集成测试本轮**未运行**，不作为验收依据。
2. 未在管理端前端 UI 中实测新接口的渲染效果（`admin/**` 属冻结范围，未改、未跑前端）。
3. 未验证真实七牛云、Kafka、Prometheus 相关链路（与本次改动无关；本地启动时七牛校验按第 11 节临时绕过）。
4. 未验证 DB 连接池耗尽以外的其它基础设施故障场景；fail-closed 只用"授权表不可用"一种故障模拟。
5. `scripts/contract-check.sh` 第 3 步的成功提示文本仍写"210 条常驻路由"（纯提示语，实际比对为真实路由表 213 条）。该脚本不在本任务文件所有权内，未修改，登记为已知文案漂移。

## 13. Git 分支和最终 SHA

- 开发分支：`feat/p2-r5-backend`（从 `ddfd5ab77ced617ae0b0020c46a8220396b52145` 创建）
- 实现提交 SHA：`261229cda75bdc7b974f8f02c58c34ba3f2f6088`
- 分支 tip：实现提交 + 本报告的元数据回填提交（`git rev-parse feat/p2-r5-backend` 复核；回填提交只改本文件的 SHA/推送/工作区三项占位，不含任何代码、契约或文档事实变更）

## 14. Push 状态

已普通 push（非 force）：`feat/p2-r5-backend` 推送到 `origin` 并建立跟踪；推送后 `git rev-parse HEAD` 与 `git rev-parse origin/feat/p2-r5-backend` 一致（实现提交 `261229cda75b…`）。

## 15. 工作区状态

实现提交后 `git status --porcelain` 为空（clean）；本次验证用的临时本地改动（`internal/cmd/cmd.go` 七牛启动校验降级）已 `git checkout --` 还原，未进入任何提交。

## 16. 本次明确没有实现的缺口

| 缺口 | 现状证据 | 本轮未实现的原因 | 后续建议 |
| --- | --- | --- | --- |
| R5-05 管理员列表 `GET /admin/admins` | `admin/src/views/system/admins/index.vue:27-35` 明确"没有管理员列表与详情接口，因此本页不展示列表"，全部操作以目标管理员 ID 定位；`docs/design/rbac.md:92` 同口径"无 /admin/admins 列表" | 现有页面无列表区域（无表格/分页/搜索），不满足"被缺失接口直接阻断"的判定；补齐需新增 `admin:list` 权限码并扩大管理员账号可枚举面，属需 Owner 决策的安全边界变更 | 新增 `admin:list`（仅授给明确需要的角色）→ `GET /admin/admins`（分页+关键词+状态筛选+`roles[]`，不返回 `password_hash`/令牌）→ 同步 seed、路由、OpenAPI、`docs/design/rbac.md` |
| R5-06 秒杀活动列表/详情 | `admin/src/views/marketing/flash-sales/index.vue:25-33` 明确"页面不提供活动列表"，只展示本次创建/更新返回的快照，刷新后无法回读 | 触发条件为"管理端已有实际列表/详情页面依赖"，当前不存在该页面；补齐需新增 `flash_sale:list` 并改造前端页面 | 现有 `Activity` DTO（`id/name/status/start_time/end_time/skus[].{sku_id,flash_price,total_stock,sold}`）已覆盖前端 adapter 全部字段，只需补 2 条 GET + `flash_sale:list` |
| 用户列表关键词查询 / 状态筛选 | `admin/src/views/user/index.vue` 无搜索框、无状态筛选控件 | 任务条件为"如果现有页面需要"，页面无对应控件 | 页面新增筛选控件后再补 `keyword`（username 前缀/模糊）与 `status` 过滤，参数校验沿用现有 400 语义 |
| 用户删除、昵称/手机号/最后登录时间 | A5 报告第 9 节列为 NOT_IMPLEMENTED；页面以"后端未提供该字段"占位 | 属任务 §五 暂缓能力与 PII 外扩，且 `users` 表无"最后登录时间"字段（需改表） | 需 Owner 立项（可能要加字段/迁移） |
| 其余暂缓项 | 任务 §五 列出的 14 项（公开优惠券模板、订单可用券、券模板编辑删除、文章管理端 CRUD、商品删除、SKU 独立列表、库存批量/聚合、订单分页筛选、真实支付/退款通道、部分退款、秒杀架构重构、会员/分销/拼团/砍价扩展） | 明确不在本次范围 | 不在本轮登记实现 |

## 17. 结束状态

已确认范围内（R5-02/R5-03/R5-04）实现完成，`go build`、`go vet`、契约门禁与针对性运行时验证通过，报告生成，提交并普通 push。等待阶段二主 Chat 审查与集成。
