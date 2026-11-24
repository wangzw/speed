/**
 * RowActionMenu in a real browser: the page behind the menu is inert while it
 * is open, and closing the menu hands focus back to the row's trigger -- with
 * Escape and after an item is chosen. The trigger is what held focus before
 * the menu opened, so it is the element MUI's focus trap restores to.
 */

import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConfirmDialog } from '@speed/ui-kit'
import { RowActionMenu } from './row-action-menu.js'
import { renderWithProviders } from '../../test-utils/render.js'

const TRIGGER = 'Actions for Ada'

function MenuThenDialog() {
  const [confirming, setConfirming] = useState(false)
  return (
    <>
      <RowActionMenu
        label={TRIGGER}
        actions={[
          {
            key: 'revoke',
            label: 'Revoke',
            onSelect: () => setConfirming(true),
          },
        ]}
      />
      <ConfirmDialog
        open={confirming}
        onConfirm={() => setConfirming(false)}
        onCancel={() => setConfirming(false)}
      />
    </>
  )
}

describe('RowActionMenu focus in Chromium', () => {
  it('makes the page inert while open and returns focus on Escape', async () => {
    const user = userEvent.setup()
    const { container } = renderWithProviders(
      <RowActionMenu
        label={TRIGGER}
        actions={[
          { key: 'revoke', label: 'Revoke', onSelect: () => undefined },
        ]}
      />,
    )
    const trigger = screen.getByRole('button', { name: TRIGGER })
    trigger.focus()

    await user.keyboard('{Enter}')
    await screen.findByRole('menuitem', { name: 'Revoke' })
    expect(container).toHaveAttribute('inert')

    await user.keyboard('{Escape}')
    await waitFor(() => expect(trigger).toHaveFocus())
    expect(container).not.toHaveAttribute('inert')
  })

  it('returns focus to the trigger after an item is chosen', async () => {
    const user = userEvent.setup()
    const chosen: string[] = []
    renderWithProviders(
      <RowActionMenu
        label={TRIGGER}
        actions={[
          {
            key: 'revoke',
            label: 'Revoke',
            onSelect: () => chosen.push('revoke'),
          },
        ]}
      />,
    )
    const trigger = screen.getByRole('button', { name: TRIGGER })
    trigger.focus()

    await user.keyboard('{Enter}')
    await user.keyboard('{Enter}')

    await waitFor(() => expect(chosen).toEqual(['revoke']))
    await waitFor(() => expect(trigger).toHaveFocus())
  })

  it('closes on Tab, returns focus to the trigger and lifts the background inert', async () => {
    const user = userEvent.setup()
    const { container } = renderWithProviders(
      <RowActionMenu
        label={TRIGGER}
        actions={[
          { key: 'revoke', label: 'Revoke', onSelect: () => undefined },
        ]}
      />,
    )
    const trigger = screen.getByRole('button', { name: TRIGGER })
    trigger.focus()

    await user.keyboard('{Enter}')
    await screen.findByRole('menuitem', { name: 'Revoke' })
    expect(container).toHaveAttribute('inert')

    await user.keyboard('{Tab}')

    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    await waitFor(() => expect(trigger).toHaveFocus())
    await waitFor(() => expect(container).not.toHaveAttribute('inert'))
  })

  it('returns focus to the trigger when an action opens a dialog and the dialog is cancelled', async () => {
    const user = userEvent.setup()
    renderWithProviders(<MenuThenDialog />)
    const trigger = screen.getByRole('button', { name: TRIGGER })
    trigger.focus()

    await user.keyboard('{Enter}')
    await user.keyboard('{Enter}')
    await screen.findByRole('dialog')

    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await waitFor(() => expect(trigger).toHaveFocus())
  })
})
