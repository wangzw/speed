import { act, fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { emittedStyleText } from '../test-utils/emitted-css.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { setCurrentTenant, setTenantNames } from '../chrome.js'
import { clearSelfTenants } from '../tenant-self.js'
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from '../test-utils/session.js'
import {
  ACCOUNT_PATH,
  ADMIN_PREFIX,
  NOTIFICATIONS_PATH,
} from '../paths.js'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenant('system')
  setTenantNames(new Map([['system', 'system']]))
  clearSelfTenants()
})

function cardFor(title: string): HTMLElement {
  const heading = screen.getByRole('heading', { level: 2, name: title })
  const anchor = heading.closest('a')
  if (anchor === null) {
    throw new Error('the card heading is not inside its link')
  }
  return anchor
}

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

    expect(
      screen.getByRole('heading', { level: 1, name: demoEnUS.home.heading }),
    ).toBeInTheDocument()
    expect(screen.getByText(demoEnUS.home.intro)).toBeInTheDocument()

    expect(cardFor(demoEnUS.home.cards.account.title)).toHaveAttribute(
      'href',
      ACCOUNT_PATH,
    )
    expect(cardFor(demoEnUS.home.cards.admin.title)).toHaveAttribute(
      'href',
      ADMIN_PREFIX,
    )
    expect(cardFor(demoEnUS.home.cards.notifications.title)).toHaveAttribute(
      'href',
      NOTIFICATIONS_PATH,
    )
  })

  it('hides the admin card from a member, keeping the member-facing cards', () => {
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: [] })),
    })

    expect(hasCard(demoEnUS.home.cards.admin.title)).toBe(false)
    expect(cardFor(demoEnUS.home.cards.account.title)).toHaveAttribute(
      'href',
      ACCOUNT_PATH,
    )
    expect(cardFor(demoEnUS.home.cards.notifications.title)).toHaveAttribute(
      'href',
      NOTIFICATIONS_PATH,
    )
  })

  it('hides the admin card from an impersonating identity whose effective set is empty', () => {
    renderDemoApp({
      session: createTestSession({
        ...authenticatedSnapshot({ system: [] }),
        impersonating: true,
      }),
    })

    expect(hasCard(demoEnUS.home.cards.admin.title)).toBe(false)
    expect(cardFor(demoEnUS.home.cards.account.title)).toHaveAttribute(
      'href',
      ACCOUNT_PATH,
    )
    expect(cardFor(demoEnUS.home.cards.notifications.title)).toHaveAttribute(
      'href',
      NOTIFICATIONS_PATH,
    )
  })

  it('hides the admin card from an impersonating identity even when its set holds a platform permission', () => {
    renderDemoApp({
      session: createTestSession({
        ...authenticatedSnapshot({ system: ['tenant.read'] }),
        impersonating: true,
      }),
    })

    expect(hasCard(demoEnUS.home.cards.admin.title)).toBe(false)
  })

  it('keeps the admin card hidden while the platform permission set is unknown', () => {
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })

    expect(hasCard(demoEnUS.home.cards.admin.title)).toBe(false)
    expect(hasCard(demoEnUS.home.cards.account.title)).toBe(true)
  })

  it('reveals the admin card once the platform permission set lands', async () => {
    const session = createTestSession(authenticatedSnapshot())
    renderDemoApp({ session })

    expect(hasCard(demoEnUS.home.cards.admin.title)).toBe(false)

    act(() => {
      session.set(authenticatedSnapshot({ system: ['tenant.read'] }))
    })

    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: demoEnUS.home.cards.admin.title,
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
    let grid: HTMLElement | null = card.parentElement
    while (grid !== null && getComputedStyle(grid).display !== 'grid') {
      grid = grid.parentElement
    }
    expect(grid).not.toBeNull()
    expect(grid).toHaveStyle({ display: 'grid' })

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

    expect(container.textContent ?? '').not.toMatch(/src\/pages|src\/routes\.tsx/)
  })

  it('opens the capability a card names, without a reload', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await user.click(cardFor(demoEnUS.home.cards.account.title))

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.profile,
      }),
    ).toBeInTheDocument()
  })

  it('leaves a modified click on a card to the browser', () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

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
