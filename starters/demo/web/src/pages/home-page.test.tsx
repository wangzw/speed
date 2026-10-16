/**
 * The app's starting page (route '/'): the skeleton's front door.
 *
 * It shows the platform's assembled capabilities as a card grid rather than
 * developer copy. Each card is an anchor carrying its target path and, on a
 * plain click, routing there without a reload. A card that declares a platform
 * permission -- the console card, held to the same code the header entry uses
 * -- is offered only to a principal that holds it and stays hidden while the
 * permission set is unknown. The member-facing cards (account, objects,
 * notifications) declare no permission and are offered to every signed-in
 * viewer.
 */

import { act, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from '../test-utils/session.js'
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

/** Whether the card for a capability is offered at all. */
function hasCard(title: string): boolean {
  return screen.queryByRole('heading', { level: 2, name: title }) !== null
}

describe('the start page', () => {
  it('offers every capability card to a platform administrator', () => {
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    // The page heading is the title and the intro sits under it.
    expect(
      screen.getByRole('heading', { level: 1, name: demoEnUS.home.heading }),
    ).toBeInTheDocument()
    expect(screen.getByText(demoEnUS.home.intro)).toBeInTheDocument()

    // One card per capability, each an anchor carrying the route it opens --
    // the console card included, since the administrator holds its code.
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

  it('hides the console card from a member, keeping the member-facing cards', () => {
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: [] })),
    })

    // The member's platform set is known and holds nothing, so the console
    // card -- and the information its title and description carry -- is not
    // rendered.
    expect(hasCard(demoEnUS.home.cards.console.title)).toBe(false)
    // The member-facing cards declare no permission and are unaffected: each
    // is still offered, with its target path.
    expect(cardFor(demoEnUS.home.cards.account.title)).toHaveAttribute(
      'href',
      ACCOUNT_PATH,
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

  it('keeps the console card hidden while the platform permission set is unknown', () => {
    // An authenticated session whose platform set is still unknown (null): the
    // state before the host has read the caller's platform permissions. The
    // gate fails closed, so the card is absent -- a member never sees it for
    // the moment before the set lands.
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })

    expect(hasCard(demoEnUS.home.cards.console.title)).toBe(false)
    expect(hasCard(demoEnUS.home.cards.account.title)).toBe(true)
  })

  it('reveals the console card once the platform permission set lands', async () => {
    // The attach-then-appear transition the host drives through
    // setPermissionSet: the set flips from unknown (null) to a known one
    // holding the card's code, and the card appears where it was hidden.
    const session = createTestSession(authenticatedSnapshot())
    renderDemoApp({ session })

    expect(hasCard(demoEnUS.home.cards.console.title)).toBe(false)

    act(() => {
      session.set(authenticatedSnapshot({ system: ['tenant.read'] }))
    })

    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: demoEnUS.home.cards.console.title,
      }),
    ).toBeInTheDocument()
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
