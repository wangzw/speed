/**
 * RefreshButton: the shared refresh control.
 *
 * The tests hold the two disabling reasons apart: an empty scope explains
 * itself through the surface's own hint, and a write in flight disables
 * the control without that explanation. A read in flight disables nothing
 * -- it marks the control busy, keeps it focusable and drops a second
 * activation. The visible text comes from the caller's label, with the
 * ui-kit namespace's key as the default.
 */

import { createRef, useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RefreshButton } from './RefreshButton.js'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const REFRESH = zhCN.refreshButton.label
/** A caller-supplied empty-scope hint; the surface owns this copy, not the control. */
const HINT = 'Choose a tenant first.'
/** A caller-supplied visible label from its own package's namespace. */
const CALLER_LABEL = 'Reload list'

/** The host state the test flips without going through a focus-taking control. */
const writePendingControls: {
  setWritePending: (pending: boolean) => void
  onClick: () => void
} = {
  setWritePending: () => undefined,
  onClick: () => undefined,
}

function WritePendingHarness() {
  const [writePending, setWritePending] = useState(false)
  writePendingControls.setWritePending = setWritePending
  return (
    <RefreshButton
      busy={false}
      writePending={writePending}
      describedBy="scope-hint"
      disabledReason={HINT}
      onClick={writePendingControls.onClick}
    />
  )
}

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

  it('explains an empty scope through the surface hint and the same tooltip text', async () => {
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

    expect(refresh).toHaveAttribute('aria-disabled', 'true')
    expect(refresh).toHaveAttribute('aria-describedby', 'scope-hint')
    // The control stays focusable while unavailable, so the tooltip
    // attaches to the button itself rather than a wrapper span.
    refresh.focus()
    expect(refresh).toBe(document.activeElement)
    await user.hover(refresh)
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

    expect(refresh).not.toHaveAttribute('aria-disabled', 'true')
    expect(refresh).not.toHaveAttribute('aria-describedby')
    expect(refresh.parentElement?.tagName).not.toBe('SPAN')
  })

  it('keeps the control focusable and refuses activation for a write in flight', async () => {
    const user = userEvent.setup()
    const onClick = vi.fn()
    writePendingControls.onClick = onClick
    renderWithProviders(<WritePendingHarness />)
    const refresh = screen.getByRole('button', { name: REFRESH })
    refresh.focus()
    expect(refresh).toHaveFocus()

    // The write starts in the surface's scope while the control holds
    // focus. The control turns unavailable, but it must neither become
    // natively disabled nor drop the focus the keyboard user brought.
    act(() => {
      writePendingControls.setWritePending(true)
    })

    expect(refresh).toHaveAttribute('aria-disabled', 'true')
    expect(refresh).not.toBeDisabled()
    expect(refresh).toHaveFocus()
    expect(refresh).not.toHaveAttribute('aria-describedby')
    expect(refresh.parentElement?.tagName).not.toBe('SPAN')
    // A hover does not raise the empty-scope tooltip either: the control
    // carries no tooltip at all.
    fireEvent.mouseOver(refresh)
    expect(screen.queryByRole('tooltip')).toBeNull()

    // Every activation route is swallowed by the guard: a pointer click,
    // Enter and Space all leave onClick uncalled.
    fireEvent.click(refresh)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(onClick).not.toHaveBeenCalled()
    expect(refresh).toHaveFocus()
  })

  it('keeps the same focused element as the empty-scope explanation comes and goes', () => {
    const controls: { setDisabled: (disabled: boolean) => void } = {
      setDisabled: () => undefined,
    }
    function DisabledHarness() {
      const [disabled, setDisabled] = useState(true)
      controls.setDisabled = setDisabled
      return (
        <RefreshButton
          busy={false}
          disabled={disabled}
          describedBy="scope-hint"
          disabledReason={HINT}
          onClick={() => undefined}
        />
      )
    }
    renderWithProviders(
      <>
        <span id="scope-hint">{HINT}</span>
        <DisabledHarness />
      </>,
    )
    const refresh = screen.getByRole('button', { name: REFRESH })
    refresh.focus()
    expect(refresh).toHaveFocus()

    // Choosing the scope drops the explanation, and the control becomes
    // available. The state change must not remount the button: a root that
    // changes from the tooltip-wrapped form to the bare form unmounts the
    // focused node and drops the keyboard user's place.
    act(() => {
      controls.setDisabled(false)
    })
    expect(screen.getByRole('button', { name: REFRESH })).toBe(refresh)
    expect(refresh).toHaveFocus()

    // Re-entering the empty-scope face is the same round trip in reverse.
    act(() => {
      controls.setDisabled(true)
    })
    expect(screen.getByRole('button', { name: REFRESH })).toBe(refresh)
    expect(refresh).toHaveFocus()
  })

  it('renders the caller label in place of the namespace default', () => {
    renderWithProviders(
      <RefreshButton busy={false} label={CALLER_LABEL} onClick={() => undefined} />,
    )
    expect(
      screen.getByRole('button', { name: CALLER_LABEL }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: REFRESH })).toBeNull()
  })

  it('forwards sx, className and ref to the control', () => {
    const ref = createRef<HTMLButtonElement>()
    renderWithProviders(
      <RefreshButton
        busy={false}
        ref={ref}
        className="host-refresh"
        sx={{ mt: 4 }}
        onClick={() => undefined}
      />,
    )
    const button = screen.getByRole('button', { name: REFRESH })
    expect(button).toHaveClass('host-refresh')
    expect(button).toBe(ref.current)
    expect(button).toHaveStyle({ marginTop: '32px' })
  })
})
