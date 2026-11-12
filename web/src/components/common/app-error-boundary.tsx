import { Component, type ErrorInfo, type ReactNode } from 'react'

import { ErrorState } from '@/components/common/error-state'
import { PageContainer } from '@/components/common/page-container'

interface AppErrorBoundaryProps {
  children: ReactNode
}

interface AppErrorBoundaryState {
  hasError: boolean
}

/**
 * 应用级错误边界：兜住 Router 之外的渲染异常（例如 Provider 初始化失败）。
 * 页面内部的异常优先由路由 errorElement 处理，二者共同保证不会白屏。
 */
export class AppErrorBoundary extends Component<AppErrorBoundaryProps, AppErrorBoundaryState> {
  state: AppErrorBoundaryState = { hasError: false }

  static getDerivedStateFromError(): AppErrorBoundaryState {
    return { hasError: true }
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error('[AppErrorBoundary] 未捕获的渲染错误', error, info.componentStack)
  }

  render(): ReactNode {
    if (this.state.hasError) {
      return (
        <PageContainer className="py-16">
          <ErrorState
            title="页面出现异常"
            description="应用渲染失败，请重新加载页面；如问题持续出现，请稍后再试。"
            onRetry={() => window.location.reload()}
          />
        </PageContainer>
      )
    }

    return this.props.children
  }
}
