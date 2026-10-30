/**
 * The feature-gating chain, end to end.
 *
 * The unit suites prove their own pieces: route-kit narrows a table against a
 * synthetic predicate, auth-core holds the feature state, the console shell
 * publishes the host's answer. This suite joins them, because the behaviour
 * the design promises is the join: the server's document (read by the
 * generated `featureBoardList` through an auth-core session) decides which
 * pages, navigation entries, header controls and home cards exist.
 *
 * Every case drives the host with a session whose feature state is narrowed,
 * pending or failed; the assertions are the user-visible surfaces, so an
 * implementation that ignores the document (always visible), ignores the
 * pages (always hidden), or reads a failed document as an empty one (everything
 * absent) fails here even though the unit suites stay green.
 */

import { screen, waitFor } from '@testing-library/react'
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
import { CONSOLE_UI_NAMESPACE, consoleUiResources } from '@speed/console-ui'
import { LAYOUT_KIT_NAMESPACE } from '@speed/layout-kit'
import {
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '@speed/notification-ui'
import { createSpeedI18n } from '@speed/product-shell/bootstrap'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import {
  setCurrentTenantId,
  setTenantAliases,
} from './chrome.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'

/**
 * The expected copy, resolved from the packages' shipped resources through a
 * production instance rather than read from a file path.
 */
const textI18n = createSpeedI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
  namespaces: [
    { namespace: CONSOLE_UI_NAMESPACE, resources: consoleUiResources },
    { namespace: ACCOUNT_UI_NAMESPACE, resources: accountUiResources },
    { namespace: NOTIFICATION_UI_NAMESPACE, resources: notificationUiResources },
  ],
})
const tConsole = textI18n.getFixedT('en-US', CONSOLE_UI_NAMESPACE)
const tLayout = textI18n.getFixedT('en-US', LAYOUT_KIT_NAMESPACE)
const tNotifications = textI18n.getFixedT(
  'en-US',
  NOTIFICATION_UI_NAMESPACE,
)

/** The tenant the suite acts in, with the alias the host has recorded for it;
 * the host holds no default roster, so a current tenant must be nameable. */
const TENANT_ID = 'acme'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenantId(TENANT_ID)
  setTenantAliases(new Map([[TENANT_ID, TENANT_ID]]))
})

/** An authenticated session over exactly this feature document. */
function sessionWith(
  features: FeatureState,
  options: { readonly system?: readonly string[] | null } = {},
) {
  return createTestSession(authenticatedSnapshot({ ...options, features }))
}

/** The notification bell's accessible name (the unread count is not read
 * here, so the plain label is the answer). */
const BELL_NAME = new RegExp(`^${tNotifications('bell.label')}`)

/** A client whose event-stream opener records each open and each close, so a
 * suite can assert the stream gating and how many subscriptions survive. */
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

/** The home card for a capability, located by its heading. */
function cardHeading(title: string): HTMLElement | null {
  return screen.queryByRole('heading', { level: 2, name: title })
}

/** A card's anchor, located through its heading. */
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
    // The document serves the notification centre alone. Its card, its
    // navigation entry and the header bell survive; the console card -- a
    // platform surface a member does not hold -- disappears, and the object
    // page the skeleton no longer ships contributes neither a card nor a
    // navigation entry.
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
      screen.getByRole('link', { name: tNotifications('inbox.title') }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: BELL_NAME }),
    ).toBeInTheDocument()

    expect(
      cardHeading(demoEnUS.home.cards.console.title),
    ).not.toBeInTheDocument()
    expect(document.querySelector('a[href="/objects"]')).toBeNull()
  })

  it('renders the not-found fallback for a gated page the document omits', async () => {
    window.history.replaceState({}, '', '/notifications')
    renderDemoApp({ session: sessionWith(readyFeatures('workspace.personal')) })

    // The notification page is not a page in this deployment: the same
    // fallback an unregistered path renders.
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

    // The positive half: the page exists when its feature does, so the
    // absent cases above cannot pass by hiding everything always.
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
    // The whole chain in one case: a scripted server answer for GET /api/v1/features
    // goes through the generated featureBoardList into a real auth-core
    // session (signed in through the generated flow operations), and the host
    // hides the surfaces the document leaves out.
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
      [FEATURES_GET]: () => ({ features: ['notification.inbox'] }),
    })
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })

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
      screen.getByRole('button', { name: BELL_NAME }),
    ).toBeInTheDocument()
    // The document leaves the console surfaces out: a member gets no console
    // card, and the removed object page has no link to it.
    expect(
      cardHeading(demoEnUS.home.cards.console.title),
    ).not.toBeInTheDocument()
    expect(document.querySelector('a[href="/objects"]')).toBeNull()
  })
})

describe('a failed or pending document is not an empty one', () => {
  it('shows the retry view, not a 404, for a gated page when the read failed', async () => {
    window.history.replaceState({}, '', '/notifications')
    renderDemoApp({
      session: sessionWith({ state: 'failed', error: new Error('unavailable') }),
    })

    // "Failed" is neither every feature absent nor every feature present: the
    // page cannot be decided, so it offers a retry rather than reading as a
    // dead link.
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

    // The ungated account card stays; the feature-gated cards and the bell do
    // not claim either answer.
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

describe('the console area follows the path, not the feature document', () => {
  it('renders the frameless failure view for a console deep link when the read failed', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    renderDemoApp({
      session: sessionWith(
        { state: 'failed', error: new Error('unavailable') },
        { system: ['tenant.read'] },
      ),
    })

    // The console's own failed view, with no tenant frame around it: the
    // region is a fact of the URL prefix, so a deep link never falls into the
    // tenant shell (whose navigation and bell are absent here).
    expect(await screen.findByText(demoEnUS.route.failed)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: demoEnUS.route.retry }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { level: 1, name: demoEnUS.signIn.heading }),
    ).not.toBeInTheDocument()
  })

  it('renders the frameless wait view for a console deep link while a retry is in flight', async () => {
    window.history.replaceState({}, '', '/console')
    renderDemoApp({
      session: sessionWith({ state: 'pending' }, { system: ['tenant.read'] }),
    })

    expect(
      await screen.findByRole('progressbar', { name: demoEnUS.route.pending }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
  })

  it('falls back to the tenant area when no console page is served', async () => {
    // A ready document that serves none of the console pages' features: the
    // console declarations narrow to nothing, so the URL is the tenant area's
    // and the gated declaration renders the same 404 an unregistered path does.
    window.history.replaceState({}, '', '/console/settings')
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
        name: tConsole('shell.navLabel'),
      }),
    ).not.toBeInTheDocument()
    // The tenant frame did render: the area decision fell back, it did not
    // sign the viewer out or leave a blank page.
    expect(
      screen.getByRole('navigation', { name: tLayout('appShell.navLabel') }),
    ).toBeInTheDocument()
  })
})

describe('a console page whose feature is absent is not reachable', () => {
  // The SMS page is gated on sms.admin (console-ui's smsAdminRoutes). The
  // baseline binary imports that module for the API contract but names it on
  // no command, so the deployment's own document omits it while serving every
  // other console page. These two cases pin the consequence: the page is not
  // reachable at all, rather than reachable and merely empty.

  it('renders the not-found fallback at /console/sms and never mounts the page', async () => {
    window.history.replaceState({}, '', '/console/sms')
    // The console area is up (settings.admin is served), but sms.admin is not:
    // the route guard withholds the declaration, so the URL renders the same
    // fallback an unregistered path gets.
    renderDemoApp({
      session: sessionWith(
        readyFeatures('tenant.admin', 'settings.admin'),
        // The dashboard declaration owns the console prefix, so it is the
        // entry selected at /console/sms; holding its code lets the page
        // guard settle and the route fallback show.
        { system: ['tenant.read'] },
      ),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).toBeInTheDocument()
    // The page's view never ran: neither its title nor its register form is
    // in the document. An excerpt of the page showing here would mean the
    // absent-feature route was reachable.
    expect(
      screen.queryByRole('heading', { level: 1, name: tConsole('sms.title') }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: tConsole('sms.register.submit') }),
    ).not.toBeInTheDocument()
  })

  it('renders the SMS page at /console/sms when the document serves sms.admin', async () => {
    // The positive half: the absent case above cannot pass by hiding the page
    // always.
    window.history.replaceState({}, '', '/console/sms')
    renderDemoApp({
      session: sessionWith(
        readyFeatures('sms.admin', 'settings.admin', 'tenant.admin'),
        { system: ['sms.template.read'] },
      ),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tConsole('sms.title'),
      }),
    ).toBeInTheDocument()
  })
})

describe('the console entry and home card follow the served declarations', () => {
  it('targets the first served console page whose permission the principal holds', async () => {
    // tenant.admin is not served, so the dashboard and roster are out; the
    // settings page remains and its code is held, so both the header entry and
    // the home card point at it instead of the console root (which would 404).
    renderDemoApp({
      session: sessionWith(readyFeatures('settings.admin'), {
        system: ['settings.read'],
      }),
    })

    expect(
      await screen.findByRole('link', { name: demoEnUS.nav.console }),
    ).toHaveAttribute('href', '/console/settings')
    expect(cardLink(demoEnUS.home.cards.console.title)).toHaveAttribute(
      'href',
      '/console/settings',
    )
  })

  it('hides both when the document serves no console page', async () => {
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
    expect(
      screen.queryByRole('link', { name: demoEnUS.nav.console }),
    ).not.toBeInTheDocument()
    expect(cardHeading(demoEnUS.home.cards.console.title)).not.toBeInTheDocument()
  })
})

describe('the live notification stream', () => {
  it('opens only while the deployment serves notification.stream', async () => {
    // The stream is tenant-scoped, so it also waits for a settled tenant
    // context: this case serves the roster and answers it, the way a
    // deployment with notification.stream always can.
    bindRequestFn((async (path: string) => {
      if (path === '/api/v1/auth/me/tenants') {
        return { tenants: [{ id: 'acme', alias: 'acme' }] }
      }
      if (path === '/api/v1/system/tenants') {
        return { tenants: [{ id: 'acme', alias: 'acme', status: 'active' }] }
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
    // The served feature opens a connection. The suite renders the production
    // stack, StrictMode included, so the mount effect runs twice: the first
    // connection is closed by the cleanup and exactly one live subscription
    // remains. A cleanup that stopped closing would leave two open.
    await waitFor(() => expect(served).toHaveLength(2))
    expect(closed).toHaveLength(1)
    expect(served.length - closed.length).toBe(1)
    unmount()
    // Unmounting closes the surviving subscription too.
    expect(served.length - closed.length).toBe(0)

    const absent: string[] = []
    renderDemoApp({
      session: sessionWith(readyFeatures('tenant.admin', 'notification.inbox')),
      api: countingClient(absent, []),
    })
    await screen.findByText(demoEnUS.home.heading)
    // The provider would otherwise open a connection the server must refuse
    // with a bare 404.
    expect(absent).toHaveLength(0)
  })
})

describe('the account page mounts only the sections its document serves', () => {
  it('hides the phone and multifactor sections when their features are absent', async () => {
    // Each section lives in its own pane, so the absence is asserted with
    // that pane open: an unrendered pane would prove nothing.
    window.history.replaceState({}, '', '/account?section=security')
    const security = renderDemoApp({ session: sessionWith(readyFeatures()) })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.security,
      }),
    ).toBeInTheDocument()
    // No two-step block exists when the document serves no such feature, so
    // there is no title to find.
    expect(
      screen.queryByRole('heading', {
        level: 2,
        name: demoEnUS.account.rows.label.mfa,
      }),
    ).toBeNull()
    security.unmount()

    // The phone block lives on the profile pane.
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
    // The blocks are on their panes from the start: no surface has to be
    // opened before it is read.
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

    // The phone block lives on the profile pane.
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

describe('the invitation landing follows its gated declaration', () => {
  it('renders the not-found fallback when the deployment serves no invitations', async () => {
    window.history.replaceState({}, '', '/invitations/token-1')
    renderDemoApp({ session: sessionWith(readyFeatures('notification.inbox')) })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).toBeInTheDocument()
  })

  it('renders the accept landing when the feature is served', async () => {
    window.history.replaceState({}, '', '/invitations/token-1')
    renderDemoApp({ session: sessionWith(readyFeatures('authn.invitations')) })

    // The landing renders outside both shells: no navigation frame, and the
    // invitation's own surface is asked to read the token.
    await waitFor(() =>
      expect(screen.queryByRole('navigation')).not.toBeInTheDocument(),
    )
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).not.toBeInTheDocument()
  })
})
