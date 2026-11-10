# A3 订单与售后管理 — 交付报告

- 阶段：A3（订单列表、订单详情、发货、退款管理、售后记录）
- 分支：`feat/phase2-admin-frontend`
- 执行模型：DeepSeek-V4.1-Flash
- 开发知识文档：`.agent/tasks/dev/A3-order-aftersales.md`
- 后端改动：无（未修改 `internal/**`、`api/**`、`migrations/**`、`contracts/**`）

---

## 1. 任务目标与实际范围

在现有 Vue 3 管理后台内实现订单与售后管理，严格复用 Go 后端已有的订单状态机、
退款闭环与权限码，不新增后端接口、不修改状态机、不触碰 Kafka 秒杀链路。

未进入 A4/A5：没有优惠券、秒杀活动、文章、用户、管理员、角色、财务或数据大屏。

---

## 2. 实际实现的功能

状态说明：**IMPLEMENTED** 已实现 / **VERIFIED** MSW+Vitest 验证 /
**PARTIALLY_VERIFIED** 部分验证 / **NOT_VERIFIED** 未联调 / **NOT_IMPLEMENTED** 未实现。

### 2.1 订单管理

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 订单列表 | IMPLEMENTED / VERIFIED | 服务端分页、id 倒序、稳定排序 |
| 订单编号搜索 | IMPLEMENTED / VERIFIED | `order_no` 精确匹配 |
| 用户筛选 | IMPLEMENTED / VERIFIED | 仅支持 `user_id` 精确匹配；后端不支持用户名 |
| 状态筛选 | IMPLEMENTED / VERIFIED | 7 种字符串状态 |
| 时间范围筛选 | IMPLEMENTED / VERIFIED | RFC3339（本地时区偏移），左闭右开 |
| 金额 / 下单时间 | IMPLEMENTED / VERIFIED | 整数分转元展示 |
| 商品摘要 / 件数 | PARTIALLY_VERIFIED | 列表接口不返回，逐条详情补全（并发 4），失败显示 `—` |
| 支付时间 / 订单来源 | NOT_IMPLEMENTED | 列表接口不返回；订单 DTO 也没有 source 字段 |
| 订单详情 | IMPLEMENTED / VERIFIED | 基本信息、金额、收货、支付时间、时间线 |
| 商品快照 | IMPLEMENTED / VERIFIED | 只读 `order_items`，不回查当前商品；已验证改名不影响历史订单 |
| 订单操作记录 | NOT_IMPLEMENTED | 后端无订单审计接口，页面明确提示不展示 |
| 订单状态修改 | NOT_IMPLEMENTED | 无通用 PUT，状态只能由业务接口推进 |

### 2.2 发货管理

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 发货 | IMPLEMENTED / VERIFIED | `POST /admin/orders/:id/ship`，仅 `paid` |
| 发货前确认 | IMPLEMENTED / VERIFIED | 弹窗展示订单号/金额/状态 + 约束说明 |
| 重复提交防护 | IMPLEMENTED / VERIFIED | `useSubmitGuard` + 后端 9002 |
| 退款互斥提示 | IMPLEMENTED / VERIFIED | 后端 18005「退款与发货冲突」原文展示 |
| 物流公司 / 运单号 | NOT_IMPLEMENTED | 后端 DTO 与表结构均无字段，未虚构表单 |
| 物流轨迹 | NOT_IMPLEMENTED | 后端无能力，未接第三方物流 |

### 2.3 退款与售后

| 功能 | 状态 | 说明 |
| --- | --- | --- |
| 退款列表 | IMPLEMENTED / VERIFIED | 服务端分页、id 倒序 |
| 按订单编号搜索 | IMPLEMENTED / VERIFIED | 订单列表精确取 id → 退款列表 `order_id`（两步服务端筛选） |
| 订单 / 用户 / 状态筛选 | IMPLEMENTED / VERIFIED | 后端真实参数 |
| 申请时间筛选 | NOT_IMPLEMENTED | 后端无时间参数，未做前端假筛选 |
| 退款详情 | IMPLEMENTED / VERIFIED | 金额、原因、审核/执行信息、状态 |
| 关联订单与商品快照 | PARTIALLY_VERIFIED | 跨接口组合（需 `order:view`），失败降级提示 |
| 退款审计记录 | IMPLEMENTED / VERIFIED | `GET /admin/refunds/:id/audits`，时间线展示 |
| 审核通过 / 拒绝 | IMPLEMENTED / VERIFIED | `review`，原因必填，二次确认，防重复 |
| 执行退款成功 / 失败 | IMPLEMENTED / VERIFIED | `execute`，人工 Mock 确认语义 |
| 关闭退款 | IMPLEMENTED / VERIFIED | `close`，仅退款失败可关闭 |
| 独立售后模块 | NOT_IMPLEMENTED | 复用退款 + 审计，不造重复数据模型 |
| 退货 / 换货 / 维修 | NOT_IMPLEMENTED | 后端无能力，本次不扩展 |

---

## 3. 订单状态机与业务约束（实测源码）

订单状态（API 字符串 / DB TINYINT）：

`pending_payment(10)`、`paid(20)`、`shipped(30)`、`received(40)`、
`completed(50)`、`cancelled(60)`、`refunded(70)`。

- 后台只允许一种迁移：**发货 `paid → shipped`**。
- 发货前检查：订单必须 `paid`（否则 9002）、无有效退款申请（否则 18005）。
- 退款成功后由后端同事务把订单 `paid → refunded` 并恢复库存。
- 前端没有任何修改订单状态字段的入口。

退款状态：

`applying(10)`、`pending_refund(20)`、`refund_failed(30)`、
`review_rejected(40)`、`revoked(50)`、`refund_succeeded(60)`、`closed(70)`。

- 审核通过 → 待退款（资金未到账）；审核拒绝 → 终态。
- 执行成功：待退款 / 退款失败 → 退款成功（订单退款 + 库存恢复）。
- 执行失败：仅待退款 → 退款失败。
- 关闭：仅退款失败 → 已关闭（订单保持已支付，可发货）。

订单状态与退款状态是两套独立枚举，页面不混用。

---

## 4. API 接入情况

### 4.1 实际接入（真实 API）

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/admin/orders` | 订单列表 |
| GET | `/admin/orders/:id` | 订单详情（含快照） |
| POST | `/admin/orders/:id/ship` | 发货（无请求体） |
| GET | `/admin/refunds` | 退款列表 |
| GET | `/admin/refunds/:id` | 退款详情 |
| GET | `/admin/refunds/:id/audits` | 退款审计（售后记录） |
| POST | `/admin/refunds/:id/review` | 审核通过/拒绝 |
| POST | `/admin/refunds/:id/execute` | 执行退款结果 |
| POST | `/admin/refunds/:id/close` | 关闭退款 |

权限码（全部为后端真实定义）：
`order:list`、`order:view`、`order:ship`、`refund:list`、`refund:view`、
`refund:review`、`refund:execute`。

### 4.2 Provisional API

无。A3 的 DTO、路径、字段与状态枚举均来自 Go 源码核对，没有暂定字段，
也没有创建虚假 OpenAPI 契约（仓库中仍不存在 `contracts/openapi.yaml`）。

### 4.3 前端组合能力（非新接口）

| 能力 | 实现 | 限制 |
| --- | --- | --- |
| 订单列表商品摘要/件数 | 列表 + 逐条详情 | 并发 4，失败显示 `—` |
| 退款列表按订单号搜索 | 订单列表 → 退款列表 | 需要 `order:list` |
| 退款详情关联订单 | 退款详情 + 订单详情 | 需要 `order:view`，403 时降级 |

### 4.4 后端不支持、前端未实现

- 订单操作日志 / 审计查询。
- 管理员取消订单、修改订单状态。
- 物流公司、运单号、物流轨迹。
- 支付方式、支付流水号。
- 订单来源（source）展示。
- 退款申请时间筛选。
- 秒杀订单后台查询（`flash_sale_orders` 独立表，不在 `/admin/orders` 范围）。

---

## 5. 主要修改文件

新增（均在 `admin/**`）：

- `src/api/types/{order,refund}.ts`
- `src/api/modules/{order,refund}.ts`
- `src/api/adapters/{order,refund}-adapter.ts`
- `src/views/order/{index.vue,detail.vue,components/ShipOrderDialog.vue}`
- `src/views/refund/{index.vue,detail.vue,components/RefundActionDialog.vue}`
- `src/router/modules/{order,refund}.ts`
- `src/mocks/handlers/{order,refund}.ts`
- `tests/unit/{order,refund}.test.ts`

修改：

- `src/mocks/handlers/mock-db.ts` — 增加订单/订单项/退款/审计夹具。
- `src/mocks/handlers/index.ts` — 注册 A3 handlers。
- `src/utils/permission.ts` — 新增 7 个真实订单/退款权限码。
- `src/router/enums.ts` — 新增菜单 rank。
- `src/components/ReIcon/src/offlineIcon.ts` — 注册 A2+A3 菜单图标
  （修复 A2 图标未离线注册导致的空白）。
- `locales/zh-CN.yaml`、`locales/en.yaml` — 菜单文案。

---

## 6. 测试结果

实际执行：

| 命令 | 结果 |
| --- | --- |
| `pnpm typecheck` | 通过（0 error） |
| `pnpm lint:eslint` | 通过（0 error / 0 warning） |
| `pnpm test` | 14 个测试文件、217 个用例全部通过 |
| `pnpm build` | 通过，产物约 3.22 MB |
| `git diff --check` | 通过 |

A3 新增 55 个用例：

- `tests/unit/order.test.ts`（29）：分页、订单号/用户/状态/时间筛选、空数据、
  400/403、详情快照、金额、取消原因、9001、发货成功、9002、18005、重复发货、
  商品改名快照不变、查询参数与权限码。
- `tests/unit/refund.test.ts`（26）：列表分页/筛选/400/403、详情与审计映射、
  18001、审核通过/拒绝/重复审核、原因校验、执行成功（订单退款 + 库存恢复）、
  执行失败、失败重试、关闭、关闭后禁止执行、幂等、动作可用性、权限码。

A0/A1/A2 回归测试全部继续通过（同一 `pnpm test`）。

### 真实联调状态

- 真实 Go 后端联调：**NOT_VERIFIED**（未启动后端，全部由 MSW 拦截）。
- 真实发货：**NOT_VERIFIED**。
- 真实退款：**NOT_VERIFIED**（后端为人工 Mock 确认，未接入支付渠道）。
- 浏览器 1366 / 1920 视觉验收：**NOT_VERIFIED**（无图形浏览器环境）。

---

## 7. 未完成事项与已知限制

1. 退款列表没有申请时间筛选（后端无参数），页面已提示。
2. 订单列表的商品摘要/件数为逐条详情补全；无 `order:view` 权限时该列显示 `—`。
3. 退款详情的关联订单需要 `order:view`；无权限时降级为订单 ID。
4. 订单操作记录无接口，页面明确不展示而非伪造。
5. 发货无物流信息（后端无字段），只推进状态。
6. 秒杀订单不在后台订单接口范围内，未实现查询。
7. 非超管角色的按钮可见性受 A1 本地角色映射限制（`/admin/me` 不返回 permissions[]）；
   后端 RBAC 仍是最终授权边界。
8. 无真实支付/退款渠道，执行退款是管理员人工确认。

---

## 8. Git

- 功能提交 SHA：`（提交后回填）`
- 文档回填提交：本条提交
- 目标分支：`feat/phase2-admin-frontend`
- 远端：`origin/feat/phase2-admin-frontend`
- 提交前远端状态：`origin/feat/phase2-admin-frontend...HEAD = 0 0`
- 推送方式：普通 `git push`（未 force push，未改写历史）

---

## 9. A4 启动条件

1. A3 提交已推送，工作区干净，A0-A3 回归测试通过。
2. 真实后端联调完成，至少确认：订单列表/详情/发货、退款审核/执行/关闭、
   审计记录与权限码在真实环境可用。
3. 秒杀订单后台查询需求明确（若需要，由后端提供独立接口，不在订单页硬塞）。
4. 后端补齐物流字段或退款时间筛选后，只修改 DTO 与 adapter，页面结构不变。
5. Owner 明确下发 A4 营销与内容管理任务书后再启动，不自动进入下一阶段。
