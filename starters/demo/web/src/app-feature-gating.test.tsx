import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn, SpeedClient } from '@speed/api-client'
import type { FeatureState } from '@speed/auth-core'
import {
  authenticatedCompletion,
  completeFlow,
  FEATURES_GET,
  LOGIN_ANSWER,
  LOGIN_START,
  makeHarness,
  principal,
  PROFILE_GET,
} from './test-utils/session-harness.js'
import { ACCOUNT_UI_NAMESPACE, accountUiResources } from '@speed/account-ui'
import { ADMIN_UI_NAMESPACE, adminUiResources } from '@speed/admin-ui'
import { authUiResources } from '@speed/auth-ui'
import { LAYOUT_KIT_NAMESPACE } from '@speed/layout-kit'
import {
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '@speed/notification-ui'
import { createSpeedI18n } from '@speed/product-shell/bootstrap'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import {
  setCurrentTenant,
  setTenantNames,
} from './chrome.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'

const textI18n = createSpeedI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
  namespaces: [
    { namespace: ADMIN_UI_NAMESPACE, resources: adminUiResources },
    { namespace: ACCOUNT_UI_NAMESPACE, resources: accountUiResources },
    { namespace: NOTIFICATION_UI_NAMESPACE, resources: notificationUiResources },
  ],
})
const tAdmin = textI18n.getFixedT('en-US', ADMIN_UI_NAMESPACE)
const tLayout = textI18n.getFixedT('en-US', LAYOUT_KIT_NAMESPACE)
const tNotifications = textI18n.getFixedT(
  'en-US',
  NOTIFICATION_UI_NAMESPACE,
)

const TENANT_ID = 'acme'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenant(TENANT_ID)
  setTenantNames(new Map([[TENANT_ID, TENANT_ID]]))
})

function sessionWith(
  features: FeatureState,
  options: { readonly system?: readonly string[] | null } = {},
) {
  return createTestSession(authenticatedSnapshot({ ...options, features }))
}

const BELL_NAME = new RegExp(`^${tNotifications('bell.label')}`)

function countingClient(opened: string[], closed: string[]): SpeedClient {
  const request = (async () => {
    throw new Error('the gating suite client must not be called')
  }) as RequestFn
  return Object.assign(request, {
    openEventStream: (path: string) => {
      opened.push(path)
      return {
        close: () => {
          closed.push(path)
        },
      }
    },
  })
}

function cardHeading(title: string): HTMLElement | null {
  return screen.queryByRole('heading', { level: 2, name: title })
}

function cardLink(title: string): HTMLElement {
  const heading = screen.getByRole('heading', { level: 2, name: title })
  const anchor = heading.closest('a')
  if (anchor === null) {
    throw new Error('the card heading is not inside its link')
  }
  return anchor
}

describe('a served document narrows the tenant area', () => {
  it('keeps the surfaces it names and hides the ones it omits', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: sessionWith(readyFeatures('notification.inbox')) })

    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: demoEnUS.home.cards.notifications.title,
      }),
    ).toBeInTheDocument()
    expect(
      cardLink(demoEnUS.home.cards.notifications.title),
    ).toHaveAttribute('href', '/notifications')
    expect(
      within(screen.getByRole('navigation')).queryByRole('link', {
        name: tNotifications('inbox.title'),
      }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: BELL_NAME }),
    ).not.toBeInTheDocument()
    expect(
      cardHeading(demoEnUS.home.cards.admin.title),
    ).not.toBeInTheDocument()
    expect(document.querySelector('a[href="/objects"]')).toBeNull()

    await user.click(screen.getByTestId('user-menu-trigger'))
    expect(
      await screen.findByRole('menuitem', { name: demoEnUS.userMenu.messages }),
    ).toBeInTheDocument()
  })

  it('renders the not-found fallback for a gated page the document omits', async () => {
    window.history.replaceState({}, '', '/notifications')
    renderDemoApp({ session: sessionWith(readyFeatures('org.personal')) })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: tNotifications('inbox.title'),
      }),
    ).not.toBeInTheDocument()
  })

  it('renders the gated page when the document serves it', async () => {
    window.history.replaceState({}, '', '/notifications')
    renderDemoApp({
      session: sessionWith(readyFeatures('notification.inbox')),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tNotifications('inbox.title'),
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).not.toBeInTheDocument()
  })

  it('drives the gate from the server document read by the real session', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
      [FEATURES_GET]: () => ({ features: ['notification.inbox'] }),
    })
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })

    const user = userEvent.setup()
    renderDemoApp({ session: harness.session })

    expect(harness.calls.some((call) => call.path === '/api/v1/features')).toBe(true)
    expect(harness.session.getSnapshot().features).toEqual({
      state: 'ready',
      names: new Set(['notification.inbox']),
    })
    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: demoEnUS.home.cards.notifications.title,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: BELL_NAME }),
    ).not.toBeInTheDocument()
    expect(
      cardHeading(demoEnUS.home.cards.admin.title),
    ).not.toBeInTheDocument()
    expect(document.querySelector('a[href="/objects"]')).toBeNull()

    await user.click(screen.getByTestId('user-menu-trigger'))
    expect(
      await screen.findByRole('menuitem', { name: demoEnUS.userMenu.messages }),
    ).toBeInTheDocument()
  })
})

describe('a failed or pending document is not an empty one', () => {
  it('shows the retry view, not a 404, for a gated page when the read failed', async () => {
    window.history.replaceState({}, '', '/notifications')
    renderDemoApp({
      session: sessionWith({ state: 'failed', error: new Error('unavailable') }),
    })

    expect(await screen.findByText(demoEnUS.route.failed)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: demoEnUS.route.retry }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).not.toBeInTheDocument()
  })

  it('keeps gated controls hidden while the read failed, without failing closed to an empty platform', async () => {
    renderDemoApp({
      session: sessionWith({ state: 'failed', error: new Error('unavailable') }),
    })

    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: demoEnUS.home.cards.account.title,
      }),
    ).toBeInTheDocument()
    expect(
      cardHeading(demoEnUS.home.cards.notifications.title),
    ).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: BELL_NAME })).not.toBeInTheDocument()
  })

  it('shows the wait view, not a 404, for a gated page while a retry is in flight', async () => {
    window.history.replaceState({}, '', '/notifications')
    renderDemoApp({ session: sessionWith({ state: 'pending' }) })

    expect(
      await screen.findByRole('progressbar', { name: demoEnUS.route.pending }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).not.toBeInTheDocument()
  })
})

describe('the admin area follows the path, not the feature document', () => {
  it('renders the frameless failure view for an admin deep link when the read failed', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    renderDemoApp({
      session: sessionWith(
        { state: 'failed', error: new Error('unavailable') },
        { system: ['tenant.read'] },
      ),
    })

    expect(await screen.findByText(demoEnUS.route.failed)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: demoEnUS.route.retry }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { level: 1, name: demoEnUS.signIn.heading }),
    ).not.toBeInTheDocument()
  })

  it('renders the frameless wait view for an admin deep link while a retry is in flight', async () => {
    window.history.replaceState({}, '', '/admin')
    renderDemoApp({
      session: sessionWith({ state: 'pending' }, { system: ['tenant.read'] }),
    })

    expect(
      await screen.findByRole('progressbar', { name: demoEnUS.route.pending }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  })

  it('falls back to the tenant area when no admin page is served', async () => {
    window.history.replaceState({}, '', '/admin/settings')
    renderDemoApp({
      session: sessionWith(readyFeatures('notification.inbox'), {
        system: ['settings.read'],
      }),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('navigation', { name: tLayout('appShell.navLabel') }),
    ).toBeInTheDocument()
  })
})

describe('an admin page whose feature is absent is not reachable', () => {

  it('renders the not-found fallback at /admin/sms and never mounts the page', async () => {
    window.history.replaceState({}, '', '/admin/sms')
    renderDemoApp({
      session: sessionWith(
        readyFeatures('tenant.admin', 'settings.admin'),
        { system: ['tenant.read'] },
      ),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { level: 1, name: tAdmin('sms.title') }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: tAdmin('sms.register.submit') }),
    ).not.toBeInTheDocument()
  })

  it('renders the SMS page at /admin/sms when the document serves sms.admin', async () => {
    window.history.replaceState({}, '', '/admin/sms')
    renderDemoApp({
      session: sessionWith(
        readyFeatures('sms.admin', 'settings.admin', 'tenant.admin'),
        { system: ['sms.template.read'] },
      ),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tAdmin('sms.title'),
      }),
    ).toBeInTheDocument()
  })
})

describe('the admin entry and home card follow the served declarations', () => {
  it('targets the first served admin page whose permission the principal holds', async () => {
    setCurrentTenant('system')
    setTenantNames(new Map([['system', 'system']]))
    const user = userEvent.setup()
    renderDemoApp({
      session: sessionWith(readyFeatures('settings.admin'), {
        system: ['settings.read'],
      }),
    })

    expect(cardLink(demoEnUS.home.cards.admin.title)).toHaveAttribute(
      'href',
      '/admin/settings',
    )
    await user.click(await screen.findByTestId('user-menu-trigger'))
    expect(
      await screen.findByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toHaveAttribute('href', '/admin/settings')
  })

  it('hides both when the document serves no admin page', async () => {
    setCurrentTenant('system')
    setTenantNames(new Map([['system', 'system']]))
    const user = userEvent.setup()
    renderDemoApp({
      session: sessionWith(readyFeatures('notification.inbox'), {
        system: ['settings.read'],
      }),
    })

    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: demoEnUS.home.cards.account.title,
      }),
    ).toBeInTheDocument()
    expect(cardHeading(demoEnUS.home.cards.admin.title)).not.toBeInTheDocument()
    await user.click(screen.getByTestId('user-menu-trigger'))
    const menu = await screen.findByRole('menu')
    expect(
      within(menu).queryByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).not.toBeInTheDocument()
  })
})

describe('the live notification stream', () => {
  it('opens only while the deployment serves notification.stream', async () => {
    bindRequestFn((async (path: string) => {
      if (path === '/api/v1/auth/me/tenants') {
        return { tenants: [{ id: 'acme', name: 'acme' }] }
      }
      if (path === '/api/v1/system/tenants') {
        return { tenants: [{ id: 'acme', name: 'acme', status: 'active' }] }
      }
      throw new Error(`the gating suite binds only the roster: ${path}`)
    }) as RequestFn)
    const served: string[] = []
    const closed: string[] = []
    const { unmount } = renderDemoApp({
      session: sessionWith(
        readyFeatures(
          'tenant.admin',
          'notification.inbox',
          'notification.stream',
        ),
      ),
      api: countingClient(served, closed),
    })
    await waitFor(() => expect(served).toHaveLength(2))
    expect(closed).toHaveLength(1)
    expect(served.length - closed.length).toBe(1)
    unmount()
    expect(served.length - closed.length).toBe(0)

    const absent: string[] = []
    renderDemoApp({
      session: sessionWith(readyFeatures('tenant.admin', 'notification.inbox')),
      api: countingClient(absent, []),
    })
    await screen.findByText(demoEnUS.home.heading)
    expect(absent).toHaveLength(0)
  })
})

describe('the account page mounts only the sections its document serves', () => {
  it('hides the phone and multifactor sections when their features are absent', async () => {
    window.history.replaceState({}, '', '/account?section=security')
    const security = renderDemoApp({ session: sessionWith(readyFeatures()) })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.security,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 2,
        name: demoEnUS.account.rows.label.mfa,
      }),
    ).toBeNull()
    security.unmount()

    window.history.replaceState({}, '', '/account/profile')
    renderDemoApp({ session: sessionWith(readyFeatures()) })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.profile,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 2,
        name: demoEnUS.account.rows.label.phone,
      }),
    ).toBeNull()
  })

  it('mounts them when the document serves their features', async () => {
    window.history.replaceState({}, '', '/account?section=security')
    const security = renderDemoApp({
      session: sessionWith(readyFeatures('authn.mfa', 'authn.phone')),
    })

    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: demoEnUS.account.rows.label.mfa,
      }),
    ).toBeInTheDocument()
    security.unmount()

    window.history.replaceState({}, '', '/account/profile')
    renderDemoApp({
      session: sessionWith(readyFeatures('authn.mfa', 'authn.phone')),
    })

    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: demoEnUS.account.rows.label.phone,
      }),
    ).toBeInTheDocument()
  })
})

describe('the invitation landing is ungated', () => {
  const authEn = authUiResources['en-US'] as unknown as {
    readonly acceptInvitation: {
      readonly title: string
      readonly missing: { readonly title: string; readonly hint: string }
    }
  }
  const pendingView = {
    state: 'pending',
    invitation: {
      tenant: 'acme',
      tenant_display_name: 'Acme',
      invited_by_name: 'Ada Lovelace',
      email: 'i***@demo.test',
      expires_at: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
    },
  }

  it('renders the landing to an anonymous viewer whatever the feature document says', async () => {
    bindRequestFn((async (path: string) => {
      if (path === '/api/v1/auth/invitations/token-1') {
        return pendingView
      }
      return undefined
    }) as RequestFn)
    window.history.replaceState({}, '', '/invitations/token-1')
    renderDemoApp()

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  })

  it('renders the landing when the feature is served, outside both shells', async () => {
    bindRequestFn((async (path: string) => {
      if (path === '/api/v1/auth/invitations/token-1') {
        return pendingView
      }
      if (path === '/api/v1/auth/me') {
        return principal()
      }
      return undefined
    }) as RequestFn)
    window.history.replaceState({}, '', '/invitations/token-1')
    renderDemoApp({ session: sessionWith(readyFeatures('authn.invitations')) })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  })

  it('renders the invalid-link state when the invitation module is not mounted', async () => {
    bindRequestFn((async (path: string) => {
      if (path === '/api/v1/auth/invitations/token-1') {
        throw { status: 404, code: 'client.http.404' }
      }
      return undefined
    }) as RequestFn)
    window.history.replaceState({}, '', '/invitations/token-1')
    renderDemoApp()

    expect(
      await screen.findByText(authEn.acceptInvitation.missing.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(authEn.acceptInvitation.missing.hint),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { name: demoEnUS.signIn.heading }),
    ).not.toBeInTheDocument()
  })
})
