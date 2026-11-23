/**
 * The admin entry's surface in a real browser, and the session gate that
 * stands in front of the admin area.
 *
 * The entry is an account-menu item: the bar carries no control of its own,
 * and the platform's own organization is where the entry is offered. The
 * browser tier owns the case beside the demo's other geometry cases because
 * the assertion is about what the rendered menu offers -- a real menu item,
 * a real link -- rather than about class names alone.
 *
 * The session-gate cases drive the same real browser because their subject is
 * where focus lands when the in-place sign-in surface is replaced by the
 * admin: a focus assertion the jsdom tier can only approximate.
 */

import { afterEach, describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { ADMIN_UI_NAMESPACE, adminUiResources } from '@speed/admin-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import {
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '@speed/notification-ui'
import { createSpeedI18n } from '@speed/product-shell/bootstrap'
import { createBackend } from './a11y/backends.js'
import { setCurrentTenant, setTenantNames } from './chrome.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

/** The admin bundle's shipped text, read as the release the app renders. */
const tAdmin = createSpeedI18n({
  supportedLanguages: ['en-US'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
  namespaces: [
    { namespace: ADMIN_UI_NAMESPACE, resources: adminUiResources },
  ],
}).getFixedT('en-US', ADMIN_UI_NAMESPACE)

describe('the admin entry surface', () => {
  it('is a menu item in the platform organization, and a real link', async () => {
    await page.viewport(1100, 900)
    // The platform's own organization: the entry is offered only there.
    setCurrentTenant('system')
    setTenantNames(new Map([['system', 'system']]))
    const session = createTestSession(
      authenticatedSnapshot({ system: ['tenant.read'] }),
    )
    renderDemoApp({ session, shellLayout: 'sidebar' })
    const user = userEvent.setup()

    await user.click(await screen.findByTestId('account-entry-trigger'))
    const entry = await screen.findByRole('menuitem', {
      name: demoEnUS.nav.admin,
    })
    // It is an action and a destination at once: a menu item whose element is
    // the anchor, so a modified click keeps the browser's own behaviour.
    expect(entry.tagName).toBe('A')
    expect(entry.getAttribute('href')).not.toBeNull()

    // The bar itself carries no such control: the entry lives in the menu.
    expect(
      screen.queryByRole('link', { name: demoEnUS.nav.admin }),
    ).toBeNull()
  })

  it('insets the account row’s name by one step on both sides', async () => {
    await page.viewport(1100, 900)
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const trigger = await screen.findByTestId('account-entry-trigger')
    const avatar = trigger.querySelector('.MuiAvatar-root')
    expect(avatar).not.toBeNull()
    const name = Array.from(trigger.querySelectorAll('span')).find(
      (node) => (node.textContent ?? '').trim() === 'Test User',
    )
    expect(name).toBeDefined()

    // The avatar's own track is wider than the navigation's icons, so the
    // name carries its own inset: one step on each side, rather than sitting
    // on the navigation label's line with the avatar crowding it.
    const style = getComputedStyle(name as Element)
    expect(style.paddingLeft).toBe('8px')
    expect(style.paddingRight).toBe('8px')
    const avatarRect = (avatar as Element).getBoundingClientRect()
    const range = document.createRange()
    range.selectNodeContents(name as Element)
    expect(range.getBoundingClientRect().left - avatarRect.right).toBeCloseTo(
      12,
      0,
    )
  })

  it('paints the unread count beside the trigger, fully inside its hit box', async () => {
    await page.viewport(1100, 900)
    // The scripted backend answers one unread message.
    bindRequestFn(createBackend('data'))
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const trigger = await screen.findByTestId('user-menu-trigger')
    const phrase = createSpeedI18n({
      supportedLanguages: ['en-US'],
      defaultLanguage: 'en-US',
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
      namespaces: [
        {
          namespace: NOTIFICATION_UI_NAMESPACE,
          resources: notificationUiResources,
        },
      ],
    }).getFixedT('en-US', NOTIFICATION_UI_NAMESPACE)('bell.labelWithCount', {
      n: '1',
    })
    await waitFor(() =>
      expect(trigger).toHaveAccessibleName(new RegExp(phrase)),
    )

    const overlay = Array.from(
      (trigger.parentElement as HTMLElement).querySelectorAll('[aria-hidden="true"]'),
    ).find((node) => (node.textContent ?? '').trim() === '1')
    expect(overlay).toBeDefined()
    const triggerRect = trigger.getBoundingClientRect()
    const overlayRect = (overlay as Element).getBoundingClientRect()
    // Every pixel of the count sits over the button: the overlay takes no
    // pointer events, so a part hanging past the trigger would be a dead
    // sliver where a click does nothing.
    expect(overlayRect.left).toBeGreaterThanOrEqual(triggerRect.left - 0.5)
    expect(overlayRect.top).toBeGreaterThanOrEqual(triggerRect.top - 0.5)
    expect(overlayRect.right).toBeLessThanOrEqual(triggerRect.right + 0.5)
    expect(overlayRect.bottom).toBeLessThanOrEqual(triggerRect.bottom + 0.5)
    // The digit stays out of the control's own text: a visible character
    // inside the button would have to appear in the accessible name.
    expect(trigger).not.toHaveTextContent('1')
  })
})

describe('the admin session gate across a sign-in', () => {
  afterEach(() => {
    window.history.replaceState({}, '', '/')
  })

  it("lands focus on the admin page's heading once the gate flips", async () => {
    // An anonymous visitor at an admin URL meets the sign-in surface. The
    // session then turns authenticated in place -- the same whole-page flip a
    // completed sign-in commits -- and the URL does not move.
    window.history.replaceState({}, '', '/admin/tenants')
    bindRequestFn(createBackend('data'))
    const session = createTestSession()
    renderDemoApp({ session })

    await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.signIn.heading,
    })
    // A completed sign-in clears the permission domains, so the flip lands on
    // the area's waiting view rather than the page. The gate must hold the
    // hand-off and complete it when the set attaches; landing on its own
    // unnamed wrapper and stopping there loses the reader.
    act(() => {
      session.set(authenticatedSnapshot())
    })
    expect(document.activeElement).not.toBe(document.body)
    act(() => {
      session.set(authenticatedSnapshot({ system: ['tenant.read'] }))
    })

    const heading = await screen.findByRole('heading', {
      level: 1,
      name: tAdmin('tenants.title'),
    })
    await waitFor(() => expect(heading).toHaveFocus())
    // The landing target is the new page's own entry, inside its main
    // landmark, not the document body.
    expect(heading.closest('main')).not.toBeNull()
  })

  it('leaves focus where the operator moved it when the gate flips', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    bindRequestFn(createBackend('data'))
    const session = createTestSession()
    renderDemoApp({ session })
    await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.signIn.heading,
    })

    // A connected control the operator moved to before the flip: the gate
    // must leave it alone.
    const elsewhere = document.createElement('button')
    elsewhere.textContent = 'elsewhere'
    document.body.appendChild(elsewhere)
    try {
      elsewhere.focus()
      expect(elsewhere).toHaveFocus()

      act(() => {
        session.set(authenticatedSnapshot({ system: ['tenant.read'] }))
      })
      const heading = await screen.findByRole('heading', {
        level: 1,
        name: tAdmin('tenants.title'),
      })
      expect(elsewhere).toHaveFocus()
      expect(heading).not.toHaveFocus()
    } finally {
      elsewhere.remove()
    }
  })
})
