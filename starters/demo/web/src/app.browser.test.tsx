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
    expect(entry.tagName).toBe('A')
    expect(entry.getAttribute('href')).not.toBeNull()

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
    expect(overlayRect.left).toBeGreaterThanOrEqual(triggerRect.left - 0.5)
    expect(overlayRect.top).toBeGreaterThanOrEqual(triggerRect.top - 0.5)
    expect(overlayRect.right).toBeLessThanOrEqual(triggerRect.right + 0.5)
    expect(overlayRect.bottom).toBeLessThanOrEqual(triggerRect.bottom + 0.5)
    expect(trigger).not.toHaveTextContent('1')
  })
})

describe('the admin session gate across a sign-in', () => {
  afterEach(() => {
    window.history.replaceState({}, '', '/')
  })

  it("lands focus on the admin page's heading once the gate flips", async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    bindRequestFn(createBackend('data'))
    const session = createTestSession()
    renderDemoApp({ session })

    await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.signIn.heading,
    })
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
