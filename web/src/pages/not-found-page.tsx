import { Link } from 'react-router'

import { PageContainer } from '@/components/common/page-container'
import { PagePlaceholder } from '@/components/common/page-placeholder'
import { Button } from '@/components/ui/button'
import { useDocumentTitle } from '@/hooks/use-document-title'

export function NotFoundPage() {
  useDocumentTitle('页面不存在 | SurgeCart 商城')

  return (
    <PageContainer className="flex min-h-[60vh] items-center justify-center py-16">
      <PagePlaceholder
        eyebrow="404"
        title="页面不存在"
        description="你访问的地址可能已被移除，或者还没有在当前的工程阶段开放。可以先回到首页查看已交付的能力。"
        actions={
          <Button asChild>
            <Link to="/">返回首页</Link>
          </Button>
        }
      />
    </PageContainer>
  )
}
