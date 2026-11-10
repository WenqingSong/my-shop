import { Link, isRouteErrorResponse, useRouteError } from 'react-router'

import { ErrorState } from '@/components/common/error-state'
import { PageContainer } from '@/components/common/page-container'
import { Button } from '@/components/ui/button'

/**
 * 路由级错误边界：捕获页面渲染/懒加载异常。
 * 只展示通用信息，不把错误对象、堆栈或接口地址暴露给用户。
 */
export function RouteErrorPage() {
  const error = useRouteError()
  const isNotFound = isRouteErrorResponse(error) && error.status === 404

  return (
    <PageContainer className="py-16">
      <ErrorState
        title={isNotFound ? '页面不存在' : '页面加载失败'}
        description={
          isNotFound
            ? '访问的页面不存在，可能是链接已失效。'
            : '页面出现异常，请重新加载；如问题持续出现，请稍后再试。'
        }
        onRetry={() => window.location.reload()}
        action={
          <Button asChild variant="outline" size="sm">
            <Link to="/">返回首页</Link>
          </Button>
        }
      />
    </PageContainer>
  )
}
