/**
 * RefreshButton: the console's one refresh control.
 *
 * The tests hold the two disabling reasons apart: an empty scope explains
 * itself through the page's own hint, and a write in flight disables the
 * control without that explanation. A read in flight disables nothing --
 * it marks the control busy, keeps it focusable and drops a second
 * activation.
 */

import { describe, expect, it, vi } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RefreshButton } from './refresh-button.js'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const REFRESH = zhCN.common.refresh
/** A caller-supplied empty-scope hint; the page owns this copy, not the control. */
const HINT = 'Choose a tenant first.'

describe('RefreshButton', () => {
  it('marks a read in flight busy, keeps the control focusable, and drops a second activation', () => {
    const onClick = vi.fn()
    renderWithProviders(<RefreshButton busy onClick={onClick} />)
    const refresh = screen.getByRole('button', { name: REFRESH })

    expect(refresh).toHaveAttribute('aria-busy', 'true')
    expect(refresh).not.toBeDisabled()
    refresh.focus()
    expect(refresh).toBe(document.activeElement)

    // A forced activation while the read is in flight issues no second
    // request and queues none.
    fireEvent.click(refresh)
    expect(onClick).not.toHaveBeenCalled()
  })

  it('explains an empty scope through the page hint and the same tooltip text', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <span id="scope-hint">{HINT}</span>
        <RefreshButton
          busy={false}
          disabled
          describedBy="scope-hint"
          disabledReason={HINT}
          onClick={() => undefined}
        />
      </>,
    )
    const refresh = screen.getByRole('button', { name: REFRESH })

    expect(refresh).toBeDisabled()
    expect(refresh).toHaveAttribute('aria-describedby', 'scope-hint')
    // A native disabled button receives no pointer events, so the tooltip
    // rides a span wrapper.
    await user.hover(refresh.parentElement as HTMLElement)
    expect(await screen.findByRole('tooltip')).toHaveTextContent(HINT)
  })

  it('drops the explanation once the scope is chosen', () => {
    renderWithProviders(
      <RefreshButton
        busy={false}
        describedBy="scope-hint"
        disabledReason={HINT}
        onClick={() => undefined}
      />,
    )
    const refresh = screen.getByRole('button', { name: REFRESH })

    expect(refresh).not.toBeDisabled()
    expect(refresh).not.toHaveAttribute('aria-describedby')
    expect(refresh.parentElement?.tagName).not.toBe('SPAN')
  })

  it('disables for a write in flight without claiming the scope is empty', () => {
    const onClick = vi.fn()
    renderWithProviders(
      <RefreshButton
        busy={false}
        writePending
        describedBy="scope-hint"
        disabledReason={HINT}
        onClick={onClick}
      />,
    )
    const refresh = screen.getByRole('button', { name: REFRESH })

    expect(refresh).toBeDisabled()
    expect(refresh).not.toHaveAttribute('aria-describedby')
    expect(refresh.parentElement?.tagName).not.toBe('SPAN')
    // A hover does not raise the empty-scope tooltip either: the control
    // carries no tooltip wrapper at all.
    fireEvent.mouseOver(refresh)
    expect(screen.queryByRole('tooltip')).toBeNull()
    fireEvent.click(refresh)
    expect(onClick).not.toHaveBeenCalled()
  })
})
