# W2-02 个人中心、收货地址与商品收藏：开发知识文档

- 任务：SurgeCart 用户商城前端 W2-02（个人中心、收货地址与商品收藏）
- 分支：`feat/phase2-user-frontend`
- 代码位置：`web/`
- 对应正式报告：`docs/reports/w2-02-user-center/DELIVERY_REPORT.md`
- 本文目标：解释个人中心结构、地址与收藏业务模型、TanStack Query 缓存设计、
  收藏状态同步机制、与认证体系的协作方式，并给出可独立维护的知识点与面试问题。

本文只描述仓库中真实存在并可运行的代码；接口字段均来自 Go 源码与真实请求验证。

---

## 1. 任务边界与开始前核查

### 1.1 边界

本期实现：

- 个人中心布局与账户概览；
- 只读个人资料；
- 收货地址增删改查与默认地址；
- 商品收藏列表、详情页收藏/取消收藏；
- 登录保护、空/错/加载状态、缓存同步。

不实现：购物车（W3）、下单与订单、资料编辑（后端无接口）、点赞、浏览足迹、
收藏分组/批量清理、地址行政区划选择器。

### 1.2 核查结果

| 检查项 | 结果 |
| --- | --- |
| 分支 | `feat/phase2-user-frontend` |
| W2-01 `ea0b025` | 在当前 HEAD 历史中 |
| 初始工作区 | clean |
| `contracts/openapi.yaml` | 不存在 |
| 地址 API | 存在（完整 CRUD + 默认地址） |
| 收藏 API | 存在（Add/Remove/List/Check） |
| 用户资料修改 API | **不存在**，只有 `GET /me`（id/username） |
| 已有 W2-02 文档 | 不存在，本次新建 |

后端相关源码：

- 地址契约：`api/address/v1/address.go`
- 地址逻辑：`internal/logic/address/address.go`
- 地址控制器：`internal/controller/address/address.go`
- 收藏契约：`api/favorite/v1/favorite.go`
- 收藏逻辑：`internal/logic/favorite/favorite.go`
- 收藏控制器：`internal/controller/favorite/favorite.go`
- 路由：`internal/cmd/routes_frontend.go`
- 错误码：`internal/codes/codes.go`
- 设计说明：`docs/design/address.md`、`docs/design/favorite.md`

---

## 2. 页面结构

使用嵌套路由 + 统一布局：

```text
/account（RequireAuth → AccountLayout）
  ├── index          账户概览（OverviewPage）
  ├── profile        个人资料（ProfilePage，只读）
  ├── addresses      收货地址（AddressesPage）
  └── favorites      我的收藏（FavoritesPage）
```

`AccountLayout` 负责：

- 页面标题「账户」与说明；
- 左侧导航（桌面）/ 横向滚动标签（移动端）；
- `<Outlet />` 渲染子页面；
- 登录保护由路由层 `RequireAuth` 包裹整个 `/account` 分支。

子页面按路由懒加载，构建产物中各自为独立 chunk；布局本身同步加载，切换子页不重复请求。

`/account` 概览保留 W2-01 的「当前登录用户」区域（用户名、用户 ID、退出登录），
并新增个人资料、收货地址、我的收藏入口卡片。

---

## 3. 用户资料数据来源

- 唯一来源：`GET /me` → `{ id, username }`（`internal/logic/iam/iam.go Me`）。
- 前端通过 `useAuthStatus()` 读取 TanStack Query 缓存 `['auth','me']`，
  不重复请求，也不把用户信息复制到 Zustand。
- 个人资料页只展示 `id` / `username`，并明确提示：
  「后端当前仅提供只读用户信息，没有昵称、头像、手机号或邮箱修改接口」。
- **没有**编辑表单、没有“保存成功”的本地假状态；避免伪造后端能力。

资料修改能力缺失已记录为后端依赖（见交付报告）。

---

## 4. 收货地址业务模型

### 4.1 接口与字段

| 方法/路径 | 说明 |
| --- | --- |
| `GET /addresses` | 本人地址列表（按 id 升序） |
| `POST /addresses` | 新增地址 |
| `GET /addresses/:id` | 详情（前端列表未使用） |
| `PUT /addresses/:id` | 更新（字段可选） |
| `DELETE /addresses/:id` | 删除（不存在/非本人 404/7001） |

字段（`api/address/v1/address.go`）：

```ts
interface Address {
  id: number
  recipient_name: string   // trim 1-32
  phone: string            // ^1[3-9]\d{9}$
  province: string         // 自由文本 1-32
  city: string             // 自由文本 1-32
  district: string         // 自由文本 1-32
  detail: string           // trim 1-255
  is_default: boolean
  created_at: string | null
  updated_at: string | null
}
```

前端不传 `user_id`：后端从认证 Principal 推导。

### 4.2 默认地址语义（以后端为准）

- 用户首条地址自动设为默认，忽略客户端 `is_default`。
- 显式设置默认时，后端在同一事务内取消旧默认；
  数据库用生成列 `default_key` + 唯一索引兜底并发，冲突返回 409/7002。
- 删除默认地址后允许“无默认”，不会自动提升其它地址。
- `PUT` 只提交 `{ is_default: true }` 即可设置默认，后端保留其它字段；
  前端“设为默认”按钮正是这样实现的。
- 编辑表单会提交完整字段 + `is_default`；取消勾选默认是允许的。

### 4.3 前端实现

- `AddressesPage`：查询列表、空状态、加载态、错误重试、成功/失败提示。
- `AddressFormDialog`：新增/编辑共用，React Hook Form + Zod。
- 校验与后端一致：
  - 收件人 1-32 字符（按 Unicode 码点）；
  - 手机号正则；
  - 省/市/区各 1-32 字（自由文本）；
  - 详细地址 1-255 字。
- 表单弹窗按 `editorOpen` 条件挂载，**每次打开都重新挂载**，
  避免取消编辑后残留上一次输入（React Hook Form 状态不会自动重置）。
- 删除使用二次确认 Dialog；删除默认地址后列表允许无默认。
- 所有写操作成功后 `invalidateQueries(['addresses'])`，服务端为唯一事实来源。

---

## 5. 商品收藏业务模型

### 5.1 接口与字段

| 方法/路径 | 说明 |
| --- | --- |
| `POST /favorites` | 添加收藏（幂等；商品必须 on_shelf） |
| `DELETE /favorites/:product_id` | 取消收藏（幂等，未收藏也成功） |
| `GET /favorites?page=&size=` | 收藏列表（按收藏时间倒序） |
| `GET /favorites/check?product_id=` | 是否已收藏（详情页） |

`FavoriteItem`（`api/favorite/v1/favorite.go`）：

```ts
interface FavoriteItem {
  id: number
  product_id: number
  product_name: string
  product_main_image: string
  product_price: number | null   // 整数分；商品删除时为 null
  available: boolean             // 商品当前 on_shelf
  unavailable_reason: string     // '' / 'off_shelf' / 'product_deleted'
  created_at: string | null
}
```

关键语义：

- 收藏与点赞是两套接口：收藏是 `/favorites`，点赞是 `/likes`，本模块只使用前者。
- 添加收藏只允许在售商品；不存在返回 4001，下架返回 11001。
- 列表保留已下架/已删除商品的收藏（软引用），用 `available/unavailable_reason`
  标识；前端不伪造跳转，避免跳到 404。
- 取消收藏按 product_id 幂等，不按收藏 id 操作，天然只作用于本人。

### 5.2 前端实现

- `/account/favorites`：分页 10 条，展示商品图、名称、价格、收藏时间、可用性。
- 可用商品：图片与名称链到 `/products/:id`。
- 不可用商品：不提供商品链接，显示「商品已下架/已删除」。
- 取消收藏：乐观更新（见下一节），并处理“删除当前页最后一条”时回退页码。
- 商品详情 `FavoriteButton`：
  - 未登录：显示「登录后收藏」，跳登录并携带 `from`，登录后回到详情；
  - 已登录：`/favorites/check` 决定「收藏 / 已收藏」；
  - 点击乐观切换，失败回滚并展示错误文案；
  - 切换成功使 `['favorites']` 列表失效，账户页下次进入自动刷新。
- 未在商品卡片上增加收藏按钮：那会产生 N 个 `/favorites/check` 请求（N+1），
  当前范围用详情页 + 收藏列表覆盖，避免性能与复杂度问题。

---

## 6. TanStack Query 缓存设计

### 6.1 Query Key

```ts
queryKeys.addresses.all        // ['addresses']
queryKeys.addresses.list()     // ['addresses','list']

queryKeys.favorites.all        // ['favorites']
queryKeys.favorites.lists()    // ['favorites','list']
queryKeys.favorites.list(page, size)
queryKeys.favorites.checks()   // ['favorites','check']
queryKeys.favorites.check(productId)
```

- 列表 key 携带 page/size，切页独立缓存；
- `all` / `lists()` 作为前缀，用于批量失效；
- `check(productId)` 与列表分开缓存，详情页与列表互不覆盖。

### 6.2 缓存一致策略

| 操作 | 处理 |
| --- | --- |
| 地址新增/编辑/删除/设默认 | `invalidateQueries(['addresses'])` |
| 详情收藏/取消 | 乐观更新 `check(productId)`；成功后失效 check + 所有收藏列表 |
| 收藏列表取消 | 乐观移除所有列表缓存中的该项目 + 把 check 置 false；失败回滚；最终失效 `['favorites']` |
| 登出 / refresh 失败 | `removeQueries(['auth'])`、`['addresses']`、`['favorites']` |

### 6.3 为什么登出要清用户缓存

同一标签页可能先登录 A 再登录 B。若只清 Token 不清 Query 缓存，
B 进入地址/收藏页会先看到 A 的缓存数据（stale-while-revalidate 会先展示旧数据），
这是明确的隐私问题。

实现：

- `useLogout` 在 finally 中立即 remove 用户私有缓存；
- `useAuthCacheReset` 挂在 `RootLayout`，监听「有会话 → 无会话」，
  兜住 refresh 失败等非主动登出路径。

### 6.4 乐观更新细节

`useToggleFavorite`：

1. `onMutate`：取消 check 查询 → 保存旧值 → 立即写入目标收藏态；
2. `onError`：恢复旧值（没有旧值时移除缓存，触发重新请求）；
3. `onSettled`：失效 check 与列表，最终以服务端为准。

收藏列表取消：

1. 取消所有 `['favorites']` 查询；
2. 保存所有列表快照；
3. 从每个列表移除对应 product_id，并把 total 减一；
4. check 缓存置 false；
5. 失败恢复快照；成功/失败最终都失效 `['favorites']`。

乐观更新的价值是交互即时；代价是需要处理回滚与并发。当前所有写操作最终都会
再对齐服务端，因此短暂的不一致可自愈。

---

## 7. 与认证体系的协作

- 路由层：`/account` 分支统一由 `RequireAuth` 保护；未登录跳
  `/login` 并携带完整来源路径，登录成功后回跳。
- 请求层：地址/收藏全部使用 W2-01 的 `authGetData/authPostData/authPutData/authDeleteData`；
  401 时单飞 refresh + 重试一次，失败清空 Token 并回到登录。
- 用户信息：复用 `useAuthStatus()` 的 `['auth','me']`，个人中心不再单独请求用户。
- Query 启用条件：地址/收藏/收藏态查询都带 `enabled: hasSession`，
  匿名状态不会发无意义请求。
- 会话结束：清理用户私有缓存（见 6.3），避免跨账号数据泄漏。

---

## 8. 关键文件与调用链

### 8.1 文件

| 文件 | 职责 |
| --- | --- |
| `web/src/pages/account/account-layout.tsx` | 个人中心布局与导航 |
| `web/src/pages/account/overview-page.tsx` | 账户概览、用户信息、入口 |
| `web/src/pages/account/profile-page.tsx` | 只读资料 |
| `web/src/pages/account/addresses-page.tsx` | 地址列表/默认/删除/反馈 |
| `web/src/pages/account/address-schema.ts` | 地址 Zod 校验 |
| `web/src/pages/account/account-errors.ts` | 地址/收藏错误文案 |
| `web/src/pages/account/favorites-page.tsx` | 收藏列表/取消/空错状态 |
| `web/src/components/account/address-form-dialog.tsx` | 地址新增/编辑弹窗 |
| `web/src/components/product/favorite-button.tsx` | 详情页收藏按钮 |
| `web/src/hooks/use-favorites.ts` | 收藏态查询与乐观切换 |
| `web/src/api/addresses.ts` | 地址 API |
| `web/src/api/favorites.ts` | 收藏 API |
| `web/src/auth/use-auth-cache-reset.ts` | 会话结束清理用户缓存 |
| `web/src/mocks/data/addresses.ts` | MSW 地址状态 |
| `web/src/mocks/data/favorites.ts` | MSW 收藏状态与不可用模拟 |

### 8.2 调用链

```text
AddressesPage
  └─ useQuery(['addresses'])
       └─ fetchAddresses → authGetData('/addresses') → GET /addresses

AddressFormDialog
  └─ useMutation
       ├─ createAddress → authPostData('/addresses')
       └─ updateAddress → authPutData('/addresses/:id')
            └─ 成功后 invalidate ['addresses']

FavoritesPage
  └─ useQuery(['favorites','list',{page,size}])
       └─ fetchFavorites → authGetData('/favorites')
  └─ removeMutation
       ├─ 乐观更新列表与 check 缓存
       ├─ removeFavorite → authDeleteData('/favorites/:product_id')
       └─ settle 后 invalidate ['favorites']

ProductDetailPage
  └─ FavoriteButton
       ├─ useFavoriteStatus → GET /favorites/check?product_id=
       └─ useToggleFavorite
            ├─ addFavorite → POST /favorites
            └─ removeFavorite → DELETE /favorites/:product_id
```

---

## 9. 技术决策与权衡

1. **嵌套路由布局**：`/account` 只保护一次，子页面复用导航与 Guard；
   比每个子页面重复包 Guard 更不容易漏保护。
2. **资料页只读**：没有后端接口就不做编辑，不用本地状态伪造成功，符合任务约束。
3. **地区自由文本**：后端没有行政区划 code/树，前端不做省市区级联选择器。
4. **地址表单弹窗**：新增/编辑共用，减少重复；每次打开重新挂载避免表单状态污染。
5. **默认地址用局部更新**：只提交 `{is_default:true}`，依赖后端保留其它字段，
   避免把整份地址回传造成覆盖风险。
6. **收藏列表乐观更新**：交互即时；最终仍以服务端失效刷新兜底。
7. **不在商品卡片加收藏**：避免 N+1 check 请求与卡片按钮的登录态分支爆炸。
8. **用户缓存清理**：主动登出 + 会话失效两条路径都清理，防止跨账号缓存泄漏。
9. **错误文案按业务码映射**：地址 7001/7002、收藏 11001/4001 都有明确提示，
   不把后端技术错误直接展示给用户。
10. **下架商品不提供详情链接**：避免用户点击后进入 404；列表明确标识不可用原因。

---

## 10. 实际问题与解决过程

### 10.1 布局拆分后测试找不到概览区域

拆分后「账户」标题由 Layout 立即渲染，概览页是懒加载，旧测试在标题出现后立刻
`getByRole('region')` 会失败。解决：使用 `findByRole` 等待异步子页面加载。

### 10.2 收藏卡片出现两个同名链接

最初图片与标题都包了带 `aria-label="查看商品：..."` 的 Link，导致无障碍树里出现
两个同名链接，Playwright/Vitest 的 strict 选择器失败。解决：图片只作为展示，
仅标题作为商品链接。

### 10.3 地址文本断言失败

地址卡片把省/市/区和详细地址放在同一个 `<p>`，中间有 `<br>`；`getByText` 精确匹配
无法命中组合文本。解决：用 `toHaveTextContent` 或作用域断言。

### 10.4 取消编辑后再次打开表单残留旧值

Radix Dialog 关闭时只卸载内容，`AddressFormDialog` 组件仍挂载，React Hook Form
状态保留。解决：父组件仅在 `editorOpen=true` 时挂载对话框，每次打开都是新实例。

### 10.5 收藏总数文本被 span 拆分

`共 <span>{total}</span> 件收藏` 会让 Text 匹配器无法命中完整字符串；
改为 `共 {total} 件收藏`（纯文本节点）后测试与可读性都更稳定。

### 10.6 Mock 商品全部在售，无法覆盖下架收藏

W1 Mock 商品列表全部是 `on_shelf`，直接改状态会破坏商品列表分页测试。
解决：在收藏 Mock 中增加 `markMockProductOffShelf(productId)` 测试钩子，
只影响收藏可用性判断，不影响商品列表。

### 10.7 真实后端无七牛凭据

沿用 W1 的 `test/serveproc` + `test/container/qiniu_mock` 仅重定向七牛 UC endpoint；
生产二进制不读取该变量，校验逻辑不变。

### 10.8 真实联调下架收藏

后端 `POST /favorites` 拒绝下架商品；为验证列表的 `off_shelf` 分支，
在临时数据库中直接插入一条历史收藏（模拟“先收藏后下架”），验证后清理数据卷。

---

## 11. 测试结果与已知限制

自动化：

- `npm run test`：26 个测试文件、119 条用例全部通过；
- `npm run test:e2e`：29 条通过（W1 商品 19 + W2-01 认证 6 + W2-02 个人中心 4）；
- 覆盖地址 CRUD/默认/校验/错误、收藏列表/取消/不可用、详情收藏态/缓存、
  未登录保护/登录回跳、只读资料、导航、退出缓存清理。

真实后端联调（详见交付报告）：

- 地址：空列表、首条自动默认、第二条设默认、只提交 is_default 切默认、编辑、
  删除、非法手机号 1001、删除不存在 7001；
- 收藏：添加/重复幂等/check/列表、下架 11001、取消、下架商品列表标识；
- 浏览器：个人资料只读、地址增改删/默认、收藏新增/取消/列表同步，共 12 项全部通过。

已知限制：

- 资料修改、头像、手机号、邮箱等后端无接口，只有只读展示；
- 地址省/市/区是自由文本，无行政区划选择器；
- 收藏仅 SPU 粒度，无 SKU 粒度、分组、批量清空；
- 商品卡片没有收藏按钮（避免 N+1）；
- 登录仍为内存 Token，刷新页面需要重新登录（W2-01 的安全取舍）。

---

## 12. 后续维护注意事项

1. 新增地址字段时同步更新：`Address` 类型、Zod schema、表单、卡片、Mock、测试。
2. 默认地址逻辑不要在前端重复实现，始终以 `is_default` 服务端结果为准。
3. 收藏写操作后必须失效 `['favorites']`，否则详情页与账户页会短暂不一致。
4. 新增用户私有数据（订单、优惠券）时，必须加入 `useAuthCacheReset` 与退出清理列表。
5. 新受保护页面继续放在 `/account` 嵌套路由下，自动继承 Guard 与布局。
6. 不要用点赞 `/likes` 接口替代收藏。
7. 收藏列表不可用商品不要提供详情链接；商品恢复上架后列表会实时变为可用。
8. 若后端未来提供资料修改接口，再新增表单；当前不要用前端状态伪造保存。
9. 地址表单受控字段较多，保持“每次打开重新挂载”的模式，避免 reset 漏字段。
10. E2E 使用 MSW，真实后端联调结果不能相互替代；报告需分开记录。

---

## 13. 值得深入学习的技术知识及面试问题

### 13.1 React Query 乐观更新

- 为什么 `onMutate` 要先 `cancelQueries`？
  - 防止在途请求在乐观更新后返回旧数据覆盖 UI。
- 失败后如何回滚多个列表缓存？
  - `getQueriesData` 保存 key/data 快照，`onError` 逐项恢复。
- 为什么最终还要 invalidate？
  - 乐观更新只是本地推测，服务端可能改变总量、排序或可用性；最终需要服务端校准。

### 13.2 缓存与用户隔离

- 退出登录只清 Token 够不够？
  - 不够；Query 缓存仍持有上一个用户的数据，必须清理用户私有 key。
- 为什么用 Query Key 前缀失效？
  - `['favorites']` 可以一次覆盖 list/check 等所有子 key，避免遗漏。

### 13.3 默认地址与并发

- 如何保证一个用户最多一个默认地址？
  - 应用层事务 + 数据库生成列唯一索引兜底；并发落败返回 409/7002。
- 为什么删除默认地址后不自动提升？
  - 业务语义允许“无默认”；自动提升会引入隐式写入与竞态。

### 13.4 幂等性

- 重复收藏、取消收藏为什么安全？
  - 唯一约束 + 条件删除；后端定义幂等语义，前端不需要额外去重。
- 如果后端不幂等，前端应该怎么做？
  - 禁用重复提交、请求去重、幂等键；本任务后端已保证，前端只做 pending 禁用。

### 13.5 认证与请求

- 401 刷新为什么要单飞？
  - refresh token 轮换 + reuse 检测，并发刷新会导致全量撤销。
- 为什么未登录不请求 `/favorites/check`？
  - 避免必然 401 的请求与无意义的 refresh 尝试。

### 13.6 表单

- React Hook Form 的 `defaultValues` 为什么在编辑切换时不生效？
  - 它只在首次挂载生效；需要 `reset` 或重新挂载。本项目选择重新挂载，简单可靠。
- 为什么密码/地址长度要按字节或码点校验？
  - 后端使用 Go 的 `len`/rune 语义；前端需与后端一致，避免“前端通过、后端拒绝”。
