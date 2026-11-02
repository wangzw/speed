/**
 * The account entry's placement: by default the skeleton puts it at the
 * bottom of the navigation column and the header carries no account menu.
 * The four panes and the sign-out action are reachable from there, the
 * collapsed rail keeps the avatar alone with the account still named, and
 * the menu is fully keyboard-operated.
 */

import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { layoutKitResources } from '@speed/layout-kit'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

const layoutEn = layoutKitResources['en-US'] as {
  readonly appShell: { readonly collapseNav: string }
}

const sectionLabels = [
  demoEnUS.account.sections.profile,
  demoEnUS.account.sections.security,
  demoEnUS.account.sections.sessions,
  demoEnUS.account.sections.preferences,
]

describe('the account entry in the navigation column', () => {
  it('is the sidebar row, with the header carrying no account menu', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const trigger = await screen.findByTestId('account-entry-trigger')
    const displayName = (
      authenticatedSnapshot().principal?.display_name ?? ''
    ).trim()
    expect(trigger).toHaveAccessibleName(new RegExp(`${displayName}$`))
    // The header keeps its tenant switcher and notices; no avatar menu.
    expect(screen.queryByTestId('user-menu-trigger')).toBeNull()
  })

  it('opens the four panes and the sign-out action, upward, from the keyboard', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    const trigger = await screen.findByTestId('account-entry-trigger')

    trigger.focus()
    await user.keyboard('{Enter}')
    for (const label of sectionLabels) {
      expect(
        await screen.findByRole('menuitem', { name: label }),
      ).toBeInTheDocument()
    }
    expect(
      screen.getByRole('menuitem', { name: demoEnUS.userMenu.signOut }),
    ).toBeInTheDocument()

    // Escape closes the menu and returns focus to the trigger.
    await user.keyboard('{Escape}')
    await waitFor(() =>
      expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
    )
    expect(trigger).toHaveFocus()
  })

  it('keeps only the avatar in the collapsed rail, still named by the account', async () => {
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
      shellLayout: 'sidebar',
    })
    const displayName = (
      authenticatedSnapshot().principal?.display_name ?? ''
    ).trim()
    const wide = await screen.findByTestId('account-entry-trigger')
    expect(within(wide).getByText(displayName)).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: layoutEn.appShell.collapseNav }),
    )
    await waitFor(() =>
      expect(screen.queryByText(displayName)).not.toBeInTheDocument(),
    )
    const narrow = screen.getByTestId('account-entry-trigger')
    expect(narrow).toHaveAccessibleName(new RegExp(displayName))
    // The glyph is still visible text inside the control.
    expect((narrow.textContent ?? '').trim()).not.toBe('')
  })
})
