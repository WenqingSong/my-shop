# W4-03 用户退款中心 交付报告

- 任务：SurgeCart 用户退款中心（W4-03）
- 执行模型：DeepSeek-V4.1-Flash
- 分支：`feat/phase2-user-frontend`
- 日期：2026-11-10
- 范围：React 用户端退款申请、我的退款记录、退款详情、撤销申请、状态展示、与订单中心联动
- 明确不在范围：Go 后端、SQL、Docker/Nginx/Kubernetes、管理端退款审核、真实第三方资金退款、W4-04 优惠券中心

---

## 1. 实际完成的页面与功能

| 交付项 | 路由 / 位置 | 说明 |
| --- | --- | --- |
| 我的退款列表 | `/account/refunds` | 服务端数据；全部 7 状态本地筛选；本地分页（每页 10）；Loading / Empty / Error / 刷新 |
| 退款详情 | `/account/refunds/:id` | 编号、关联订单（可跳转）、金额、申请原因、申请时间、当前状态、审核/处理信息（有值才展示）、撤销入口、资金真实性声明 |
| 退款申请 | 订单详情页「退款」区域 | 仅 `paid` 且无有效申请时展示；整单退款；表单只有「退款原因」；成功跳转退款详情 |
| 撤销退款 | 退款详情页 | 仅 `applying` 展示；确认 Dialog；真实 `POST /refunds/:id/revoke`；成功/失败反馈；缓存同步 |
| 退款状态展示 | 列表徽标 / 详情徽标 / 订单卡片徽标 | 7 状态一一映射，`data-refund-status` 保留后端枚举 |
| 订单详情联动 | `/account/orders/:id` | 独立「退款」区域：关联退款记录列表 + 申请入口 + 「全部退款记录」入口 |
| 订单列表联动 | `/account/orders` | 卡片展示该订单最新退款状态徽标（数据来自 `GET /refunds` 映射） |
| 账户概览入口 | `/account` | 新增「我的退款」功能卡 |
| 个人中心导航 | `/account/*` 侧边栏 | 新增「我的退款」，`refunds/:id` 子路由高亮 |
| 移动端导航 | 移动端抽屉 | 已登录时新增「我的退款」 |
| 账号隔离 | 退出登录 | 显式清理 `refunds` 与 `orders` 查询缓存 |

所有 `/account/*` 页面继续由现有 `RequireAuth` 保护，未新增鉴权逻辑。

---

## 2. 退款 API 契约（Method + Path）

事实来源：`api/refund/v1/refund.go`、`internal/cmd/routes_frontend.go:98-103`。

| # | Method | Path | 请求 | 响应 data |
| --- | --- | --- | --- | --- |
| 1 | POST | `/orders/:id/refund` | `{ "reason": string }`（必填，trim 后 1–255 字符） | `Refund`（status=`applying`） |
| 2 | GET | `/refunds` | 可选 query `order_id`（>0 时服务端过滤） | `{ items: Refund[] }`（本人，id 倒序，无分页） |
| 3 | GET | `/refunds/:id` | path id | `Refund` |
| 4 | POST | `/refunds/:id/revoke` | 空 body，仅 path id | `Refund`（status=`revoked`） |

`Refund` 响应结构：

```json
{
  "id": 8,
  "order_id": 2,
  "user_id": 1,
  "status": "applying",
  "amount": 209900,
  "reason": "真实后端 UI 联调退款",
  "review_reason": "",
  "reviewed_by": null,
  "reviewed_at": null,
  "execute_reason": "",
  "executed_by": null,
  "executed_at": null,
  "created_at": "2026-11-10 18:31:05",
  "updated_at": "2026-11-10 18:31:05"
}
```

只读参考（用户端不可用）：后台 `GET /admin/refunds/:id/audits`（需 `refund:view`）返回退款审计，本任务**未使用**。

---

## 3. 退款状态枚举

| API 字符串 | DB | 中文 | 用户可撤销 | 终态 |
| --- | --- | --- | --- | --- |
| `applying` | 10 | 申请中 | ✅ | ❌ |
| `pending_refund` | 20 | 待退款 | ❌ | ❌ |
| `refund_failed` | 30 | 退款失败 | ❌ | ❌ |
| `review_rejected` | 40 | 审核拒绝 | ❌ | ✅ |
| `revoked` | 50 | 已撤销 | ❌ | ✅ |
| `refund_succeeded` | 60 | 退款成功 | ❌ | ✅ |
| `closed` | 70 | 已关闭 | ❌ | ✅ |

前端未新增任何状态；有效状态集合 `{applying, pending_refund, refund_failed}` 与后端一致。

---

## 4. 退款申请前置条件

后端强制（前端仅做入口可见性）：

1. 订单存在且属于当前登录用户（否则 `18001` / 404，防枚举）；
2. `orders.status === 'paid'`（已支付、未发货）；其他一切状态 `18003` / 409；
3. `reason` trim 后非空且 ≤255 字符；否则 `1001` / 400；
4. 同一订单不存在有效退款申请（10/20/30）；否则 `18004` / 409（DB `uk_active` 兜底）。

前端入口条件：`order.status === 'paid'` 且退款列表已加载且无有效申请。

---

## 5. 撤销退款条件

- 仅 `applying(10)` 可撤销（`18002` / 409 表示其他状态）；
- 仅本人（`18001` / 404）；
- 撤销不恢复库存、不改订单状态；撤销后可重新申请。

---

## 6. 退款金额的权威来源

- 唯一来源：`orders.total_amount`（订单创建时的服务端金额快照）；
- 申请时由服务端复制为 `refund_requests.amount`；
- 请求体**不存在**金额字段，前端不提交、不计算、不可编辑；
- 用券订单按实付金额退款（联调：`209900 - 50000 = 159900`）。

---

## 7. 是否涉及真实资金退回

**否。**

- 项目使用 Mock 支付：`POST /orders/:id/pay` 只把订单推进到 `paid` 并写 `paid_at`；
- 退款执行是后台人工 Mock 确认（`success` / `failure`），无支付网关 SDK、无支付流水、无回调、无对账；
- 前端三处明确声明：「退款状态反映项目内部业务处理进度，不代表真实资金已经退回」；
- 页面不展示退款渠道、银行卡/支付宝/微信、流水号、预计到账时间。

---

## 8. 退款与订单状态关系

| 退款动作 | 订单状态变化 |
| --- | --- |
| 申请 | 无（保持 `paid`） |
| 审核同意 / 拒绝 | 无（保持 `paid`） |
| 撤销 | 无（保持 `paid`） |
| 退款失败 | 无（保持 `paid`） |
| 关闭 | 无（保持 `paid`） |
| **执行成功** | `paid(20) → refunded(70)`，写 `refunded_at` |

---

## 9. 退款与库存、优惠券关系

| 动作 | 库存 | 优惠券 |
| --- | --- | --- |
| 申请 / 审核 / 撤销 / 失败 / 关闭 | 不变 | 不变 |
| **执行成功** | 恢复一次（同事务，订单 `status=20` 条件更新闸门保证只补偿一次） | **不返还**，保持 `used` |

真实后端实测（SKU 初始 100）：

```
订单1 下单 100→99；退款成功 →100
订单2 下单 →99；撤销 →99
订单3 下单 →98；执行失败 →98；关闭 →98
订单4（用券）下单 →97；退款成功 →98
终态 inventories.quantity = 98
```

用券实测：支付后券 `used(order_id=4)` → 退款成功 → 仍 `used(order_id=4)`。

---

## 10. 真实后端联调结果（非 Mock）

### 10.1 环境

- MySQL 8.0.46（独立容器 `127.0.0.1:3307`，全新库，`migrate up` 至 `20261001000024`）
- Redis 7（独立容器 `127.0.0.1:6380`，DB 9）
- Go 服务：`go build -tags serve_lifecycle ./test/serveproc` + `test/container/qiniu_mock`
  - 目的：绕过启动期七牛 bucket 真实外网校验；**未修改任何 Go 代码**，业务校验逻辑不变
- 前端：`vite` dev（`127.0.0.1:5174`）+ `/api` 代理到真实后端 `127.0.0.1:8000`，**未设置 `VITE_ENABLE_MOCK`**
- 浏览器：Playwright Chromium，真实点击交互

### 10.2 接口级验证：48 / 48 PASS

| 分类 | 验证项（全部 PASS） |
| --- | --- |
| 申请 | 已支付订单申请成功；状态 `applying`；金额 = 订单实付；重复申请 `18004/409`；空原因 `1001/400`；已退款订单 `18003/409` |
| 申请影响 | 申请阶段订单仍 `paid` |
| 列表 | 未登录 401；包含本人记录；id 倒序；`order_id` 服务端过滤生效 |
| 详情 | 可查询；他人访问 `18001/404`；他人列表为空 |
| 审核/执行 | 审核通过 → `pending_refund`；待退款撤销 `18002/409`；执行成功 → `refund_succeeded` + `executed_at` |
| 订单联动 | 退款成功 → 订单 `refunded` + `refunded_at`；库存 99→100 |
| 撤销 | `applying → revoked`；库存不变（99）；订单仍 `paid`；撤销后可再次申请；再次撤销成功；他人撤销 `18001/404` |
| 失败/关闭 | 执行失败 → `refund_failed`；订单仍 `paid`；库存不变（98）；关闭 → `closed`；库存不变（98） |
| 优惠券 | 用券下单 `209900-50000=159900`；支付后 `used`；退款金额 159900；退款成功后仍 `used` 且 `order_id` 不变；订单 `refunded` |

### 10.3 UI 级验证：30 / 30 PASS

| 分类 | 验证项（全部 PASS） |
| --- | --- |
| 鉴权 | 未登录访问订单详情跳登录；登录后回到原页面；退出后访问 `/account/refunds` 重新要求登录 |
| 申请入口 | 已支付订单展示退款区域与「申请退款」 |
| 申请弹层 | 说明整单退款；说明需商家审核；说明不代表资金退回；无支付宝/微信/银联；展示订单实付 `¥2,099.00` |
| 提交结果 | 成功跳转退款详情；状态「申请中」；展示申请原因；展示金额；展示资金声明；说明不提供状态时间轴 |
| 列表 | 展示新记录；展示「申请中」；筛选器存在；「申请中」筛选后只剩本次申请且历史原因消失 |
| 订单联动 | 从退款卡片进入关联订单；订单详情展示关联退款记录；有有效退款时不再展示申请入口 |
| 撤销 | 确认框说明订单状态不变；撤销成功反馈；状态变「已撤销」；撤销入口消失 |
| 撤销后 | 订单仍「已支付」；订单卡片退款徽标同步「已撤销」 |
| 稳定性 | 无未捕获页面异常；无非预期控制台错误 |

### 10.4 联调数据库终态

```
refund_requests: 1=60(order1) 5=70(order3) 6=60(order4) 8=50(order2)
orders:          1=70 2=20 3=20 4=70
inventories:     sku 1 quantity=98
refund_audits:   refund 8 → apply(operator_type=1, 0→10), revoke(operator_type=1, 10→50)
```

---

## 11. Mock 测试结果（与真实联调严格区分）

| 层 | 环境 | 规模 | 结果 |
| --- | --- | --- | --- |
| 单元 / 组件 | Vitest + jsdom + MSW | 39 文件 / 276 用例 | 全部通过 |
| e2e | Playwright + `VITE_ENABLE_MOCK=true`（MSW） | 43 用例（新增 3 个退款用例） | 全部通过 |

Mock 侧新增能力（仅测试/本地 Mock 存在，真实后端没有）：

- `src/mocks/data/refunds.ts`：申请/列表/详情/撤销，状态闸门与错误码对齐后端；
- `seedMockRefund`：构造任意状态记录；
- `setMockRefundDelay` / `getMockRefundWriteCount`：验证提交中禁用与防重复提交。

**声明**：e2e 使用 MSW，不代表真实后端；真实后端结果见第 10 节。

---

## 12. 后端尚未支持的能力

| 能力 | 现状 | 前端处理 |
| --- | --- | --- |
| 部分退款 / 商品级退款 | 不支持（仅整单全额） | 表单不提供商品选择与金额输入 |
| 用户侧退款审计/时间轴 | 无（仅 `/admin/refunds/:id/audits`，需 `refund:view`） | 不构造时间轴，明确说明 |
| 用户侧审核/执行 | 无 | 不实现后台能力 |
| 退款列表分页 / 状态筛选 | 无（仅 `order_id` 过滤） | 本地筛选 + 本地分页，页面标注 |
| 退款凭证上传 | 无退款图片上传接口 | 不提供上传 |
| 真实资金退回 / 渠道 / 流水 / 到账时间 | 无 | 不展示、不承诺 |
| 退款完成后撤销 | 不支持（终态） | 不提供入口 |
| 优惠券返还 | 不支持（保持 used） | 文案明确说明 |
| 退款申请次数/统计 | 无 | 不展示 |

---

## 13. 关键文件与职责

### 13.1 数据层

| 文件 | 职责 |
| --- | --- |
| `web/src/api/refunds.ts` | 退款 DTO、状态枚举、4 个 API 函数（事实来源注释齐全） |
| `web/src/api/query-keys.ts` | 新增 `refunds` key 工厂（全量列表 / 按订单列表 / 详情） |
| `web/src/hooks/use-refunds.ts` | `useRefundList` / `useRefundDetail` / `useApplyRefund` / `useRevokeRefund`，缓存同步与重试策略 |
| `web/src/utils/refunds.ts` | 状态文案/色调/筛选器/可申请与可撤销判断/时间格式化 |
| `web/src/pages/account/refund-errors.ts` | 18001/18002/18003/18004/18005/1001/网络错误 → 用户文案 |

### 13.2 组件

| 文件 | 职责 |
| --- | --- |
| `web/src/components/refund/refund-status-badge.tsx` | 退款状态徽标 |
| `web/src/components/refund/refund-card.tsx` | 列表卡片（编号/订单号/金额/原因/状态/时间/详情入口） |
| `web/src/components/refund/apply-refund-dialog.tsx` | 申请入口 + 表单 + 提交/错误反馈/跳转 |
| `web/src/components/refund/revoke-refund-dialog.tsx` | 撤销确认与调用，结果通过 `onResult` 交由详情页持有 |

### 13.3 页面与路由

| 文件 | 职责 |
| --- | --- |
| `web/src/pages/account/refunds-page.tsx` | 退款列表页 |
| `web/src/pages/account/refund-detail-page.tsx` | 退款详情页（含撤销反馈与资金声明） |
| `web/src/pages/account/refund-schema.ts` | 退款原因 zod 校验（对齐 Go） |
| `web/src/pages/account/order-detail-page.tsx` | 新增「退款」联动区域与申请入口 |
| `web/src/pages/account/orders-page.tsx` | 退款状态映射与卡片徽标 |
| `web/src/components/order/order-card.tsx` | 可选退款徽标 |
| `web/src/pages/account/account-layout.tsx` | 侧边栏「我的退款」 |
| `web/src/pages/account/overview-page.tsx` | 账户概览入口 |
| `web/src/components/layout/mobile-nav.tsx` | 移动端入口 |
| `web/src/auth/use-auth.ts` | 登出清理 `orders` + `refunds` 缓存 |
| `web/src/router/index.tsx`、`lazy-pages.tsx` | `/account/refunds`、`/account/refunds/:id` 懒加载路由 |

### 13.4 Mock 与测试

| 文件 | 职责 |
| --- | --- |
| `web/src/mocks/data/refunds.ts` | MSW 退款状态机与测试钩子 |
| `web/src/mocks/handlers.ts` | 4 个退款路由 handler |
| `web/src/test/setup.ts` | 每用例重置退款 Mock |
| `web/src/api/refunds.test.ts` | 11 用例 |
| `web/src/pages/account/refunds-page.test.tsx` | 5 用例 |
| `web/src/pages/account/refund-detail-page.test.tsx` | 17 用例 |
| `web/src/pages/account/refund-actions.test.tsx` | 17 用例 |
| `web/tests/e2e/refunds.spec.ts` | 3 个 e2e 用例（MSW） |

---

## 14. 全部测试结果

执行目录：`web/`

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | 通过（0 error，0 warning） |
| `npm run typecheck` | 通过 |
| `npm run test` | 39 个测试文件 / 276 个用例全部通过 |
| `npm run build` | 通过（含 `refund-detail-page` 独立 chunk） |
| `npm run test:e2e` | 43 个用例全部通过（MSW） |

真实后端联调（独立执行，不属于上述命令）：接口级 48/48 PASS，UI 级 30/30 PASS。

---

## 15. 验收对照

| 验收项 | 证据 |
| --- | --- |
| 退款申请：合法申请 | `refunds.test.ts` 申请成功；`refund-actions.test.tsx` 成功跳转；真实后端 48 项 |
| 退款申请：不允许的订单状态 | `refunds.test.ts` 四种非 paid 状态 18003；`refund-actions.test.tsx` 入口不展示 |
| 退款申请：参数校验 | 空原因、256 字符两个用例 + 后端 1001 |
| 退款申请：重复提交 | `setMockRefundDelay` + 写请求计数 = 1 |
| 退款申请：后端业务错误 | 18003 / 18004 用例 |
| 退款申请：网络异常 | 网络错误文案用例 |
| 退款列表：正常/空/筛选/失败/刷新 | `refunds-page.test.tsx` 5 用例 |
| 退款详情：正常/不存在/无权/非法 ID/状态映射/金额 | `refund-detail-page.test.tsx` 17 用例（7 状态参数化） |
| 撤销：允许/不允许/成功/失败/缓存同步 | `refund-detail-page.test.tsx` 撤销组 4 用例 |
| 业务整合：订单详情跳转申请 | `refund-actions.test.tsx` + e2e + 真实 UI |
| 业务整合：退款详情返回关联订单 | 详情页关联订单链接 + 真实 UI 30 项 |
| 业务整合：未登录保护 | e2e 首/末用例 |
| 业务整合：账号隔离 | `refund-actions.test.tsx` 登出清缓存；`refunds.test.ts` 越权 18001 |
| 业务整合：退出登录缓存清理 | `useLogout` 增加 `orders`/`refunds` + 断言 |
| 开发知识文档 | `.agent/tasks/dev/W4-03-refund-center.md` |
| 正式交付报告 | `docs/reports/w4-03-refund-center/DELIVERY_REPORT.md` |

---

## 16. Git

- 分支：`feat/phase2-user-frontend`
- 提交信息：`feat(w4-03): add user refund center`
- 提交内容：前端代码 + MSW + 测试 + 开发知识文档 + 本报告
- Commit SHA：由执行者在提交后向 Owner 汇报（同一提交内无法写入自身 SHA）
- 工作区：提交后 clean

未执行：修改 main、修改 Go 后端、修改 SQL、修改管理端、修改 Docker/Nginx/K8s、force push、push、合并其他分支。

---

## 17. 已知限制

同开发知识文档第 13 节，摘要：

1. 无真实资金退回（Mock 支付，无渠道/流水/回调/到账时间）；
2. 仅整单全额退款；
3. 用户侧无审计接口，无状态时间轴；
4. 无用户侧审核/执行能力；
5. 退款列表无服务端分页与状态筛选（本地实现）；
6. 退款金额不可编辑、不可由前端计算；
7. 退款成功为终态，不可撤销；
8. 退款不返还优惠券；
9. 订单号映射依赖订单接口，失败时降级为「订单 #id」；
10. 非主动登出（401）只隐藏不清缓存对象，主动登出已显式清理；
11. 无退款凭证上传；
12. 无退款统计接口。

---

## 18. 后续维护提示

- 新增退款状态：同步类型、文案、色调、筛选器、有效状态集合；
- 后端若开放用户侧审计：用真实事件流做时间轴，禁止前端推导；
- 若支持部分退款：金额语义、表单、文案、列表均需重写；
- 若接入真实支付/退款：删除或改写全部「不代表真实资金退回」文案与状态说明；
- 新增账号私有 Query Key（如 W4-04 优惠券）时，必须加入 `useLogout` 清理清单；
- 不要在前端计算退款金额，始终展示 `refund.amount`。
