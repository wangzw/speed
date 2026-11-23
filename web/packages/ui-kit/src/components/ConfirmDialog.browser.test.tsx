/**
 * ConfirmDialog in a real browser: both states that make the confirm
 * button unavailable keep the focus a keyboard user brought with them.
 *
 * jsdom implements neither the focus correction a browser runs when a
 * focused element becomes natively disabled nor the dialog focus trap's
 * response to it, so these cases live in the browser tier. Under the old
 * native `disabled` the button stops holding focus; browsers differ in
 * when and how they move it, and inside the modal's focus trap it lands
 * on the dialog's own container.
 */

import { describe, expect, it } from 'vitest'
import { useState } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { userEvent } from 'vitest/browser'
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { ConfirmDialog } from './ConfirmDialog.js'

/** A host that starts the write the moment the confirm is activated. */
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

describe('ConfirmDialog in Chromium', () => {
  it('keeps focus on the confirm button when the write starts', async () => {
    renderWithProviders(<LoadingOnConfirm />, { language: 'en-US' })

    const confirm = await screen.findByRole('button', {
      name: enUS.confirmDialog.confirmLabel,
    })
    await userEvent.click(confirm)
    await waitFor(() => {
      expect(confirm).toHaveAttribute('aria-busy', 'true')
    })
    // The keyboard user who just activated the button must not lose their
    // place while the write is in flight.
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

    // A keyboard user presses Enter to arm the guard; the button re-labels
    // and enters the double-click lockout. The user waiting to press Enter
    // again must keep their focus.
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
