/**
 * The app's starting page (route '/'): the skeleton's front door.
 *
 * It shows the platform's assembled capabilities as a card grid rather than
 * developer copy. Each card is an anchor carrying its target path and, on a
 * plain click, routing there without a reload. The suite renders the app in
 * the authenticated frame at the root and drives the cards.
 */

import { beforeEach, describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from '../test-utils/render.js'
import { authenticatedSnapshot, createTestSession } from '../test-utils/session.js'
import {
  ACCOUNT_PATH,
  CONSOLE_PREFIX,
  NOTIFICATIONS_PATH,
  OBJECTS_PATH,
} from '../paths.js'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

/** The card link for a capability, located by the heading it carries (the
 * shell's navigation entries share the capability names, so the heading is
 * what tells a card apart from a nav entry). */
function cardFor(title: string): HTMLElement {
  const heading = screen.getByRole('heading', { level: 2, name: title })
  const anchor = heading.closest('a')
  if (anchor === null) {
    throw new Error('the card heading is not inside its link')
  }
  return anchor
}

describe('the start page', () => {
  it('names the page and points a card at each assembled capability', () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The page heading is the title and the intro sits under it.
    expect(
      screen.getByRole('heading', { level: 1, name: demoEnUS.home.heading }),
    ).toBeInTheDocument()
    expect(screen.getByText(demoEnUS.home.intro)).toBeInTheDocument()

    // One card per capability, each an anchor carrying the route it opens.
    expect(cardFor(demoEnUS.home.cards.account.title)).toHaveAttribute(
      'href',
      ACCOUNT_PATH,
    )
    expect(cardFor(demoEnUS.home.cards.console.title)).toHaveAttribute(
      'href',
      CONSOLE_PREFIX,
    )
    expect(cardFor(demoEnUS.home.cards.objects.title)).toHaveAttribute(
      'href',
      OBJECTS_PATH,
    )
    expect(cardFor(demoEnUS.home.cards.notifications.title)).toHaveAttribute(
      'href',
      NOTIFICATIONS_PATH,
    )
  })

  it('shows the signed-in viewer no source-tree copy', () => {
    const { container } = renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })

    // The landing faces a signed-in user, not the skeleton's author, so it
    // never points at the source tree. The note that once did is a developer
    // note, and it lives in README.md / AGENTS.md now.
    expect(container.textContent ?? '').not.toMatch(/src\/pages|src\/routes\.tsx/)
  })

  it('opens the capability a card names, without a reload', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await user.click(cardFor(demoEnUS.home.cards.account.title))

    // The account page renders in place: the card routed the viewer to it.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.title,
      }),
    ).toBeInTheDocument()
  })
})
