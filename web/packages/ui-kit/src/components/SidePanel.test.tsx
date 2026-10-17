/**
 * SidePanel contract: a controlled modal panel whose close button,
 * Escape key and backdrop click all report onClose and nothing closes
 * itself; `busy` makes all three inert; onExited reports the end of the
 * exit transition (never the open flip); the dialog is named by its
 * title (an h2) and described only when a description is given; role and
 * aria-modal come from MUI exactly once; the panel stacks at
 * theme.zIndex.modal, above the AppShell AppBar; focus moves into the
 * panel on open and back to the opener on close. Built-in text comes
 * from the ui-kit namespace; the close label is host-overridable.
 */

import { useState } from 'react'
import { act, fireEvent, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { useTheme } from '@mui/material/styles'
import type { Theme } from '@mui/material/styles'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { switchLanguage } from '@speed/i18n'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { SidePanel } from './SidePanel.js'
import type { SidePanelProps } from './SidePanel.js'

const TITLE = 'Account details'
const DESCRIPTION = 'Review the account and its sessions.'
const BODY = 'Panel body'

function setup(overrides: Partial<SidePanelProps> = {}) {
  const onClose = vi.fn()
  const onExited = vi.fn()
  const utils = renderWithProviders(
    <SidePanel open title={TITLE} onClose={onClose} onExited={onExited} {...overrides}>
      <p>{BODY}</p>
    </SidePanel>,
  )
  return { onClose, onExited, ...utils }
}

/** A host that opens the panel from a button, so focus restoration has an opener. */
function FocusHarness() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open panel
      </button>
      <SidePanel
        open={open}
        title={TITLE}
        onClose={() => setOpen(false)}
        onExited={() => {}}
      >
        <button type="button">Inside the panel</button>
      </SidePanel>
    </>
  )
}

/** A host whose close control drives `open`, so the exit transition runs on a live tree. */
function ExitHarness({ onExited }: { readonly onExited: () => void }) {
  const [open, setOpen] = useState(true)
  return (
    <SidePanel open={open} title={TITLE} onClose={() => setOpen(false)} onExited={onExited}>
      <button type="button" onClick={() => setOpen(false)}>
        Close the panel
      </button>
    </SidePanel>
  )
}

describe('SidePanel', () => {
  it('renders the dialog, its h2 title, the body and the built-in close label', () => {
    const { getByRole, getByText } = setup()
    const dialog = getByRole('dialog')
    expect(dialog).toBeInTheDocument()
    expect(getByRole('heading', { level: 2, name: TITLE })).toBeInTheDocument()
    expect(getByText(BODY)).toBeInTheDocument()
    expect(getByRole('button', { name: zhCN.sidePanel.closeLabel })).toBeInTheDocument()
  })

  it('switches the built-in close label to en-US when the language changes', async () => {
    const { i18n, getByRole, queryByRole } = setup()
    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })
    expect(getByRole('button', { name: enUS.sidePanel.closeLabel })).toBeInTheDocument()
    expect(queryByRole('button', { name: zhCN.sidePanel.closeLabel })).not.toBeInTheDocument()
  })

  it('lets closeLabel override the close control accessible name', () => {
    const { getByRole, queryByRole } = setup({ closeLabel: 'Dismiss details' })
    expect(getByRole('button', { name: 'Dismiss details' })).toBeInTheDocument()
    expect(queryByRole('button', { name: zhCN.sidePanel.closeLabel })).not.toBeInTheDocument()
  })

  it('names the dialog from the title and describes it only when a description is given', () => {
    const withoutDescription = setup()
    const named = withoutDescription.getByRole('dialog')
    const title = withoutDescription.getByRole('heading', { level: 2, name: TITLE })
    expect(named).toHaveAttribute('aria-labelledby', title.id)
    expect(named).not.toHaveAttribute('aria-describedby')
    withoutDescription.unmount()

    const withDescription = setup({ description: DESCRIPTION })
    const described = withDescription.getByRole('dialog')
    const descriptionId = described.getAttribute('aria-describedby')
    expect(descriptionId).not.toBeNull()
    expect(document.getElementById(descriptionId!)).toHaveTextContent(DESCRIPTION)
  })

  it('takes role="dialog" and aria-modal from the drawer exactly once', () => {
    setup()
    expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1)
    expect(document.querySelectorAll('[aria-modal]')).toHaveLength(1)
    expect(document.querySelector('[aria-modal]')).toHaveAttribute('aria-modal', 'true')
  })

  it('stacks at theme.zIndex.modal, above the AppShell AppBar', () => {
    // Read the resolved theme through the same provider tree the panel
    // renders under, so the assertion compares against the theme's value
    // rather than a number copied into the test.
    let theme: Theme | undefined
    function ThemeProbe() {
      theme = useTheme()
      return null
    }
    renderWithProviders(
      <>
        <ThemeProbe />
        <SidePanel open title={TITLE} onClose={vi.fn()} onExited={vi.fn()}>
          <p>{BODY}</p>
        </SidePanel>
      </>,
    )
    expect(theme).toBeDefined()
    const root = document.querySelector<HTMLElement>('.MuiDrawer-root')
    expect(root).not.toBeNull()
    // The AppShell AppBar sits at theme.zIndex.drawer + 1; the panel must
    // resolve its stacking context above that.
    expect(theme!.zIndex.modal).toBeGreaterThan(theme!.zIndex.drawer + 1)
    expect(getComputedStyle(root!).zIndex).toBe(String(theme!.zIndex.modal))
  })

  it('takes the medium width by default and the wide tier when asked, filling narrow viewports', () => {
    const medium = setup()
    const mediumCss = emittedStyleText()
    expect(mediumCss).toContain('36rem')
    expect(mediumCss).not.toContain('52rem')
    expect(mediumCss).toContain('@media (min-width:600px)')
    medium.unmount()

    setup({ size: 'wide' })
    expect(emittedStyleText()).toContain('52rem')
  })

  it('calls onClose on Escape when not busy', () => {
    const { onClose, getByRole } = setup()
    fireEvent.keyDown(getByRole('dialog'), { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not call onClose on Escape while busy', () => {
    const { onClose, getByRole } = setup({ busy: true })
    fireEvent.keyDown(getByRole('dialog'), { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()
  })

  it('calls onClose on a backdrop click when not busy', () => {
    const { onClose } = setup()
    const backdrop = document.querySelector('.MuiBackdrop-root')
    expect(backdrop).not.toBeNull()
    fireEvent.click(backdrop!)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not call onClose on a backdrop click while busy', () => {
    const { onClose } = setup({ busy: true })
    const backdrop = document.querySelector('.MuiBackdrop-root')
    expect(backdrop).not.toBeNull()
    fireEvent.click(backdrop!)
    expect(onClose).not.toHaveBeenCalled()
  })

  it('calls onClose on the close button when not busy', async () => {
    const { onClose, getByRole } = setup()
    const user = userEvent.setup()
    await user.click(getByRole('button', { name: zhCN.sidePanel.closeLabel }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('disables the close button and ignores its click while busy', () => {
    const { onClose, getByRole } = setup({ busy: true })
    const close = getByRole('button', { name: zhCN.sidePanel.closeLabel })
    expect(close).toBeDisabled()
    // fireEvent bypasses the pointer-events check user-event enforces on a
    // disabled control; the handler's own gate is also under test here.
    fireEvent.click(close)
    expect(onClose).not.toHaveBeenCalled()
  })

  it('fires onExited only after the exit transition, never synchronously with the open change', async () => {
    const onExited = vi.fn()
    const { getByRole } = renderWithProviders(<ExitHarness onExited={onExited} />)
    expect(onExited).not.toHaveBeenCalled()

    // The host closes the panel; the open flip alone is not the exit.
    fireEvent.click(getByRole('button', { name: 'Close the panel' }))
    expect(getByRole('dialog')).toBeInTheDocument()
    expect(onExited).not.toHaveBeenCalled()
    await waitFor(() => expect(onExited).toHaveBeenCalledTimes(1))
  })

  it('moves focus into the panel on open and back to the opener on close', async () => {
    const user = userEvent.setup()
    const { getByRole } = renderWithProviders(<FocusHarness />)
    const opener = getByRole('button', { name: 'Open panel' })

    await user.click(opener)
    const dialog = getByRole('dialog')
    await waitFor(() => expect(dialog).toHaveFocus())

    await user.click(within(dialog).getByRole('button', { name: zhCN.sidePanel.closeLabel }))
    await waitFor(() => expect(opener).toHaveFocus())
  })

  it('passes axe over an open panel', async () => {
    setup({ description: DESCRIPTION })
    await expectNoAxeViolations()
  })
})
