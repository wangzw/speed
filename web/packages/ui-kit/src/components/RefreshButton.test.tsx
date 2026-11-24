
import { createRef, useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RefreshButton } from './RefreshButton.js'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const REFRESH = zhCN.refreshButton.label
const HINT = 'Choose a tenant first.'
const CALLER_LABEL = 'Reload list'

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

    act(() => {
      writePendingControls.setWritePending(true)
    })

    expect(refresh).toHaveAttribute('aria-disabled', 'true')
    expect(refresh).not.toBeDisabled()
    expect(refresh).toHaveFocus()
    expect(refresh).not.toHaveAttribute('aria-describedby')
    expect(refresh.parentElement?.tagName).not.toBe('SPAN')
    fireEvent.mouseOver(refresh)
    expect(screen.queryByRole('tooltip')).toBeNull()

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

    act(() => {
      controls.setDisabled(false)
    })
    expect(screen.getByRole('button', { name: REFRESH })).toBe(refresh)
    expect(refresh).toHaveFocus()

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
