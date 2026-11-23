/**
 * CursorPager: the shared previous/next control for cursor-paged lists.
 *
 * A page change starts a read while the control itself holds focus, so the
 * unavailable state must not take focus away from the direction the operator
 * activated. The direction that started the read reports it as busy; the
 * other direction, a write in flight, and a list boundary make a direction
 * unavailable without a reason.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CursorPager } from './cursor-pager.js'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const ZH = zhCN.common as {
  readonly pager: { readonly previous: string; readonly next: string }
}

function directions() {
  return {
    previous: screen.getByRole('button', { name: ZH.pager.previous }),
    next: screen.getByRole('button', { name: ZH.pager.next }),
  }
}

describe('CursorPager', () => {
  it('keeps focus on the next direction when the last page arrives', async () => {
    const user = userEvent.setup()
    function Harness() {
      const [page, setPage] = useState(1)
      return (
        <CursorPager
          page={page}
          hasNext={page === 1}
          onPrevious={() => setPage((current) => current - 1)}
          onNext={() => setPage((current) => current + 1)}
        />
      )
    }
    renderWithProviders(<Harness />)
    const next = screen.getByRole('button', { name: ZH.pager.next })

    await user.click(next)

    expect(next).toHaveFocus()
    await waitFor(() =>
      expect(next).toHaveAttribute('aria-disabled', 'true'),
    )
  })

  it('reports the activated direction busy during the read and refuses a second activation', async () => {
    const user = userEvent.setup()
    const onNext = vi.fn()
    function Harness() {
      const [state, setState] = useState({ page: 0, reading: false })
      return (
        <CursorPager
          page={state.page}
          hasNext
          reading={state.reading}
          onPrevious={() =>
            setState((current) => ({
              page: current.page - 1,
              reading: true,
            }))
          }
          onNext={() => {
            onNext()
            setState((current) => ({
              page: current.page + 1,
              reading: true,
            }))
          }}
        />
      )
    }
    renderWithProviders(<Harness />)
    const { previous, next } = directions()

    await user.click(next)

    expect(next).toHaveAttribute('aria-busy', 'true')
    expect(next).toHaveAttribute('aria-disabled', 'true')
    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(previous).not.toHaveAttribute('aria-busy')
    expect(next).toHaveFocus()

    await user.click(next)
    expect(onNext).toHaveBeenCalledTimes(1)
  })

  it('reports the previous direction busy when the read started from it', async () => {
    const user = userEvent.setup()
    function Harness() {
      const [state, setState] = useState({ page: 1, reading: false })
      return (
        <CursorPager
          page={state.page}
          hasNext
          reading={state.reading}
          onPrevious={() =>
            setState((current) => ({
              page: current.page - 1,
              reading: true,
            }))
          }
          onNext={() =>
            setState((current) => ({
              page: current.page + 1,
              reading: true,
            }))
          }
        />
      )
    }
    renderWithProviders(<Harness />)
    const { previous, next } = directions()

    await user.click(previous)

    expect(previous).toHaveAttribute('aria-busy', 'true')
    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(next).toHaveAttribute('aria-disabled', 'true')
    expect(next).not.toHaveAttribute('aria-busy')
    expect(previous).toHaveFocus()
  })

  it('makes both directions unavailable while a read nobody started here runs', () => {
    renderWithProviders(
      <CursorPager page={1} hasNext reading onPrevious={() => undefined} onNext={() => undefined} />,
    )
    const { previous, next } = directions()

    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(next).toHaveAttribute('aria-disabled', 'true')
    expect(previous).not.toHaveAttribute('aria-busy')
    expect(next).not.toHaveAttribute('aria-busy')
  })

  it('makes both directions unavailable without a reason while a write is in flight', async () => {
    const user = userEvent.setup()
    const onPrevious = vi.fn()
    const onNext = vi.fn()
    renderWithProviders(
      <CursorPager page={1} hasNext busy onPrevious={onPrevious} onNext={onNext} />,
    )
    const { previous, next } = directions()

    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(next).toHaveAttribute('aria-disabled', 'true')
    expect(previous).not.toHaveAttribute('aria-busy')
    expect(next).not.toHaveAttribute('aria-busy')
    expect(previous).not.toHaveAttribute('aria-describedby')
    expect(next).not.toHaveAttribute('aria-describedby')

    await user.click(previous)
    await user.click(next)
    expect(onPrevious).not.toHaveBeenCalled()
    expect(onNext).not.toHaveBeenCalled()
  })

  it('makes a boundary direction unavailable without a reason', async () => {
    const user = userEvent.setup()
    const onPrevious = vi.fn()
    renderWithProviders(
      <CursorPager page={0} hasNext onPrevious={onPrevious} onNext={() => undefined} />,
    )
    const { previous, next } = directions()

    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(previous).not.toHaveAttribute('aria-describedby')
    expect(next).not.toHaveAttribute('aria-disabled')
    expect(next).not.toHaveAttribute('aria-busy')

    await user.click(previous)
    expect(onPrevious).not.toHaveBeenCalled()
  })
})
