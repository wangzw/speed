/**
 * The account menu's background, in Chromium.
 *
 * A MUI menu is a modal: MUI hides the page behind it from assistive
 * technology with `aria-hidden` alone, which leaves it in the tab order, so
 * the host marks that page inert while the menu is open and lifts it in the
 * layout cleanup of the closing commit. jsdom carries the same wiring; the
 * browser run tells the attribute apart from the markup MUI leaves behind
 * during the exit transition.
 *
 * Where focus lands when a menu entry changes the page is
 * navigation-focus.browser.test.tsx's subject, including the landing on the
 * new page's heading (REQ-presentation-042).
 */

import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

describe('the account menu background', () => {
  it('marks the page inert while open and lifts it on Escape', async () => {
    await page.viewport(1280, 900)
    const { container } = renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })
    const user = userEvent.setup()
    const trigger = await screen.findByTestId('account-entry-trigger')

    trigger.focus()
    await user.keyboard('{Enter}')
    const menu = await screen.findByRole('menu')

    await waitFor(() => expect(container).toHaveAttribute('inert'))
    const menuRoot = menu.closest('.MuiModal-root')
    expect(menuRoot).not.toBeNull()
    expect(menuRoot?.hasAttribute('inert')).toBe(false)

    await user.keyboard('{Escape}')

    // The attribute comes off in the closing commit, not when the exit
    // transition later takes the menu out of the document.
    await waitFor(() => expect(container).not.toHaveAttribute('inert'))
  })

  it('closes on Tab, returns focus to the trigger and lifts the background inert', async () => {
    await page.viewport(1280, 900)
    const { container } = renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })
    const user = userEvent.setup()
    const trigger = await screen.findByTestId('account-entry-trigger')

    trigger.focus()
    await user.keyboard('{Enter}')
    await screen.findByRole('menu')
    await waitFor(() => expect(container).toHaveAttribute('inert'))

    await user.keyboard('{Tab}')

    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    await waitFor(() => expect(trigger).toHaveFocus())
    await waitFor(() => expect(container).not.toHaveAttribute('inert'))
  })
})

describe('the header user menu background', () => {
  it('marks the page inert while open, closes on Tab and lifts the inert', async () => {
    await page.viewport(1280, 900)
    const { container } = renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })
    const user = userEvent.setup()
    const trigger = await screen.findByTestId('user-menu-trigger')

    trigger.focus()
    await user.keyboard('{Enter}')
    const menu = await screen.findByRole('menu')
    await waitFor(() => expect(container).toHaveAttribute('inert'))
    const menuRoot = menu.closest('.MuiModal-root')
    expect(menuRoot).not.toBeNull()
    expect(menuRoot?.hasAttribute('inert')).toBe(false)

    await user.keyboard('{Tab}')

    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    await waitFor(() => expect(trigger).toHaveFocus())
    await waitFor(() => expect(container).not.toHaveAttribute('inert'))
  })
})
