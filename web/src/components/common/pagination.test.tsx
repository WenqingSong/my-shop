import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { Pagination } from '@/components/common/pagination'

describe('Pagination', () => {
  it('首页禁用上一页，点击页码向上冒泡', async () => {
    const user = userEvent.setup()
    const onPageChange = vi.fn()

    render(<Pagination page={1} totalPages={12} onPageChange={onPageChange} />)

    expect(screen.getByRole('button', { name: '上一页' })).toBeDisabled()

    await user.click(screen.getByRole('button', { name: '第 2 页' }))
    expect(onPageChange).toHaveBeenCalledWith(2)
  })

  it('当前页标记 aria-current 并禁用下一页到边界', () => {
    render(<Pagination page={12} totalPages={12} onPageChange={() => undefined} />)

    expect(screen.getByRole('button', { name: '第 12 页' })).toHaveAttribute(
      'aria-current',
      'page',
    )
    expect(screen.getByRole('button', { name: '下一页' })).toBeDisabled()
  })

  it('只有一页时不渲染分页', () => {
    const { container } = render(
      <Pagination page={1} totalPages={1} onPageChange={() => undefined} />,
    )

    expect(container).toBeEmptyDOMElement()
  })
})
