/**
 * RowActionMenu: a row's actions behind one trigger that stays focusable
 * while the menu is unavailable.
 *
 * Selecting an item starts the row's write and the host marks the menu
 * unavailable while it runs. The trigger held focus when the item was
 * selected, so it must keep focus -- not drop it to the document body --
 * and a second activation must not reopen the menu.
 */

import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../test-utils/render.js'
import { RowActionMenu } from './row-action-menu.js'
import type { RowAction } from './row-action-menu.js'

const TRIGGER = 'Actions for Ada'

/** The trigger's accessible name is the row it belongs to. */
function trigger(): HTMLElement {
  return screen.getByRole('button', { name: TRIGGER })
}

/**
 * A row whose first write marks the whole menu unavailable, as a host does
 * while the write it started is in flight.
 */
function UnavailableAfterSelect({ initiallyDisabled = false }) {
  const [disabled, setDisabled] = useState(initiallyDisabled)
  const actions: readonly RowAction[] = [
    {
      key: 'revoke',
      label: 'Revoke',
      onSelect: () => setDisabled(true),
    },
  ]
  return (
    <RowActionMenu label={TRIGGER} actions={actions} disabled={disabled} />
  )
}

describe('RowActionMenu', () => {
  it('keeps focus on the trigger and refuses reopening once its write starts', async () => {
    const user = userEvent.setup()
    renderWithProviders(<UnavailableAfterSelect />)

    await user.click(trigger())
    await user.click(await screen.findByRole('menuitem', { name: 'Revoke' }))

    expect(document.activeElement).toBe(trigger())
    expect(trigger()).toHaveAttribute('aria-disabled', 'true')

    await user.click(trigger())
    expect(screen.queryByRole('menuitem', { name: 'Revoke' })).toBeNull()
  })

  it('keeps the unavailable trigger focusable and refuses activation', async () => {
    const user = userEvent.setup()
    renderWithProviders(<UnavailableAfterSelect initiallyDisabled />)

    const unavailableTrigger = trigger()
    expect(unavailableTrigger).toHaveAttribute('aria-disabled', 'true')
    expect(unavailableTrigger).not.toBeDisabled()

    unavailableTrigger.focus()
    expect(document.activeElement).toBe(unavailableTrigger)

    await user.click(unavailableTrigger)
    expect(screen.queryByRole('menuitem', { name: 'Revoke' })).toBeNull()

    await user.keyboard('{Enter}')
    expect(screen.queryByRole('menuitem', { name: 'Revoke' })).toBeNull()

    await user.keyboard(' ')
    expect(screen.queryByRole('menuitem', { name: 'Revoke' })).toBeNull()
  })
})
