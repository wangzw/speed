
import { createRef, useState } from 'react'
import { act, fireEvent, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { useTheme } from '@mui/material/styles'
import type { Theme } from '@mui/material/styles'
import { backdropClasses } from '@mui/material/Backdrop'
import { drawerClasses } from '@mui/material/Drawer'
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

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/** jsdom cannot evaluate a breakpoint at-rule, so this reads the emitted CSS for the element's own class. */
function widthAt(
  css: string,
  element: Element,
  minWidth: number,
): string | undefined {
  const emotionClass = Array.from(element.classList).find((name) =>
    name.startsWith('css-'),
  )
  if (emotionClass === undefined) {
    return undefined
  }
  const rule = new RegExp(
    `@media \\(min-width:${minWidth}px\\)\\{\\.${escapeRegExp(emotionClass)}\\{([^}]*)\\}\\}`,
  ).exec(css)
  if (rule === null) {
    return undefined
  }
  return /(?:^|;)width:([^;]+)/.exec(rule[1] ?? '')?.[1]
}

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

const busyControls: {
  setBusy: (busy: boolean) => void
  onClose: () => void
} = {
  setBusy: () => undefined,
  onClose: () => undefined,
}

function BusyHarness() {
  const [busy, setBusy] = useState(false)
  busyControls.setBusy = setBusy
  return (
    <SidePanel
      open
      title={TITLE}
      busy={busy}
      onClose={busyControls.onClose}
      onExited={() => undefined}
    >
      <p>{BODY}</p>
    </SidePanel>
  )
}

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

  it('takes role="dialog", aria-modal and its tabIndex from the drawer exactly once', () => {
    setup()
    expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(1)
    expect(document.querySelectorAll('[aria-modal]')).toHaveLength(1)
    const dialog = document.querySelector('[role="dialog"]')
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(dialog).toBe(document.querySelector(`.${drawerClasses.paper}`))
    expect(dialog).toHaveAttribute('tabindex', '-1')
  })

  it('stacks at theme.zIndex.modal, above the AppShell AppBar', () => {
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
      { tenantOverrides: { zIndex: { values: { modal: 1600 } } } },
    )
    expect(theme).toBeDefined()
    expect(theme!.zIndex.modal).toBe(1600)
    const root = document.querySelector<HTMLElement>(`.${drawerClasses.root}`)
    expect(root).not.toBeNull()
    expect(theme!.zIndex.modal).toBeGreaterThan(theme!.zIndex.drawer + 1)
    expect(getComputedStyle(root!).zIndex).toBe('1600')
  })

  it('fills the viewport width below sm and takes the tier width from sm up', () => {
    const medium = setup()
    const mediumPaper = document.querySelector(`.${drawerClasses.paper}`)
    expect(mediumPaper).not.toBeNull()
    const mediumCss = emittedStyleText()
    expect(widthAt(mediumCss, mediumPaper!, 0)).toBe('100%')
    expect(widthAt(mediumCss, mediumPaper!, 600)).toBe('36rem')
    expect(mediumCss).not.toContain('52rem')
    medium.unmount()

    setup({ size: 'wide' })
    const widePaper = document.querySelector(`.${drawerClasses.paper}`)
    expect(widePaper).not.toBeNull()
    const wideCss = emittedStyleText()
    expect(widthAt(wideCss, widePaper!, 0)).toBe('100%')
    expect(widthAt(wideCss, widePaper!, 600)).toBe('52rem')
  })

  it('anchors the panel to the right edge', () => {
    setup()
    const root = document.querySelector(`.${drawerClasses.root}`)
    const paper = document.querySelector<HTMLElement>(`.${drawerClasses.paper}`)
    expect(root).not.toBeNull()
    expect(paper).not.toBeNull()
    expect(root).toHaveClass(drawerClasses.anchorRight)
    expect(getComputedStyle(paper!).right).toBe('0px')
    expect(getComputedStyle(paper!).left).toBe('auto')
  })

  it('keeps the paper fixed and makes the content box the only scroll layer', () => {
    const { getByText } = setup()
    const paper = document.querySelector<HTMLElement>(`.${drawerClasses.paper}`)
    expect(paper).not.toBeNull()
    expect(paper).toHaveStyle({ overflowY: 'hidden' })

    const content = getByText(BODY).parentElement
    expect(content).not.toBeNull()
    expect(content).toHaveStyle({
      overflowY: 'auto',
      flexGrow: '1',
      minHeight: '0px',
    })
  })

  it('renders the actions footer below the scroll layer, not inside it', () => {
    const { getByRole, getByText } = setup({
      actions: <button type="button">Save details</button>,
    })
    const content = getByText(BODY).parentElement
    const action = getByRole('button', { name: 'Save details' })
    expect(content).not.toBeNull()
    expect(content).not.toContainElement(action)
    expect(content!.nextElementSibling).toContainElement(action)
    expect(content!.nextElementSibling).toHaveStyle({
      display: 'flex',
      justifyContent: 'flex-end',
    })
  })

  it('renders no footer when no actions are given', () => {
    const { getByText } = setup()
    const content = getByText(BODY).parentElement
    expect(content).not.toBeNull()
    expect(content!.nextElementSibling).toBeNull()
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
    const backdrop = document.querySelector(`.${backdropClasses.root}`)
    expect(backdrop).not.toBeNull()
    fireEvent.click(backdrop!)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('does not call onClose on a backdrop click while busy', () => {
    const { onClose } = setup({ busy: true })
    const backdrop = document.querySelector(`.${backdropClasses.root}`)
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

  it('keeps the close button focusable and refuses activation while busy', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    busyControls.onClose = onClose
    const { getByRole } = renderWithProviders(<BusyHarness />)
    const dialog = getByRole('dialog')
    await waitFor(() => expect(dialog).toHaveFocus())

    const close = within(dialog).getByRole('button', {
      name: zhCN.sidePanel.closeLabel,
    })
    close.focus()
    expect(close).toHaveFocus()

    act(() => {
      busyControls.setBusy(true)
    })

    expect(close).toHaveAttribute('aria-disabled', 'true')
    expect(close).not.toBeDisabled()
    expect(close).toHaveFocus()

    fireEvent.click(close)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(onClose).not.toHaveBeenCalled()
    expect(close).toHaveFocus()
  })

  it('fires onExited only after the exit transition, never synchronously with the open change', async () => {
    const onExited = vi.fn()
    const { getByRole } = renderWithProviders(<ExitHarness onExited={onExited} />)
    expect(onExited).not.toHaveBeenCalled()

    fireEvent.click(getByRole('button', { name: 'Close the panel' }))
    expect(getByRole('dialog')).toBeInTheDocument()
    expect(onExited).not.toHaveBeenCalled()
    await waitFor(() => expect(onExited).toHaveBeenCalledTimes(1))
  })

  it('fires onExited when the exit transition ends, with the page reachable and the panel still mounted', async () => {
    const onExited = vi.fn(() => {
      expect(container).not.toHaveAttribute('aria-hidden')
      expect(document.querySelector('[role="dialog"]')).not.toBeNull()
    })
    const { getByRole, container } = renderWithProviders(
      <ExitHarness onExited={onExited} />,
    )
    expect(container).toHaveAttribute('aria-hidden', 'true')

    fireEvent.click(getByRole('button', { name: 'Close the panel' }))
    await waitFor(() => expect(onExited).toHaveBeenCalledTimes(1))
    await waitFor(() =>
      expect(document.querySelector('[role="dialog"]')).toBeNull(),
    )
  })

  it('unmounts the panel content after the exit transition', async () => {
    const onExited = vi.fn()
    const { getByRole } = renderWithProviders(<ExitHarness onExited={onExited} />)
    fireEvent.click(getByRole('button', { name: 'Close the panel' }))
    await waitFor(() => expect(onExited).toHaveBeenCalledTimes(1))
    await waitFor(() =>
      expect(document.querySelector('[role="dialog"]')).toBeNull(),
    )
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

  it('marks the page behind the panel inert while open and lifts it on close', async () => {
    const user = userEvent.setup()
    const { container, getByRole } = renderWithProviders(<FocusHarness />)
    expect(container).not.toHaveAttribute('inert')

    await user.click(getByRole('button', { name: 'Open panel' }))
    await waitFor(() => expect(container).toHaveAttribute('inert'))
    expect(document.querySelector('.MuiModal-root')).not.toHaveAttribute('inert')

    await user.click(getByRole('button', { name: zhCN.sidePanel.closeLabel }))
    await waitFor(() => expect(container).not.toHaveAttribute('inert'))
  })

  it('passes axe over an open panel', async () => {
    setup({ description: DESCRIPTION })
    await expectNoAxeViolations()
  })

  it('forwards sx, className and ref to the drawer root', () => {
    const ref = createRef<HTMLDivElement>()
    renderWithProviders(
      <SidePanel
        open
        title={TITLE}
        ref={ref}
        className="host-panel"
        sx={{ mt: 4 }}
        onClose={vi.fn()}
        onExited={vi.fn()}
      >
        <p>{BODY}</p>
      </SidePanel>,
    )
    expect(ref.current).not.toBeNull()
    expect(ref.current).toHaveClass('host-panel')
    expect(ref.current).toHaveStyle({ marginTop: '32px' })
  })
})
