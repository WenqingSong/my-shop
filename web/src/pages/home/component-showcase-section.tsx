import { ShoppingBag } from 'lucide-react'
import { useState } from 'react'

import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { LoadingState } from '@/components/common/loading-state'
import { PageContainer } from '@/components/common/page-container'
import { Pagination } from '@/components/common/pagination'
import { PriceDisplay } from '@/components/common/price-display'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Panel, SectionHeading } from '@/pages/home/section-primitives'

const priceExamples = [
  { label: '零元', value: 0 },
  { label: '常见商品价', value: 1990 },
  { label: '千元价位', value: 129900 },
  { label: '高客单价', value: 123456789 },
]

export function ComponentShowcaseSection() {
  const [page, setPage] = useState(3)

  return (
    <section id="components" className="scroll-mt-32 border-b border-border bg-surface py-14 lg:py-20">
      <PageContainer className="flex flex-col gap-8">
        <SectionHeading
          eyebrow="Components"
          title="组件与交互状态"
          description="按钮、表单、弹层、金额与页面状态使用统一规范，hover / focus / disabled / loading 都在这里验证。"
        />

        <div className="grid items-start gap-6 lg:grid-cols-2">
          <Panel title="按钮" description="六种变体、三种尺寸，禁用与提交中保持可辨识。">
            <div className="flex flex-col gap-4">
              <div className="flex flex-wrap items-center gap-2.5">
                <Button>主要按钮</Button>
                <Button variant="secondary">次要按钮</Button>
                <Button variant="outline">描边按钮</Button>
                <Button variant="ghost">幽灵按钮</Button>
                <Button variant="danger">危险操作</Button>
                <Button variant="link">文字链接</Button>
              </div>
              <div className="flex flex-wrap items-center gap-2.5">
                <Button size="sm">小尺寸</Button>
                <Button size="md">中尺寸</Button>
                <Button size="lg">大尺寸</Button>
                <Button disabled>禁用</Button>
                <Button isLoading>提交中</Button>
                <Button size="icon" variant="outline" aria-label="加入购物袋">
                  <ShoppingBag aria-hidden="true" />
                </Button>
              </div>
            </div>
          </Panel>

          <Panel title="表单控件" description="默认、禁用与校验失败三种状态。">
            <div className="flex flex-col gap-3">
              <Input placeholder="请输入商品关键词" aria-label="默认输入框" />
              <Input placeholder="禁用状态" aria-label="禁用输入框" disabled />
              <Input
                defaultValue="格式不正确的内容"
                aria-label="错误输入框"
                aria-invalid="true"
                aria-describedby="input-error-hint"
              />
              <p id="input-error-hint" className="text-xs text-danger">
                请输入 1–30 个字符的商品名称。
              </p>
            </div>
          </Panel>

          <Panel title="弹层与反馈" description="Dialog 复用 Radix 的焦点管理与 Esc 关闭。">
            <div className="flex flex-wrap items-center gap-2.5">
              <Dialog>
                <DialogTrigger asChild>
                  <Button variant="outline">打开 Dialog</Button>
                </DialogTrigger>
                <DialogContent>
                  <DialogHeader>
                    <DialogTitle>确认移除商品？</DialogTitle>
                    <DialogDescription>
                      这是组件示例，不会触发真实购物车操作。后续业务的弹层会沿用同一套结构与动效。
                    </DialogDescription>
                  </DialogHeader>
                  <DialogFooter>
                    <DialogClose asChild>
                      <Button variant="outline">取消</Button>
                    </DialogClose>
                    <DialogClose asChild>
                      <Button>确认</Button>
                    </DialogClose>
                  </DialogFooter>
                </DialogContent>
              </Dialog>
              <span className="text-xs text-muted-foreground">遮罩、Esc 关闭、焦点回归均已内置。</span>
            </div>
          </Panel>

          <Panel title="金额展示" description="入参为整数分，仅负责展示，不做金额计算。">
            <ul className="divide-y divide-border">
              {priceExamples.map((example) => (
                <li key={example.label} className="flex items-center justify-between gap-4 py-2.5">
                  <span className="text-sm text-muted-foreground">{example.label}</span>
                  <PriceDisplay value={example.value} />
                </li>
              ))}
            </ul>
            <div className="mt-3 flex items-end gap-5 border-t border-border pt-3">
              <PriceDisplay value={1990} size="sm" />
              <PriceDisplay value={1990} size="md" />
              <PriceDisplay value={1990} size="lg" />
            </div>
          </Panel>
        </div>

        <div className="grid items-start gap-6 lg:grid-cols-3">
          <Panel title="加载中">
            <LoadingState description="正在获取商品数据" className="py-10" />
          </Panel>
          <Panel title="空状态">
            <EmptyState
              title="购物车还是空的"
              description="这是空状态示例，商品与购物车能力将在后续阶段接入。"
              action={
                <Button variant="outline" size="sm">
                  去逛逛
                </Button>
              }
              className="border-0 bg-transparent px-0 py-6"
            />
          </Panel>
          <Panel title="错误状态">
            <ErrorState
              description="示例错误：请求超时，请稍后重试。"
              onRetry={() => undefined}
              className="border-0 bg-transparent px-0 py-6"
            />
          </Panel>
        </div>

        <Panel title="分页" description="首尾页固定，当前页左右各留一页，其余折叠。">
          <div className="flex flex-col items-center gap-3">
            <Pagination page={page} totalPages={12} onPageChange={setPage} />
            <p className="text-xs text-muted-foreground">当前第 {page} 页 / 共 12 页</p>
          </div>
        </Panel>
      </PageContainer>
    </section>
  )
}
