# SurgeCart 设计系统（W0 基线）

本文件是 SurgeCart 用户商城的视觉与交互规范。所有规范都已落地到
`src/styles/globals.css` 的 CSS 变量与 Tailwind Theme 中，组件只允许引用语义 Token，
**不允许在业务代码里直接写十六进制色值或临时圆角/阴影**。

---

## 1. 品牌视觉方向

SurgeCart 面向真实消费者，是 PC 优先的电商商城，不是后台管理系统。

**关键词**：现代、简洁、专业、可信赖。

**设计原则**

1. **层级优先**：价格、促销、商品标题、辅助信息的视觉权重依次递减，用户 3 秒内能读懂卡片。
2. **克制用色**：页面以白色与浅灰为主，品牌蓝只用于主操作、链接与选中态，彩色面积不超过页面的 15%。
3. **留白稳定**：区块间用统一间距节奏（56 / 80px），卡片内边距 20 / 24px，不靠拥挤堆信息。
4. **反馈短促**：hover / focus / active 过渡控制在 120–180ms，只改变颜色与边框，不做位移和缩放。
5. **可信赖**：金额、库存、配送信息必须清楚可读；错误与空状态必须给出下一步动作。

**明确不做**

- 大面积渐变、玻璃拟态、彩色大投影。
- 装饰性动画、自动轮播、滚动视差。
- 为了"科技感"堆叠发光边框或无意义图标。
- 暗色模式（W0 未提供；后续如需支持，必须先补齐暗色 Token 再使用 `dark:`）。

---

## 2. 色彩规范

### 2.1 品牌色板（Brand）

| Token | 色值 | Tailwind | 用途 |
| --- | --- | --- | --- |
| brand-50 | `#eff5ff` | `bg-brand-50` | 主色最浅底、标签背景 |
| brand-100 | `#dbe8fe` | `bg-brand-100` | hover 浅底、选中背景 |
| brand-200 | `#bfd6fe` | `bg-brand-200` | 浅描边 |
| brand-300 | `#93b9fd` | `bg-brand-300` | 描边 hover |
| brand-400 | `#6094fa` | `bg-brand-400` | 图形辅助 |
| brand-500 | `#3b76f6` | `bg-brand-500` | 图表、次级强调 |
| brand-600 | `#1e5ae8` | `bg-brand-600` / `bg-primary` | **主色**：主按钮、链接、选中态 |
| brand-700 | `#1a4bcc` | `bg-brand-700` / `bg-primary-hover` | 主按钮 hover |
| brand-800 | `#1b3fa6` | `bg-brand-800` / `bg-primary-active` | 主按钮 active |
| brand-900 | `#1c3a83` | `bg-brand-900` | 深色文字（极少使用） |

### 2.2 中性色板（Neutral）

| Token | 色值 | Tailwind | 用途 |
| --- | --- | --- | --- |
| gray-25 | `#fafbfc` | `bg-gray-25` | 卡片内浅底 |
| gray-50 | `#f5f7fa` | `bg-gray-50` | 页面背景 |
| gray-100 | `#eef1f5` | `bg-gray-100` | 分区底色、hover 底 |
| gray-200 | `#e5e7eb` | `bg-gray-200` | 分隔线 |
| gray-300 | `#d5dae2` | `bg-gray-300` | 输入框描边 |
| gray-400 | `#9aa3b2` | `bg-gray-400` | placeholder |
| gray-500 | `#6b7280` | `bg-gray-500` | 辅助文字 |
| gray-600 | `#4b5563` | `bg-gray-600` | 次级正文 |
| gray-700 | `#374151` | `bg-gray-700` | 强调辅助文字 |
| gray-900 | `#111827` | `bg-gray-900` | 正文、标题 |

### 2.3 语义 Token

| Token | 色值 | Tailwind | 用途 |
| --- | --- | --- | --- |
| background | `#f5f7fa` | `bg-background` | 页面背景 |
| surface | `#ffffff` | `bg-surface` | 卡片、Header、弹层 |
| foreground | `#111827` | `text-foreground` | 正文与标题 |
| muted | `#eef1f5` | `bg-muted` | 弱化区块底 |
| muted-foreground | `#6b7280` | `text-muted-foreground` | 辅助说明 |
| border | `#e5e7eb` | `border-border` | 默认描边、分隔线 |
| border-strong | `#d5dae2` | `border-border-strong` | 输入框、需要强调的描边 |
| primary | `#1e5ae8` | `bg-primary` / `text-primary` | 主操作 |
| primary-hover | `#1a4bcc` | `bg-primary-hover` | 主操作 hover |
| primary-active | `#1b3fa6` | `bg-primary-active` | 主操作 active |
| primary-foreground | `#ffffff` | `text-primary-foreground` | 主色上的文字 |
| primary-soft | `#eff5ff` | `bg-primary-soft` | 弱强调底 |
| success | `#0f9d63` | `text-success` | 成功、库存充足 |
| success-soft | `#ecfdf3` | `bg-success-soft` | 成功提示底 |
| warning | `#b45309` | `text-warning` | 库存紧张、提醒 |
| warning-soft | `#fffaeb` | `bg-warning-soft` | 提醒底 |
| danger | `#dc2626` | `text-danger` | 错误、危险操作 |
| danger-hover | `#b91c1c` | `bg-danger-hover` | 危险操作 hover |
| danger-soft | `#fef2f2` | `bg-danger-soft` | 错误提示底 |
| promo | `#e5484d` | `bg-promo` / `text-promo` | 促销标签，**不用于正文** |
| price | `#d92d3a` | `text-price` | 商品价格数字 |
| ring | `#1e5ae8` | `ring-ring` | 键盘焦点环 |

**使用规则**

- 价格只使用 `text-price`，促销标签只使用 `promo`，两者都不用于长文本。
- 成功/警告/错误只表达状态，不做装饰色块。
- 同一屏内不出现两种"主色蓝"，次级强调统一用 `primary-soft` + `primary`。
- 文字与背景对比度需 ≥ 4.5:1；辅助文字不得低于 `gray-500`。

---

## 3. 字体规范

### 3.1 字体栈（中文优先）

```css
--font-sans:
  'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', 'Noto Sans SC',
  'Source Han Sans SC', system-ui, -apple-system, 'Segoe UI', Roboto,
  'Helvetica Neue', Arial, sans-serif;
```

不加载 Web Font，避免首屏字体请求与 FOUT；数字统一使用 `tabular-nums`。

### 3.2 字号与行高

| 层级 | 字号 / 行高 | 字重 | Tailwind |
| --- | --- | --- | --- |
| 展示标题 | 48 / 56 | 600 | `text-5xl font-semibold` |
| 页面标题 | 30 / 38 | 600 | `text-3xl font-semibold` |
| 区块标题 | 20 / 28 | 600 | `text-xl font-semibold` |
| 卡片标题 | 16 / 24 | 500 | `text-base font-medium` |
| 正文 | 16 / 28 | 400 | `text-base` |
| 辅助文字 | 14 / 22 | 400 | `text-sm text-muted-foreground` |
| 说明文字 | 12 / 18 | 400 | `text-xs text-muted-foreground` |
| 价格（大） | 30 / 36 | 600 | `PriceDisplay size="lg"` |
| 价格（中） | 20 / 24 | 600 | `PriceDisplay size="md"` |
| 价格（小） | 14 / 20 | 600 | `PriceDisplay size="sm"` |

规则：

- 字重只用 400 / 500 / 600，不使用 700 以上。
- 标题 `letter-spacing: -0.01em`，中文标题不额外加字距。
- 正文段落最大宽度约 65 个字符，超过时用 `max-w-*` 限制。
- 不使用全角空格缩进，不使用连续感叹号。

---

## 4. 页面宽度与栅格

| 场景 | 宽度 | 实现 |
| --- | --- | --- |
| 商城内容区最大宽度 | 1440px | `PageContainer`（`max-w-[1440px]`） |
| 正文/表单窄容器 | 1080px | `PageContainer width="narrow"` |
| 左右留白 | 16 / 24 / 32px | `px-4 sm:px-6 xl:px-8` |
| Header 主行高度 | 64px（<1024）/ 80px（≥1024） | `h-16 lg:h-20` |
| Header 导航行高度 | 48px | `h-12` |
| 内容栅格 | 12 列，间距 16 / 24px | `grid gap-4 lg:gap-6` |

- 1440px 与 1920px 是主设计基准；1920px 下内容居中，两侧留白由容器承担。
- 商品列表后续采用 4–5 列（1440px），卡片最小宽度不低于 220px。

---

## 5. 响应式断点

沿用 Tailwind 默认断点，设计以 1440px 为基准向下适配：

| 断点 | 宽度 | 目标设备 | 关键变化 |
| --- | --- | --- | --- |
| — | < 640px | 手机 | 单列、Header 出现汉堡菜单与独立搜索行、隐藏次要标签 |
| sm | ≥ 640px | 大屏手机 | 显示 W0 标记、部分双列 |
| md | ≥ 768px | 平板竖屏 | 搜索框回到 Header 主行 |
| lg | ≥ 1024px | 平板横屏 / 小笔记本 | 显示桌面导航行、Header 操作文案、双列栅格 |
| xl | ≥ 1280px | 桌面 | 内容留白提升到 32px |
| 2xl | ≥ 1536px | 大屏 | 内容仍限制 1440px |

规则：优先使用断点前缀调整布局，不用 JS 判断屏幕宽度（移动端导航除外）。

---

## 6. 间距规范

基础单位为 4px，所有间距取自 4 的倍数：

| Token | 值 | 典型用途 |
| --- | --- | --- |
| 1 | 4px | 图标与文字、标签内边距 |
| 2 | 8px | 行内元素间距 |
| 3 | 12px | 表单控件间距 |
| 4 | 16px | 卡片内元素、列表间距 |
| 5 | 20px | 卡片内边距（小屏） |
| 6 | 24px | 卡片内边距、栅格间距 |
| 8 | 32px | 区块内分组 |
| 10 | 40px | 大分组 |
| 12 | 48px | Header 导航行高度 |
| 14 | 56px | 移动端区块上下留白 |
| 20 | 80px | 桌面端区块上下留白 |

- 区块上下留白：`py-14 lg:py-20`（56 / 80px）。
- 卡片内边距：`p-5 sm:p-6`（20 / 24px）。
- 栅格间距：`gap-4 lg:gap-6`（16 / 24px）。
- 不使用 `p-[13px]` 这类无规律数值。

---

## 7. 圆角规范

| Token | 值 | 用途 |
| --- | --- | --- |
| sm | 4px | 标签内小元素、导航 hover 底 |
| md | 6px | 按钮、输入框、图标按钮 |
| lg | 8px | 大按钮、图标容器、图片容器 |
| xl | 12px | 卡片、弹层、空/错状态容器 |
| 2xl | 16px | 页面级提示卡、Hero 面板 |
| full | 9999px | 头像、徽标、胶囊标签 |

规则：同一容器内不混用两种以上圆角；卡片圆角必须大于等于内部图片圆角。

---

## 8. 阴影规范

| Token | 值 | 用途 |
| --- | --- | --- |
| shadow-xs | `0 1px 2px rgba(17,24,39,.04)` | 主按钮、图标按钮的轻微抬起 |
| shadow-card | `0 1px 2px rgba(17,24,39,.04), 0 2px 8px -2px rgba(17,24,39,.06)` | 卡片默认 |
| shadow-popover | `0 12px 32px -8px rgba(17,24,39,.16), 0 2px 8px -2px rgba(17,24,39,.08)` | Dialog、下拉、浮层 |

规则：

- 静态卡片默认只用 `shadow-card`，hover 不加大阴影，改为边框变色。
- 同一屏不超过两层阴影层级。
- 禁止使用彩色阴影与内阴影。

---

## 9. 按钮规范

实现：`src/components/ui/button.tsx` + `src/components/ui/button-variants.ts`

| 变体 | 用途 | 视觉 |
| --- | --- | --- |
| primary | 页面唯一主操作 | 主色底 + 白字 |
| secondary | 次要操作 | 浅蓝底 + 主色字 |
| outline | 并列操作、筛选 | 白底 + 灰描边，hover 变主色 |
| ghost | 工具类、分页 | 透明底，hover 浅灰 |
| danger | 删除、取消订单 | 红色底 + 白字 |
| link | 行内跳转 | 主色文字 + 下划线 |

| 尺寸 | 高度 | 字号 | 用途 |
| --- | --- | --- | --- |
| sm | 32px | 13px | 表格行、分页、标签内操作 |
| md | 40px | 14px | 常规操作（默认） |
| lg | 48px | 16px | 首屏主 CTA、结算 |
| icon | 40×40 | — | 纯图标按钮（必须带 `aria-label`） |

**状态**

- hover：只改变背景/边框颜色，150ms。
- focus-visible：`ring-2 ring-ring/40 ring-offset-2`，不使用浏览器默认 outline。
- active：加深一档颜色。
- disabled：`opacity-50` + `pointer-events-none`，保持文案可读。
- loading：左侧 16px spinner，`aria-busy="true"`，禁止重复提交。

**文案**：动词开头，2–6 个汉字，如「加入购物车」「立即购买」「重新加载」；不使用「点击这里」。

---

## 10. 表单规范

实现：`src/components/ui/input.tsx`，后续表单统一使用 React Hook Form + Zod。

- 输入框高度 40px，圆角 `md`，描边 `border-strong`。
- 默认、hover、focus、disabled、invalid 五种状态必须齐备。
- focus：主色描边 + `ring-2 ring-ring/25`；invalid：`danger` 描边 + `danger/20` ring。
- 错误文案紧贴控件下方 8px，12px `text-danger`，通过 `aria-describedby` 关联。
- 标签在控件上方，8px 间距；placeholder 只做示例，不替代 label。
- 必填项在 label 后加 `*`，并在提交时给出统一错误摘要。
- 校验时机：失焦与提交时校验，输入过程中只清除已有错误，不实时报错。
- 数字、金额输入使用 `inputMode="decimal"`，金额在提交前转换为整数分。

---

## 11. 商品卡片规范

> W0 不实现商品列表与卡片组件，以下为 W1 的实现约束。

结构（从上到下）：

```text
┌───────────────────────────┐
│  商品主图 1:1（object-cover）│
│  [促销标签]（可选）         │
├───────────────────────────┤
│  商品标题（14px，最多 2 行） │
│  卖点/规格（12px，1 行，可选）│
│  ¥1,299.00   ¥1,599.00     │  ← 现价 20px 价格色；划线价 12px 辅助灰
│  销量 / 库存提示（12px）     │
└───────────────────────────┘
```

规范：

- 卡片：`bg-surface`、`rounded-xl`、`border border-border`、`shadow-card`、`p-3`。
- 图片：1:1 比例，`object-cover`，底色 `bg-gray-100`，加载前展示骨架。
- 标题：`text-sm font-medium leading-6 text-foreground line-clamp-2`，固定两行高度，避免栅格跳动。
- 价格：必须使用 `PriceDisplay`（整数分入参），不使用字符串拼接。
- 促销标签：`promo` 底 + 白字，圆角 `sm`，字号 11–12px，最多 4 个字。
- hover：只改变边框为 `brand-300` 并轻微加深阴影；不放大图片、不位移卡片。
- 整卡可点击，点击区域覆盖卡片；卡片内不嵌套第二个主链接。
- 库存为 0 时展示「暂时缺货」遮罩，按钮置灰。
- 栅格：1440px 下 4–5 列，间距 16–24px；1024px 下 3 列；768px 下 2 列。

---

## 12. 页面状态规范

| 状态 | 组件 | 要求 |
| --- | --- | --- |
| 加载中 | `LoadingState` | 居中 spinner + 说明；列表场景优先骨架屏，避免整页空白 |
| 空状态 | `EmptyState` | 图标 + 标题 + 一句说明 + 下一步动作按钮 |
| 错误 | `ErrorState` | `role="alert"`，说明原因，必须提供「重新加载」 |
| 404 | `PagePlaceholder` | 保留 Header/Footer，提供「返回首页」 |
| 未开放能力 | 禁用的 `Button` / 文本占位 | 必须用 `title` 或可见文案说明开放阶段，不制造死链 |

- 所有状态容器保持最小高度（≥ 240px），避免加载完成时布局跳动。
- 错误信息不暴露堆栈、接口地址或内部错误码。
- 空状态文案不指责用户（避免「你还没有…」），改为中性描述。

---

## 13. 交互与可访问性

- 所有可交互元素必须有可见的 `focus-visible` 状态；不允许 `outline: none` 而不提供替代。
- 页面提供「跳到主要内容」skip link，Header 使用语义化 `<header>/<nav>`，主内容使用 `<main id="main-content">`。
- 图标按钮必须提供 `aria-label`；纯装饰图标设置 `aria-hidden="true"`。
- Dialog 依赖 Radix 的焦点管理与 Esc 关闭，标题必须使用 `DialogTitle`。
- 动效时长 120–180ms，`prefers-reduced-motion` 下自动降级为无动画。
- 颜色对比度：正文 ≥ 4.5:1，大字号 ≥ 3:1。

---

## 14. 规范落地位置

| 内容 | 文件 |
| --- | --- |
| CSS 变量 + Tailwind Theme + 基础样式 | `src/styles/globals.css` |
| 按钮样式与状态 | `src/components/ui/button-variants.ts`、`button.tsx` |
| 输入框 | `src/components/ui/input.tsx` |
| 弹层 | `src/components/ui/dialog.tsx` |
| 容器与页面宽度 | `src/components/common/page-container.tsx` |
| 金额展示 | `src/components/common/price-display.tsx`、`src/utils/price.ts` |
| 页面状态 | `src/components/common/{loading,empty,error}-state.tsx`、`page-placeholder.tsx` |
| 公共布局 | `src/components/layout/*`、`src/layouts/root-layout.tsx` |

新增 Token 的流程：**原始色板 → 语义 Token → `@theme inline` 映射 → 更新本文档**。
四步缺一不可，禁止绕过文档直接加工具类。

---

## 15. 评审清单（提交前自查）

- [ ] 没有新增裸色值（`#xxxxxx`）、裸圆角（`rounded-[7px]`）或裸阴影。
- [ ] 所有交互元素具备 hover / focus-visible / disabled 状态。
- [ ] 金额使用 `PriceDisplay` 或 `formatPrice`，入参为整数分。
- [ ] 1024px / 768px / 390px 三个宽度无横向滚动、无文字竖排。
- [ ] 空、错、加载状态可复现且有下一步动作。
- [ ] 图标按钮有 `aria-label`，标题层级从 `h1` 开始不跳级。
