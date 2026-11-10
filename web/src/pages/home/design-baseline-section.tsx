import { PageContainer } from '@/components/common/page-container'
import { Panel, SectionHeading } from '@/pages/home/section-primitives'

const colorTokens = [
  { name: 'Primary 主色', hex: '#1e5ae8', className: 'bg-primary', usage: '主按钮、链接、选中态' },
  { name: 'Primary Soft', hex: '#eff5ff', className: 'bg-primary-soft', usage: '弱强调背景、标签底' },
  { name: 'Background', hex: '#f5f7fa', className: 'bg-background', usage: '页面背景' },
  { name: 'Surface', hex: '#ffffff', className: 'bg-surface', usage: '卡片、弹层、Header' },
  { name: 'Foreground', hex: '#111827', className: 'bg-foreground', usage: '正文与标题' },
  { name: 'Muted', hex: '#6b7280', className: 'bg-muted-foreground', usage: '辅助说明文字' },
  { name: 'Border', hex: '#e5e7eb', className: 'bg-border', usage: '分隔线与描边' },
  { name: 'Success', hex: '#0f9d63', className: 'bg-success', usage: '成功、库存充足' },
  { name: 'Warning', hex: '#b45309', className: 'bg-warning', usage: '库存紧张、提醒' },
  { name: 'Danger', hex: '#dc2626', className: 'bg-danger', usage: '错误、危险操作' },
  { name: 'Promo', hex: '#e5484d', className: 'bg-promo', usage: '促销标签（非正文）' },
  { name: 'Price', hex: '#d92d3a', className: 'bg-price', usage: '商品价格数字' },
]

const typeScale = [
  { label: '展示标题', sample: '品质好物 稳定抵达', className: 'text-5xl font-semibold tracking-tight', spec: '48 / 56' },
  { label: '页面标题', sample: '商品列表', className: 'text-3xl font-semibold tracking-tight', spec: '30 / 38' },
  { label: '区块标题', sample: '热销推荐', className: 'text-xl font-semibold', spec: '20 / 28' },
  { label: '正文', sample: '前端基线已完成设计系统与公共布局。', className: 'text-base', spec: '16 / 28' },
  { label: '辅助文字', sample: '满减、配送与售后说明使用 14px。', className: 'text-sm text-muted-foreground', spec: '14 / 22' },
  { label: '说明文字', sample: '价格单位：人民币（元）', className: 'text-xs text-muted-foreground', spec: '12 / 18' },
]

const radiusTokens = [
  { label: 'sm · 4px', className: 'rounded-sm' },
  { label: 'md · 6px', className: 'rounded-md' },
  { label: 'lg · 8px', className: 'rounded-lg' },
  { label: 'xl · 12px', className: 'rounded-xl' },
  { label: '2xl · 16px', className: 'rounded-2xl' },
]

const shadowTokens = [
  { label: 'shadow-xs', className: 'shadow-xs' },
  { label: 'shadow-card', className: 'shadow-card' },
  { label: 'shadow-popover', className: 'shadow-popover' },
]

export function DesignBaselineSection() {
  return (
    <section id="design-baseline" className="scroll-mt-32 border-b border-border bg-background py-14 lg:py-20">
      <PageContainer className="flex flex-col gap-8">
        <SectionHeading
          eyebrow="Design Tokens"
          title="设计基线"
          description="颜色、字体、圆角与阴影全部落在 Tailwind Theme 中，组件只引用语义 Token，不在业务代码里散落色值。"
        />

        <Panel title="色彩规范" description="语义色板：主色仅承担品牌与交互强调，价格与促销使用独立对比色。">
          <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6">
            {colorTokens.map((token) => (
              <li key={token.name} className="rounded-lg border border-border p-2">
                <span className={`block h-12 rounded-md border border-black/5 ${token.className}`} />
                <p className="mt-2 text-xs font-medium text-foreground">{token.name}</p>
                <p className="font-mono text-[11px] uppercase text-muted-foreground">{token.hex}</p>
                <p className="mt-1 text-[11px] leading-4 text-muted-foreground">{token.usage}</p>
              </li>
            ))}
          </ul>
        </Panel>

        <div className="grid gap-6 lg:grid-cols-2">
          <Panel title="字体层级" description="中文优先字体栈，字重只用 400 / 500 / 600 三档。">
            <ul className="divide-y divide-border">
              {typeScale.map((item) => (
                <li key={item.label} className="flex items-baseline justify-between gap-4 py-3">
                  <span className={`truncate ${item.className}`}>{item.sample}</span>
                  <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{item.spec}</span>
                </li>
              ))}
            </ul>
          </Panel>

          <div className="flex flex-col gap-6">
            <Panel title="圆角" description="卡片统一使用 lg / xl，按钮使用 md。">
              <div className="flex flex-wrap items-end gap-3">
                {radiusTokens.map((token) => (
                  <div key={token.label} className="flex flex-col items-center gap-2">
                    <span className={`size-14 border border-border-strong bg-gray-50 ${token.className}`} />
                    <span className="font-mono text-[11px] text-muted-foreground">{token.label}</span>
                  </div>
                ))}
              </div>
            </Panel>

            <Panel title="阴影" description="阴影只用于表达层级，不做装饰性大投影。">
              <div className="grid grid-cols-3 gap-3">
                {shadowTokens.map((token) => (
                  <div key={token.label} className="flex flex-col items-center gap-2">
                    <span className={`h-12 w-full rounded-lg border border-border bg-surface ${token.className}`} />
                    <span className="font-mono text-[11px] text-muted-foreground">{token.label}</span>
                  </div>
                ))}
              </div>
            </Panel>
          </div>
        </div>
      </PageContainer>
    </section>
  )
}
