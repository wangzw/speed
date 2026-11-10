/**
 * The account entry placed in the header instead: the assembly's one-line
 * switch really moves it, and the header's avatar menu carries the same
 * unconditional entries the sidebar account entry offers.
 */

import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

vi.mock('./account-entry.js', () => ({
  ACCOUNT_ENTRY: 'header',
  // The predicate the assembly asks instead of comparing the constant at each
  // call site: this stub stands for the real module's answer for 'header'.
  accountEntryInUserSlot: (shellLayout?: string) => shellLayout !== 'sidebar',
  accountEntryInNavFooter: (shellLayout?: string) => shellLayout === 'sidebar',
}))

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
      demoEnUS.userMenu.transferRequests,
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

describe('the account entry when the shell shape and the constant disagree', () => {
  it('keeps the row as the only entry in the sidebar shape', async () => {
    // The sidebar shape's bottom row sits under the account row, so that
    // shape never renders the header menu; the row is then the only entry,
    // whatever this constant says.
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
      shellLayout: 'sidebar',
    })

    expect(await screen.findByTestId('account-entry-trigger')).toBeInTheDocument()
    expect(screen.queryByTestId('user-menu-trigger')).toBeNull()
  })
})
