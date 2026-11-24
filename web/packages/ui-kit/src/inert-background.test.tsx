
import { StrictMode, useRef, useState } from 'react'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import Dialog from '@mui/material/Dialog'
import MenuItem from '@mui/material/MenuItem'
import TextField from '@mui/material/TextField'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { useInertBackground, useInertSelectMenu } from './inert-background.js'
import { ConfirmDialog } from './components/ConfirmDialog.js'

function InlineOverlay({ open }: { readonly open: boolean }) {
  const ref = useRef<HTMLDivElement>(null)
  useInertBackground(open, ref)
  if (!open) {
    return null
  }
  return (
    <div ref={ref} data-testid="overlay">
      <button type="button">Inside the overlay</button>
    </div>
  )
}

function InlineHarness({ open = true }: { readonly open?: boolean }) {
  return (
    <div data-testid="app">
      <button type="button">Background action</button>
      <InlineOverlay open={open} />
    </div>
  )
}

function KeepMountedOverlay({ open }: { readonly open: boolean }) {
  const ref = useRef<HTMLDivElement>(null)
  useInertBackground(open, ref)
  return (
    <Dialog ref={ref} open={open} keepMounted>
      <button type="button">Inside the kept overlay</button>
    </Dialog>
  )
}

function StackLayer({
  open,
  name,
}: {
  readonly open: boolean
  readonly name: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  useInertBackground(open, ref)
  if (!open) {
    return null
  }
  return (
    <div ref={ref} data-testid={name}>
      <button type="button">{name} action</button>
    </div>
  )
}

function StackHarness() {
  const [outer, setOuter] = useState(false)
  const [inner, setInner] = useState(false)
  return (
    <>
      <div data-testid="app">
        <button type="button">App action</button>
      </div>
      <div>
        <button type="button" onClick={() => setOuter((value) => !value)}>
          toggle outer
        </button>
        <button type="button" onClick={() => setInner((value) => !value)}>
          toggle inner
        </button>
      </div>
      <StackLayer open={outer} name="outer" />
      <StackLayer open={inner} name="inner" />
    </>
  )
}

function toggle(name: string): void {
  fireEvent.click(screen.getByRole('button', { name }))
}

function SelectHarness() {
  const menu = useInertSelectMenu()
  return (
    <TextField
      select
      label="Choice"
      value="alpha"
      onChange={() => undefined}
      slotProps={{ select: menu }}
    >
      <MenuItem value="alpha">Alpha</MenuItem>
      <MenuItem value="beta">Beta</MenuItem>
    </TextField>
  )
}

describe('useInertBackground', () => {
  it('marks the background siblings inert while the overlay is open', () => {
    const { container } = renderWithProviders(<InlineHarness open />)
    const app = screen.getByTestId('app')
    expect(app).not.toHaveAttribute('inert')
    expect(container).not.toHaveAttribute('inert')
    expect(screen.getByRole('button', { name: 'Background action' })).toHaveAttribute(
      'inert',
    )
    expect(screen.getByTestId('overlay')).not.toHaveAttribute('inert')
    expect(
      screen.getByRole('button', { name: 'Inside the overlay' }),
    ).not.toHaveAttribute('inert')
  })

  it('lifts the inert flag when the overlay closes', () => {
    const { container, rerender } = renderWithProviders(<InlineHarness open />)
    expect(screen.getByRole('button', { name: 'Background action' })).toHaveAttribute(
      'inert',
    )
    rerender(<InlineHarness open={false} />)
    expect(screen.getByRole('button', { name: 'Background action' })).not.toHaveAttribute(
      'inert',
    )
    expect(container).not.toHaveAttribute('inert')
  })

  it('does not mark a keepMounted overlay that is already in the document', async () => {
    const { container, rerender } = renderWithProviders(
      <KeepMountedOverlay open={false} />,
    )
    const root = document.querySelector('.MuiModal-root')
    expect(root).not.toBeNull()
    expect(root).not.toHaveAttribute('inert')
    expect(container).not.toHaveAttribute('inert')

    rerender(<KeepMountedOverlay open />)
    await waitFor(() => expect(container).toHaveAttribute('inert'))
    expect(document.querySelector('.MuiModal-root')).not.toHaveAttribute('inert')

    rerender(<KeepMountedOverlay open={false} />)
    await waitFor(() => expect(container).not.toHaveAttribute('inert'))
  })

  it('renders the start sentinel of a closing dialog with tabindex -1', () => {
    function ClosingDialogHarness() {
      const [open, setOpen] = useState(true)
      return (
        <Dialog open={open} onClose={() => setOpen(false)}>
          <button type="button" onClick={() => setOpen(false)}>
            Close the dialog
          </button>
        </Dialog>
      )
    }
    renderWithProviders(<ClosingDialogHarness />)
    expect(
      document.querySelector('[data-testid="sentinelStart"]'),
    ).toHaveAttribute('tabindex', '0')

    fireEvent.click(screen.getByRole('button', { name: 'Close the dialog' }))

    // The modal root outlives the closing commit until the exit transition
    // ends; the sentinel already carries -1, which the hook reads to tell a
    // closing overlay from an open one.
    const sentinel = document.querySelector('[data-testid="sentinelStart"]')
    expect(sentinel).not.toBeNull()
    expect(sentinel).toHaveAttribute('tabindex', '-1')
    expect(sentinel?.closest('.MuiModal-root')).not.toBeNull()
  })

  it('leaves an inert flag the host set in place after the overlay closes', () => {
    function HostInertHarness() {
      const [open, setOpen] = useState(false)
      return (
        <>
          <div data-testid="host-inert" inert>
            <button type="button">Host hidden</button>
          </div>
          <button type="button" onClick={() => setOpen(true)}>
            Open overlay
          </button>
          <button type="button" onClick={() => setOpen(false)}>
            Close overlay
          </button>
          <InlineOverlay open={open} />
        </>
      )
    }
    renderWithProviders(<HostInertHarness />)
    fireEvent.click(screen.getByRole('button', { name: 'Open overlay' }))
    expect(screen.getByTestId('host-inert')).toHaveAttribute('inert')
    fireEvent.click(screen.getByRole('button', { name: 'Close overlay' }))
    expect(screen.getByTestId('host-inert')).toHaveAttribute('inert')
  })

  it('holds stacked layers by count, and an out-of-order close keeps the inner hold', () => {
    const { container } = renderWithProviders(<StackHarness />)
    toggle('toggle outer')
    expect(screen.getByTestId('app')).toHaveAttribute('inert')

    toggle('toggle inner')
    expect(screen.getByTestId('app')).toHaveAttribute('inert')
    expect(screen.getByTestId('outer')).toHaveAttribute('inert')

    toggle('toggle inner')
    expect(screen.getByTestId('outer')).not.toHaveAttribute('inert')
    expect(screen.getByTestId('app')).toHaveAttribute('inert')

    toggle('toggle outer')
    expect(screen.getByTestId('app')).not.toHaveAttribute('inert')
    expect(container).not.toHaveAttribute('inert')
  })

  it('keeps the outer hold when the outer layer closes first', () => {
    renderWithProviders(<StackHarness />)
    toggle('toggle outer')
    toggle('toggle inner')

    toggle('toggle outer')
    expect(screen.getByTestId('app')).toHaveAttribute('inert')

    toggle('toggle inner')
    expect(screen.getByTestId('app')).not.toHaveAttribute('inert')
  })

  it('does not mark nodes inserted into body while the overlay is open', () => {
    renderWithProviders(<InlineHarness open />)
    const inserted = document.createElement('div')
    document.body.append(inserted)
    try {
      expect(inserted).not.toHaveAttribute('inert')
    } finally {
      inserted.remove()
    }
  })

  it('skips non-rendering elements in the background', () => {
    const script = document.createElement('script')
    document.body.append(script)
    try {
      renderWithProviders(<InlineHarness open />)
      expect(script).not.toHaveAttribute('inert')
    } finally {
      script.remove()
    }
  })

  it('stays idempotent under StrictMode double-invoked effects', () => {
    const { rerender } = renderWithProviders(
      <StrictMode>
        <InlineHarness open />
      </StrictMode>,
    )
    expect(screen.getByRole('button', { name: 'Background action' })).toHaveAttribute(
      'inert',
    )
    rerender(
      <StrictMode>
        <InlineHarness open={false} />
      </StrictMode>,
    )
    expect(screen.getByRole('button', { name: 'Background action' })).not.toHaveAttribute(
      'inert',
    )
  })

  it('marks the background inert while a select menu is open and lifts it on close', async () => {
    const { container, getByRole } = renderWithProviders(<SelectHarness />)
    expect(container).not.toHaveAttribute('inert')

    fireEvent.mouseDown(getByRole('combobox'))
    await waitFor(() => expect(container).toHaveAttribute('inert'))

    fireEvent.keyDown(getByRole('listbox'), { key: 'Escape' })
    await waitFor(() => expect(container).not.toHaveAttribute('inert'))
  })

  it('leaves no aria-hidden-focus report on an open dialog with an inert background', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    try {
      renderWithProviders(
        <>
          <button type="button">Background action</button>
          <ConfirmDialog open onConfirm={vi.fn()} onCancel={vi.fn()} />
        </>,
      )
      await expectNoAxeViolations()
      const reports = warn.mock.calls
        .map((call) => call.join(' '))
        .filter((line) => line.includes('aria-hidden-focus'))
      expect(reports).toEqual([])
    } finally {
      warn.mockRestore()
    }
  })
})
