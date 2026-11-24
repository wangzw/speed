
import { StrictMode, useRef, useState } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { userEvent } from 'vitest/browser'
import Dialog from '@mui/material/Dialog'
import Drawer from '@mui/material/Drawer'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import TextField from '@mui/material/TextField'
import enUS from './locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { useInertBackground, useInertSelectMenu } from './inert-background.js'
import { ConfirmDialog } from './components/ConfirmDialog.js'
import { SidePanel } from './components/SidePanel.js'

function SelectHarness() {
  const menu = useInertSelectMenu()
  return (
    <>
      <button type="button">Background trigger</button>
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
    </>
  )
}

function StackedHarness() {
  const [panelOpen, setPanelOpen] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setPanelOpen(true)}>
        Open panel
      </button>
      <SidePanel
        open={panelOpen}
        title="Account details"
        onClose={() => setPanelOpen(false)}
        onExited={() => undefined}
      >
        <button type="button" onClick={() => setDialogOpen(true)}>
          Remove account
        </button>
        <ConfirmDialog
          open={dialogOpen}
          onConfirm={() => undefined}
          onCancel={() => setDialogOpen(false)}
        />
      </SidePanel>
    </>
  )
}

async function openSelect(): Promise<HTMLElement> {
  const combobox = await screen.findByRole('combobox')
  combobox.focus()
  await userEvent.keyboard('{ArrowDown}')
  await screen.findByRole('listbox')
  return combobox
}

describe('inert background in Chromium', () => {
  it('marks the page inert while a select menu is open and returns focus on Tab', async () => {
    renderWithProviders(<SelectHarness />, { language: 'en-US' })
    // Query before opening: the inert background leaves the accessibility tree.
    const backgroundTrigger = await screen.findByRole('button', {
      name: 'Background trigger',
    })
    const combobox = await openSelect()
    const menuRoot = screen.getByRole('listbox').closest('.MuiModal-root')
    expect(menuRoot).not.toBeNull()
    expect(menuRoot!.contains(document.activeElement)).toBe(true)
    expect(backgroundTrigger.closest('[inert]')).not.toBeNull()
    expect(combobox.closest('[inert]')).not.toBeNull()

    // Menu closes a Select on Tab (its own `tabKeyDown` behaviour) and hands
    // focus back to the control. The focus call only lands because the inert
    // flag was lifted in the app's layout phase.
    await userEvent.keyboard('{Tab}')

    expect(document.activeElement).not.toBe(backgroundTrigger)
    await waitFor(() => expect(document.activeElement).toBe(combobox))
    expect(combobox.closest('[inert]')).toBeNull()
  })

  it('returns focus to the select control when an item is chosen', async () => {
    renderWithProviders(<SelectHarness />, { language: 'en-US' })
    const combobox = await openSelect()

    await userEvent.click(screen.getByRole('option', { name: 'Beta' }))

    await waitFor(() => expect(document.activeElement).toBe(combobox))
  })

  it('returns focus to the select control when Escape closes the menu', async () => {
    renderWithProviders(<SelectHarness />, { language: 'en-US' })
    const combobox = await openSelect()

    await userEvent.keyboard('{Escape}')

    await waitFor(() => expect(document.activeElement).toBe(combobox))
  })

  it('returns focus to the panel trigger after the stacked dialog closes', async () => {
    renderWithProviders(<StackedHarness />, { language: 'en-US' })
    const pageOpener = await screen.findByRole('button', { name: 'Open panel' })
    await userEvent.click(pageOpener)

    const panelTrigger = await screen.findByRole('button', {
      name: 'Remove account',
    })
    await userEvent.click(panelTrigger)
    // The dialog hides the panel from the accessibility tree, so wait for the
    // dialog's own control rather than re-querying the panel by role.
    const confirm = await screen.findByRole('button', {
      name: enUS.confirmDialog.confirmLabel,
    })

    await userEvent.keyboard('{Escape}')

    await waitFor(() => expect(document.activeElement).toBe(panelTrigger))
    // Until the exit transition ends the dialog is still the modal manager's
    // top modal, so the next Escape would be swallowed by the wrong layer.
    await waitFor(() => expect(confirm.isConnected).toBe(false))

    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(document.activeElement).toBe(pageOpener))
  })

  it('leaves no aria-hidden-focus report on a scanned open menu', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    try {
      renderWithProviders(<SelectHarness />, { language: 'en-US' })
      await openSelect()
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

function BareMenuHarness() {
  const [open, setOpen] = useState(false)
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  useInertBackground(open, menuRef)
  return (
    <>
      <button
        type="button"
        onClick={(event) => {
          setAnchorEl(event.currentTarget)
          setOpen(true)
        }}
      >
        Open menu
      </button>
      <Menu
        ref={menuRef}
        anchorEl={anchorEl}
        open={open}
        onClose={() => setOpen(false)}
      >
        <MenuItem onClick={() => setOpen(false)}>First item</MenuItem>
      </Menu>
    </>
  )
}

function BareDialogHarness() {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useInertBackground(open, ref)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open dialog
      </button>
      <Dialog ref={ref} open={open} onClose={() => setOpen(false)}>
        <button type="button">Inside dialog</button>
      </Dialog>
    </>
  )
}

function BareDrawerHarness() {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useInertBackground(open, ref)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open drawer
      </button>
      <Drawer
        ref={ref}
        open={open}
        onClose={() => setOpen(false)}
        anchor="right"
        variant="temporary"
      >
        <button type="button">Inside drawer</button>
      </Drawer>
    </>
  )
}

function MountOpenDialog() {
  const [open, setOpen] = useState(true)
  const ref = useRef<HTMLDivElement>(null)
  useInertBackground(open, ref)
  return (
    <Dialog ref={ref} open={open} onClose={() => setOpen(false)}>
      <button type="button">Inside mounted dialog</button>
    </Dialog>
  )
}

function MountOpenHarness() {
  const [mounted, setMounted] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setMounted(true)}>
        Mount an open dialog
      </button>
      {mounted ? <MountOpenDialog /> : null}
    </>
  )
}

async function expectFocusInsideAnOpenModal(): Promise<void> {
  await waitFor(() => {
    expect(document.activeElement?.closest('.MuiModal-root')).not.toBeNull()
  })
}

describe('inert background under StrictMode', () => {
  it('returns focus to a bare menu trigger after Escape', async () => {
    renderWithProviders(
      <StrictMode>
        <BareMenuHarness />
      </StrictMode>,
      { language: 'en-US' },
    )
    const trigger = await screen.findByRole('button', { name: 'Open menu' })
    trigger.focus()
    await userEvent.keyboard('{Enter}')
    await screen.findByRole('menu')
    await expectFocusInsideAnOpenModal()

    await userEvent.keyboard('{Escape}')

    await waitFor(() => expect(document.activeElement).toBe(trigger))
    await waitFor(() => expect(trigger.closest('[inert]')).toBeNull())
  })

  it('returns focus to a dialog opener after Escape', async () => {
    renderWithProviders(
      <StrictMode>
        <BareDialogHarness />
      </StrictMode>,
      { language: 'en-US' },
    )
    const opener = await screen.findByRole('button', { name: 'Open dialog' })
    opener.focus()
    await userEvent.keyboard('{Enter}')
    await screen.findByRole('dialog')
    await expectFocusInsideAnOpenModal()

    await userEvent.keyboard('{Escape}')

    await waitFor(() => expect(document.activeElement).toBe(opener))
    await waitFor(() => expect(opener.closest('[inert]')).toBeNull())
  })

  it('returns focus to a drawer opener after Escape', async () => {
    renderWithProviders(
      <StrictMode>
        <BareDrawerHarness />
      </StrictMode>,
      { language: 'en-US' },
    )
    const opener = await screen.findByRole('button', { name: 'Open drawer' })
    opener.focus()
    await userEvent.keyboard('{Enter}')
    await expectFocusInsideAnOpenModal()

    await userEvent.keyboard('{Escape}')

    await waitFor(() => expect(document.activeElement).toBe(opener))
    await waitFor(() => expect(opener.closest('[inert]')).toBeNull())
  })

  it('returns focus to the select control after Escape closes its menu', async () => {
    renderWithProviders(
      <StrictMode>
        <SelectHarness />
      </StrictMode>,
      { language: 'en-US' },
    )
    const combobox = await openSelect()
    await expectFocusInsideAnOpenModal()

    await userEvent.keyboard('{Escape}')

    await waitFor(() => expect(document.activeElement).toBe(combobox))
  })

  it('returns focus to the opener when a dialog mounts already open', async () => {
    renderWithProviders(
      <StrictMode>
        <MountOpenHarness />
      </StrictMode>,
      { language: 'en-US' },
    )
    const opener = await screen.findByRole('button', {
      name: 'Mount an open dialog',
    })
    await userEvent.click(opener)
    await screen.findByRole('dialog')
    await expectFocusInsideAnOpenModal()

    await userEvent.keyboard('{Escape}')

    await waitFor(() => expect(document.activeElement).toBe(opener))
    await waitFor(() => expect(opener.closest('[inert]')).toBeNull())
  })

  it('returns focus across a stacked dialog and panel', async () => {
    renderWithProviders(
      <StrictMode>
        <StackedHarness />
      </StrictMode>,
      { language: 'en-US' },
    )
    const pageOpener = await screen.findByRole('button', { name: 'Open panel' })
    pageOpener.focus()
    await userEvent.keyboard('{Enter}')

    const panelTrigger = await screen.findByRole('button', {
      name: 'Remove account',
    })
    await userEvent.click(panelTrigger)
    const confirm = await screen.findByRole('button', {
      name: enUS.confirmDialog.confirmLabel,
    })

    await userEvent.keyboard('{Escape}')

    await waitFor(() => expect(document.activeElement).toBe(panelTrigger))
    await waitFor(() => expect(confirm.isConnected).toBe(false))

    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(document.activeElement).toBe(pageOpener))
  })
})
