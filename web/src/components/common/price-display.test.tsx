import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { PriceDisplay } from '@/components/common/price-display'

describe('PriceDisplay', () => {
  it('按人民币规则展示金额', () => {
    const { container } = render(<PriceDisplay value={129900} />)

    expect(container.textContent).toBe('¥1,299.00')
    expect(screen.getByText('1,299')).toBeInTheDocument()
    expect(screen.getByText('.00')).toBeInTheDocument()
  })

  it('零元与小额价格同样保留两位小数', () => {
    const { container, rerender } = render(<PriceDisplay value={0} />)
    expect(container.textContent).toBe('¥0.00')

    rerender(<PriceDisplay value={5} />)
    expect(container.textContent).toBe('¥0.05')
  })

  it('支持不同字号与中性色调', () => {
    const { container } = render(<PriceDisplay value={1990} size="lg" tone="neutral" />)

    expect(container.textContent).toBe('¥19.90')
    expect(container.firstElementChild).toHaveClass('text-foreground')
  })
})
