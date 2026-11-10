import { PageContainer } from '@/components/common/page-container'
import { BrandLogo } from '@/components/layout/brand-logo'

const footerGroups = [
  {
    title: '购物指南',
    items: ['账户与登录', '商品浏览', '购物车与下单'],
  },
  {
    title: '配送与售后',
    items: ['配送范围与时效', '退换货政策', '发票与保修'],
  },
  {
    title: '关于 SurgeCart',
    items: ['品牌介绍', '服务承诺', '联系我们'],
  },
]

export function SiteFooter() {
  const currentYear = new Date().getFullYear()

  return (
    <footer className="mt-auto border-t border-border bg-surface">
      <PageContainer className="grid gap-10 py-12 md:grid-cols-2 lg:grid-cols-[1.5fr_repeat(3,1fr)] lg:gap-8">
        <div className="max-w-sm">
          <BrandLogo />
          <p className="mt-4 text-sm leading-6 text-muted-foreground">
            SurgeCart 用户商城，面向真实消费者的 PC 优先电商体验。当前已开放商品分类、商品搜索与商品详情浏览。
          </p>
          <p className="mt-4 font-mono text-[11px] uppercase tracking-wide text-gray-400">
            React 19 · TypeScript · Tailwind CSS 4
          </p>
        </div>

        {footerGroups.map((group) => (
          <div key={group.title}>
            <h2 className="text-sm font-semibold text-foreground">{group.title}</h2>
            <ul className="mt-4 space-y-2.5">
              {group.items.map((item) => (
                <li key={item} className="text-sm text-muted-foreground">
                  {item}
                </li>
              ))}
            </ul>
          </div>
        ))}
      </PageContainer>

      <div className="border-t border-border">
        <PageContainer className="flex flex-col gap-2 py-5 text-xs text-muted-foreground sm:flex-row sm:items-center sm:justify-between">
          <p>© {currentYear} SurgeCart · 用户商城</p>
          <p>商品浏览已开放，购物车、下单与账号功能将在后续阶段接入。</p>
        </PageContainer>
      </div>
    </footer>
  )
}
