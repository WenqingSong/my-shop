# W4-04 用户优惠券中心 交付报告

- 任务：SurgeCart 用户优惠券中心（W4-04，W4 最后一个业务开发任务）
- 执行模型：DeepSeek-V4.1-Flash
- 分支：`feat/phase2-user-frontend`
- 日期：2026-11-10
- 范围：我的优惠券、结算页优惠券选择、用券下单、状态同步、与订单/退款中心整合
- 明确不在范围：Go 后端、SQL、管理端券管理、Docker/Nginx/K8s、秒杀/拼团/砍价、W5 最终验收

---

## 1. 实际完成的页面与功能

| 交付项 | 路由 / 位置 | 说明 |
| --- | --- | --- |
| 我的优惠券 | `/account/coupons` | 卡片展示券名、减免金额、使用门槛、有效期、状态、领取/锁定/使用时间、关联订单；全部 + 3 状态本地筛选；Loading / Empty / Error / 刷新；状态说明与能力说明 |
| 结算页优惠券选择 | `/checkout` 优惠券区域 | 展示后端返回的全部券；默认「不使用」；可选券可选、不可选券禁用并给出原因；显示预估优惠；明确声明以服务端为准 |
| 用券下单 | `/checkout` → `POST /orders` | 请求携带真实 `coupon_id`；金额完全由服务端计算；成功后跳成功页 |
| 下单成功金额展示 | `/checkout/success` | 展示服务端 `original_amount` / `discount_amount` / `total_amount` 与 `user_coupon_id` |
| 优惠券状态同步 | 下单 / 支付 / 取消 / 登出 | 下单 locked、支付 used、取消 available、登出清缓存；全部通过 TanStack Query 失效 |
| 订单/退款整合 | 券卡片「查看关联订单」 | 券可跳转占用/核销它的订单；退款成功不返还券（文案明确） |
| 个人中心整合 | 侧栏 / 概览 / 移动端导航 | 新增「我的优惠券」入口 |

所有 `/account/*` 页面继续使用现有 `RequireAuth`；`/account/coupons` 使用路由懒加载。

---

## 2. 使用的 API（Method + Path）

事实来源：`api/coupon/v1/coupon.go`、`internal/cmd/routes_frontend.go:141-142`、`internal/cmd/routes_admin.go:185-187`。

| # | Method | Path | 使用方 | 本期是否接入 |
| --- | --- | --- | --- | --- |
| 1 | GET | `/my/coupons` | 用户端 | ✅ 我的优惠券 + 结算页候选 |
| 2 | POST | `/coupons/:template_id/claim` | 用户端 | ❌ 无合法 template_id 来源，本期不接入 |
| 3 | GET | `/admin/coupons` | 管理端 | ❌ 需 `coupon:list`，普通用户 403 |
| 4 | POST | `/admin/coupons` | 管理端 | ❌ 仅联调造数使用（非前端代码） |
| 5 | POST | `/admin/coupons/:id/stop` | 管理端 | ❌ 不需要 |

订单创建（既有关键接口，本次新增 `coupon_id` 传参）：

| Method | Path | 请求新增字段 |
| --- | --- | --- |
| POST | `/orders` | `coupon_id?: int64`（0/缺省 = 不使用；<0 → 1001） |

---

## 3. 请求与响应结构

### 3.1 `GET /my/coupons`

```json
// 响应 data（本人，按 id 倒序，无分页）
{
  "items": [
    {
      "id": 3,
      "template_id": 2,
      "status": "locked",
      "order_id": 4,
      "received_at": "2026-11-10 19:08:00",
      "locked_at": "2026-11-10 19:10:12",
      "used_at": null,
      "name": "满减券B08965658",
      "threshold_amount": 0,
      "discount_amount": 30000,
      "use_start_time": "2026-01-01 00:00:00",
      "use_end_time": "2027-01-01 00:00:00"
    }
  ]
}
```

### 3.2 `POST /orders`（用券）

```json
{
  "source": "cart",
  "address_id": 1,
  "idempotency_key": "8f1c...",
  "coupon_id": 3
}
```

```json
// 响应 data（节选，真实后端实测）
{
  "id": 9,
  "status": "pending_payment",
  "original_amount": 429900,
  "discount_amount": 30000,
  "total_amount": 399900,
  "user_coupon_id": 3
}
```

### 3.3 `POST /coupons/:template_id/claim`（未接入，仅记录契约）

```json
// 响应 data：UserCoupon（status=available）
// 错误：19001 模板不存在(404)、19004 已领取(409)、19005 暂不可领取(409)、19006 已领完(409)
```

---

## 4. 优惠券真实状态枚举

| API 字符串 | DB | 中文 | 触发 |
| --- | --- | --- | --- |
| `available` | 10 | 可使用 | 领取；取消/超时取消释放 |
| `locked` | 20 | 已锁定 | 下单事务预占 |
| `used` | 30 | 已使用 | 支付事务核销 |

**后端没有 `expired` 状态**。前端状态筛选严格只有「全部 + 可使用 + 已锁定 + 已使用」，未新增「已过期」。

---

## 5. 领取能力及限制

- 后端存在 `POST /coupons/:template_id/claim`；
- **不存在面向普通用户的公开券模板列表**：`/admin/coupons` 需要 `coupon:list`，普通用户 403；
- 因此前端无法获得合法 `template_id`。

本期决策：**不实现领券中心**。

- 未调用任何管理员接口；
- 未硬编码券 ID；
- 未虚构活动或券数据；
- 未把「我的优惠券」当作可领取列表；
- 未提供「手动输入券 ID」这类伪功能。

页面明确说明：「本期不提供领券中心：后端没有面向用户的公开优惠券模板列表接口」。

---

## 6. 优惠券选择和使用流程

```
结算页 → useCouponList(GET /my/coupons)
       → 前端估算（状态 available / 有效期 / 门槛）
       → 用户选择某张券或「不使用」
       → buildOrderSignature(addressId, lines, couponId)
       → POST /orders { coupon_id }
       → 服务端 GetDiscount 计算优惠 + LockInTx 预占
       → 成功页展示服务端 original/discount/total/user_coupon_id
```

- 默认不使用优惠券；
- 不可选券不隐藏，禁用并展示原因（已被占用 / 已使用 / 未到开始时间 / 已过有效期 / 未达门槛）；
- 选中券在列表刷新后若变为不可选，自动回落到「不使用」；
- 后端拒绝（19003/19007）时提示、失效券缓存、回落到「不使用」，用户可直接重新提交（新幂等键）。

---

## 7. 服务端金额计算机制

`internal/logic/order/order.go:152-178` + `internal/logic/coupon/coupon.go:274-315`：

```
original_amount = Σ(skus.price × quantity)     # 服务端定价
discount_amount = min(coupon.discount_amount, original_amount)   # 需通过归属/有效期/门槛校验
total_amount    = original_amount - discount_amount
```

真实后端实测：

```
商品 ¥4,299.00 + 券面额 ¥500.00 → original=429900, discount=50000, total=379900
封顶：券面额 > 商品金额 → discount=429900, total=0
```

前端不提交金额、不参与计算、不可编辑。

---

## 8. 前端适用性判断的边界

| 判断 | 依据 | 边界 |
| --- | --- | --- |
| 券状态 | 后端 `status` | 服务端 `LockInTx` 才是权威闸门 |
| 有效期 | 后端 `use_start_time`/`use_end_time` + 浏览器时间 | 服务端用 SQL `NOW()` |
| 门槛 | 购物车预估金额 vs `threshold_amount` | 服务端用订单 `original_amount` |
| 预估优惠 | `min(discount_amount, 预估金额)` | 最终以响应 `discount_amount` 为准 |
| 适用范围 | **后端无此字段** | 平台通用券，不做范围判断 |

UI 明确声明：「优惠券适用性及最终优惠金额以服务端下单校验结果为准。」
摘要区只展示「优惠（预估）」「应付金额（预估）」；成功页才展示服务端正式金额。

---

## 9. 幂等键调整情况

原有实现 `buildOrderSignature(addressId, lines)` 固定 `coupon_id = 0`。

本次修复：

```ts
buildOrderSignature(addressId, lines, couponId = 0)
// → `cart|${addressId}|${couponId}|${sku:qty,...}`（与后端 requestHash 完全一致）
```

`useCreateOrder` 的 key 复用逻辑改为按新 signature 判断：

| 场景 | 幂等键 |
| --- | --- |
| 同地址 + 同商品 + 同券重试（含网络超时） | 复用 |
| 换券 | 新键 |
| 取消用券 | 新键 |
| 换地址 / 商品变化 | 新键 |

真实后端实测：同键同内容重试返回既有订单；同键不同券返回 `9006/409`。

---

## 10. 优惠券状态流转

```
领取 → available
       ├── 下单（事务内预占） → locked
       │      ├── 支付（事务内核销） → used（终态，退款不返还）
       │      └── 取消/超时取消（事务内释放） → available
       └── 未使用（超出有效期后服务端下单返回 19007，但状态仍 available）
```

---

## 11. 下单、取消、退款后的状态同步

| 动作 | 服务端券状态 | 前端缓存动作 |
| --- | --- | --- |
| 下单成功 | available → locked | 失效 `coupons.all` + 订单列表 + 购物车 |
| 下单失败 19003/19007/9006 | 不变 | 失效 `coupons.all`，回落「不使用」 |
| 支付成功 | locked → used | 失效 `coupons.all` + 订单详情/列表 |
| 取消订单成功 | locked → available | 失效 `coupons.all` + 订单详情/列表 |
| 退款申请/撤销 | 不变 | 无（退款不改变券） |
| 退款成功（后台） | 保持 used | 无强刷；用户进入券页时按 staleTime 重新拉取 |
| 退出登录 | — | `removeQueries(coupons.all)` |

---

## 12. 真实后端联调结果（非 Mock）

### 12.1 环境

- MySQL 8.0（独立容器 `127.0.0.1:3307`，全新库，`migrate up` 到 `20261001000024`）
- Redis 7（`127.0.0.1:6380`，DB 9）
- Go 服务：`go build -tags serve_lifecycle ./test/serveproc` + `test/container/qiniu_mock`（未修改任何 Go 代码）
- 前端：`vite` dev（5174）+ `/api` 代理到真实后端 8000，未设置 `VITE_ENABLE_MOCK`
- 浏览器：Playwright Chromium 真实交互

### 12.2 接口级：35 / 35 PASS

| 分类 | 验证项 |
| --- | --- |
| 鉴权 | 未登录 `/my/coupons` 401 |
| 领取 | 领取成功 available；我的券包含该券与模板快照；重复领取 19004/409 |
| 用券下单 | original=429900、discount=50000、total=379900、user_coupon_id=1；券 locked 且绑定订单 |
| 幂等 | 同键同内容返回既有订单；同键不同券 9006/409 |
| 支付 | 券 used + used_at |
| 取消 | 券恢复 available、`order_id`/`locked_at` 清空 |
| 退款 | 申请 + 审核通过 + 执行成功 → 订单 refunded，券仍 used 且关联原订单（不返还） |
| 错误分支 | 未达门槛 19007；过期券 19007；他人券 19003；他人券列表为空 |

### 12.3 UI 级：32 / 32 PASS

| 分类 | 验证项 |
| --- | --- |
| 鉴权 | 未登录跳登录 |
| 我的优惠券 | 减免金额、门槛、有效期、状态说明、已使用/可使用券展示；「可使用」筛选生效 |
| 结算页 | 声明以服务端为准；默认不使用；高门槛券禁用；过期券禁用；可选券可选 |
| 预估金额 | 优惠（预估）¥300.00；应付（预估）¥3,999.00 |
| 成功页 | 商品总额 ¥4,299.00；优惠 ¥300.00；应付 ¥3,999.00；标注本单使用优惠券 |
| 锁定 | 下单后券「已锁定」并展示关联订单入口 |
| 取消 | 取消订单后券恢复「可使用」，关联入口消失 |
| 核销 | 再次下单并模拟支付后券「已使用」并展示使用时间 |
| 稳定性 | 无未捕获异常、无非预期控制台错误 |

### 12.4 联调数据库终态

```
user_coupons: 1=used(order1) 3=used(order9) 4=used(order5 退款订单) 5=available(高门槛) 6=available(过期)
orders:       1=paid 4=cancelled 5=refunded 7=cancelled 8=cancelled 9=paid
inventories:  sku 1 quantity=96
```

---

## 13. Mock 测试结果（与真实联调严格区分）

| 层 | 环境 | 规模 | 结果 |
| --- | --- | --- | --- |
| 单元 / 组件 | Vitest + jsdom + MSW | 43 文件 / 314 用例 | 全部通过 |
| e2e | Playwright + `VITE_ENABLE_MOCK=true`（MSW） | 46 用例（新增 3 个优惠券用例） | 全部通过 |

Mock 侧新增能力（仅测试/本地 Mock，真实后端没有）：

- `src/mocks/data/coupons.ts`：券状态机、`seedMockCoupon`（构造任意状态）、`expireMockCoupon`（构造过期）、锁/核销/释放；
- 下单 Mock 支持 `coupon_id`：归属/有效期/门槛校验、`min(减免, 原价)`、幂等指纹含券 id。

E2E 覆盖限制说明：后端没有公开模板列表、前端没有领券入口，浏览器侧无法在纯 UI 流程中造出优惠券，因此 E2E 只覆盖空状态、能力说明、无券下单与成功页金额；有券流程由组件测试与真实后端联调覆盖。

**声明**：e2e 使用 MSW，不代表真实后端；真实后端结果见第 12 节。

---

## 14. 后端尚未支持的能力

| 能力 | 现状 | 前端处理 |
| --- | --- | --- |
| 公开可领取券模板列表 | 不存在（仅 `/admin/coupons`） | 不做领券中心，页面说明原因 |
| 订单可用券查询 / 优惠试算 | 不存在 | 只做前端估算，明确以服务端为准 |
| `expired` 状态 | 不存在 | 只展示有效期，不伪造状态 |
| 商品/分类适用范围 | 不存在（平台通用券） | 不展示范围，不做范围判断 |
| 折扣率券 / 品类券 / 运费券 | 不存在 | 只支持固定金额满减 |
| 券码兑换 | 不存在 | 不提供输入券码 |
| 退款返券 | 不支持（used 终态） | 文案明确「不返还」 |
| 我的券分页 / 状态筛选 | 不存在 | 本地筛选 |

---

## 15. 关键文件及职责

### 15.1 数据层

| 文件 | 职责 |
| --- | --- |
| `web/src/api/coupons.ts` | `UserCoupon`/状态枚举、`fetchMyCoupons`、领取能力边界说明 |
| `web/src/api/query-keys.ts` | 新增 `coupons` key |
| `web/src/hooks/use-coupons.ts` | `useCouponList`（登录态、staleTime、重试） |
| `web/src/utils/coupons.ts` | 状态文案/色调/筛选、有效期与门槛文案、结算页估算与排序 |
| `web/src/utils/idempotency.ts` | `buildOrderSignature` 加入 `couponId`（与后端 requestHash 对齐） |

### 15.2 组件与页面

| 文件 | 职责 |
| --- | --- |
| `web/src/components/coupon/coupon-status-badge.tsx` | 状态徽标（`data-coupon-status`） |
| `web/src/components/coupon/coupon-card.tsx` | 我的优惠券卡片 |
| `web/src/pages/account/coupons-page.tsx` | `/account/coupons` |
| `web/src/components/checkout/checkout-coupon-section.tsx` | 结算页选券 |
| `web/src/components/checkout/checkout-summary.tsx` | 预估优惠与预估应付 |
| `web/src/pages/checkout-page.tsx` | 选券状态、签名组装、19007 回落 |
| `web/src/hooks/use-checkout.ts` | 下单 mutation、幂等键复用、券缓存失效 |
| `web/src/hooks/use-orders.ts` | 支付/取消后失效券缓存 |
| `web/src/pages/checkout-success-page.tsx` | 服务端三项金额与券 id |
| `web/src/pages/account/account-layout.tsx` | 侧栏入口 |
| `web/src/pages/account/overview-page.tsx` | 概览入口 |
| `web/src/components/layout/mobile-nav.tsx` | 移动端入口 |
| `web/src/auth/use-auth.ts` | 登出清理券缓存 |
| `web/src/router/index.tsx`、`lazy-pages.tsx` | `/account/coupons` 懒加载路由 |
| `web/src/pages/checkout-errors.ts` | 19003/19007 文案与 `isCouponRejected` |

### 15.3 Mock 与测试

| 文件 | 职责 |
| --- | --- |
| `web/src/mocks/data/coupons.ts` | MSW 券状态机与测试钩子 |
| `web/src/mocks/data/orders.ts` | 下单用券、支付核销、取消释放、指纹含券 |
| `web/src/mocks/handlers.ts` | `GET /my/coupons` handler |
| `web/src/test/setup.ts` | 每用例重置券 Mock |
| `web/src/api/coupons.test.ts` | 10 用例 |
| `web/src/utils/idempotency.test.ts` | 8 用例 |
| `web/src/pages/account/coupons-page.test.tsx` | 10 用例 |
| `web/src/pages/checkout-coupon.test.tsx` | 10 用例 |
| `web/tests/e2e/coupons.spec.ts` | 3 个 e2e 用例（MSW） |

---

## 16. 测试结果

执行目录：`web/`

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | 通过（0 error，0 warning） |
| `npm run typecheck` | 通过 |
| `npm run test` | 43 个测试文件 / 314 个用例全部通过 |
| `npm run build` | 通过 |
| `npm run test:e2e` | 46 个用例全部通过（MSW） |

真实后端联调（独立执行，不在上述命令内）：接口级 35/35 PASS，UI 级 32/32 PASS。

---

## 17. 验收对照

| 验收项 | 证据 |
| --- | --- |
| 我的优惠券：正常列表 | `coupons-page.test.tsx` 字段用例 + 真实 UI |
| 我的优惠券：空列表 | 组件用例 + E2E |
| 我的优惠券：各状态展示 | 3 状态参数化用例 + 真实 UI（used/available） |
| 我的优惠券：本地状态筛选 | 组件用例 + E2E + 真实 UI |
| 我的优惠券：加载失败 | 错误态 + 重试用例 |
| 我的优惠券：有效期展示 | 字段断言 + 真实 UI |
| 我的优惠券：账号隔离 | 组件用例 + API 用例 + 登出清缓存用例 |
| 结算页：展示可选券 | `checkout-coupon.test.tsx` + 真实 UI |
| 结算页：不选择/选择/更换/取消 | 4 个组件用例 + 真实 UI |
| 结算页：无效券 | 锁定/已使用用例 |
| 结算页：不满足门槛 | 组件用例 + 真实 UI（高门槛券禁用） |
| 结算页：商品范围不匹配 | 后端无范围字段，不适用（文档说明） |
| 结算页：后端拒绝优惠券 | 19007 用例 + 真实后端 19007 |
| 结算页：成功下单后的优惠金额 | 成功页三项金额用例 + 真实 UI |
| 幂等：同内容复用 | `idempotency.test.ts` + API 用例 + 真实后端 |
| 幂等：换券生成新键 | 组件用例 + 指纹用例 + 真实后端 9006 |
| 幂等：取消用券生成新键 | 组件用例 + 指纹用例 |
| 幂等：网络重试复用 | `checkout-page.test.tsx` 既有用例（同内容） |
| 幂等：不同内容不共用 | 指纹用例（换券/换地址/换商品） |
| 状态同步：下单锁定 | 组件用例 + 真实 UI |
| 状态同步：取消恢复 | 组件用例 + 真实后端 + 真实 UI |
| 状态同步：退款不返还 | API 用例 + 真实后端（执行成功后退款，券仍 used） |
| 状态同步：登出清缓存 | 组件用例 |
| 开发知识文档 | `.agent/tasks/dev/W4-04-coupon-center.md` |
| 正式交付报告 | `docs/reports/w4-04-coupon-center/DELIVERY_REPORT.md` |

---

## 18. Git

- 分支：`feat/phase2-user-frontend`
- 提交信息：`feat(w4-04): add user coupon center`
- 提交内容：前端代码 + MSW + 测试 + 开发知识文档 + 本报告
- Commit SHA：由执行者在提交后向 Owner 汇报（同一提交内无法写入自身 SHA）
- 工作区：提交后 clean

未执行：修改 main、修改 Go 后端、修改 SQL、修改管理端、修改 Docker/Nginx/K8s、force push、push、合并其他分支。

---

## 19. 已知限制

同开发知识文档第 16 节，摘要：

1. 无领券中心（后端无公开模板列表）；
2. 无订单可用券查询/试算，适用性为前端估算；
3. 无 `expired` 状态，过期由服务端拒绝；
4. 无商品/分类适用范围；
5. 仅固定金额满减券；
6. 一人一模板一张；
7. 退款不返还券；
8. 我的券无分页与状态筛选（本地实现）；
9. 结算页预估金额可能与最终订单金额存在差异；
10. 无券码兑换。

---

## 20. 后续维护提示

- 新增券状态：同步类型、文案、色调、筛选器、估算逻辑；
- 后端若开放公开模板列表：新增模板查询与领取，复用 `CouponCard`，领取后失效 `coupons.all`；
- 后端若提供可用券/试算接口：替换前端估算，删除降级文案；
- 若支持适用范围：扩展 DTO 与估算判据；
- 若退款改为返券：同步缓存失效与全部相关文案；
- 新增账号私有 Query Key：必须加入 `useLogout` 清理；
- 切勿只改前端指纹而不同步后端 `requestHash`（会导致 9006 或错误命中）。
