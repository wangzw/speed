
import { describe, expect, it } from 'vitest'
import { useState } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { userEvent } from 'vitest/browser'
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
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
