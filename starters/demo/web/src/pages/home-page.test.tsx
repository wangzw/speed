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

import { act, fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { cardClasses } from '@mui/material/Card'
import { emittedStyleText } from '../test-utils/emitted-css.js'
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

  it('hides the console card during an impersonation, keeping the member-facing cards', () => {
    // The impersonated snapshot still carries a platform set (the empty one
    // is attached later by the identity-keyed read). The card must follow
    // the identity, exactly as the header entry does, so the two never
    // disagree while that set is stale.
    renderDemoApp({
      session: createTestSession({
        ...authenticatedSnapshot({ system: ['tenant.read'] }),
        impersonating: true,
      }),
    })

    expect(hasCard(demoEnUS.home.cards.console.title)).toBe(false)
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

  it('reflows the card grid by its own container width, not the viewport', () => {
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    const card = cardFor(demoEnUS.home.cards.account.title)
    const grid = card.closest(`.${cardClasses.root}`)?.parentElement
    expect(grid).not.toBeNull()
    expect(grid).toHaveStyle({ display: 'grid' })

    // The grid's containment context is an ancestor of it -- the card
    // surface around the grid adds a content layer but declares no
    // container of its own, and the grid cannot own the rule (a container
    // query resolves against an ancestor, never the element that owns it).
    // Without the declaration the grid's `@container` rule matches nothing
    // and the grid never collapses in a narrow panel -- the failure the
    // layout design records. jsdom evaluates no container queries, so the
    // proof is the generated CSS text, the same assertion FormLayout's
    // suite makes.
    const css = emittedStyleText()
    const containerRule = (name: string): RegExp =>
      new RegExp(`\\.${name}\\s*\\{[^}]*container-type:\\s*inline-size`)
    let containerClass: string | undefined
    for (
      let element = grid?.parentElement ?? null;
      element !== null && containerClass === undefined;
      element = element.parentElement
    ) {
      containerClass = Array.from(element.classList).find(
        (name) => name.startsWith('css-') && containerRule(name).test(css),
      )
    }
    expect(containerClass).toBeDefined()

    expect(css).toMatch(
      new RegExp(
        `\\.${containerClass}\\s*\\{[^}]*container-type:\\s*inline-size`,
      ),
    )
    // Both track declarations were wired in: one column at the
    // container's narrow end, two equal tracks from its `sm` (600px) up.
    // The second sits inside an `@container` block, not an `@media` one
    // -- keyed on the viewport it would be blind to a narrow panel.
    expect(css).toMatch(/grid-template-columns:1fr/)
    expect(css).toMatch(
      /@container \(min-width:600px\)\{[^}]*grid-template-columns:repeat\(2,\s*1fr\)/,
    )
    expect(css).not.toMatch(
      /@media \(min-width:600px\)\{[^}]*grid-template-columns:repeat\(2,\s*1fr\)/,
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

  it('leaves a modified click on a card to the browser', () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // Ctrl/⌘-click means "open in a new tab": the anchor keeps the browser's
    // own action instead of being intercepted into an in-app navigation. The
    // document-level probe reads the event after the card's own handler has
    // run (React's root listener is closer to the target) and cancels the
    // default action so jsdom does not attempt a navigation it cannot make.
    const anchor = cardFor(demoEnUS.home.cards.account.title)
    let prevented: boolean | undefined
    const probe = (event: MouseEvent): void => {
      prevented = event.defaultPrevented
      event.preventDefault()
    }
    document.addEventListener('click', probe)
    try {
      fireEvent.click(anchor, { ctrlKey: true })
    } finally {
      document.removeEventListener('click', probe)
    }

    expect(prevented).toBe(false)
    expect(window.location.pathname).toBe('/')
  })
})
