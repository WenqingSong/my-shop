# PHASE2 R5-01 范围冻结：管理端 API 能力补齐

- 任务：R5（Go 后端管理端 API 能力补齐）
- 集成分支基线：`ddfd5ab77ced617ae0b0020c46a8220396b52145`（`feat/phase2-ecommerce-business`）
- 开发分支 / Worktree：`feat/p2-r5-backend` / `/root/projects/my-shop-worktrees/r5-backend`
- 本文件是编码前的内部范围冻结记录，交付报告见 `docs/reports/PHASE2_R5_BACKEND.md`

## 1. 核查方法

只读审查（未修改任何前端文件）：

| 类别 | 实际核查对象 |
| --- | --- |
| 管理端 API Client | `admin/src/api/modules/**`（18 个 client）、`admin/src/api/types/**`（16 个类型文件）、`admin/src/api/adapters/**`（13 个转换器，0 处 HTTP 调用） |
| 管理端页面 | `admin/src/views/**`（82 个文件），重点：`views/user/index.vue`、`views/system/admins/index.vue`、`views/system/roles/**`、`views/marketing/flash-sales/index.vue` |
| 管理端路由 | `admin/src/router/**`（15 个文件，含 `modules/access.ts`、`modules/marketing.ts`、`guard.ts`） |
| 管理端状态 | `admin/src/store/modules/user.ts`、`admin/src/utils/permission.ts`、`admin/src/composables/useAccessRefresh.ts` |
| 审查报告 | `docs/reports/A5-user-admin-permissions.md`（管理端用户/管理员/权限）、`docs/reports/A1-admin-auth-dashboard.md`（管理端认证与 `/admin/me`）、`docs/reports/A4-marketing-content.md` |
| 后端事实源 | `internal/cmd/routes_admin.go`、`internal/cmd/routes_test.go`、`api/admin/v1/admin.go`、`api/iam/v1/iam.go`、`internal/logic/admin/admin.go`、`internal/logic/iam/**`、`internal/middleware/auth.go`、`internal/boot/seed.go`、`internal/migrations/sql/**` |

说明：仓库内不存在独立命名的「Agent 1 后端 API 审查报告」；旧版 `docs/phase2/B1-01_API_INVENTORY.md` 按任务要求不作事实来源，一律以当前源码为准（本文件所有结论均可由 `admin/src/**` 与 `internal/**` 直接核对）。

## 2. 判定规则

某项能力满足以下任一条件才纳入本轮实现：

1. 管理端已有代码**实际读取**该接口/字段，且缺失导致功能 fail-closed 或降级；
2. 管理端已有页面存在**被该接口直接阻断的 UI 区域**（页面声明"因为没有该接口，所以不显示/不提供"），且补齐后可被现有页面直接接线。

不满足上述条件、或需要新增敏感权限码并改造前端页面才能成立的，登记为缺口，不在本轮实现（任务 §五、§十二 约束）。

## 3. 范围冻结表

| 能力 | 现有前端页面 | 现有后端接口 | 本次是否实现 |
| --- | --- | --- | --- |
| R5-02 `/admin/me` 返回 `permissions[]` | 全局：`store/modules/user.ts`（登录后与路由守卫均调用 `GET /admin/me`） | `GET /admin/me` 仅返回 `id/username/is_super/roles` | **实现** |
| R5-03 角色已授权限查询 | `views/system/roles/components/RolePermissionDrawer.vue`（权限分配抽屉） | `GET /admin/roles`、`POST/DELETE /admin/roles/:id/permissions`；无 GET | **实现** |
| R5-04 用户列表 / 详情 | `views/user/index.vue`（路由 `/users`，`meta.permissions=[user:read]`） | 仅 `GET/PUT /admin/users/:id/status` | **实现** |
| R5-05 管理员列表 | `views/system/admins/index.vue`（按目标 ID 操作的表单页） | `POST /admin/admins`、`PUT /admin/admins/:id/status`、`DELETE /admin/admins/:id`、`POST/DELETE /admin/admins/:id/roles` | **不实现（登记缺口）** |
| R5-06 秒杀活动列表 / 详情 | `views/marketing/flash-sales/index.vue`（创建 / 按 ID 更新表单） | `POST /admin/flash-sales`、`PUT /admin/flash-sales/:id`、`POST .../requests/:id/repair`、`GET .../requests/:id/audits` | **不实现（登记缺口）** |

## 4. 逐项证据与结论

### 4.1 R5-02 `/admin/me` 增加 `permissions[]` —— 实现

- 前端已有真实调用：`admin/src/api/modules/admin-auth.ts:63-67`；调用点 `admin/src/store/modules/user.ts:131`（登录后）与 `:207`（`ensureAdminProfile()`，路由守卫每跳转复用）。
- 前端已写好读取分支：`admin/src/store/modules/user.ts:66-74` 把 `profile.permissions` 写入 pinia 并调用 `setPermissionSubject`；`admin/src/utils/permission.ts:143-149` 的 `resolvePermissions()` 语义为：`is_super → ["*"]`、`permissions !== null → 以后端为准`、否则 `[]`（fail-closed）。
- 现状后果：后端不下发该字段 → `permissions === null` → **所有非超管的菜单、路由守卫与 21 处 `hasPerms()` 按钮全部 fail-closed**，只能看到无 `meta.permissions` 的页面。
- 前端类型已标注"后端当前不返回该字段"：`admin/src/api/modules/admin-auth.ts:34-46`；A1 报告第 283-284 行明确"若新增 `permissions[]`，前端无需改动"。
- `GET /admin/me` 的认证语义为 `AdminAuth` + 从 `AdminPrincipal.AdminID` 取身份，天然无法通过前端传参查询他人权限。
- 结论：**实现**，且必须复用现有 RBAC 查询链路（`admins → admin_roles → role_permissions → permissions`），不新增第二套判定。

### 4.2 R5-03 `GET /admin/roles/:id/permissions` —— 实现

- 现有页面：`admin/src/views/system/roles/components/RolePermissionDrawer.vue`。
- 页面文件头注释第 25-26 行明确："**没有**「查询角色已有权限」接口，因此这里不显示勾选状态"；`:150` 的界面提示为"当前无法读取角色已拥有的权限"；`admin/src/api/modules/role.ts:12` 同声明。
- 该缺口直接阻断抽屉的"已授权"展示（前端因此取消了勾选态，只保留授予/移除两个幂等写操作），属"被接口直接阻断的 UI 区域"。
- 权限保护：沿用 `role:list`（查看角色），不新增权限码、不改动 `POST`/`DELETE` 授权语义。
- 结论：**实现**，返回结构与既有 `GET /admin/permissions` 的 `Permission` 结构一致（`{items:[{id,code,name,description}]}`），可直接复用前端 `toPermissionView`。

### 4.3 R5-04 用户列表 / 详情 —— 实现

- 现有页面：`admin/src/views/user/index.vue`（路由 `/users`，`admin/src/router/modules/access.ts:32-39`，`meta.permissions=[user:read]`）。
- 页面头注释 `:20-26` 明确"只有 `GET/PUT /admin/users/:id/status`；**没有**用户列表、用户详情、搜索、分页、删除接口"；`:139-145` 的 `el-alert` 与 `:231-235` 的空态文案（"输入用户 ID 查询账号状态（后端没有用户列表接口）"）是页面为缺失列表能力所留的唯一占位。
- 页面当前无法完成"发现用户 → 查看 → 禁用/启用"闭环（必须先知道用户 ID），属被缺失接口直接阻断的功能。
- 权限：复用现有 `user:read`（列表与详情）；写操作仍由 `user:status` 保护，语义不变。
- 字段最小化：只返回 `id/username/status/created_at`。查询语句显式 `Fields(...)` 白名单，**不查询** `password_hash`、`auth_epoch`、`nickname`、`phone`、`birthday`；`nickname/phone/birthday` 属资料类 PII，页面当前无对应控件，本轮不外扩。
- 分页遵循项目统一约定（`page` 默认 1、`size` 默认 20 上限 100，越界/非法 400，与 `GET /admin/orders` 一致）；排序固定 `id DESC`（唯一键，翻页稳定）。
- 关键词查询与状态筛选：页面无搜索框、无状态筛选控件，按任务条件"如果现有页面需要"判定为**不需要**，本轮不实现，登记为可扩展项。

### 4.4 R5-05 管理员列表 —— 不实现

- 现有页面 `admin/src/views/system/admins/index.vue` 明确写出设计取舍：`:27-35` "**没有**管理员列表与详情接口，因此本页**不展示列表**，也不伪造管理员数据；所有针对已有管理员的操作用「目标管理员 ID」定位"；`:243` el-alert "输入管理员 ID 执行管理操作，暂不提供管理员列表及详情查询"。
- 该页没有列表区域（无表格、无分页、无搜索控件），写操作全部以目标 ID 为入参，列表对完成创建/禁用/删除/分配角色并非必需 → 不满足 §2 的"被阻断 UI"条件（该页把"不展示列表"作为既有设计，而非待接线的空态）。
- 设计文档同口径：`docs/design/rbac.md:92` "无 `/admin/admins` 列表（`/admin/me` 覆盖自身信息）"。
- 成本/风险差异：补齐需新增 `admin:list` 权限码（改动权限清单、Seed、OpenAPI、权限文档），并扩大管理员账号的可枚举面；任务要求"不得为了方便而让所有管理员都能查询管理员账户"，属需要 Owner 明确决策的安全边界变更。
- 结论：**本轮不实现**。若后续确认需要，建议方案：新增 `admin:list`（仅授给明确需要的角色，超管天然具备）→ `GET /admin/admins`（分页 + 关键词 + 状态筛选 + `roles[]`，不返回 `password_hash`/令牌）→ 同步 `seed.go`、`routes_admin.go`、`routes_test.go`、OpenAPI 与 `docs/design/rbac.md`。

### 4.5 R5-06 秒杀活动列表 / 详情 —— 不实现

- 现有页面 `admin/src/views/marketing/flash-sales/index.vue` 明确"页面不提供活动列表"（`:25-33`），只展示本次创建/更新接口返回的活动快照（`:167-168`、`:262`、`:398-400` 均说明刷新后无法回读历史活动）。
- 任务触发条件为"仅当 R5-01 发现管理端**已有实际秒杀活动列表或详情页面依赖**"——当前不存在列表/详情页面，管理端唯一秒杀"读"接口是工作台聚合 `GET /admin/dashboard/flash-sales`（`admin/src/views/dashboard/components/FlashSalePanel.vue`），本轮不改。
- 若补齐，需新增 `flash_sale:list` 权限码并改造前端页面（新增列表/详情路由与视图），超出"后端补齐 + 前端冻结"的本轮边界。
- 结论：**本轮不实现**，登记缺口。已核实的低成本路径：现有 `api/flashsale/v1` 的 `Activity`（`id/name/status/start_time/end_time/skus[].{sku_id,flash_price,total_stock,sold}`）已覆盖前端 `flash-sale-adapter.ts:80-104` 的 `toActivityView` 全部字段，后续只需 `GET /admin/flash-sales` + `GET /admin/flash-sales/:id` 与 `flash_sale:list`。

## 5. 冻结结论

本轮实现 3 个新接口 + 1 个既有响应扩展：

| # | 变更 | 路由数变化 |
| --- | --- | --- |
| R5-02 | `GET /admin/me` 响应增加 `permissions[]`（不新增路由） | 0 |
| R5-03 | 新增 `GET /admin/roles/:id/permissions`（`role:list`） | +1 |
| R5-04 | 新增 `GET /admin/users`、`GET /admin/users/:id`（`user:read`） | +2 |

常驻 JSON API 路由：210 → 213（后台 110 → 113，前台 98，健康检查 2）。

不做：R5-05、R5-06、以及任务 §五列出的 14 项暂缓能力。新增数据库表 / 修改历史 Migration / 修改前端代码：无。
