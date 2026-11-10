import { useEffect } from 'react'

/** 设置当前页面标题，卸载时恢复上一个标题。 */
export function useDocumentTitle(title: string): void {
  useEffect(() => {
    const previousTitle = document.title
    document.title = title

    return () => {
      document.title = previousTitle
    }
  }, [title])
}
