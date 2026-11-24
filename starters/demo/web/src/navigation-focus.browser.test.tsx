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

const layoutEnUS = layoutKitResources['en-US'] as {
  readonly appShell: { readonly openNav: string }
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

describe('focus after a page change in the demo host', () => {
  it('lands on the account page heading when an account-menu entry navigates', async () => {
    await page.viewport(1280, 900)
    const { container } = renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })
    const user = userEvent.setup()

    const trigger = await screen.findByTestId('account-entry-trigger')
    trigger.focus()
    await user.keyboard('{Enter}')

    await waitFor(() => expect(container).toHaveAttribute('inert'))

    const entry = await screen.findByRole('menuitem', {
      name: demoEnUS.account.sections.profile,
    })
    entry.focus()
    await user.keyboard('{Enter}')

    const heading = await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.account.sections.profile,
    })
    await waitFor(() => {
      expect(document.activeElement).toBe(heading)
    })
    await waitFor(() => expect(container).not.toHaveAttribute('inert'))
  })

  it('lands on the home heading when the session flips to authenticated', async () => {
    await page.viewport(1280, 900)
    const session = createTestSession()
    renderDemoApp({ session })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).toBeInTheDocument()

    act(() => {
      session.set(authenticatedSnapshot())
    })

    const heading = await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.home.heading,
    })
    await waitFor(() => {
      expect(document.activeElement).toBe(heading)
    })
  })

  it('lands on the home heading after navigating from the narrow-screen drawer', async () => {
    window.history.replaceState({}, '', ACCOUNT_SECTION_PATHS.profile)
    await page.viewport(375, 800)
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    const user = userEvent.setup()

    const toggle = await screen.findByRole('button', {
      name: layoutEnUS.appShell.openNav,
    })
    await user.click(toggle)
    const homeEntry = await screen.findByRole('link', {
      name: demoEnUS.nav.home,
    })
    await user.click(homeEntry)

    const heading = await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.home.heading,
    })
    await waitFor(() => {
      expect(document.activeElement).toBe(heading)
    })
  })
})
