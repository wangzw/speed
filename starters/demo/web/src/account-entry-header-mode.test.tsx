/**
 * The account entry placed in the header instead: the assembly's one-line
 * switch really moves it, and the header's avatar menu carries the same six
 * entries the sidebar account entry offers.
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
      screen.getAllByRole('menuitem').map((item) => item.textContent),
    ).toEqual([
      demoEnUS.userMenu.messages,
      demoEnUS.account.sections.profile,
      demoEnUS.account.sections.security,
      demoEnUS.account.sections.sessions,
      demoEnUS.userMenu.preferences,
      demoEnUS.userMenu.signOut,
    ])
    await user.keyboard('{Escape}')
    await waitFor(() =>
      expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
    )
  })
})
