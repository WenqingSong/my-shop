# PHASE2 Task 4 Git 集成报告

- 任务：Task 5 — 将已验收的前后端联调分支合并进阶段二集成分支
- 执行日期：2026-11-13（Asia/Shanghai）
- 执行方式：主 Agent 独立执行；仅 Git 集成、合并后工程验证与报告，无业务改动

## 1. 集成前分支 SHA

| 分支 | 本地 SHA | 远端 SHA | 是否一致 |
| --- | --- | --- | --- |
| `feat/phase2-ecommerce-business`（目标集成分支） | `607c022b215c590230d8ae538ddf2ae949d04761` | `607c022b215c590230d8ae538ddf2ae949d04761` | 是 |
| `feat/phase2-frontback-alignment`（待合并） | `4b2410e25fb51dbf3e419ce7547c1ebc2e9274ad` | `4b2410e25fb51dbf3e419ce7547c1ebc2e9274ad` | 是 |
| `main` | `c60e4323723b6316c0c8f44a4a388df626e8b530` | 同 | 未参与 |

- 合并前已执行 `git fetch --all --prune`，目标分支无未经确认的新提交（仍为交接基线 `607c022`）。
- 两个 Worktree 合并前均为 clean：集成 Worktree `/root/projects/my-shop-integration`、开发 Worktree `/root/projects/my-shop-worktrees/frontback-alignment`。
- `merge-base` = `607c022b215c590230d8ae538ddf2ae949d04761`，开发分支未混入其他基线的提交。
- 待合并 3 个提交：

  ```
  b83db1a feat(alignment): 管理端接入 R5 查询接口并修复结算试算与购物车幂等
  f1e3ce9 docs(report): 新增 PHASE2 前后端接口对齐与真实联调报告
  4b2410e docs(report): 回填对齐报告提交 SHA（仅元数据）
  ```

## 2. Task 4 合并提交

| 项 | 值 |
| --- | --- |
| 合并方式 | `git merge --no-ff`（未使用 ours/theirs、未 squash、未 rebase） |
| 合并提交 | `c2657aac45f435fdfe396bea083d1b3868b3d4e3` |
| 提交信息 | `merge(feat/phase2-frontback-alignment): 集成前后端接口对齐与结算/幂等修复` |
| 合并前 `merge-tree` 预检 | `05af57c87f9fc9904d49dd48416ce71d3fa6bdb3`（无冲突输出） |
| 实际变更 | 25 文件，+1175 / −298 |

## 3. 合并冲突情况

**无冲突**。合并由 ort 策略自动完成，`git ls-files -u` 为空；未人工解决、未修改任何文件内容，合并树与开发分支内容一致（除合并父提交关系）。

## 4. 合并内容核验

在合并后的集成分支（`c2657aa`）逐项确认：

### 4.1 Vue 管理后台

| 能力 | 证据 |
| --- | --- |
| 用户分页列表 | `admin/src/api/modules/user.ts:23 listUsers`；`admin/src/views/user/index.vue:51 listUsers({page,size})` + `el-pagination` |
| 用户详情抽屉 | `admin/src/views/user/index.vue:95 getUser(id)`；详情抽屉与 404/权限错误处理 |
| 角色已授权限查询 | `admin/src/api/modules/role.ts:64 listRolePermissions`；`RolePermissionDrawer.vue:98` 打开时读取 |
| 权限勾选状态回显 | 抽屉按服务端集合初始化勾选态（真实环境 4/73，与接口一致） |
| 权限差异 POST/DELETE | `admin/src/api/modules/role.ts` 保留 `assignRolePermission` / `removeRolePermission`；抽屉按差异提交 |
| 保存后重新读取 | `RolePermissionDrawer.vue:159 await load()`（保存成功后强制回读） |
| 管理员权限控制 | 用户页禁用按钮受 `user:status`、角色页写操作受 `role:create/update/delete/assign_permission` 控制；`/admin/me` 驱动菜单与按钮 |

### 4.2 React 商城

| 能力 | 证据 |
| --- | --- |
| 空购物车 Quote 修复 | `web/src/pages/checkout-page.tsx:193` `onSuccess` 不再 `removeQueries` 活跃 Quote query（注释说明原因） |
| 成功后不触发旧结算流程 Quote | `web/src/pages/checkout-success-page.tsx:44` 在成功页挂载时清理 `queryKeys.orders.quotes()` |
| 正常试算未被破坏 | `web/src/pages/checkout-page.tsx` 的 `useOrderQuote` 启用条件与 Query Key 逻辑未变；336 个 React 用例（含新增回归断言）通过 |

### 4.3 Go 后端

| 能力 | 证据 |
| --- | --- |
| 购物车订单幂等重放 | `internal/logic/order/order.go:210` 在 `buildLines` 返回 9004 时调用 `replayConsumedCartOrder` |
| 同键返回原有订单 | `order.go:797 replayConsumedCartOrder`：键命中且地址/券一致 → `toOrder` 返回既有订单 |
| 同键不同地址/券 → 9006 | `order.go:825` 返回 `CodeOrderIdempotencyConflict` |
| 真实 MySQL 定向测试 | `internal/logic/order/order_cart_idempotency_test.go`（新增文件，91 行）完整进入集成分支 |

### 4.4 文档与工程文件

- `docs/reports/PHASE2_FRONTBACK_ALIGNMENT.md`（Task 4 正式报告）已合并。
- `openapi/components/schemas.yaml`（`TotalAmount` 说明）与 `scripts/contract-check.sh`（210 → 213）已合并。
- MSW 夹具、Vue/React 单元测试、契约与注释修正随分支完整进入。

## 5. 工程验证结果

均在合并后集成分支 `c2657aa` 执行：

| 检查 | 命令 | 退出码 | 结果 |
| --- | --- | --- | --- |
| Go 构建 | `go build ./...` | 0 | PASS |
| Go 静态检查 | `go vet ./...` | 0 | PASS |
| 幂等定向测试 | `go test ./internal/logic/order/ -run TestCartOrderIdempotentReplayAfterCartConsumed -count=1 -v` | 0 | PASS（3.68s，隔离 MySQL 8 + Redis 7） |
| 契约门禁 | `bash scripts/contract-check.sh` | 0 | PASS（213 路由 / 4 planned / 497 Schema / 73 权限码 / NOT RUN 0） |
| React Lint | `npm run lint` | 0 | PASS |
| React 类型 | `npm run typecheck` | 0 | PASS |
| React 单测 | `npm test` | 0 | 45 文件 / **336 用例** |
| React 构建 | `npm run build` | 0 | PASS（`✓ built in 450ms`） |
| Vue ESLint | `npm run lint:eslint:check` | 0 | PASS（`--max-warnings 0`） |
| Vue Prettier | `npm run lint:prettier:check` | 0 | PASS |
| Vue Stylelint | `npx stylelint "**/*.{html,vue,css,scss}"` | 0 | PASS |
| Vue 类型 | `npm run typecheck` | 0 | PASS |
| Vue 单测 | `npx vitest run` | 0 | 25 文件 / **362 用例** |
| Vue 构建 | `npm run build` | 0 | PASS（3.38 MB） |

说明：

- 定向 Go 测试使用临时隔离 Compose 项目 `surgecart-merge-test`（MySQL `127.0.0.1:13307`、Redis `127.0.0.1:16380`），测试后已 `down -v` 清理；未触碰 `my-shop-mysql` / `my-shop-redis`。
- 未执行 `go test ./...` 与 `make test-unit`（按任务约束）。
- 未重复执行真实浏览器联调；Task 4 已提供真实环境证据（见 `PHASE2_FRONTBACK_ALIGNMENT.md` 第 5 节）。合并未引入与 Task 4 验收状态不同的差异（合并树内容与开发分支一致）。

## 6. 遗留问题

本次集成本身未新增问题。沿用 Task 4 已登记的 P3 项，均不阻断：

| # | 问题 | 优先级 | 影响 | 归属 |
| --- | --- | --- | --- | --- |
| 1 | 库存 `created_at` 展示时区与商品/订单不一致（ORM 进程时间 UTC vs 原生 SQL + `Asia/Shanghai` 会话） | P3 | 仅展示/排查困惑 | 平台时间策略独立任务 |
| 2 | Vue 用户页无关键词/状态筛选（后端未提供参数） | P3 | 列表可用，筛选缺失 | 后续产品/后端联合任务 |
| 3 | 角色列表无权限数量列（避免每角色一次 N+1 查询） | P3 | 需进入抽屉查看 | 后续性能评估 |
| 4 | 商城 token 仅存内存，刷新需重新登录（安全取舍） | P3 | 体验 | 认证体系独立任务 |
| 5 | 有券下单成功页未在真实浏览器覆盖（已有单测） | P3 | 无阻断 | 回归测试补强 |

## 7. 最终集成分支 SHA

| 阶段 | SHA |
| --- | --- |
| 集成前 | `607c022b215c590230d8ae538ddf2ae949d04761` |
| Task 4 合并提交 | `c2657aac45f435fdfe396bea083d1b3868b3d4e3` |
| 报告提交 | 见本文件提交 |
| 最终 HEAD | 报告 SHA 回填提交后（仅元数据），由交付消息给出 |

## 8. 远端推送结果

- 使用普通 `git push`（非 force），将 `feat/phase2-ecommerce-business` 从 `607c022` 快进更新到最终 HEAD。
- 推送前已确认远端仍为 `607c022`，无并发提交；推送后本地与 `origin/feat/phase2-ecommerce-business` 一致。
- 原开发分支 `feat/phase2-frontback-alignment`（`4b2410e`）保留，本地与远端均未删除、未改写。

## 9. Git 工作区状态

- 集成分支工作区：报告提交后 clean（`git status --porcelain` 无输出）。
- 未修改 `main`（仍为 `c60e432`）、未 rebase、未 force push、未使用 ours/theirs、未删除任何分支。
- 合并后无残留临时容器/网络/卷（`surgecart-merge-test` 已清理）。

## 10. 结论

Task 4 开发分支已完整、无冲突地并入阶段二集成分支；Vue 三接口接入、React 结算修复、Go 购物车幂等修复及配套测试/文档均已进入集成分支；合并后 Go、React、Vue 与契约门禁全部通过，无回归。集成完成。
