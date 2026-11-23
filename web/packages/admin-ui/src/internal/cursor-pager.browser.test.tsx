/**
 * CursorPager in a real browser: the direction that starts a page read
 * keeps the focus the activation gave it.
 *
 * jsdom cannot tell a natively disabled button from an `aria-disabled` one,
 * so a case that merely reads `document.activeElement` there passes even
 * when the control was taken out of the Tab order. Chromium applies the
 * real focusability rules, so the direction is proven to still hold focus
 * when the read settles into the last page.
 */

import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { CursorPager } from './cursor-pager.js'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const ZH = zhCN.common as {
  readonly pager: { readonly previous: string; readonly next: string }
}

describe('CursorPager focus in Chromium', () => {
  it('keeps focus on the next direction when the last page arrives', async () => {
    await page.viewport(900, 700)
    const user = userEvent.setup()
    function Harness() {
      const [current, setCurrent] = useState(1)
      return (
        <CursorPager
          page={current}
          hasNext={current === 1}
          onPrevious={() => setCurrent((page) => page - 1)}
          onNext={() => setCurrent((page) => page + 1)}
        />
      )
    }
    renderWithProviders(<Harness />)
    const next = screen.getByRole('button', { name: ZH.pager.next })

    await user.click(next)

    expect(document.activeElement).toBe(next)
    expect(next).toHaveFocus()
    await waitFor(() =>
      expect(next).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(document.activeElement).toBe(next)
  })

  it('keeps focus on the direction that started a page read while it runs', async () => {
    await page.viewport(900, 700)
    const user = userEvent.setup()
    renderWithProviders(
      <CursorPager
        page={0}
        hasNext
        reading
        onPrevious={() => undefined}
        onNext={() => undefined}
      />,
    )
    const previous = screen.getByRole('button', { name: ZH.pager.previous })
    const next = screen.getByRole('button', { name: ZH.pager.next })

    next.focus()
    await user.click(next)

    expect(document.activeElement).toBe(next)
    expect(next).toHaveAttribute('aria-disabled', 'true')
    expect(previous).toHaveAttribute('aria-disabled', 'true')
  })
})
