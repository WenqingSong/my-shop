import {
  ArrowRight,
  Boxes,
  Check,
  Database,
  FileCode,
  Server,
  ShieldCheck,
  Sparkles,
} from 'lucide-react'

import { PageContainer } from '@/components/common/page-container'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'
import { ComponentShowcaseSection } from '@/pages/home/component-showcase-section'
import { DesignBaselineSection } from '@/pages/home/design-baseline-section'
import { SectionHeading } from '@/pages/home/section-primitives'

const deliveryItems = [
  '设计系统与 Tailwind Theme 落地',
  '商城 Header / Footer / 内容容器',
  'API Client 与 TanStack Query 接入',
  'Vitest、MSW、Playwright 测试工程',
]

const stackStats = [
  { label: '框架', value: 'React 19' },
  { label: '构建', value: 'Vite' },
  { label: '类型', value: 'TS strict' },
  { label: '样式', value: 'Tailwind 4' },
]

const engineeringItems = [
  {
    icon: Server,
    title: 'API Client',
    description: '默认 /api 基础路径，15s 超时，统一 ApiError 与 Token 扩展点，业务层不直接调用 fetch。',
  },
  {
    icon: Database,
    title: '数据层职责',
    description: 'TanStack Query 管理后端数据与缓存失效；Zustand 只保存导航开关等轻量 UI 状态。',
  },
  {
    icon: ShieldCheck,
    title: '质量保障',
    description: 'Vitest + Testing Library 覆盖组件，MSW 拦截请求，Playwright 校验真实浏览器行为。',
  },
  {
    icon: FileCode,
    title: '契约优先',
    description: 'openapi-typescript 已配置生成命令，等待后端 B0 提供正式 OpenAPI 契约后再生成业务类型。',
  },
]

const roadmap = [
  { stage: 'W0', title: '工程初始化与设计基线', state: '本期交付' },
  { stage: 'W1', title: '商品浏览与分类', state: '待启动' },
  { stage: 'W2', title: '商品详情与搜索', state: '待启动' },
  { stage: 'W3', title: '购物车', state: '待启动' },
  { stage: 'W4', title: '下单与结算', state: '待启动' },
  { stage: 'W5', title: '订单与账户', state: '待启动' },
]

function Hero() {
  return (
    <section className="border-b border-border bg-surface">
      <PageContainer className="grid gap-10 py-14 lg:grid-cols-[minmax(0,1fr)_400px] lg:items-center lg:gap-16 lg:py-20">
        <div>
          <span className="inline-flex items-center gap-2 rounded-full border border-brand-200 bg-primary-soft px-3 py-1 text-xs font-medium text-primary">
            <Sparkles className="size-3.5" aria-hidden="true" />
            W0 · 前端工程基线
          </span>
          <h1 className="mt-5 text-3xl font-semibold leading-tight tracking-tight text-foreground sm:text-4xl lg:text-5xl lg:leading-[1.15]">
            把好物，<span className="text-primary">稳稳</span>送到手
          </h1>
          <p className="mt-5 max-w-xl text-base leading-7 text-muted-foreground">
            SurgeCart 用户商城前端基线版本：设计系统、公共布局、接口基础设施与测试工程已经就绪，为商品浏览、购物车与订单流程打好底座。
          </p>
          <div className="mt-8 flex flex-wrap items-center gap-3">
            <Button asChild size="lg">
              <a href="#design-baseline">
                查看设计基线
                <ArrowRight aria-hidden="true" />
              </a>
            </Button>
            <Button asChild size="lg" variant="outline">
              <a href="#components">组件与状态</a>
            </Button>
          </div>
          <dl className="mt-10 grid max-w-xl grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-4">
            {stackStats.map((stat) => (
              <div key={stat.label}>
                <dt className="text-xs text-muted-foreground">{stat.label}</dt>
                <dd className="mt-1 text-sm font-semibold text-foreground">{stat.value}</dd>
              </div>
            ))}
          </dl>
        </div>

        <div className="rounded-2xl border border-border bg-gray-25 p-6 shadow-card">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-semibold text-foreground">本期交付</h2>
            <span className="rounded-full bg-surface px-2 py-0.5 font-mono text-[11px] text-muted-foreground">
              W0 / 6
            </span>
          </div>
          <ul className="mt-5 space-y-3.5">
            {deliveryItems.map((item) => (
              <li key={item} className="flex items-start gap-2.5 text-sm leading-6 text-foreground">
                <span className="mt-1 grid size-4 shrink-0 place-items-center rounded-full bg-success-soft text-success">
                  <Check className="size-3" aria-hidden="true" />
                </span>
                {item}
              </li>
            ))}
          </ul>
          <p className="mt-5 border-t border-border pt-4 text-xs leading-5 text-muted-foreground">
            本阶段不包含登录、商品列表、购物车与下单等业务实现，这些能力将按 W1–W5 排期接入。
          </p>
        </div>
      </PageContainer>
    </section>
  )
}

function EngineeringSection() {
  return (
    <section className="border-b border-border bg-surface py-14 lg:py-16">
      <PageContainer className="flex flex-col gap-8">
        <SectionHeading
          eyebrow="Engineering"
          title="工程能力"
          description="先行建立请求、状态、测试与契约四条基础设施，避免后续业务开发反复返工。"
        />
        <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {engineeringItems.map((item) => (
            <li key={item.title} className="rounded-xl border border-border bg-background p-5">
              <span className="grid size-10 place-items-center rounded-lg bg-primary-soft text-primary">
                <item.icon className="size-5" aria-hidden="true" />
              </span>
              <h3 className="mt-4 text-sm font-semibold text-foreground">{item.title}</h3>
              <p className="mt-2 text-sm leading-6 text-muted-foreground">{item.description}</p>
            </li>
          ))}
        </ul>
      </PageContainer>
    </section>
  )
}

function RoadmapSection() {
  return (
    <section className="border-b border-border bg-background py-14 lg:py-16">
      <PageContainer className="flex flex-col gap-8">
        <SectionHeading
          eyebrow="Roadmap"
          title="阶段规划"
          description="排期以后端契约与主 Chat 计划为准；本页只说明前端工程的分阶段边界。"
        />
        <ol className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {roadmap.map((item) => {
            const isCurrent = item.stage === 'W0'

            return (
              <li
                key={item.stage}
                className={
                  isCurrent
                    ? 'rounded-xl border border-brand-200 bg-primary-soft p-5'
                    : 'rounded-xl border border-border bg-surface p-5'
                }
              >
                <div className="flex items-center justify-between">
                  <span className="font-mono text-xs font-semibold text-primary">{item.stage}</span>
                  <span
                    className={
                      isCurrent
                        ? 'rounded-full bg-surface px-2 py-0.5 text-[11px] font-medium text-primary'
                        : 'rounded-full bg-gray-100 px-2 py-0.5 text-[11px] text-muted-foreground'
                    }
                  >
                    {item.state}
                  </span>
                </div>
                <p className="mt-3 text-sm font-medium text-foreground">{item.title}</p>
              </li>
            )
          })}
        </ol>
        <p className="flex items-start gap-2 text-xs leading-5 text-muted-foreground">
          <Boxes className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          前端不会自行定义后端接口：商品、库存、订单等数据契约均由后端 B0 输出，前端通过 openapi-typescript 生成类型。
        </p>
      </PageContainer>
    </section>
  )
}

export function HomePage() {
  useDocumentTitle('SurgeCart 商城 | 品质好物，稳定抵达')

  return (
    <>
      <Hero />
      <DesignBaselineSection />
      <ComponentShowcaseSection />
      <EngineeringSection />
      <RoadmapSection />
    </>
  )
}
