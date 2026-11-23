/**
 * Where focus lands after the demo host changes the page, in Chromium.
 *
 * Two changes swap the page out from under the viewer: a navigation from the
 * account menu, and the tenant shell's flip from its anonymous branch to the
 * authenticated frame. Both leave the initiating control unmounted, and
 * whether focus reaches the new page's heading instead of the document body
 * (or the branch's anonymous container) is a question only a real browser
 * answers -- jsdom has no focus traversal, and the account menu's focus
 * return happens through a real focus trap.
 *
 * The account-menu case also guards the ordering claim the landing rests on:
 * MUI's menu returns focus to its trigger in an effect cleanup, and the
 * navigation's landing runs in a later effect create. A browser run tells the
 * two apart; if the return ever moved after the landing, focus would sit on
 * the trigger again and this case would fail.
 *
 * The same ordering question has a second shape at a narrow width, where the
 * navigation lives in AppShell's temporary drawer: activating an entry closes
 * the drawer and navigates in one batch. The drawer is a MUI modal, so the
 * app root stays aria-hidden until its exit transition ends, and the landing
 * runs with that attribute still on an ancestor above the new page's `main`.
 * The case below guards that this ancestor does not hide the new heading from
 * the search: the rule's suppressed-subtree check stops at the landmark, so
 * an `aria-hidden` or `inert` ancestor above `main` is outside it. A check
 * that walked past the landmark would skip the heading and land on the
 * underlying `main` instead.
 */

import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { layoutKitResources } from '@speed/layout-kit'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { ACCOUNT_SECTION_PATHS } from './paths.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

/** The shipped en-US app-shell strings the cases assert on. */
const layoutEnUS = layoutKitResources['en-US'] as {
  readonly appShell: { readonly openNav: string }
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

describe('focus after a page change in the demo host', () => {
  it('lands on the account page heading when an account-menu entry navigates', async () => {
    await page.viewport(1280, 900)
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    const user = userEvent.setup()

    // Open the account menu from the keyboard: the entry that follows is a
    // menu item, not a link in the page, so only the menu can hand focus on.
    const trigger = await screen.findByTestId('account-entry-trigger')
    trigger.focus()
    await user.keyboard('{Enter}')

    // The profile pane is the account settings' first page, the one the
    // menu's account-settings entry opens.
    const entry = await screen.findByRole('menuitem', {
      name: demoEnUS.account.sections.profile,
    })
    entry.focus()
    await user.keyboard('{Enter}')

    // The new page's heading takes focus, not the menu's trigger button the
    // closing menu restores focus to.
    const heading = await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.account.sections.profile,
    })
    await waitFor(() => {
      expect(document.activeElement).toBe(heading)
    })
  })

  it('lands on the home heading when the session flips to authenticated', async () => {
    await page.viewport(1280, 900)
    const session = createTestSession()
    renderDemoApp({ session })

    // The visitor starts on the tenant area's sign-in branch.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).toBeInTheDocument()

    // The same URL, a new branch: the shell's flip replaces the sign-in
    // surface with the authenticated frame.
    act(() => {
      session.set(authenticatedSnapshot())
    })

    // Focus lands on the home page's heading, not on the anonymous container
    // the previous branch rendered.
    const heading = await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.home.heading,
    })
    await waitFor(() => {
      expect(document.activeElement).toBe(heading)
    })
  })

  it('lands on the home heading after navigating from the narrow-screen drawer', async () => {
    // A phone-sized viewport puts the navigation in AppShell's temporary
    // drawer rather than the permanent column. Start on an account pane so
    // the drawer's Home entry is a real page change.
    window.history.replaceState({}, '', ACCOUNT_SECTION_PATHS.profile)
    await page.viewport(375, 800)
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    const user = userEvent.setup()

    // Open the drawer from its toggle, then navigate from the entry inside
    // it: activating the entry closes the drawer and navigates in the same
    // batch, exactly as the narrow-screen shell does.
    const toggle = await screen.findByRole('button', {
      name: layoutEnUS.appShell.openNav,
    })
    await user.click(toggle)
    const homeEntry = await screen.findByRole('link', {
      name: demoEnUS.nav.home,
    })
    await user.click(homeEntry)

    // The new page's heading takes focus, not the drawer's toggle (which the
    // closing drawer restores focus to) and not the page's main landmark,
    // which is all the landing can find while the drawer's exit transition
    // keeps the app root aria-hidden.
    const heading = await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.home.heading,
    })
    await waitFor(() => {
      expect(document.activeElement).toBe(heading)
    })
  })
})
