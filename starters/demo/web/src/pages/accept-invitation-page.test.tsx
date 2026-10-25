/**
 * The invitation landing (/invitations/:token): the public route an
 * invitee's link points at.
 *
 * The host gates the landing on a session: an anonymous viewer meets the
 * host's sign-in surface, and a signed-in viewer meets the accept screen,
 * whose two invitation operations reach the backend through the app's
 * generated SDK surface (src/app-api). The accept surface itself is public
 * -- the token is the whole credential -- so that gate is the host's
 * composition, not what binds the acceptance. The suite binds a scripted
 * request function over the api-sdk runtime seam so the real generated
 * operations run against responses it controls; accepting then navigates
 * the viewer home.
 */

import { beforeEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import type { AuthSession, AuthSnapshot, FeatureState } from '@speed/auth-core'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { accountUiResources } from '@speed/account-ui'
import { authUiResources } from '@speed/auth-ui'
import {
  FEATURES_GET,
  LOGIN_ANSWER,
  LOGIN_METHODS,
  LOGIN_START,
  PROFILE_GET,
  authenticatedCompletion,
  completeFlow,
  makeHarness,
  methodList,
  openFlow,
  principal,
} from '../test-utils/session-harness.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import {
  getCurrentTenantAlias,
  getCurrentTenantId,
  setCurrentTenantId,
  setTenantAliases,
} from '../chrome.js'
import { clearSelfTenants } from '../tenant-self.js'
import { renderDemoApp } from '../test-utils/render.js'
import { authenticatedSnapshot, createTestSession } from '../test-utils/session.js'
import { INVITATION_PREFIX } from '../paths.js'

/**
 * The auth-ui bundle the accept screen and the sign-in surface render from,
 * read as test data: the package's shipped text, not a repository file path.
 * A renamed label fails here rather than silently in a selector.
 */
const authEn = authUiResources['en-US'] as unknown as {
  readonly acceptInvitation: { readonly title: string; readonly accept: string }
  readonly mode: {
    readonly registerEntry: string
  }
  readonly register: {
    readonly usernameLabel: string
    readonly emailLabel: string
    readonly passwordLabel: string
    readonly confirmPasswordLabel: string
    readonly submit: string
  }
  readonly smsSignIn: {
    readonly phoneLabel: string
    readonly sendCode: string
    readonly codeLabel: string
    readonly submit: string
  }
}

/** The account-ui verification section a password registration lands on. */
const accountEn = accountUiResources['en-US'] as unknown as {
  readonly verification: {
    readonly codeLabel: string
    readonly verify: string
  }
}

const REGISTER = 'POST /api/v1/auth/register'

const TOKEN = 'tok-123'
const PATH = `${INVITATION_PREFIX}/${TOKEN}`

/** The tenant the acceptance reports, and the different alias the caller
 * tenant read carries for it: the accept answer has no alias, so the landing
 * resolves one from that read and must announce the read's value, never the
 * identifier. */
const INVITED_TENANT_ID = 'tenant-7f3a'
const INVITED_TENANT_ALIAS = 'acme-renamed'

/** The acceptView answer for a pending invitation: pending and still ahead of
 * its deadline, so the landing offers the accept action. The deadline is
 * relative to the run because a fixed instant would one day pass and turn the
 * landing into its invalid state. */
const VIEW = {
  invitation: {
    id: 'inv-1',
    tenant: 'Acme',
    email: 'invitee@example.com',
    status: 'pending',
    invited_by: 'acc-1',
    expires_at: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
    accepted_at: '0001-01-01T00:00:00Z',
    created_at: '2026-10-15T12:00:00Z',
  },
}

/**
 * The scripted invitation endpoints: a GET answers the pending invitation
 * and a POST accepts it (recording that it was redeemed). The caller tenant
 * read answers `selfBefore` until the acceptance commits and `selfAfter`
 * from then on, so a case can tell the landing's own read from the one the
 * app issued on mount.
 */
function makeInvitationBackend(
  options: {
    readonly selfBefore?: () => unknown
    readonly selfAfter?: () => unknown
  } = {},
) {
  const accepted: string[] = []
  const selfReads: ('before' | 'after')[] = []
  let acceptedOnce = false
  const fn: RequestFn = <T,>(path: string, requestOptions?: RequestOptions) => {
    const method = requestOptions?.method ?? 'GET'
    if (path === '/api/v1/auth/me/tenants' && method === 'GET') {
      selfReads.push(acceptedOnce ? 'after' : 'before')
      const answer = acceptedOnce
        ? (options.selfAfter ?? (() => ({ tenants: [] })))()
        : (options.selfBefore ?? (() => ({ tenants: [] })))()
      if (answer instanceof Error) {
        return Promise.reject(answer)
      }
      return Promise.resolve(answer as T)
    }
    if (path === `/api/v1/auth/invitations/${TOKEN}` && method === 'GET') {
      return Promise.resolve(VIEW as T)
    }
    if (path === `/api/v1/auth/invitations/${TOKEN}` && method === 'POST') {
      accepted.push(TOKEN)
      acceptedOnce = true
      return Promise.resolve({
        account: 'acc-1',
        tenant: INVITED_TENANT_ID,
        active: true,
        source: 'invite',
        sso_exempt: false,
      } as T)
    }
    return Promise.resolve(undefined as T)
  }
  return { fn, accepted, selfReads }
}

/**
 * The harness session with an already-settled feature document. A real page
 * load reads the document while the visitor is still anonymous, so the
 * invitation landing -- whose declaration is feature-gated -- renders before
 * the visitor signs in. The harness session reads features on a login commit
 * only, so this wrapper supplies the same initial answer.
 */
function sessionWithFeatures(
  session: AuthSession,
  names: readonly string[],
): AuthSession {
  const features: FeatureState = { state: 'ready', names: new Set(names) }
  let snapshot: AuthSnapshot = { ...session.getSnapshot(), features }
  const listeners = new Set<(value: AuthSnapshot) => void>()
  session.subscribe((next) => {
    snapshot = { ...next, features }
    for (const listener of listeners) {
      listener(snapshot)
    }
  })
  return {
    ...session,
    getSnapshot: () => snapshot,
    subscribe: (listener) => {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
  }
}

beforeEach(() => {
  window.history.replaceState({}, '', PATH)
  clearSelfTenants()
  setCurrentTenantId(null)
  setTenantAliases(new Map())
})

describe('the invitation landing', () => {
  it('shows the host sign-in surface to an anonymous viewer', () => {
    renderDemoApp()

    // The host gates the landing on a session: an anonymous viewer meets
    // the sign-in / register surface, never the accept screen.
    expect(
      screen.getByRole('heading', { level: 1, name: demoEnUS.signIn.heading }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        name: authEn.acceptInvitation.title,
      }),
    ).not.toBeInTheDocument()
  })

  it('accepts the invitation for a signed-in viewer and navigates home', async () => {
    const backend = makeInvitationBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The accept screen renders the pending invitation (read through the
    // generated operation) beside its accept action.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      }),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: authEn.acceptInvitation.accept }),
    )
    await waitFor(() => {
      expect(backend.accepted).toEqual([TOKEN])
    })
    // Acceptance navigates the viewer home (the tenant shell's business slot).
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
  })

  it('lands in the invited tenant and resolves its alias from the caller tenant read', async () => {
    const backend = makeInvitationBackend({
      selfBefore: () => ({ tenants: [{ id: 'other-tenant', alias: 'other' }] }),
      selfAfter: () => ({
        tenants: [
          { id: 'other-tenant', alias: 'other' },
          { id: INVITED_TENANT_ID, alias: INVITED_TENANT_ALIAS },
        ],
      }),
    })
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await user.click(
      await screen.findByRole('button', { name: authEn.acceptInvitation.accept }),
    )
    await waitFor(() => expect(backend.accepted).toEqual([TOKEN]))
    await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.home.heading,
    })

    // The acceptance names the tenant by identifier only. The alias is read
    // from the caller tenant list after it, and the two diverge here: a
    // landing that announced the identifier (or fell back to an unrelated
    // tenant) would fail this assertion.
    expect(backend.selfReads).toContain('after')
    expect(getCurrentTenantId()).toBe(INVITED_TENANT_ID)
    expect(getCurrentTenantAlias()).toBe(INVITED_TENANT_ALIAS)
  })

  it('navigates home with no current tenant when the alias read fails', async () => {
    const backend = makeInvitationBackend({
      selfBefore: () => ({ tenants: [] }),
      selfAfter: () =>
        Object.assign(new Error('the caller tenant read failed'), {
          status: 503,
        }),
    })
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await user.click(
      await screen.findByRole('button', { name: authEn.acceptInvitation.accept }),
    )
    await waitFor(() => expect(backend.accepted).toEqual([TOKEN]))

    // The acceptance still lands the viewer home...
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    // ...but no current tenant is written that the host cannot name: the
    // alias read failed, so the tenant stays unknown and tenant-scoped reads
    // stay disabled rather than naming a placeholder.
    expect(backend.selfReads).toContain('after')
    expect(getCurrentTenantId()).toBeNull()
    expect(getCurrentTenantAlias()).toBeNull()
  })

  it('confirms the new membership on the tenant home and dismisses once', async () => {
    const backend = makeInvitationBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await user.click(
      await screen.findByRole('button', {
        name: authEn.acceptInvitation.accept,
      }),
    )
    await waitFor(() => expect(backend.accepted).toEqual([TOKEN]))

    // The journey asks for the confirmation AND the return to the tenant
    // home: the acceptance navigates home, and the confirmation is shown
    // there rather than on the landing it just left.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    // The page's visible confirmation, inside the tenant shell's main
    // landmark: the host's live region carries the same sentence outside it.
    const confirmation = await within(screen.getByRole('main')).findByText(
      demoEnUS.home.membershipNotice.title,
    )
    expect(confirmation).toBeInTheDocument()

    // The landing unmounted on the way here, so the confirmation takes the
    // focus: keyboard and screen-reader users otherwise land on the document
    // body with no signal that the page changed.
    await waitFor(() => expect(document.activeElement).not.toBe(document.body))
    expect(document.activeElement).toHaveTextContent(
      demoEnUS.home.membershipNotice.title,
    )

    // Dismissing it removes the confirmation.
    await user.click(
      screen.getByRole('button', {
        name: demoEnUS.home.membershipNotice.dismiss,
      }),
    )
    expect(
      screen.queryByText(demoEnUS.home.membershipNotice.title),
    ).not.toBeInTheDocument()

    // A later visit to the home page in the same session does not bring it
    // back.
    await user.click(
      screen.getByRole('link', { name: demoEnUS.nav.account }),
    )
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.title,
      }),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('link', { name: demoEnUS.nav.home }))
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(demoEnUS.home.membershipNotice.title),
    ).not.toBeInTheDocument()
  })

  it('fills one standing live region with the confirmation on acceptance', async () => {
    const backend = makeInvitationBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The invitation summary settled; the acceptance's live region is the
    // host's, already in the tree and empty. A region born together with its
    // text is not reliably announced, so the confirmation has to be a change
    // to a region the screen reader already knows.
    await screen.findByRole('button', {
      name: authEn.acceptInvitation.accept,
    })
    const region = screen.getByRole('status', {
      name: demoEnUS.home.membershipNotice.region,
    })
    expect(region).toHaveTextContent('')

    await user.click(
      screen.getByRole('button', { name: authEn.acceptInvitation.accept }),
    )
    await waitFor(() => expect(backend.accepted).toEqual([TOKEN]))
    await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.home.heading,
    })

    // One and the same region throughout (never a second one born with its
    // text), now carrying the confirmation.
    expect(
      screen.getByRole('status', {
        name: demoEnUS.home.membershipNotice.region,
      }),
    ).toBe(region)
    expect(region).toHaveTextContent(demoEnUS.home.membershipNotice.title)
  })

  it('returns a password registration to the invitation after its address is verified', async () => {
    // An anonymous visitor on the invitation URL has no feature document yet
    // (it is readable only to an authenticated caller), so the landing route
    // is not matched and the tenant shell's own sign-in surface renders. A
    // password registration there must first prove the address on the account
    // page, and the invitation token has to survive that detour: the visitor
    // comes back to the link and can accept it.
    let verified = false
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'workspace.personal'],
      }),
      [LOGIN_METHODS]: () =>
        methodList([{ name: 'password', kind: 'form' }], {
          signup: true,
          signupPassword: true,
        }),
      [REGISTER]: () => ({
        state: 'registered',
        account_id: 'acc-1',
        verification: { state: 'pending', email: VIEW.invitation.email },
      }),
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () =>
        principal({
          verification: verified
            ? {
                state: 'verified',
                email: VIEW.invitation.email,
                since: '2026-10-25T00:00:00Z',
              }
            : { state: 'pending', email: VIEW.invitation.email, since: '' },
        }),
      ['POST /api/v1/auth/identifiers']: () => ({
        state: 'pending',
        expires_in: 600,
        resend_after: 0,
      }),
      ['POST /api/v1/auth/identifiers/verify']: () => {
        verified = true
        return { state: 'verified' }
      },
      [`GET /api/v1/auth/invitations/${TOKEN}`]: () => VIEW,
      [`POST /api/v1/auth/invitations/${TOKEN}`]: () => ({
        account: 'acc-1',
        tenant: INVITED_TENANT_ID,
        active: true,
        source: 'invite',
        sso_exempt: false,
      }),
      ['GET /api/v1/auth/me/tenants']: () => ({
        tenants: [{ id: INVITED_TENANT_ID, alias: INVITED_TENANT_ALIAS }],
      }),
    })
    const user = userEvent.setup()
    renderDemoApp({ session: harness.session })

    // The anonymous surface the visitor meets at the invitation URL.
    await user.click(
      await screen.findByRole('button', { name: authEn.mode.registerEntry }),
    )
    await user.type(
      screen.getByLabelText(authEn.register.usernameLabel),
      'invitee',
    )
    await user.type(
      screen.getByLabelText(authEn.register.emailLabel),
      VIEW.invitation.email,
    )
    await user.type(
      screen.getByLabelText(authEn.register.passwordLabel),
      's3cret-pass',
    )
    await user.type(
      screen.getByLabelText(authEn.register.confirmPasswordLabel),
      's3cret-pass',
    )
    await user.click(
      screen.getByRole('button', { name: authEn.register.submit }),
    )

    // The password registration's product landing: the account page's email
    // verification section, which sends one code on arrival.
    await waitFor(() => expect(window.location.pathname).toBe('/account'))
    const codeField = await screen.findByLabelText(
      accountEn.verification.codeLabel,
    )
    await user.type(codeField, '123456')
    await user.click(
      screen.getByRole('button', { name: accountEn.verification.verify }),
    )

    // The address is proved, so the detour's purpose is met: the visitor is
    // back on the link they started from and can accept it.
    await waitFor(() => expect(window.location.pathname).toBe(PATH))
    await user.click(
      await screen.findByRole('button', {
        name: authEn.acceptInvitation.accept,
      }),
    )

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    expect(
      await within(screen.getByRole('main')).findByText(
        demoEnUS.home.membershipNotice.title,
      ),
    ).toBeInTheDocument()
  })

  it('stays on the invitation landing after a registration through SMS', async () => {
    // The URL carries the token: the register mode's landing belongs to the
    // application's own sign-in page, not to a sign-in surface embedded in
    // this page. The session flip must re-render the accept screen in place.
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'workspace.personal'],
      }),
      [LOGIN_METHODS]: () =>
        methodList(
          [{ name: 'sms', kind: 'form', opens_account: true }],
          { signup: true },
        ),
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [
              { name: 'code', kind: 'code', required: true, secret: true },
            ],
          },
        }),
      [LOGIN_ANSWER]: (call) =>
        (call.options?.body as { step?: string }).step === 'complete'
          ? authenticatedCompletion()
          : completeFlow(2),
      [PROFILE_GET]: () => principal(),
      [`GET /api/v1/auth/invitations/${TOKEN}`]: () => VIEW,
    })
    const user = userEvent.setup()
    renderDemoApp({
      session: sessionWithFeatures(harness.session, [
        'authn.invitations',
        'workspace.personal',
      ]),
    })

    await user.click(
      await screen.findByRole('button', {
        name: authEn.mode.registerEntry,
      }),
    )
    await user.type(
      screen.getByLabelText(authEn.smsSignIn.phoneLabel),
      '+14155550123',
    )
    await user.click(
      screen.getByRole('button', { name: authEn.smsSignIn.sendCode }),
    )
    await user.type(
      await screen.findByLabelText(authEn.smsSignIn.codeLabel),
      '123456',
    )
    const form = screen
      .getByLabelText(authEn.smsSignIn.codeLabel)
      .closest('form') as HTMLFormElement
    await user.click(
      within(form).getByRole('button', { name: authEn.smsSignIn.submit }),
    )

    // The registration stayed on the invitation URL and the page re-rendered
    // onto its accept screen, which read the token's invitation.
    await waitFor(() => expect(window.location.pathname).toBe(PATH))
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
  })
})
