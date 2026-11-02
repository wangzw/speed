/**
 * The account entry placed in the header instead: the assembly's one-line
 * switch really moves it. With `ACCOUNT_ENTRY = 'header'` the header's
 * avatar menu carries the account settings and sign-out, and the
 * navigation column renders no account row.
 */

import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

vi.mock('./account-entry.js', () => ({ ACCOUNT_ENTRY: 'header' }))

import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

describe('the account entry in the header', () => {
  it('moves the entry to the header and leaves the column without a row', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const trigger = await screen.findByTestId('user-menu-trigger')
    expect(screen.queryByTestId('account-entry-trigger')).toBeNull()

    await user.click(trigger)
    expect(
      await screen.findByRole('menuitem', {
        name: demoEnUS.userMenu.accountSettings,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('menuitem', { name: demoEnUS.userMenu.signOut }),
    ).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await waitFor(() =>
      expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
    )
  })
})
