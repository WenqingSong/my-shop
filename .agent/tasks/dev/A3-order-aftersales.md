# A3 订单与售后管理 — 开发知识文档

> 交付阶段：A3（订单列表、订单详情、发货、退款审核与执行、售后记录）
> 目标分支：`feat/phase2-admin-frontend`
> 依据：最终代码 + 对 Go 后端的只读核查；未修改任何后端文件。

---

## 1. 前端架构

```
admin/src/
├── api/
│   ├── types/
│   │   ├── order.ts          # OrderDTO / AdminOrderListItemDTO / 查询参数
│   │   └── refund.ts         # RefundDTO / RefundAuditDTO / 查询参数
│   ├── modules/
│   │   ├── order.ts          # listOrders / getOrder / shipOrder / findOrderIdByOrderNo
│   │   └── refund.ts         # listRefunds / getRefund / listRefundAudits /
│   │                         # reviewRefund / executeRefund / closeRefund
│   └── adapters/
│       ├── order-adapter.ts  # 状态映射、金额视图、快照行小计、查询参数、发货条件
│       └── refund-adapter.ts # 退款状态机、动作可用性、审计映射、原因校验
├── views/
│   ├── order/
│   │   ├── index.vue                  # /orders
│   │   ├── detail.vue                 # /orders/:id
│   │   └── components/ShipOrderDialog.vue
│   └── refund/
│       ├── index.vue                  # /refunds
│       ├── detail.vue                 # /refunds/:id
│       └── components/RefundActionDialog.vue
├── router/modules/{order,refund}.ts
└── mocks/handlers/{order,refund}.ts + mock-db.ts（扩展订单/退款夹具）
```

调用方向（单向）：页面 → API 模块 → Axios；页面展示字段一律来自 adapter 的视图模型，
页面不拼 URL、不做金额换算、不硬编码状态文案。

路由与菜单：

| 路径 | 路由名 | 菜单 |
| --- | --- | --- |
| `/orders` | OrderList | 订单管理 / 订单列表 |
| `/orders/:id` | OrderDetail | 隐藏（`showLink: false`） |
| `/refunds` | RefundList | 售后管理 / 退款管理 |
| `/refunds/:id` | RefundDetail | 隐藏 |

---

## 2. 后端事实（只读审查结论）

### 2.1 真实接口

| 方法 | 路径 | 权限 |
| --- | --- | --- |
| GET | `/admin/orders` | `order:list` |
| GET | `/admin/orders/stats` | `order:list`（本次未使用） |
| GET | `/admin/orders/:id` | `order:view` |
| POST | `/admin/orders/:id/ship` | `order:ship` |
| GET | `/admin/refunds` | `refund:list` |
| GET | `/admin/refunds/:id` | `refund:view` |
| GET | `/admin/refunds/:id/audits` | `refund:view` |
| POST | `/admin/refunds/:id/review` | `refund:review` |
| POST | `/admin/refunds/:id/execute` | `refund:execute` |
| POST | `/admin/refunds/:id/close` | `refund:execute` |

权限种子来源：`internal/boot/seed.go`（7 个码，见上表）。
没有 `order:update` / `refund:update` / `refund:reject` 独立码；
拒绝退款由 `review` 的 `result=reject` 表达。

### 2.2 后端不存在的能力

- 订单删除、订单取消（管理员侧）、订单状态直接修改、订单操作日志/审计 API。
- 发货参数：`ShipReq` 只有路径 id，**没有**物流公司、运单号、备注字段；
  表结构也没有相应列；没有物流轨迹查询。
- 支付方式、支付流水号：`Order` 结构无相关字段。
- 订单来源 `source`：仅创建请求有，`Order` 出参没有；订单表也没有该列。
- 退款列表的时间范围筛选、订单号筛选（只有 `order_id` / `user_id` / `status`）。
- 退款详情内的订单快照/商品明细（需跨接口组合）。
- 独立售后 / 退货 / 换货模块：退款审计即为售后处理记录。
- 秒杀订单后台查询：`/admin/orders` 只查 `orders` 表。

### 2.3 关键字段

订单金额：`original_amount - discount_amount = total_amount`，全部为 int64 **整数分**。
订单项为自足快照：`product_name / sku_name / product_main_image / price / quantity`。

退款金额：`amount` 为订单 `total_amount` 的快照（服务端派生），整数分；
前端不计算可退金额。

---

## 3. 订单状态机与前端映射

API 出参为字符串枚举，DB 为 TINYINT：

| DB | API | 文案 | 前端标签 | 终态 |
| --- | --- | --- | --- | --- |
| 10 | `pending_payment` | 待支付 | warning | 否 |
| 20 | `paid` | 已支付 | primary | 否 |
| 30 | `shipped` | 已发货 | info | 否 |
| 40 | `received` | 已收货 | success | 否 |
| 50 | `completed` | 已完成 | success | 是 |
| 60 | `cancelled` | 已取消 | info | 是 |
| 70 | `refunded` | 已退款 | danger | 是 |

后台可执行的状态迁移只有一种：**发货 `paid → shipped`**（`POST /admin/orders/:id/ship`）。
其余迁移由用户端（支付/取消/收货）或退款执行（`paid → refunded`）驱动。

前端不提供"改状态"的方法；`api/modules/order.ts` 中没有 PUT。

发货前置条件（后端事务内判断）：

1. 订单当前必须是 `paid`，否则 9002；
2. 不存在有效退款申请（`applying / pending_refund / refund_failed`），否则 18005；
3. 满足后写 `status=shipped` + `shipped_at`。

前端按钮只在 `status === paid` 且持有 `order:ship` 时显示；
18005 无法提前判断，以后端返回为准。

---

## 4. 商品快照展示

- 订单详情使用 `GET /admin/orders/:id` 返回的 `items[]`，字段全部来自 `order_items`。
- 页面**不**调用商品/SKU 接口补齐或覆盖名称、图片、价格。
- 行小计 = `price × quantity`（整数分），只用于展示；订单总额使用 `total_amount`，
  不用行小计求和替代（避免与后端优惠口径不一致）。
- 已通过测试验证：商品改名后，历史订单仍显示下单时的名称。

---

## 5. 普通订单与秒杀订单

- 后台接口只查询 `orders` 表；`AdminStats` 注释也明确"仅作用于 orders 表，
  天然不混入秒杀订单"。
- 秒杀订单在 `flash_sale_orders` 独立表，本次**没有**新增查询，也没有改秒杀链路。
- 因此订单页面展示的是普通订单；不展示"来源"字段（后端不返回），
  也不猜测哪些订单来自秒杀。
- 未实现秒杀订单后台查询，交付报告中标记为 NOT_IMPLEMENTED。

---

## 6. 发货调用链

```
订单列表「发货」/ 订单详情「发货」
  → ShipOrderDialog（展示订单号/金额/状态 + 两条约束说明）
  → 点击确认
  → useSubmitGuard 防重复
  → POST /admin/orders/:id/ship（无请求体）
  → 成功：关闭弹窗 + 刷新列表/详情
  → 失败：保留弹窗，展示后端原文（9002 / 18005 / 9001）
```

弹窗没有输入字段：后端 DTO 为空，虚构物流表单会造成"以为已保存运单号"的误解。

---

## 7. 退款审核与执行

退款状态机（DB TINYINT / API）：

| DB | API | 文案 | 终态 |
| --- | --- | --- | --- |
| 10 | `applying` | 申请中 | 否 |
| 20 | `pending_refund` | 待退款 | 否 |
| 30 | `refund_failed` | 退款失败 | 否 |
| 40 | `review_rejected` | 审核拒绝 | 是 |
| 50 | `revoked` | 已撤销 | 是 |
| 60 | `refund_succeeded` | 退款成功 | 是 |
| 70 | `closed` | 已关闭 | 是 |

前端动作可用性（`getRefundActions`）：

| 状态 | 审核 | 执行成功 | 执行失败 | 关闭 |
| --- | --- | --- | --- | --- |
| applying | ✅ | — | — | — |
| pending_refund | — | ✅ | ✅ | — |
| refund_failed | — | ✅（重试） | — | ✅ |
| 终态 | — | — | — | — |

权限叠加：

- 审核通过/拒绝：`refund:review`
- 执行成功/失败、关闭：`refund:execute`

调用链：

```
退款详情
  → RefundActionDialog（模式：review_approve / review_reject /
                        execute_success / execute_failure / close）
  → 必填原因校验（trim 后非空、≤255 字符）
  → ElMessageBox 二次确认
  → reviewRefund / executeRefund / closeRefund
  → 成功：重新加载退款、审计、关联订单
  → 失败：保留弹窗与原因，展示后端原文（18002 等）
```

关键语义：

- 审核通过只进入 `pending_refund`，**资金未到账**；页面文案明确区分。
- 执行成功：后端同一事务内 `订单 paid→refunded` + 恢复库存 + `退款→60`；
  已成功再次调用是幂等返回。
- 执行失败：仅 `pending_refund → refund_failed`，订单/库存不变。
- 关闭：仅 `refund_failed → closed`，订单保持 `paid`，解除发货阻塞。
- 退款执行是后端"人工 Mock 确认"语义，不接入真实支付渠道。

---

## 8. 订单状态与退款状态的区别

| 维度 | 订单状态 | 退款状态 |
| --- | --- | --- |
| 枚举 | 7 个（pending_payment…refunded） | 7 个（applying…closed） |
| 谁推进 | 用户支付/取消/收货、管理员发货、退款成功 | 用户申请/撤销、管理员审核/执行/关闭 |
| 退款成功后的关系 | 订单变 `refunded`（同时恢复库存） | 退款变 `refund_succeeded` |
| 审核通过 | 订单不变（仍 `paid`） | 退款变 `pending_refund` |
| 审核拒绝/关闭 | 订单不变（仍 `paid`，可发货） | 各自进入终态 |

页面不把两套枚举混用；退款列表只显示退款状态，订单列表只显示订单状态。

---

## 9. API Adapter 与暂定契约

DTO 全部来自 Go 源码核对（`api/order/v1/order.go`、`api/refund/v1/refund.go`），
本次**没有** provisional 字段或假接口。前端"组合能力"如下：

| 能力 | 实现 | 性质 |
| --- | --- | --- |
| 订单列表商品摘要/件数 | 列表 + 逐条 `GET /admin/orders/:id`（并发 4） | 前端组合，失败显示 `—` |
| 退款列表按订单编号搜索 | `GET /admin/orders?order_no=` → 取 id → `GET /admin/refunds?order_id=` | 两步服务端筛选 |
| 退款详情展示关联订单/快照 | `GET /admin/refunds/:id` + `GET /admin/orders/:id` | 跨模块组合，需 order:view |
| 退款列表时间筛选 | 后端无参数，**未实现** | 未做前端假筛选 |
| 秒杀订单查询 | 后端无后台接口，**未实现** | 不猜测 |

`toOrderListQuery` 负责把页面筛选转换为后端参数：空值不发送、用户 id ≤0 不发送、
时间区间转换为带本地时区偏移的 RFC3339（`toRFC3339`），非法区间整体丢弃。

金额：adapter 输出 `*Text` 字段（`formatCentsAsCny`），页面只渲染文本；
不使用浮点数做金额运算。

---

## 10. 核心技术决策

1. **不为订单造状态接口**：订单只有发货一个后台写操作；列表/详情只读。
2. **发货不做物流表单**：后端无字段，弹窗只做确认与约束提示。
3. **退款分阶段**：审核与执行拆成两个动作，文案严格区分"待退款"与"退款成功"。
4. **动作可用性集中在 adapter**：`getRefundActions(status)` 单点维护，
   页面只叠加权限码，避免状态判断散落。
5. **审计前后状态用整数码映射**：`RefundAuditDTO.before_status/after_status` 是
   TINYINT，adapter 统一转文案；不复用 API 字符串枚举。
6. **列表竞态保护**：order/refund 列表与详情均使用请求令牌，只接受最后一次响应。
7. **权限码真实**：`order:list/view/ship`、`refund:list/view/review/execute`；
   没有编造 `order:update` 或 `refund:reject`。

---

## 11. 实际遇到的问题与排查

1. **订单列表缺少商品摘要**：`AdminOrderListItem` 只有 6 个字段。
   选择"按需详情补全"而不是改后端；并发上限 4，失败显示 `—`。
2. **退款列表没有订单号参数**：改为"订单号 → 订单 id → 退款列表"两步查询；
   订单列表接口需 `order:list`，无权限时展示明确错误。
3. **退款列表没有时间筛选**：坚持不做"只过滤当前页"的假筛选，页面提示后端限制。
4. **退款审计状态是整数**：最初想复用字符串状态映射会失败；新增
   `formatRefundStatusByCode` 单独处理 0/10/20/30/40/50/60/70。
5. **菜单图标不显示（A2 遗留）**：`useRenderIcon` 对不含 `:` 的图标走离线注册表，
   A2 的 `ep/goods` 等未在 `offlineIcon.ts` 注册。A3 注册 A2+A3 共 9 个图标，
   一并修复了 A2 菜单图标空白问题。
6. **重复提交**：审核/执行共用 `useSubmitGuard`，请求中重复点击被拦截；
   后端也会以 18002 拒绝重复审核。
7. **业务错误文案**：后端业务错误用 HTTP 400/404/409，Axios 走 error 分支；
   复用 A2 在 `utils/http` 附加的 `businessCode/businessMessage`，
   `resolveErrorMessage` 优先展示后端原文（如"退款与发货冲突"）。

---

## 12. 测试与未验证

已执行：

| 命令 | 结果 |
| --- | --- |
| `pnpm typecheck` | 通过 |
| `pnpm lint:eslint` | 通过（0 error / 0 warning） |
| `pnpm test` | 14 文件 / 217 用例通过（A3 新增 55 个） |
| `pnpm build` | 通过（3.22 MB） |

A3 测试文件：

- `tests/unit/order.test.ts`（29）：列表分页/筛选/时间范围/空数据/400/403、
  详情快照/金额/取消原因/9001、发货成功/9002/18005/重复发货、查询参数、权限码、
  商品改名后快照不变。
- `tests/unit/refund.test.ts`（26）：列表分页/筛选/400/403、详情/审计映射/18001、
  审核通过/拒绝/重复审核/原因校验、执行成功（订单退款 + 库存恢复）/执行失败/
  失败重试/关闭/关闭后不可执行/幂等、动作可用性、权限码。

未验证：

- 真实 Go 后端联调：**NOT_VERIFIED**（本次未启动后端，全部走 MSW）。
- 真实发货：**NOT_VERIFIED**（无后端实例）。
- 真实退款：**NOT_VERIFIED**（后端本身为 Mock 人工确认，未接支付渠道）。
- 浏览器 1366 / 1920 视觉验收：**NOT_VERIFIED**（无图形浏览器环境）。

---

## 13. 后续维护约束

1. 不允许新增"直接修改订单/退款状态"的接口调用或通用 PUT。
2. 不允许把订单状态与退款状态合并成一套枚举。
3. 发货接口新增物流字段时，只改 `ShipOrderDialog` + DTO，不改变状态机调用。
4. 退款审核与执行必须保持两阶段；禁止把"审核通过"描述成资金到账。
5. 退款原因/审核意见在前后端都必填且 ≤255 字符；修改长度需同时改后端。
6. 订单商品信息只能来自 `order_items` 快照，不得回查当前商品/SKU。
7. 金额保持整数分；新增字段若为分，继续在 adapter 输出文本。
8. 秒杀订单如需在后台展示，必须新增独立接口；不要在现有订单页混入。
9. `/admin/me` 若开始返回 `permissions[]`，`ROLE_PERMISSION_MAP` 会自动失效，
   届时删除本地角色映射即可。
