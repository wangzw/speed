
import { describe, expect, it } from 'vitest'
import { StrictMode, useState } from 'react'
import { act, screen, waitFor } from '@testing-library/react'
import { userEvent } from 'vitest/browser'
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { expectFocusTrapWalk } from '../../test-utils/focus-walk.js'
import { SidePanel } from './SidePanel.js'

const controls: { setBusy: (busy: boolean) => void } = {
  setBusy: () => undefined,
}

function BusyHarness() {
  const [busy, setBusy] = useState(false)
  controls.setBusy = setBusy
  return (
    <SidePanel
      open
      title="Account details"
      busy={busy}
      onClose={() => undefined}
      onExited={() => undefined}
    >
      <p>Panel body</p>
    </SidePanel>
  )
}

function PanelFromPage() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open panel
      </button>
      <SidePanel
        open={open}
        title="Account details"
        onClose={() => setOpen(false)}
        onExited={() => undefined}
      >
        <button type="button">Inside the panel</button>
      </SidePanel>
    </>
  )
}

async function openPanel(): Promise<HTMLElement> {
  const opener = await screen.findByRole('button', { name: 'Open panel' })
  opener.focus()
  await userEvent.keyboard('{Enter}')
  const dialog = await screen.findByRole('dialog', { name: 'Account details' })
  await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true))
  return opener
}

describe('SidePanel in Chromium', () => {
  it('returns focus to the opener after the close button closes the panel', async () => {
    renderWithProviders(<PanelFromPage />, { language: 'en-US' })
    const opener = await openPanel()

    await userEvent.click(
      screen.getByRole('button', { name: enUS.sidePanel.closeLabel }),
    )

    await waitFor(() => expect(document.activeElement).toBe(opener))
  })

  it('returns focus to the opener after Escape closes the panel', async () => {
    renderWithProviders(<PanelFromPage />, { language: 'en-US' })
    const opener = await openPanel()

    await userEvent.keyboard('{Escape}')

    await waitFor(() => expect(document.activeElement).toBe(opener))
  })

  it('returns focus to the opener after Escape under StrictMode', async () => {
    renderWithProviders(
      <StrictMode>
        <PanelFromPage />
      </StrictMode>,
      { language: 'en-US' },
    )
    const opener = await screen.findByRole('button', { name: 'Open panel' })
    opener.focus()
    await userEvent.keyboard('{Enter}')
    const dialog = await screen.findByRole('dialog', { name: 'Account details' })
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true))

    await userEvent.keyboard('{Escape}')

    await waitFor(() => expect(document.activeElement).toBe(opener))
  })

  it('keeps Tab and Shift+Tab inside the panel', async () => {
    renderWithProviders(<PanelFromPage />, { language: 'en-US' })
    await openPanel()

    const panel = screen.getByRole('dialog', { name: 'Account details' })

    const press = (keys: string) => userEvent.keyboard(keys)
    await expectFocusTrapWalk(panel, 'forward', { press })
    await expectFocusTrapWalk(panel, 'backward', { press })
  })

  it('keeps focus on the close button when the panel becomes busy', async () => {
    renderWithProviders(<BusyHarness />, { language: 'en-US' })

    const close = await screen.findByRole('button', {
      name: enUS.sidePanel.closeLabel,
    })
    close.focus()
    expect(close).toHaveFocus()

    act(() => {
      controls.setBusy(true)
    })
    expect(close).toHaveFocus()
  })
})
