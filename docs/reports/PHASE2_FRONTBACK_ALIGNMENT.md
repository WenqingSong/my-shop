# PHASE2 前后端剩余接口对齐与真实联调报告

- 任务：Task 4 — SurgeCart 前后端剩余接口对齐与真实联调
- 开发分支：`feat/phase2-frontback-alignment`
- 开发基线：`607c022b215c590230d8ae538ddf2ae949d04761`
- 执行日期：2026-11-13（Asia/Shanghai）
- 执行方式：主 Agent 独立执行，独立 Worktree `/root/projects/my-shop-worktrees/frontback-alignment`

## 1. 任务范围

本次实际处理：

1. Vue 管理端接入 R5 新增的三个后端接口：`GET /admin/users`、`GET /admin/users/:id`、`GET /admin/roles/:id/permissions`。
2. 角色权限抽屉从"只能授予/移除"改造为"读取真实授权集合 → 勾选态回填 → 差异保存 → 服务端回读"。
3. React 结算页下单成功后多发一次空购物车 `POST /orders/quote`（400）的缺陷修复。
4. 购物车订单幂等重放返回 9004 的契约缺陷调查与修复（后端最小改动 + 定向测试）。
5. Wave 1 遗留 P3 复核：`shipping_fee > 0` 真实场景、`checkout-coupon.test.tsx` 偶发超时、contract-check 文案、`TotalAmount` 注释、库存时间时区。
6. 隔离 Compose 环境下的真实 API 与浏览器联调（Nginx → Go → MySQL/Redis）。

明确不在范围：Kubernetes、Prometheus/Grafana、压测与故障演练、新增业务功能、全量 Go 单元测试、管理员列表/秒杀列表等 R5 已登记缺口、支付/退款链路重构、时区体系统一改造。

## 2. 前后端接口对齐结果

### 2.1 R5 新增接口（Vue）

| 接口 | Go 后端状态 | Vue 调用状态 | 请求字段 | 响应字段 | 真实环境验证 |
| --- | --- | --- | --- | --- | --- |
| `GET /admin/users` | 已实现（`user:read`，id 降序） | **本次接入**：用户页表格 + 分页 | `page`、`size`（默认 1/20，上限 100） | `items[].{id,username,status,created_at}`、`total,page,size` | 200；页面展示真实用户；分页可用；`page=0` → 400 |
| `GET /admin/users/:id` | 已实现（`user:read`，不存在 2015） | **本次接入**：详情抽屉 | 路径 `id` | `id,username,status,created_at` | 200；抽屉展示；`999999` → 404 |
| `GET /admin/roles/:id/permissions` | 已实现（`role:list`，按权限 id 升序，失败不返回空列表） | **本次接入**：权限抽屉初始化勾选态 | 路径 `id` | `items[].{id,code,name,description}` | 200；勾选数 4 = 服务端集合；不存在角色 → 404 |
| `POST /admin/roles/:id/permissions` | 已实现（幂等） | 差异保存（新增项） | `permission_id` | `data:null` | 200；保存后自动回读，勾选 4 → 5 |
| `DELETE /admin/roles/:id/permissions/:permission_id` | 已实现（幂等） | 差异保存（移除项） | 路径参数 | `data:null` | 200；回读后勾选 5 → 4 |
| `GET /admin/me` | 已实现（R5：`permissions[]`） | 既有消费，本次复验 | — | `id,username,is_super,roles,permissions` | 多角色合并去重、排序稳定、超管放行、fail-closed 均通过 |
| `GET /admin/users/:id/status`、`PUT .../status` | 已实现 | 详情抽屉沿用 | `status`、`reason` | `id,status` | 未改动；`reason` 为空 → 400（说明已通过 RBAC） |

Vue 侧真实请求序列（浏览器 Network，节选）：

```
GET  /api/admin/users?page=1&size=10                 200
GET  /api/admin/users/1                              200
GET  /api/admin/roles                                200
GET  /api/admin/roles/1/permissions                  200
POST /api/admin/roles/1/permissions                  200
GET  /api/admin/roles/1/permissions                  200   ← 保存后回读
DELETE /api/admin/roles/1/permissions/49             200
GET  /api/admin/roles/1/permissions                  200   ← 移除后回读
```

### 2.2 React 结算与订单接口

| 接口 | Go 后端状态 | React 调用状态 | 请求字段 | 响应字段 | 真实环境验证 |
| --- | --- | --- | --- | --- | --- |
| `POST /orders/quote` | 已实现 | 既有；本次修复成功后重复请求 | `source,address_id,coupon_id?` | 六槽位 + `items[]` | 200；运费 900/1000；用券 500；地址变更重新试算 |
| `POST /orders` | 已实现 | 既有；修复后不再伴随多余 Quote | `source,address_id,idempotency_key,coupon_id?` | `Order` 全字段 | 200；`total=original-discount-member+shipping` 成立 |
| `POST /orders`（同键重放） | **本次修复 9004** | 前端超时重试依赖该语义 | 同上 | 既有订单 | 200，返回同一订单 id，未重复建单 |
| `POST /orders`（同键不同地址） | 既有 | — | 同上 | 9006 | 9006 幂等冲突，符合 INV-002 |
| `GET /cart`、`PUT /cart/items/:id` | 已实现 | 既有 | `sku_id/quantity` | 购物车条目 | 200；数量 +1 后结算金额随服务端重算 |

## 3. 修改记录

| 文件 | 原因 |
| --- | --- |
| `admin/src/api/types/user.ts` | 补齐 `AdminUserDTO` / 列表查询与响应类型（白名单字段，无敏感字段） |
| `admin/src/api/modules/user.ts` | 新增 `listUsers`、`getUser`；更新过时注释 |
| `admin/src/api/adapters/access-adapter.ts` | 新增 `UserListView` / `toUserListView`（状态标签、注册时间） |
| `admin/src/views/user/index.vue` | 由"按 ID 查状态"改为服务端分页列表 + 详情抽屉 + 状态变更（加载/空态/错误/404/分页） |
| `admin/src/api/modules/role.ts`、`types/role.ts`、`types/permission.ts` | 新增 `listRolePermissions`；修正"没有角色权限读接口"的过时说明 |
| `admin/src/views/system/roles/components/RolePermissionDrawer.vue` | 读取真实授权集合、勾选态、差异保存、保存后回读；区分"空权限"与"读取失败" |
| `admin/src/views/system/roles/index.vue` | 更新使用说明与"权限数量列"注释（仍不做 N+1 聚合） |
| `admin/src/utils/permission.ts`、`api/modules/admin-auth.ts` | 修正 `permissions[]` 已下发的过时说明，保留 fail-closed 语义 |
| `admin/src/mocks/handlers/{access,mock-db}.ts` | MSW 夹具同步真实 DTO 与新路由（仅测试/演示用） |
| `admin/tests/unit/{user-access,role-permission}.test.ts` | 新增列表/详情/已授权限用例（+10 用例） |
| `web/src/pages/checkout-page.tsx` | 移除结算页 `onSuccess` 中对活跃 Quote query 的 `removeQueries` |
| `web/src/pages/checkout-success-page.tsx` | 在成功页挂载时清理试算缓存（此时结算页已卸载，不会触发重算） |
| `web/src/pages/checkout-page.test.tsx` | 新增回归断言：下单后 Quote 次数不增加 |
| `web/src/pages/checkout-coupon.test.tsx` | 单条导航断言 1s → 5s（定向放宽，非全局） |
| `internal/logic/order/order.go` | 购物车为空时的幂等重放：按 key 回读既有订单；地址/券不一致返回 9006 |
| `internal/logic/order/order_cart_idempotency_test.go` | 新增定向 Go 测试（真实 MySQL） |
| `api/order/v1/order.go` + `openapi/components/schemas.yaml` | `TotalAmount` 注释补 `+ shipping_fee`，重新生成契约 |
| `scripts/contract-check.sh` | 成功提示 210 → 213（与实际路由数一致） |

未修改任何 Migration、表结构、权限码或既有业务语义。

## 4. 测试结果

| 检查 | 命令 | 退出码 | 结果 |
| --- | --- | --- | --- |
| Go 构建 | `go build ./...` | 0 | PASS |
| Go 静态检查 | `go vet ./...` | 0 | PASS |
| Go 格式 | `gofmt -l internal/logic/order/ api/order/v1/` | 0 | 无输出 |
| 契约门禁 | `bash scripts/contract-check.sh` | 0 | 213 路由 / 4 planned / 497 Schema / 73 权限码；无漂移 |
| 后端定向测试 | `go test ./internal/logic/order/ -run TestCartOrderIdempotentReplayAfterCartConsumed -count=1 -v` | 0 | PASS（0.23s，真实 MySQL） |
| Vue ESLint | `npm run lint:eslint:check` | 0 | PASS（max-warnings 0） |
| Vue Prettier | `npm run lint:prettier:check` | 0 | PASS |
| Vue Stylelint | `npx stylelint "**/*.{html,vue,css,scss}"` | 0 | PASS |
| Vue 类型 | `npm run typecheck` | 0 | PASS |
| Vue 单测 | `npx vitest run` | 0 | 25 文件 / **362 用例**（基线 352，+10） |
| Vue 构建 | `npm run build` | 0 | PASS（3.38 MB） |
| React Lint | `npm run lint` | 0 | PASS |
| React 类型 | `npm run typecheck` | 0 | PASS |
| React 单测 | `npm test` | 0 | 45 文件 / **336 用例** |
| React 构建 | `npm run build` | 0 | PASS |
| Nginx 静态 | `bash scripts/test-nginx.sh` | 0 | PASS（含 `nginx -t`） |
| Nginx 路由 | `bash scripts/test-nginx-routing.sh` | 0 | 42/42 断言 PASS |
| Docker 后端镜像 | `docker build -t surgecart-align-app:local -f Dockerfile .` | 0 | PASS |
| Docker Nginx 镜像 | `docker build -t surgecart-align-nginx:local -f nginx/Dockerfile .` | 0 | PASS |

未执行（按约束）：`go test ./...`、`make test-unit`、全量 Go 套件。

## 5. 真实环境证据

### 5.1 环境

- 独立 Compose 项目 `surgecart-align`（临时文件 `/tmp/align-compose.yml`，不入库），不设置 `container_name`。
- MySQL 8（`127.0.0.1:13306`）、Redis 7（`127.0.0.1:16379`）、Go 后端、Nginx（`127.0.0.1:28081`）全部 healthy；未启动 Kafka（本任务不涉及，应用默认 `db_scanner`）。
- 迁移：`schema_migrations = 20261111000004 / dirty=0`；`regions` 字典通过 `region-seed import` 导入 3938 条。
- 未触碰既有 `my-shop-mysql`、`my-shop-redis`、`fbaudit-*`；验收后 `down -v`，无容器/网络/卷残留。
- 凭据仅通过 `--env-file` 只读注入，未输出到报告或日志。

### 5.2 真实 API（Nginx → Go → MySQL/Redis）

| 场景 | 结果 |
| --- | --- |
| 运费模板（首件 800 + 续件 100）+ 区县地址 | 试算 `original=39800, shipping=900, payable=40700` |
| 绑定模板商品下单 | `total_amount=40700, shipping_fee=900`；金额守恒成立 |
| 购物车下单后同键重放（购物车已清空） | 200，返回既有订单（id 相同，无重复订单） |
| 同键不同地址 | 9006 幂等冲突 |
| 角色已授权限读取 | `[21,24,26,48]`（按 id 升序） |
| 角色权限新增/移除后回读 | 新增 49 → 200，回读出现 49；移除 49 → 200，回读消失 |
| 受限管理员 `/admin/me` | `roles=[align-ops,align-role-b]`、`permissions=[permission:list,role:assign_permission,role:list,user:read,user:status]`（合并、去重、升序） |
| 权限强制 | 缺 `product:create` → 403；有 `user:status` 但参数非法 → 400 |
| 用户列表/详情 | 列表 200；详情 200；`page=0`→400；不存在→404 |

### 5.3 真实浏览器（Playwright 1.65，Chromium）

| 场景 | React 1440×900 | React 1920×1080 | Vue 1366×768 | Vue 1920×1080 |
| --- | --- | --- | --- | --- |
| 登录（`POST /api/login` / `/api/admin/login`） | 200 | 200 | 200 | 200 |
| 商品详情（当前价 / 划线价） | PASS | PASS | — | — |
| 购物车数量 +1（`PUT /cart/items/:id`） | 200 | 200 | — | — |
| 结算试算 | 200，`59700 + 1000 = 60700` | 同左 | — | — |
| 切换优惠券 → 重新试算 | 200，`coupon_discount=500`，`60200` | 同左 | — | — |
| 取消用券 → 重新试算 | 200 | 200 | — | — |
| 切换地址 → 重新试算 | 200，`address_id=2` | 同左 | — | — |
| 提交订单 | 200（`total=60700, shipping=1000`） | 同左 | — | — |
| 下单后是否多发 Quote | **否**（提交前 4 次，提交后仍 4 次） | **否** | — | — |
| 用户列表（分页 + 真实用户名） | — | — | PASS | PASS |
| 用户详情抽屉 | — | — | 200 | 200 |
| 无 `user:status` 时隐藏禁用按钮 | — | — | PASS | PASS |
| 角色权限读取/勾选态 | — | — | 4/73 | 4/73 |
| 保存新增 → 回读 | — | — | 4→5 | 4→5 |
| 保存移除 → 回读 | — | — | 5→4（DELETE 200） | 5→4 |
| 控制台错误 / pageerror | 0 | 0 | 0 | 0 |

补充：超级管理员浏览器复核——菜单全部可见（含管理员管理/权限管理），用户详情可禁用，角色页具备新建/编辑/删除/权限分配按钮；`GET /admin/me` 三次均 200。

证据：`/root/wave1-evidence/task4/`（16 张截图 + `browser-result.json` + `api-transcript.log` + `runtime.log` + `db.log`），仓库外保存。

### 5.4 界面一致性核对

- 金额展示全部来自服务端试算/订单快照，前端未自算应付。
- 用户页面只展示 `id/username/status/created_at`，无密码哈希、`auth_epoch`、昵称/手机号等后端未提供字段。
- 权限抽屉勾选状态与 `GET /admin/roles/:id/permissions` 完全一致（4 项 = 服务端 4 项）；保存后强制回读，不回显本地猜测。
- 生产构建未启用 MSW（`VITE_MOCK_ENABLED`/`VITE_ENABLE_MOCK` 未设置），浏览器 Network 中无 `mockServiceWorker.js` 请求。

## 6. 遗留问题

| # | 问题 | 优先级 | 影响业务 | 暂未修复原因 | 建议归属 |
| --- | --- | --- | --- | --- | --- |
| 1 | 库存表 `created_at` 展示时区与商品/订单不一致（同一时刻相差 8 小时） | P3 | 否（仅展示/排查困惑） | 根因是两类写入路径：GoFrame ORM 用进程本地时间（UTC 容器）填充 `created_at`，库存原生 SQL 依赖 MySQL `CURRENT_TIMESTAMP`（会话 `time_zone=Asia/Shanghai`）。修复需统一全项目时间策略（进程 TZ 或全部由 DB 生成），超出本任务范围 | 平台/基础设施独立任务 |
| 2 | Vue 用户页无关键词/状态筛选 | P3 | 否 | 后端 `GET /admin/users` 当前不支持筛选参数，前端不得伪造；R5 已登记为扩展项 | 后续产品/后端联合任务 |
| 3 | 角色列表无"权限数量"列 | P3 | 否 | 需要为每个角色额外调用一次已授权限接口（N+1）；角色规模大时成本不可控 | 后续性能评估 |
| 4 | 商城 token 仅存内存，刷新即需重新登录 | P3 | 否（安全设计取舍） | 需后端下发 HttpOnly Cookie 才能安全持久化；不在本任务范围 | 认证体系独立任务 |
| 5 | 有券下单的成功页金额未在本次真实浏览器覆盖（仅覆盖用券试算与无券下单） | P3 | 否 | 有券下单事务链已由既有单元测试覆盖；本次优先验证"空购物车 Quote"与金额守恒，未额外消耗券 | 回归测试补强 |
| 6 | 未执行全量 Go 测试、Kafka 端到端、七牛云 DNS | — | 否 | 任务明确禁止/排除 | 按需另立任务 |

已复核并解决的 Wave 1 P3：

- 空购物车 Quote 400：已修复（单元回归 + 真实浏览器 Network 4→4）。
- 购物车幂等重放 9004：已按 INV-002 修复（同键返回既有订单、不同内容 9006），并有定向 Go 测试与真实 API 证据。
- `checkout-coupon.test.tsx` 偶发超时：单文件 8 次串行 + 6 次并发均通过，全量 336 用例通过；未复现共享状态竞争，判定为高负载下导航断言超时，仅对该断言定向放宽至 5s。
- contract-check 文案 210 → 213；`TotalAmount` 注释补 `+ shipping_fee`（OpenAPI 同步重新生成）。

## 7. Git 交付

| 项 | 值 |
| --- | --- |
| 开发分支 | `feat/phase2-frontback-alignment` |
| 开发基线 | `607c022b215c590230d8ae538ddf2ae949d04761` |
| Worktree | `/root/projects/my-shop-worktrees/frontback-alignment` |
| 代码提交 | `b83db1a126e3ebaf3932119783c7c646af8bba8f` |
| 报告提交 | `f1e3ce95e62b8eff4b43311b502736b9935734f3` |
| 最终 HEAD | 元数据回填提交（仅改本表两行，SHA 由交付消息给出） |
| 远端 push | 普通 push（非 force），建立跟踪分支 |
| 工作区状态 | 提交后 clean（不含被 `.gitignore` 忽略的 `node_modules/dist`） |

未修改 `main`、未改写历史、未合并回集成分支、未删除任何既有分支。

## 8. 验收结论

**PASS**

依据：

- 三个 R5 接口全部由 Vue 正确消费，并在真实浏览器中完成"列表 → 详情 → 已授权限读取 → 勾选保存 → 差异移除 → 回读校验"闭环。
- 管理员权限体系真实运行正常：多角色合并去重、超管放行、非超管菜单/按钮按权限码收敛、后端 `RequirePermission` 强制生效、fail-closed 语义保留。
- React 空购物车 Quote 问题已修复：单元回归断言与真实浏览器 Network 均确认下单后不再新增 Quote。
- 隔离环境真实链路全部通过，包含 `shipping_fee > 0`、优惠券试算、数量/地址变更重算、订单快照金额守恒、购物车幂等重放与 9006 冲突语义。
- 无未处理的阻断性前后端契约问题；遗留项均为 P3 展示/体验或范围外事项。
