# W2-02 个人中心、收货地址与商品收藏：交付报告

- 任务：SurgeCart 用户商城前端 W2-02（个人中心、收货地址与商品收藏）
- 分支：`feat/phase2-user-frontend`
- 执行模型：DeepSeek-V4.1-Flash
- 交付日期：2026-11-10
- 开发知识文档：`.agent/tasks/dev/W2-02-user-center.md`

---

## 0. 摘要

本期在 W2-01 认证体系上完成用户中心：

- `/account` 嵌套路由布局与账户概览；
- 只读个人资料（后端只有 `GET /me`，无修改接口）；
- 收货地址完整 CRUD、默认地址切换；
- 商品收藏列表、详情页收藏/取消、收藏状态与缓存同步；
- 登录保护、空/加载/错误状态、退出登录用户缓存清理。

接口全部来自真实 Go 源码，未修改后端、未修改 SQL/Docker/Nginx/K8s 配置。
已执行全部质量门禁，并完成真实后端联调（接口 + 浏览器）。

---

## 1. 执行前检查

| 检查项 | 结果 |
| --- | --- |
| 分支 | `feat/phase2-user-frontend` |
| W2-01 `ea0b025` | 在当前 HEAD 历史中 |
| 初始工作区 | clean |
| W0/W1-01 提交 | 均在历史中 |
| `contracts/openapi.yaml` | 不存在 |
| 已有 W2-02 文档 | 不存在，未覆盖 |
| 地址 API | 存在，完整 CRUD + 默认地址 |
| 收藏 API | 存在，Add/Remove/List/Check |
| 用户资料修改 API | 不存在，仅 `GET /me` |

---

## 2. 实际完成的页面与功能

| 路由 | 功能 |
| --- | --- |
| `/account` | 账户概览：用户名、用户 ID、退出登录、个人资料/地址/收藏入口 |
| `/account/profile` | 只读资料：用户 ID、用户名；明确说明无资料修改接口 |
| `/account/addresses` | 地址列表、新增、编辑、删除、设为默认、表单校验、成功/失败提示、空/错/加载状态 |
| `/account/favorites` | 收藏列表、商品可用性、商品详情跳转、取消收藏、分页、空/错/加载状态 |
| `/products/:id` | 新增收藏按钮：未登录跳登录、已登录收藏/取消、状态来自服务端 |

其他：

- `/account` 整个分支由 `RequireAuth` 保护；未登录跳 `/login` 并记录来源，
  登录后回到原目标（含子路由）。
- 个人中心导航桌面侧栏 / 移动端横向标签。
- 地址/资料/收藏页面按路由懒加载。
- 退出登录或会话失效时清理地址、收藏、用户缓存，避免跨账号数据泄漏。

---

## 3. 使用的 API Method + Path

| 功能 | Method + Path | 鉴权 |
| --- | --- | --- |
| 当前用户 | `GET /me` | Bearer |
| 地址列表 | `GET /addresses` | Bearer |
| 新增地址 | `POST /addresses` | Bearer |
| 更新地址 | `PUT /addresses/:id` | Bearer |
| 删除地址 | `DELETE /addresses/:id` | Bearer |
| 收藏列表 | `GET /favorites?page=&size=` | Bearer |
| 添加收藏 | `POST /favorites` | Bearer |
| 取消收藏 | `DELETE /favorites/:product_id` | Bearer |
| 收藏状态 | `GET /favorites/check?product_id=` | Bearer |

前端继续通过 `/api` 前缀访问，转发层剥离（开发环境 Vite rewrite）。

---

## 4. 请求参数与响应结构

统一响应包装：`{code:number,message:string,data:T}`；错误为非 2xx + `data:null`。

### 4.1 `GET /me`

无参数；`data`：

```json
{ "id": 1, "username": "realuser2" }
```

### 4.2 `GET /addresses`

无参数；`data`：

```json
{
  "items": [
    {
      "id": 1,
      "recipient_name": "张三",
      "phone": "13800138000",
      "province": "广东省",
      "city": "深圳市",
      "district": "南山区",
      "detail": "科技园路 3 号 303 室",
      "is_default": true,
      "created_at": "2026-11-10 05:02:25",
      "updated_at": "2026-11-10 05:02:25"
    }
  ]
}
```

### 4.3 `POST /addresses`

请求：

```json
{
  "recipient_name": "张三",
  "phone": "13800138000",
  "province": "广东省",
  "city": "深圳市",
  "district": "南山区",
  "detail": "科技园路 1 号 101 室",
  "is_default": false
}
```

响应 `data`：创建后的完整 `Address`。

- 首条地址由后端自动设为默认；
- 非首条 `is_default=true` 时后端事务取消旧默认；
- 参数错误 400/1001；并发默认冲突 409/7002。

### 4.4 `PUT /addresses/:id`

请求字段全部可选：

```json
{
  "recipient_name": "张三",
  "phone": "13800138000",
  "province": "广东省",
  "city": "深圳市",
  "district": "南山区",
  "detail": "科技园路 3 号 303 室",
  "is_default": true
}
```

- 只提交 `{ "is_default": true }` 也可设置默认，后端保留其它字段；
- 不存在/非本人 404/7001；默认冲突 409/7002。

### 4.5 `DELETE /addresses/:id`

无 body；成功 `data: {}`；不存在/非本人 404/7001。

### 4.6 `GET /favorites`

参数：`page`（默认 1）、`size`（默认 20，最大 100；前端 10）。

```json
{
  "items": [
    {
      "id": 2,
      "product_id": 2,
      "product_name": "云听 Air 3 主动降噪耳机",
      "product_main_image": "/storage/banners/banner-2.png",
      "product_price": 89900,
      "available": true,
      "unavailable_reason": "",
      "created_at": "2026-11-10 05:10:00"
    }
  ],
  "total": 1,
  "page": 1,
  "size": 10
}
```

- 已下架：`available=false`、`unavailable_reason="off_shelf"`；
- 商品删除：`product_name/main_image` 为空、`product_price=null`、
  `unavailable_reason="product_deleted"`。

### 4.7 `POST /favorites`

请求：

```json
{ "product_id": 1 }
```

响应 `data`：`FavoriteItem`。重复收藏幂等；商品不存在 404/4001；
商品不在售 409/11001。

### 4.8 `DELETE /favorites/:product_id`

成功 `data: {}`；未收藏也幂等成功。

### 4.9 `GET /favorites/check?product_id=`

```json
{ "favorited": true }
```

参数缺失/≤0 返回 400/1001；未登录 401/1002。

---

## 5. 对应 Go 源码位置

| 能力 | 源码 |
| --- | --- |
| 地址契约 | `api/address/v1/address.go` |
| 地址逻辑（默认地址事务、校验、7001/7002） | `internal/logic/address/address.go` |
| 地址控制器 | `internal/controller/address/address.go` |
| 收藏契约 | `api/favorite/v1/favorite.go` |
| 收藏逻辑（实时联查、可用性、11001、幂等） | `internal/logic/favorite/favorite.go` |
| 收藏控制器 | `internal/controller/favorite/favorite.go` |
| 路由与 Auth 分组 | `internal/cmd/routes_frontend.go` |
| 当前用户 | `api/iam/v1/iam.go`、`internal/logic/iam/iam.go` |
| 错误码 | `internal/codes/codes.go` |

仓库仍无 OpenAPI 契约；前端 DTO 定义在 `web/src/api/types.ts`，字段与 JSON tag 对齐。

---

## 6. 真实后端联调与 Mock 验证

### 6.1 真实后端联调

环境：临时 MySQL/Redis + 仓库 Go 后端（migrate + serve）；
七牛检查使用仓库 `test/serveproc` + `test/container/qiniu_mock` 仅重定向 UC endpoint，
生产二进制不读取该变量；Vite 不启用 MSW。

接口级结果：

| 场景 | 结果 |
| --- | --- |
| `GET /addresses` 空列表 | 200，`items: []` |
| 新增首条地址 | 200，`is_default=true` |
| 新增第二条并设默认 | 200，第二条默认；第一条自动变 false |
| 仅提交 `{is_default:true}` | 200，默认切换成功，其它字段保留 |
| 更新详细地址 | 200，字段更新且默认状态不变 |
| 非法手机号 | 400 / 1001 |
| 删除地址 | 200，`data: {}` |
| 删除不存在地址 | 404 / 7001 |
| 添加收藏 | 200，返回商品快照，`available=true` |
| 重复添加收藏 | 200，列表 total 仍为 1（幂等） |
| `GET /favorites/check` | 200，`{favorited:true}` |
| 收藏下架商品 | 409 / 11001 |
| 取消收藏 | 200；check 变为 false |
| 收藏列表含下架商品 | 200，`available=false`、`unavailable_reason=off_shelf` |

说明：为验证「先收藏后下架」的列表语义，在临时数据库直接插入一条下架商品收藏，
验证后已删除数据卷；`POST /favorites` 对下架商品的拒绝已单独验证。

浏览器级结果（真实后端，12/12 通过）：

- 登录后 Header/账户概览；
- 个人资料只读、无编辑输入框；
- 地址列表、编辑、新增并设默认、删除；
- 收藏列表、下架商品提示、详情页已收藏态、取消收藏、列表同步；
- 商品详情新增收藏、收藏列表出现新商品；
- 无浏览器 console 错误。

### 6.2 仅 Mock 验证

- `npm run test`（Vitest + MSW）：26 个文件、119 条用例；
- `npm run test:e2e`：29 条 Playwright 用例（W1 商品 19 + W2-01 认证 6 + W2-02 个人中心 4）；
- 地址 7002 默认并发冲突；
- 收藏商品删除（`product_deleted`）展示；
- 空状态、500 错误状态、分页边界、移动端布局；
- 所有自动化用例均使用 MSW 内存数据，**不是真实后端联调**。

---

## 7. 后端尚未支持的能力

| 缺口 | 影响 |
| --- | --- |
| 无资料修改接口（仅 `GET /me`） | 资料页只能只读，无法修改用户名/昵称/头像/手机号/邮箱 |
| 地址省/市/区为自由文本 | 无行政区划选择器/校验码表 |
| 无地址数量上限 | 极端用户可无限新增，列表靠前端分页/滚动 |
| 删除默认地址不自动提升 | 允许“无默认”，前端不伪造默认 |
| 收藏仅 SPU 粒度 | 无 SKU 收藏 |
| 收藏无分组/备注/批量清空 | 仅单条取消 |
| 收藏列表无“被收藏次数”等统计 | 无法展示商品热度 |
| 无商品卡片收藏态批量接口 | 未做卡片收藏按钮，避免 N+1 |
| 无用户中心统计接口 | 概览不展示地址/收藏数量，避免额外请求 |
| 无 HttpOnly Cookie | 登录仍为内存 Token，刷新页面需重新登录（W2-01 已知限制） |

---

## 8. 关键文件及职责

### 8.1 新增

| 文件 | 职责 |
| --- | --- |
| `web/src/pages/account/account-layout.tsx` | 个人中心布局与导航 |
| `web/src/pages/account/overview-page.tsx` | 概览、用户信息、入口卡片 |
| `web/src/pages/account/profile-page.tsx` | 只读资料 |
| `web/src/pages/account/addresses-page.tsx` | 地址列表、默认、删除、反馈 |
| `web/src/pages/account/address-schema.ts` | 地址 Zod 校验 |
| `web/src/pages/account/account-errors.ts` | 地址/收藏错误文案 |
| `web/src/pages/account/favorites-page.tsx` | 收藏列表、乐观取消 |
| `web/src/components/account/address-form-dialog.tsx` | 地址新增/编辑弹窗 |
| `web/src/components/product/favorite-button.tsx` | 详情页收藏按钮 |
| `web/src/components/ui/textarea.tsx` | 多行地址输入 |
| `web/src/hooks/use-favorites.ts` | 收藏态查询与乐观切换 |
| `web/src/api/addresses.ts` | 地址 API |
| `web/src/api/favorites.ts` | 收藏 API |
| `web/src/auth/use-auth-cache-reset.ts` | 会话结束清理用户缓存 |
| `web/src/mocks/data/addresses.ts` | MSW 地址状态 |
| `web/src/mocks/data/favorites.ts` | MSW 收藏状态与不可用模拟 |
| `web/src/test/auth.ts` | 测试登录辅助 |
| `web/tests/e2e/account.spec.ts` | 个人中心 E2E |

### 8.2 修改

| 文件 | 变更 |
| --- | --- |
| `web/src/api/envelope.ts` | 新增 `putData`、`deleteData` |
| `web/src/api/auth-request.ts` | 新增 `authPutData`、`authDeleteData` |
| `web/src/api/query-keys.ts` | 新增地址/收藏 key |
| `web/src/api/types.ts` | 新增 `Address`、`FavoriteItem` |
| `web/src/auth/use-auth.ts` | 登出清理地址/收藏缓存 |
| `web/src/layouts/root-layout.tsx` | 挂载会话缓存清理 |
| `web/src/router/index.tsx` | `/account/*` 嵌套路由与 Guard |
| `web/src/router/lazy-pages.tsx` | 个人中心子页面懒加载 |
| `web/src/pages/product-detail-page.tsx` | 接入收藏按钮 |
| `web/src/pages/product-detail-page.test.tsx` | 收藏态/缓存/登录回跳测试 |
| `web/src/mocks/handlers.ts` | 地址/收藏 MSW 接口 |
| `web/src/test/setup.ts` | 每用例重置地址/收藏 Mock |
| `web/tests/e2e/product-detail.spec.ts` | 适配新增收藏入口文案 |
| `web/README.md` | W2-02 使用说明 |

### 8.3 删除

- `web/src/pages/account-page.tsx`（拆分为 account 布局与子页面）
- `web/src/pages/account-page.test.tsx`（迁移为 `account/overview-page.test.tsx`）

---

## 9. 测试命令与真实执行结果

执行目录：`/root/projects/my-shop/web`

| 命令 | 结果 |
| --- | --- |
| `npm run lint` | 通过，0 error / 0 warning |
| `npm run typecheck` | 通过 |
| `npm run test` | 26 个文件、119 条用例全部通过 |
| `npm run build` | 通过；地址/收藏/资料/概览独立 chunk |
| `npm run test:e2e` | 29 条全部通过 |

关键构建产物：

- `addresses-page` 10.84 kB
- `favorites-page` 4.31 kB
- `overview-page` 2.73 kB
- `profile-page` 2.13 kB
- `product-detail-page` 14.67 kB

E2E 覆盖：

- 个人资料只读；
- 地址空状态、新增、默认切换、编辑、删除；
- 收藏详情新增、列表展示、取消、列表同步；
- 未登录访问收藏页 → 登录 → 回跳；
- W1 商品与 W2-01 认证回归。

---

## 10. 交付文档路径

1. 开发知识文档：`.agent/tasks/dev/W2-02-user-center.md`
2. 正式交付报告：`docs/reports/w2-02-user-center/DELIVERY_REPORT.md`（本文件）

---

## 11. Git

- 本任务代码、开发知识文档与正式报告在同一提交中完成，提交信息：
  `feat(web): implement user center, addresses and favorites`。
- 提交 SHA 与最终工作区状态以本次交付汇报为准。
- 未修改 main、后端 Go 代码、SQL 迁移、contracts、Docker/Nginx/K8s 配置、管理端代码；
  未 force push、未 push、未合并其他分支。

---

## 12. W2 整体完成情况

| 模块 | 状态 |
| --- | --- |
| W2-01 注册/登录/退出/当前用户/401/路由保护 | 完成 |
| W2-02 个人中心布局/概览 | 完成 |
| W2-02 只读个人资料 | 完成后端支持范围内的只读展示 |
| W2-02 收货地址 CRUD + 默认地址 | 完成 |
| W2-02 商品收藏 + 详情收藏态 + 缓存同步 | 完成 |
| 资料修改/头像/手机号/邮箱 | **后端未提供，未实现** |
| 登录持久化（HttpOnly Cookie） | **后端未提供，仍为内存 Token** |
| W3 购物车/订单 | 未开始（符合任务边界） |

结论：W2 用户域在前端与后端现有契约范围内已完成；剩余能力依赖后端新增契约，
未使用 Mock 或前端本地状态伪造。

---

## 13. 风险与后续建议

1. **资料修改**：后端补齐资料字段与更新接口后，再新增编辑表单；不得用本地状态伪造。
2. **地址地区**：如未来引入行政区划码表，需要同步调整 DTO、表单、列表与校验。
3. **收藏卡片入口**：若要在列表卡片加收藏，先设计批量状态接口，避免 N+1。
4. **缓存隔离**：新增用户私有数据时必须加入 `useAuthCacheReset` 与登出清理。
5. **默认地址并发**：前端已处理 7002 文案；如需要自动重试，应保证幂等。
6. **收藏批量能力**：后端未提供批量删除，前端未实现“清空收藏”。
7. **真实联调数据**：本轮临时数据库已清理，仓库不包含地址/收藏种子数据。
