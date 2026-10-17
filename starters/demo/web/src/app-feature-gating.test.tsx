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
} from '@speed/test-utils/session-harness'
import consoleUiEnUS from '../../../../web/packages/console-ui/src/locales/en-US.json' with { type: 'json' }
import accountUiEnUS from '../../../../web/packages/account-ui/src/locales/en-US.json' with { type: 'json' }
import layoutKitEnUS from '../../../../web/packages/layout-kit/src/locales/en-US.json' with { type: 'json' }
import notificationUiEnUS from '../../../../web/packages/notification-ui/src/locales/en-US.json' with { type: 'json' }
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { DEFAULT_TENANT_ID, setCurrentTenantId } from './chrome.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenantId(DEFAULT_TENANT_ID)
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
const BELL_NAME = new RegExp(`^${notificationUiEnUS.bell.label}`)

/** A client whose event-stream opener records each open, so a suite can
 * assert the stream gating without a transport. */
function countingClient(opened: string[]): SpeedClient {
  const request = (async () => {
    throw new Error('the gating suite client must not be called')
  }) as RequestFn
  return Object.assign(request, {
    openEventStream: (path: string) => {
      opened.push(path)
      return { close: () => undefined }
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
    // The document serves the object store alone. The object card and
    // navigation entry survive; the notification surfaces -- bell, card,
    // navigation entry -- must disappear with the page they lead to.
    renderDemoApp({ session: sessionWith(readyFeatures('objectstore.objects')) })

    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: demoEnUS.home.cards.objects.title,
      }),
    ).toBeInTheDocument()
    expect(cardLink(demoEnUS.home.cards.objects.title)).toHaveAttribute(
      'href',
      '/objects',
    )
    expect(
      screen.getByRole('link', { name: demoEnUS.nav.objects }),
    ).toBeInTheDocument()

    expect(screen.queryByRole('button', { name: BELL_NAME })).not.toBeInTheDocument()
    expect(
      cardHeading(demoEnUS.home.cards.notifications.title),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: notificationUiEnUS.inbox.title }),
    ).not.toBeInTheDocument()
  })

  it('renders the not-found fallback for a gated page the document omits', async () => {
    window.history.replaceState({}, '', '/notifications')
    renderDemoApp({ session: sessionWith(readyFeatures('objectstore.objects')) })

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
        name: notificationUiEnUS.inbox.title,
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
        name: notificationUiEnUS.inbox.title,
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
    // The whole chain in one case: a scripted server answer for GET /features
    // goes through the generated featureBoardList into a real auth-core
    // session (signed in through the generated flow operations), and the host
    // hides the surfaces the document leaves out.
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
      [FEATURES_GET]: () => ({ features: ['objectstore.objects'] }),
    })
    await harness.session.startFlow({ method: 'password' })
    await harness.session.answerFlow({ seq: 2, step: 'complete' })

    renderDemoApp({ session: harness.session })

    expect(harness.calls.some((call) => call.path === '/features')).toBe(true)
    expect(harness.session.getSnapshot().features).toEqual({
      state: 'ready',
      names: new Set(['objectstore.objects']),
    })
    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: demoEnUS.home.cards.objects.title,
      }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: BELL_NAME })).not.toBeInTheDocument()
    expect(
      cardHeading(demoEnUS.home.cards.notifications.title),
    ).not.toBeInTheDocument()
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
    expect(cardHeading(demoEnUS.home.cards.objects.title)).not.toBeInTheDocument()
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
        name: consoleUiEnUS.shell.navLabel,
      }),
    ).not.toBeInTheDocument()
    // The tenant frame did render: the area decision fell back, it did not
    // sign the viewer out or leave a blank page.
    expect(
      screen.getByRole('navigation', { name: layoutKitEnUS.appShell.navLabel }),
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
    const served: string[] = []
    const { unmount } = renderDemoApp({
      session: sessionWith(
        readyFeatures('notification.inbox', 'notification.stream'),
      ),
      api: countingClient(served),
    })
    await waitFor(() => expect(served).toHaveLength(1))
    unmount()

    const absent: string[] = []
    renderDemoApp({
      session: sessionWith(readyFeatures('notification.inbox')),
      api: countingClient(absent),
    })
    await screen.findByText(demoEnUS.home.heading)
    // The provider would otherwise reconnect through its backoff against an
    // endpoint the deployment does not mount.
    expect(absent).toHaveLength(0)
  })
})

describe('the account page mounts only the sections its document serves', () => {
  it('hides the phone and multifactor sections when their features are absent', async () => {
    window.history.replaceState({}, '', '/account')
    renderDemoApp({ session: sessionWith(readyFeatures()) })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.title,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 2,
        name: accountUiEnUS.mfa.title,
      }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 2,
        name: accountUiEnUS.phone.title,
      }),
    ).not.toBeInTheDocument()
  })

  it('mounts them when the document serves their features', async () => {
    window.history.replaceState({}, '', '/account')
    renderDemoApp({
      session: sessionWith(readyFeatures('authn.mfa', 'authn.phone')),
    })

    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: accountUiEnUS.mfa.title,
      }),
    ).toBeInTheDocument()
    expect(
      await screen.findByRole('heading', {
        level: 2,
        name: accountUiEnUS.phone.title,
      }),
    ).toBeInTheDocument()
  })
})

describe('the invitation landing follows its gated declaration', () => {
  it('renders the not-found fallback when the deployment serves no invitations', async () => {
    window.history.replaceState({}, '', '/invitations/token-1')
    renderDemoApp({ session: sessionWith(readyFeatures('objectstore.objects')) })

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
