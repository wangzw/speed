
import { describe, expect, it } from 'vitest'
import { StrictMode, useState } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { userEvent } from 'vitest/browser'
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { expectFocusTrapWalk } from '../../test-utils/focus-walk.js'
import { ConfirmDialog } from './ConfirmDialog.js'

function LoadingOnConfirm() {
  const [loading, setLoading] = useState(false)
  return (
    <ConfirmDialog
      open
      confirmLoading={loading}
      onConfirm={() => setLoading(true)}
      onCancel={() => undefined}
    />
  )
}

function DialogFromPage() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open dialog
      </button>
      <ConfirmDialog
        open={open}
        onConfirm={() => undefined}
        onCancel={() => setOpen(false)}
      />
    </>
  )
}

describe('ConfirmDialog in Chromium', () => {
  it('returns focus to the opener after Escape closes the dialog', async () => {
    renderWithProviders(<DialogFromPage />, { language: 'en-US' })
    const opener = await screen.findByRole('button', { name: 'Open dialog' })
    opener.focus()
    await userEvent.keyboard('{Enter}')

    const dialog = await screen.findByRole('dialog')
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true))

    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(document.activeElement).toBe(opener))
  })

  it('returns focus to the opener after Escape under StrictMode', async () => {
    renderWithProviders(
      <StrictMode>
        <DialogFromPage />
      </StrictMode>,
      { language: 'en-US' },
    )
    const opener = await screen.findByRole('button', { name: 'Open dialog' })
    opener.focus()
    await userEvent.keyboard('{Enter}')

    const dialog = await screen.findByRole('dialog')
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true))

    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(document.activeElement).toBe(opener))
  })

  it('keeps Tab and Shift+Tab inside the dialog', async () => {
    renderWithProviders(<DialogFromPage />, { language: 'en-US' })
    const opener = await screen.findByRole('button', { name: 'Open dialog' })
    opener.focus()
    await userEvent.keyboard('{Enter}')

    const dialog = await screen.findByRole('dialog')
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true))

    const press = (keys: string) => userEvent.keyboard(keys)
    await expectFocusTrapWalk(dialog, 'forward', { press })
    await expectFocusTrapWalk(dialog, 'backward', { press })
  })

  it('returns focus to the opener after the cancel button closes the dialog', async () => {
    renderWithProviders(<DialogFromPage />, { language: 'en-US' })
    const opener = await screen.findByRole('button', { name: 'Open dialog' })
    opener.focus()
    await userEvent.keyboard('{Enter}')

    const cancel = await screen.findByRole('button', {
      name: enUS.confirmDialog.cancelLabel,
    })
    await waitFor(() => expect(screen.getByRole('dialog').contains(document.activeElement)).toBe(true))
    await userEvent.click(cancel)

    await waitFor(() => expect(document.activeElement).toBe(opener))
  })

  it('keeps focus on the confirm button when the write starts', async () => {
    renderWithProviders(<LoadingOnConfirm />, { language: 'en-US' })

    const confirm = await screen.findByRole('button', {
      name: enUS.confirmDialog.confirmLabel,
    })
    await userEvent.click(confirm)
    await waitFor(() => {
      expect(confirm).toHaveAttribute('aria-busy', 'true')
    })
    expect(confirm).toHaveFocus()
  })

  it('keeps focus on the armed confirm button through the lockout', async () => {
    renderWithProviders(
      <ConfirmDialog
        open
        variant="danger"
        doubleConfirm
        onConfirm={() => undefined}
        onCancel={() => undefined}
      />,
      { language: 'en-US' },
    )

    const confirm = await screen.findByRole('button', {
      name: enUS.confirmDialog.confirmLabel,
    })
    confirm.focus()
    expect(confirm).toHaveFocus()

    await userEvent.keyboard('{Enter}')
    const again = screen.getByRole('button', {
      name: enUS.confirmDialog.confirmAgainLabel,
    })
    expect(again).toBe(confirm)
    await waitFor(() => {
      expect(document.activeElement).toBe(confirm)
    })
  })
})
