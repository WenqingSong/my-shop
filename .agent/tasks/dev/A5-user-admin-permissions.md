# A5 开发知识文档：用户、权限与最终交付

> 适用仓库：`/root/projects/my-shop`
> 目标分支：`feat/phase2-admin-frontend`
> 范围：仅 `admin/**`（Vue 3 管理端），未修改任何 Go 后端文件。
> 本文记录**最终实现与真实证据**；后端缺口与未验证项在文末集中列出。

---

## 1. 用户管理（A5-1）

### 1.1 后端真实能力（只读审查结论）

审查文件：

- `internal/cmd/routes_admin.go`
- `api/iam/v1/iam.go`
- `internal/logic/iam/user_status.go`
- `internal/migrations/sql/20261001000001_baseline.up.sql`
- `internal/migrations/sql/20261001000019_user_status.up.sql`

真实路由只有两条：

| 方法 | 路径 | 权限码 | 说明 |
| --- | --- | --- | --- |
| GET | `/admin/users/:id/status` | `user:read` | 返回 `{id, status}` |
| PUT | `/admin/users/:id/status` | `user:status` | body `{status, reason}`，`reason` 必填、trim 后 ≤255 |

确实**不存在**：

- 用户列表 / 用户详情 / 用户搜索 / 用户分页；
- 用户删除；
- 用户密码修改 / 重置、短信、手机号换绑、实名、资金、积分。

`users` 表字段只有：`id / username / password_hash / status / auth_epoch / created_at / updated_at`。
**没有昵称、手机号、最后登录时间**，因此前端没有可展示的脱敏手机号，也不存在"手机号是否脱敏"的实现问题。

### 1.2 前端实现

- 页面：`admin/src/views/user/index.vue`（路由 `/users`，菜单「用户管理 → 用户状态管理」）。
- API：`admin/src/api/modules/user.ts`。
- DTO：`admin/src/api/types/user.ts`。
- 视图/校验：`admin/src/api/adapters/access-adapter.ts`。

页面形态：**按用户 ID 查询状态 → 禁用/启用**。

- 初始为 `el-empty` 空态，不发任何"列表"请求；
- ID 用 `parsePositiveId` 校验（正整数）；
- Loading、错误 + 重试（记录最近一次尝试的 ID）、空态齐备；
- 禁用/启用走 `ElMessageBox.prompt`：既做二次确认，又收集后端必填的 `reason`；
- 成功后**重新 GET 状态**（需求要求"成功后重新获取用户详情"）；
- 使用 `useSubmitGuard` 防止重复提交；
- 无 `user:status` 权限时只展示状态，不渲染操作按钮。

安全边界：

- 不请求、不展示 `password_hash`、`auth_epoch` 等认证数据；
- 前端不复制"禁用即撤销会话"的逻辑，完全依赖后端：禁用会 `auth_epoch + 1`、撤销 refresh family、best-effort 撤销 Redis 会话，且每请求校验 `users.status`。

### 1.3 未实现（后端缺失，NOT_IMPLEMENTED）

用户列表、详情、搜索、分页、排序、状态筛选、删除、手机号、昵称、最后登录时间、关联订单。
页面内用 `el-alert` 明确告知，不使用假数据。

---

## 2. 管理员身份域与普通用户身份域

两者是**完全独立的认证域**：

| 维度 | 普通用户（前台） | 管理员（后台） |
| --- | --- | --- |
| Token type | `user` | `admin` |
| 登录入口 | `/login`（前台） | `/admin/login` |
| 数据表 | `users` | `admins` |
| 会话 | Redis 用户会话 + `auth_epoch` | Redis 管理员会话（sid） |
| 中间件 | `middleware.Auth` | `middleware.AdminAuth` |
| 互串结果 | 用户 token 进后台 → `parseAdminPrincipal` 判定 type ≠ admin → **403** | 管理员 token 调前台受保护接口 → type ≠ user → **403** |

前端对应约束：

- 管理端只保存 `admin` token（`admin/src/utils/auth.ts`）；
- `GET /admin/me` 是唯一身份事实来源，不解析 JWT payload；
- 不保存密码、不保存 refresh token。

---

## 3. 管理员创建、禁用、角色分配调用链

### 3.1 创建

```
admin/views/system/admins/index.vue submitCreate()
  → buildCreateAdminPayload()（trim 用户名）
  → api/modules/admin.ts createAdmin()
  → POST /admin/admins            （RequirePermission("admin:create")）
  → internal/controller/admin.CreateAdmin
  → internal/logic/admin.CreateAdmin
      validateUsername: ^[a-zA-Z0-9]{3,24}$
      validatePassword: 8~24
      bcrypt 哈希 → INSERT admins(is_super=0, status=1)
      唯一键冲突 1062 → 2004
```

### 3.2 禁用/启用

```
handleStatus() → ElMessageBox.confirm
  → updateAdminStatus(id, {status})
  → PUT /admin/admins/:id/status   （admin:disable）
  → logic.UpdateAdminStatus
      validateStatus(0/1)
      checkTargetAdmin: 不存在 2003 / 超管 2005 / 自身 2006
      UPDATE admins.status
```

后端每次请求都在 `AdminAuth` 里查 `admins.status`，因此禁用**即时生效**（下一次请求即 401），不依赖前端。

### 3.3 角色分配 / 移除

```
handleAssignRole("assign"|"remove")
  → assignAdminRole / removeAdminRole
  → POST /admin/admins/:id/roles            （admin:assign_role）
  → DELETE /admin/admins/:id/roles/:role_id （admin:assign_role）
  → logic.AssignAdminRole / RemoveAdminRole
      checkTargetAdmin（同上）
      POST: 角色不存在 2007；重复分配幂等返回成功
      DELETE: 幂等
  → refreshAccess()（重取 /admin/me、重算菜单、复查当前页面）
```

### 3.4 页面为什么"按 ID 操作"

后端**没有管理员列表与详情接口**，前端不能拼凑假列表，因此：

- 页面不渲染管理员表格；
- 创建表单 + 目标管理员 ID 输入框 + 操作按钮；
- 角色下拉来自 `GET /admin/roles`（需 `role:list`）；
- 没有 `role:list` 时明确提示无法选择角色（不提供手填 role_id 的伪能力）。

---

## 4. RBAC 数据模型

```
admins 1 ──< admin_roles >── 1 roles 1 ──< role_permissions >── 1 permissions
```

| 表 | 关键列 | 说明 |
| --- | --- | --- |
| `admins` | id, username, password_hash, status, is_super | 管理员与超管标记 |
| `roles` | id, name, description | **无 status 字段** |
| `permissions` | id, code, name, description | code 唯一，「资源:动作」 |
| `admin_roles` | admin_id, role_id | 复合主键 |
| `role_permissions` | role_id, permission_id | 复合主键 |

后端授权判定（`internal/middleware/auth.go`）：

1. `IsSuper` → 直接放行；
2. 否则 `permissions p ⋈ role_permissions rp ⋈ admin_roles ar`，按 `p.code` 计数；
3. DB 查询失败 **fail-closed**（500），不进 handler。

删除语义（后端事务）：删角色级联 `role_permissions` 与 `admin_roles`；删权限级联 `role_permissions`；删管理员级联 `admin_roles`。

---

## 5. 角色与权限码的关系

- 权限码唯一定义在 `internal/boot/seed.go` 的 `seedPermissionList`（49 条，启动幂等写入）；
- 路由通过 `RequirePermission(code)` 引用这些码；
- 角色是权限的**集合容器**；
- 角色名**不是**权限集合：后端 `/admin/me` 只返回 `roles: string[]`，没有任何接口返回"角色 → 权限码"。

A5 前端没有 `ROLE_PERMISSION_MAP`，也不把 `super_admin` 之类的角色名映射成权限。
权限码常量集中在 `admin/src/utils/permission.ts` 的 `PERMISSION`，页面与路由都引用它，不散落字符串。

---

## 6. 当前管理员有效权限的获取方式（A5 核心结论）

按需求给出的优先级逐项核对真实后端：

| 优先级 | 能力 | 是否存在 |
| --- | --- | --- |
| 1 | `/admin/me` 返回 `permissions[]` | **不存在**（只有 `id/username/is_super/roles`） |
| 2 | 当前管理员有效权限查询接口 | **不存在** |
| 3 | 可由角色关联数据可靠推导 | **不存在**（无角色-权限读接口） |
| 4 | 以上都不具备 → fail-closed | **已采用** |

因此 `admin/src/utils/permission.ts` 的规则是：

```ts
resolvePermissions(subject):
  isSuper            → ["*"]
  permissions !== null → [...permissions]   // 未来后端下发即生效
  否则                → []                 // fail-closed
```

关键点：

- `roles` 只用于展示，不参与授权计算；
- `GET /admin/permissions` 是**系统全部权限定义**，绝不当作当前管理员权限；
- 非超级管理员在 `permissions[]` 落地前，只能访问"后端仅要求 AdminAuth"的页面（商品/分类/库存/轮播图/推荐位/秒杀读/文章只读），写操作按钮一律隐藏，后端仍会 403 兜底。

这与需求 7.1 的 fail-closed 要求一致；代价是"新建的非超管默认看不到需要权限码的页面"，已在交付报告列为后端能力缺口。

---

## 7. 前端菜单与按钮权限

### 7.1 权限码与 meta

- `admin/src/utils/permission.ts`：`PERMISSION`、`hasPermission`、`hasMetaPermission`、`getPermissionSignature`。
- `admin/types/router.d.ts`：新增 `meta.permissions?: string[]`、`meta.permissionsMode?: "every" | "some"`。

路由声明只使用后端真实码：

| 路由 | meta.permissions | 模式 |
| --- | --- | --- |
| `/dashboard` | `dashboard:view` | every |
| `/orders` | `order:list` | every |
| `/orders/:id` | `order:view` | every |
| `/refunds` | `refund:list` | every |
| `/refunds/:id` | `refund:view` | every |
| `/marketing/coupons` | `coupon:list` | every |
| `/users` | `user:read` | every |
| `/system/admins` | `admin:create/disable/delete/assign_role` | **some** |
| `/system/roles` | `role:list` | every |
| `/system/permissions` | `permission:list` | every |

商品、分类、SKU、库存、轮播图、推荐位、秒杀、文章：后端读接口没有权限码 → **不声明** `meta.permissions`，页面仅要求登录；不编造读码。

### 7.2 菜单过滤

`admin/src/router/utils.ts`：

```
handleWholeMenus(routes)
  → filterTree            // 去掉 showLink:false
  → filterNoPermissionTree // 逐节点 hasMetaPermission(meta)
  → filterChildrenTree    // 去掉空目录
```

父级目录不声明权限，子级被过滤后目录自动消失。

### 7.3 按钮权限

页面统一 `hasPerms(PERMISSION.X)`（`utils/auth.ts` → `utils/permission.ts`）：

- 用户：`user:status`
- 管理员：`admin:create/disable/delete/assign_role`
- 角色：`role:create/update/delete/assign_permission`，权限定义读取需 `permission:list`
- 权限：`permission:create/update/delete`

按钮隐藏只是体验层，后端 `RequirePermission` 才是边界。

---

## 8. 路由守卫的权限判断过程

`admin/src/router/index.ts` + `admin/src/router/guard.ts`：两阶段。

```
beforeEach(to):
  外部链接 → 打开后拦截
  resolveAuthGuard(to):                 # 认证
      登录页 + 有本地 token → 校验 /admin/me → /dashboard
      无 token/过期 → /login?redirect=...
      /admin/me 失败 → 清本地态 → /login
      成功 → allow
  syncMenusWithPermissions():           # 认证通过后重建菜单（签名变化才执行）
  resolvePermissionGuard(to):           # 授权
      登录页 / 403 页 → allow
      hasRoutePermission(to) → allow    # 逐级检查 matched[].meta.permissions
      否则 → 第一个可访问菜单；没有则 /access-denied
```

- `getPermissionSignature()` 用"有效权限集合排序后拼接"判断是否需要重建菜单，避免每次导航深拷贝路由树；
- 菜单在应用启动（未登录）时就会构建一次，登录后必须重建，否则侧边栏停留在匿名结果；
- 直接输入 URL 同样经过 `beforeEach`，因此无法通过 URL 绕过前端守卫；但前端守卫不是安全边界。

---

## 9. 超级管理员权限处理

- 后端 `is_super` 由 `AdminAuth` **每请求**从 `admins` 表读取，不信任 JWT；
- `RequirePermission` 对 `IsSuper` 直接放行；
- 前端 `isSuper` 仅用于界面：`hasPermission` 直接 true、`resolvePermissions` 返回 `["*"]`；
- 超管保护：`checkTargetAdmin` 对**任何** `is_super=1` 的目标返回 2005（比"最后一个超管"更强）；操作自身返回 2006；
- 前端不预判、不绕过这些规则，只展示后端错误。

---

## 10. 401 与 403 的区别

| 状态 | 含义 | 前端行为 |
| --- | --- | --- |
| 401 | 未认证 / token 过期 / 会话撤销 / 管理员被禁用 | 触发统一失效处理：清 token、重置 Pinia、清标签页、重置路由、跳 `/login` |
| 403 | 已认证但无权限（或 token type 不匹配） | **不清除登录态**，仅展示"没有权限执行该操作" |

实现位置：`admin/src/utils/http/index.ts`（只对 401 调用 `handleUnauthorized`）、`admin/src/main.ts`（注册失效处理）、`admin/src/utils/error-message.ts`。

---

## 11. 权限变更后的刷新机制

`admin/src/composables/useAccessRefresh.ts`：

```
refreshAccess():
  1. ensureAdminProfile({ force: true })   # 重取 /admin/me
  2. syncMenusWithPermissions()            # 权限签名变化则重建菜单
  3. resolvePermissionGuard(当前路由)       # 失去权限 → 跳转
```

触发点：

- 角色权限授予/移除（角色页抽屉）
- 角色删除
- 权限删除
- 管理员角色分配/移除

限制：`/admin/me` 无 `permissions[]`，因此非超管刷新后仍为 fail-closed；这是后端契约缺口，不是前端遗漏。

---

## 12. 关键技术决策与权衡

1. **不为缺失的列表接口造页面**
   用户/管理员列表在后端不存在，A5 用"按 ID 查询/操作 + 明确的能力边界提示"，而不是假表格或永久 Mock 数据。

2. **fail-closed 优先于"看起来能用"**
   角色名映射权限会让界面可用，但会误导授权语义并掩盖后端缺口；A5 选择 fail-closed，并在页面/文档中显式说明。

3. **菜单签名重建**
   用一个字符串签名判断是否需要 `handleWholeMenus([])`，避免每导航深拷贝；代价是必须在登出/重置时把签名置空（已处理）。

4. **无权限时跳"第一个可访问菜单"**
   直接跳 403 会让非超管进入全屏死路（403 页没有侧边栏）；跳第一个可访问菜单保留可操作空间，只有完全没有可访问页面时才落 403。

5. **角色权限不做"勾选回显"**
   没有读接口就不伪装已授权状态；只提供幂等的"授予/移除"，提交结果以成功或后端错误为准。

6. **权限码只来自后端**
   页面、路由、测试断言都用 `PERMISSION` 常量；没有为"页面好看"编造 `admin:list`、`user:list` 等码。

7. **管理员页用 some 模式**
   后端没有 `admin:list`，用"任一管理权限"作为页面入口，避免持有 `admin:delete` 的管理员看不到页面，也避免把写权限伪装成读权限。

---

## 13. 实际遇到的问题与排查过程

1. **构建失败但错误被掩盖**
   `pnpm build` 报 `ENOENT: scandir 'dist'`，没有真实错误信息。原因是 `admin/build/info.ts` 的 `closeBundle` 里 `getPackageSize` 在构建失败时异步 `throw err`，把真正的构建错误盖掉了。
   用 `git archive HEAD` 建基线副本构建 → 基线通过，确认是本次改动引入；再在临时副本里把 `readdir` 的错误改为打印，暴露真实原因：
   `locales/*.yaml` 出现重复 key `purePermission`（模板已有同名菜单键）→ 重命名为 `purePermissionAdmin` 后构建通过。
   未修改 `admin/build/**` 的生产逻辑（保持脚手架行为），只修正了重复 key。

2. **原有权限测试断言与新语义冲突**
   A1 的 `permission.test.ts` 断言"`super_admin` 角色映射到 `dashboard:view`"。A5 明确禁止角色名映射权限，因此重写该测试为 fail-closed 语义，并补充 `meta` 权限、签名、A5 权限码断言；未降低覆盖。

3. **路由 meta 类型扩展**
   `types/router.d.ts` 的 `CustomizeRouteMeta` 增加 `permissions/permissionsMode`；`satisfies` 校验下 `permissionsMode` 在联合类型中不可直接访问，测试里用显式断言读取。

4. **菜单在启动时先构建一次**
   发现 `initRouter()` 早于登录执行，若不重建，侧边栏会停留在匿名权限结果；因此新增签名 + 守卫内重建，并让 `resetRouter()` 清空签名。

---

## 14. 测试与验证结果（真实执行）

在 `admin/` 下执行：

| 命令 | 结果 |
| --- | --- |
| `pnpm typecheck` | 通过 |
| `pnpm lint:eslint` | 通过（0 error / 0 warning） |
| `pnpm test` | 22 文件 / 341 用例全部通过 |
| `pnpm build` | 通过（3.34 MB，产物含 A5 页面 chunk） |
| `git diff --check` | 通过 |

A5 新增测试文件：

- `tests/unit/user-access.test.ts`（12）
- `tests/unit/admin-management.test.ts`（17）
- `tests/unit/role-permission.test.ts`（17）

同时扩展：

- `tests/unit/permission.test.ts`：fail-closed、meta 权限、签名、A5 权限码
- `tests/unit/auth-guard.test.ts`：`hasRoutePermission` / `resolvePermissionGuard` / some 模式 / 无权限兜底
- `tests/unit/router.test.ts`：A5 路由与真实权限码声明

未验证：

- 真实 Go 后端联调（未启动后端）：**NOT_VERIFIED**
- 1366 / 1920 浏览器 UI 验收：**NOT_VERIFIED**（无图形浏览器环境）
- `pnpm lint:stylelint`：无法运行（`stylelint-config-standard` 未安装，A0 起的既有工具链问题），不属于本次验收命令

---

## 15. 后端不支持的能力（集中记录）

**用户域**

- 用户列表 / 详情 / 搜索 / 分页 / 排序 / 状态筛选
- 用户删除
- 昵称、手机号、注册时间以外的资料、最后登录时间
- 关联订单信息
- 密码修改/重置、短信、换绑、实名、资金、积分

**管理员域**

- 管理员列表、详情、资料修改
- 当前管理员角色查询（只有 `/admin/me` 的 `roles` 名称）
- "最后一个超级管理员"专门保护（后端保护所有超管，语义更强）
- 管理员密码重置

**角色域**

- 角色状态（表无 status）
- 角色详情
- 角色已有权限查询（只有写接口）
- 系统内置角色概念与保护

**权限域**

- 权限详情
- 权限树 / 菜单数据
- 业务域字段（前端由 code 前缀推导）
- 分页 / 搜索参数

**认证/授权域**

- `/admin/me` 的 `permissions[]`
- 当前管理员有效权限查询接口

---

## 16. 管理端最终架构与维护注意事项

### 16.1 目录

```
admin/src/
  api/
    modules/       # 按业务域封装的请求函数（A5: user/admin/role/permission）
    types/         # 与 Go DTO 对齐的类型（含 envelope）
    adapters/      # DTO → 视图、表单校验、请求体构造
    generated/     # OpenAPI 生成目录（当前仍为空）
  composables/     # useSubmitGuard、useAccessRefresh
  mocks/           # MSW handlers + 内存 mock-db（仅开发/测试）
  router/          # 静态路由模块、guard、菜单过滤
  store/modules/   # user（身份）、permission（菜单/缓存）
  utils/           # http、permission、auth、money、datetime、error-message
  views/           # 按业务分目录的页面
```

### 16.2 数据流

```
页面 → composable → api/modules → utils/http（envelope + 401）
                              → adapters（校验/转换）
store/user ← GET /admin/me
store/permission ← router/utils 过滤 constantMenus
router/guard ← store/user + utils/permission
```

### 16.3 维护注意事项

1. **新增后端权限码时**：先在 `utils/permission.ts` 增加常量，再在路由 `meta.permissions` 与按钮使用；不要写字符串字面量。
2. **新增页面时**：确认后端读接口是否真的要求权限码；只在有真实码时声明 `meta.permissions`，否则不要编造。
3. **不要恢复角色名 → 权限映射**：后端没有该契约，恢复即引入越权展示风险。
4. **后端一旦在 `/admin/me` 下发 `permissions[]`**：前端无需改页面，`resolvePermissions` 会自动以它为准；此时应移除页面上的"fail-closed"说明并补充测试。
5. **菜单重建**：任何"绕过守卫直接改权限快照"的做法都必须同时调用 `syncMenusWithPermissions()`，否则侧边栏与页面权限不一致。
6. **不要在前端持久化密码或权限集合**：权限快照只存在 Pinia 内存，刷新后由 `/admin/me` 重建。
7. **MSW 数据不是业务事实**：`src/mocks/handlers/access.ts` 与 `mock-db.ts` 仅用于演示/测试，生产构建不会启用（`VITE_MOCK_ENABLED`）。
8. **A5 之后不再新增管理端阶段**：剩余工作属于后端契约补齐，不应新建 A6。

---

## 附：本次修改文件清单（实现相关）

新增：

- `src/api/types/{user,admin,role,permission}.ts`
- `src/api/modules/{user,admin,role,permission}.ts`
- `src/api/adapters/access-adapter.ts`
- `src/composables/useAccessRefresh.ts`
- `src/mocks/handlers/access.ts`
- `src/router/modules/access.ts`
- `src/views/user/index.vue`
- `src/views/system/admins/index.vue`
- `src/views/system/roles/index.vue`
- `src/views/system/roles/components/{RoleFormDialog,RolePermissionDrawer}.vue`
- `src/views/system/permissions/index.vue`
- `src/views/system/permissions/components/PermissionFormDialog.vue`
- 三个 A5 测试文件

修改：

- `src/utils/permission.ts`（A5 权限码、fail-closed、meta、签名）
- `src/router/{guard,index,utils,enums}.ts`
- `src/router/modules/{home,order,refund,marketing}.ts`
- `types/router.d.ts`
- `src/store/{types.ts,modules/user.ts}`（注释与语义对齐）
- `src/mocks/handlers/{index.ts,mock-db.ts}`
- `locales/{zh-CN,en}.yaml`
- `tests/unit/{permission,auth-guard,router}.test.ts`
