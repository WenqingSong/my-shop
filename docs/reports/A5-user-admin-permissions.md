# A5 交付报告：用户、权限与最终交付

- 项目：SurgeCart
- 仓库：`/root/projects/my-shop`
- 分支：`feat/phase2-admin-frontend`
- 范围：仅 Vue 管理端（`admin/**`），**未修改 Go 后端**
- 状态图例：`IMPLEMENTED`（已实现）/ `VERIFIED`（已验证）/ `PARTIALLY_VERIFIED`（部分验证）/ `NOT_VERIFIED`（未验证）/ `NOT_IMPLEMENTED`（后端不支持，未实现）

---

## 1. 任务目标

完成管理端最后一个阶段：

1. 用户管理（后端真实能力范围内）；
2. 管理员管理；
3. 角色管理；
4. 权限管理；
5. 完善前端权限控制（重点是 `/admin/me` 无 `permissions[]` 的遗留问题）；
6. A0–A4 完整回归；
7. 管理端最终工程验收与交付。

不新增 A6，不扩展新业务模块，不做无限重构。

---

## 2. 用户管理实现情况

### 2.1 后端真实能力（复核结论）

| 能力 | 是否存在 | 依据 |
| --- | --- | --- |
| 后台用户列表 | 否 | `routes_admin.go` 无 `/admin/users` 列表路由 |
| 用户详情 | 否 | 只有 `/admin/users/:id/status` |
| 用户搜索 / 分页 / 排序 | 否 | 无列表接口 |
| 用户状态查询 | 是 | `GET /admin/users/:id/status`（`user:read`） |
| 用户禁用/启用 | 是 | `PUT /admin/users/:id/status`（`user:status`，`reason` 必填） |
| 用户删除 | 否 | 无路由 |
| 手机号脱敏 | 不适用 | `users` 表无手机号字段 |
| 用户状态与认证关联 | 有 | 禁用即 `auth_epoch+1`、撤销 refresh family，且每请求校验 `status` |

### 2.2 前端交付

| 项 | 状态 |
| --- | --- |
| `/users` 用户状态管理页 | IMPLEMENTED / VERIFIED（MSW） |
| 按 ID 查询用户状态 | IMPLEMENTED / VERIFIED |
| 禁用 / 启用 + 必填原因 + 二次确认 | IMPLEMENTED / VERIFIED |
| 状态变更成功后重新获取状态 | IMPLEMENTED / VERIFIED |
| Loading / 空态 / 错误重试 / 防重复提交 | IMPLEMENTED / VERIFIED |
| 不展示密码哈希等认证数据 | IMPLEMENTED / VERIFIED |
| 用户列表 / 详情 / 搜索 / 分页 / 删除 | NOT_IMPLEMENTED（后端无接口） |
| 昵称 / 手机号 / 最后登录时间 / 关联订单 | NOT_IMPLEMENTED（表无字段） |

页面明确提示后端能力边界，不使用假列表。

---

## 3. 管理员管理实现情况

### 3.1 后端真实能力（复核结论）

| 能力 | 是否存在 | 权限码 |
| --- | --- | --- |
| 管理员列表 | **否** | — |
| 管理员详情 | **否** | — |
| 当前角色查询 | **否** | — |
| 创建管理员 | 是 | `admin:create` |
| 禁用/启用 | 是 | `admin:disable` |
| 删除 | 是 | `admin:delete` |
| 分配/移除角色 | 是 | `admin:assign_role` |
| 修改资料/密码 | 否 | — |

保护规则（后端强制）：

- 目标为超级管理员 → `2005`（保护**所有**超管，比"最后一个超管"更严格）；
- 目标为调用者自身 → `2006`；
- 目标不存在 → `2003`；用户名重复 → `2004`。

### 3.2 前端交付

| 项 | 状态 |
| --- | --- |
| `/system/admins` 管理员管理页 | IMPLEMENTED / VERIFIED（MSW） |
| 创建管理员（用户名 3~24 字母数字 / 密码 8~24） | IMPLEMENTED / VERIFIED |
| 密码输入 `type=password`、不持久化、不写日志、成功后清空 | IMPLEMENTED / VERIFIED |
| 禁用/启用（二次确认，展示后端错误） | IMPLEMENTED / VERIFIED |
| 删除（高危二次确认） | IMPLEMENTED / VERIFIED |
| 角色分配/移除（角色来自 `GET /admin/roles`） | IMPLEMENTED / VERIFIED |
| 角色变更后刷新当前管理员权限 | IMPLEMENTED / VERIFIED |
| 管理员列表 / 详情 / 当前角色回显 / 资料修改 | NOT_IMPLEMENTED（后端无接口） |

页面按"目标管理员 ID"操作，不拼凑假列表、不使用永久 Mock 伪装数据。
没有 `role:list` 时明确提示无法加载角色，不提供手填 `role_id` 的伪能力。

---

## 4. 角色管理实现情况

| 能力 | 后端 | 前端状态 |
| --- | --- | --- |
| 角色列表 | `GET /admin/roles`（`role:list`） | IMPLEMENTED / VERIFIED |
| 创建角色 | `POST /admin/roles`（`role:create`） | IMPLEMENTED / VERIFIED |
| 修改名称/描述 | `PUT /admin/roles/:id`（`role:update`） | IMPLEMENTED / VERIFIED |
| 删除角色 | `DELETE /admin/roles/:id`（`role:delete`） | IMPLEMENTED / VERIFIED |
| 授予/移除权限 | `POST/DELETE /admin/roles/:id/permissions`（`role:assign_permission`） | IMPLEMENTED / VERIFIED（MSW） |
| 角色已有权限查询 | **无接口** | NOT_IMPLEMENTED；界面不显示勾选状态 |
| 角色状态启停 | **表无 status** | NOT_IMPLEMENTED |
| 权限数量列 | **无读接口** | NOT_IMPLEMENTED（不伪造） |
| 系统内置角色保护 | **无该概念** | 不适用 |

权限分配抽屉按后端真实权限码的业务域前缀分组展示；授予/移除均为幂等写操作，提交失败保留原列表并展示后端错误。

---

## 5. 权限管理实现情况

| 能力 | 后端 | 前端状态 |
| --- | --- | --- |
| 权限定义列表 | `GET /admin/permissions`（`permission:list`） | IMPLEMENTED / VERIFIED |
| 创建权限 | `POST /admin/permissions`（`permission:create`） | IMPLEMENTED / VERIFIED |
| 修改权限 | `PUT /admin/permissions/:id`（`permission:update`） | IMPLEMENTED / VERIFIED |
| 删除权限 | `DELETE /admin/permissions/:id`（`permission:delete`） | IMPLEMENTED / VERIFIED |
| 权限详情 / 权限树 / 菜单数据 | **无接口** | NOT_IMPLEMENTED |
| 业务域字段 | **无字段** | 由真实 `code` 前缀推导展示（不生成权限码） |
| 分页/搜索参数 | **无参数** | 前端在完整权限定义上筛选与分页 |

页面明确说明：该列表是**系统全部权限定义**，不是当前管理员的有效权限，也不是某个角色已有的权限。

---

## 6. 权限体系最终实现方式

### 6.1 权限来源优先级（按真实后端核对）

1. `/admin/me` 返回 `permissions[]`：**后端不存在**（无该字段）；
2. 当前管理员有效权限查询接口：**不存在**；
3. 从角色关联数据可靠推导：**不存在**（无角色-权限读接口）；
4. 以上都不具备 → **fail-closed**：`isSuper=false` 且 `permissions=null` 时有效权限为空。

前端实现（`admin/src/utils/permission.ts`）：

```
isSuper              → ["*"]（仅界面展示，最终授权在后端）
permissions !== null → 以后端下发为准（为将来契约预留）
否则                  → []（fail-closed）
```

明确禁止并已移除：角色名 → 权限码的本地映射（A1 遗留的 `ROLE_PERMISSION_MAP` 已删除）。

### 6.2 菜单、路由与按钮

- 路由新增 `meta.permissions` / `meta.permissionsMode`；
- 菜单：`filterNoPermissionTree` 使用 `hasMetaPermission`，父目录随子项自动消失；
- 守卫：认证通过后重建菜单（权限签名变化才重建），再逐级校验 `matched[].meta.permissions`；
- 无权限：跳第一个可访问菜单，完全无可用页面才跳 `/access-denied`；
- 按钮：统一 `hasPerms(PERMISSION.X)`，权限码集中在 `utils/permission.ts`；
- 页面级权限码只使用后端真实码；读接口无权限码的模块不编造读权限。

### 6.3 权限变更后刷新

`refreshAccess()`：强制重取 `/admin/me` → 重算菜单 → 复查当前页面。
触发点：角色权限授予/移除、角色删除、权限删除、管理员角色分配/移除。

### 6.4 安全语义

- 前端权限判断**不是安全边界**，后端 `RequirePermission` 才是；
- 超级管理员绕过只在界面层，后端按 `is_super` 独立判定；
- 非超管无法通过修改浏览器状态获得真实后端权限（权限快照仅内存、每次由 `/admin/me` 重建）；
- 401 清理登录态；403 **不**清理登录态。

| 项 | 状态 |
| --- | --- |
| 超管全量权限（界面） | IMPLEMENTED / VERIFIED |
| 非超管 fail-closed | IMPLEMENTED / VERIFIED（单元测试） |
| 菜单/路由/按钮三层控制 | IMPLEMENTED / VERIFIED（单元测试） |
| 后端下发 `permissions[]` 的兼容路径 | IMPLEMENTED / NOT_VERIFIED（后端未下发） |

---

## 7. API 接入情况

新增 API 模块（沿用现有 `http` 实例与 envelope 解析，路径均为真实后端路由，页面不拼接路径）：

| 模块 | 端点 |
| --- | --- |
| `api/modules/user.ts` | `GET/PUT /admin/users/:id/status` |
| `api/modules/admin.ts` | `POST /admin/admins`、`PUT /admin/admins/:id/status`、`DELETE /admin/admins/:id`、`POST /admin/admins/:id/roles`、`DELETE /admin/admins/:id/roles/:role_id` |
| `api/modules/role.ts` | `GET/POST /admin/roles`、`PUT/DELETE /admin/roles/:id`、`POST/DELETE /admin/roles/:id/permissions...` |
| `api/modules/permission.ts` | `GET/POST /admin/permissions`、`PUT/DELETE /admin/permissions/:id` |

- DTO：`api/types/{user,admin,role,permission}.ts`，字段依据 Go 源码手写；
- 转换与校验：`api/adapters/access-adapter.ts`；
- `contracts/openapi.yaml` 不存在，未创建虚假契约、未把临时类型放入 `generated/`；
- 未破坏 A1–A4 既有 API 模块。

---

## 8. 后端不支持的能力（集中清单）

**用户**

- 用户列表、详情、搜索、分页、排序、状态筛选
- 用户删除
- 昵称、手机号、最后登录时间、关联订单
- 密码修改/重置、短信、换绑、实名、资金、积分

**管理员**

- 管理员列表、详情、资料修改、密码重置
- 当前管理员角色查询（仅 `/admin/me` 的角色名）

**角色**

- 角色状态、角色详情、角色已有权限查询
- 系统内置角色概念与保护

**权限**

- 权限详情、权限树、菜单数据、业务域字段、分页/搜索参数

**认证/授权**

- `/admin/me` 的 `permissions[]`
- 当前管理员有效权限查询接口

以上能力全部标记 `NOT_IMPLEMENTED`，未用永久 Mock 伪装，也未创建 A6。

---

## 9. A0–A4 回归情况

| 阶段 | 回归项 | 结果 |
| --- | --- | --- |
| A0 | 工程配置、路由、Axios、构建 | 通过 |
| A1 | 登录/退出/Token/Dashboard/权限守卫 | 通过（守卫测试扩展后仍全绿） |
| A2 | 商品、分类、SKU、库存、图片上传 | 通过 |
| A3 | 订单、发货、退款、审计 | 通过 |
| A4 | 优惠券、秒杀、轮播图、推荐位、文章 | 通过 |

检查结论：

- 路由：路由名与路径无重复，A5 路由不与既有模块冲突；
- 菜单：仍由静态路由生成，A5 页面进入正确分组（用户管理 / 系统管理）；
- 401/403：401 清理登录态并跳登录；403 仅展示权限提示，行为未被改动；
- 权限码：全部来自后端 seed 与 `RequirePermission` 实际参数；
- API：新增模块统一走 `utils/http`，无页面拼接路径；无重复请求（`/admin/me` 有进行中请求合并）；
- 金额：未改金额转换逻辑（A5 无金额字段）；
- 上传：未改上传实现；
- 遗留演示页 / 无用 Mock：未发现新增演示页；A5 Mock 只覆盖真实存在的端点，不提供假列表路由；
- 默认放行：`hasPermission` 在无 `permissions` 且非超管时返回 false，未发现默认放行。

---

## 10. 测试结果

执行环境：`admin/`，Node.js v22.23.3。

| 命令 | 结果 |
| --- | --- |
| `pnpm typecheck` | 通过 |
| `pnpm lint:eslint` | 通过（0 error / 0 warning） |
| `pnpm test` | **22 文件 / 341 用例全部通过** |
| `pnpm build` | 通过（3.34 MB） |
| `git diff --check` | 通过 |

A5 新增测试：`user-access`（12）、`admin-management`（17）、`role-permission`（17）。
扩展测试：`permission`（fail-closed / meta / 签名 / A5 权限码）、`auth-guard`（页面级权限、兜底跳转、some 模式）、`router`（A5 路由与真实权限码）。

覆盖的关键场景：

- 用户：查询、状态转换、幂等、reason 必填/超长、404、403、空态/失败文案；
- 管理员：创建、重复用户名、非法输入、禁用/启用、删除、角色分配/移除、超管保护、403、重复提交锁、ID 解析；
- 角色/权限：列表、创建/更新/删除、重名冲突、幂等授予/移除、业务域推导、分组/筛选、表单校验；
- 权限体系：超管、非超管 fail-closed、未知角色、`permissions[]` 优先、菜单可见性、页面访问控制、无权限兜底。

---

## 11. 真实后端联调状态

| 项 | 状态 |
| --- | --- |
| 真实 Go 后端联调 | **NOT_VERIFIED**（未启动后端，全部 MSW） |
| 真实登录/会话/权限码端到端 | **NOT_VERIFIED** |
| 1366 / 1920 浏览器 UI 验收 | **NOT_VERIFIED**（无图形浏览器环境） |
| MSW 下的前端逻辑 | VERIFIED |
| 生产构建 | VERIFIED |
| 单元测试 / 类型检查 / ESLint | VERIFIED |

---

## 12. 已知限制

1. **非超管默认不可用受权限码保护的页面**：后端未下发 `permissions[]`，且无角色-权限读接口，前端只能 fail-closed。新建管理员在获得权限后，前端仍无法得知其有效权限；这是契约缺口，不是前端遗漏。
2. **用户与管理员无列表**：只能按 ID 查询/操作，无法浏览、搜索或分页。
3. **角色权限无法回显**：抽屉只能"授予/移除"，不能展示当前已授权集合。
4. **管理员角色分配依赖 `role:list`**：没有该权限时无法选择角色。
5. **管理员操作后无法回读状态**：后端无详情接口，成功提示不代表页面可展示最新状态。
6. **超管保护强于"最后一个超管"**：任何超管都不可被禁用/删除/改角色，无法在界面上转移超管身份。
7. **用户资料字段缺失**：无手机号/昵称/最后登录时间，无法实现脱敏展示或资料管理。
8. `pnpm lint:stylelint` 无法运行（缺少 `stylelint-config-standard`，A0 起的既有工具链问题），不属于本次验收命令。

---

## 13. 主要修改文件

新增：

- `admin/src/api/types/{user,admin,role,permission}.ts`
- `admin/src/api/modules/{user,admin,role,permission}.ts`
- `admin/src/api/adapters/access-adapter.ts`
- `admin/src/composables/useAccessRefresh.ts`
- `admin/src/mocks/handlers/access.ts`
- `admin/src/router/modules/access.ts`
- `admin/src/views/user/index.vue`
- `admin/src/views/system/admins/index.vue`
- `admin/src/views/system/roles/index.vue`、`components/{RoleFormDialog,RolePermissionDrawer}.vue`
- `admin/src/views/system/permissions/index.vue`、`components/PermissionFormDialog.vue`
- `admin/tests/unit/{user-access,admin-management,role-permission}.test.ts`

修改：

- `admin/src/utils/permission.ts`（A5 权限码、fail-closed、meta 权限、签名）
- `admin/src/router/{guard,index,utils,enums}.ts`
- `admin/src/router/modules/{home,order,refund,marketing}.ts`
- `admin/types/router.d.ts`
- `admin/src/store/{types,modules/user}.ts`
- `admin/src/mocks/handlers/{index,mock-db}.ts`
- `admin/locales/{zh-CN,en}.yaml`
- `admin/tests/unit/{permission,auth-guard,router}.test.ts`

未修改：`internal/**`、`api/**`、`migrations/**`、`contracts/**`、`web/**`、Dockerfile、docker-compose、`deploy/**`、`nginx/**`、Kubernetes / Prometheus 配置。

---

## 14. Git Commit

- 功能提交 SHA：`a9e3f96928a4f1201a9859f6905884e39411211a`
  （`feat(admin): complete user and access management`，42 files changed）
- 文档回填提交：本条提交（`docs(admin): 记录 A5 提交与推送结果`）
- 目标分支：`feat/phase2-admin-frontend`
- 远端：`origin/feat/phase2-admin-frontend`
- 提交前远端状态：`origin/feat/phase2-admin-frontend...HEAD = 0 0`
- 推送方式：普通 `git push`（未 force push、未改写历史）
- 开发知识文档：`.agent/tasks/dev/A5-user-admin-permissions.md`
- 交付报告：`docs/reports/A5-user-admin-permissions.md`

---

## 15. 最终交付结论

- 管理端 A0–A5 开发**完成**，不再新增 A6。
- 真实可用（依赖后端已有能力）：管理员登录/退出、工作台、商品/分类/SKU/库存、订单/退款、优惠券/秒杀/轮播图/推荐位/文章只读、用户状态管理、管理员创建/禁用/删除/角色分配、角色与权限的增删改查。
- 仅通过 MSW 验证（真实后端未联调）：A5 全部写操作、A2–A4 既有接口。
- 需要后端补齐后才能完整使用：用户/管理员列表与详情、`/admin/me` 的 `permissions[]`（或有效权限查询）、角色已有权限查询、权限树/菜单、用户资料字段。
- 尚未经过真实环境验证：真实后端端到端、真实浏览器 UI、真实 1366/1920 布局。
- 前端守卫与按钮隐藏只影响界面；真正的授权边界始终是 Go 后端 `AdminAuth + RequirePermission`。
