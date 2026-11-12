import { LoadingState } from '@/components/common/loading-state'

/** 路由级懒加载占位：保持 Header/Footer 不变，只替换内容区。 */
export function RouteLoading() {
  return (
    <div className="flex min-h-[50vh] items-center justify-center">
      <LoadingState label="页面加载中" />
    </div>
  )
}
