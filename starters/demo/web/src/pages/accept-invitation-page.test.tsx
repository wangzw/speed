/**
 * The invitation landing (/invitations/:token): the public route an
 * invitee's link points at.
 *
 * The landing renders the public invitation to everyone: an anonymous viewer
 * sees the inviter, the organization, the masked address and the host's
 * sign-in / create-account actions, and a signed-in viewer meets the accept
 * screen whose operations reach the backend through the app's generated SDK
 * surface (src/app-api). The token read is public, but the acceptance needs
 * the signed-in account to hold the invited address as a proven email; the
 * signed-in surface reads the guarded eligibility operation on arrival so the
 * proof step appears before a refused acceptance. The suite binds a scripted
 * request function over the api-sdk runtime seam so the real generated
 * operations run against responses it controls; accepting then navigates the
 * viewer home.
 */

import { beforeEach, describe, expect, it } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RequestFn, RequestOptions } from '@speed/api-client'
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
  getCurrentTenantName,
  getCurrentTenant,
  setCurrentTenant,
  setTenantNames,
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
  readonly acceptInvitation: {
    readonly title: string
    readonly accept: string
    readonly invited: string
    readonly invitedBy: string
    readonly invitedByWithRole: string
    readonly email: string
    readonly signIn: string
    readonly createAccount: string
    readonly expired: { readonly title: string }
    readonly revoked: { readonly title: string }
    readonly used: { readonly title: string }
    readonly member: {
      readonly title: string
      readonly open: string
    }
    readonly missing: { readonly title: string; readonly hint: string }
    readonly unproved: { readonly title: string }
  }
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

/** The tenant the acceptance reports, and the different name the caller
 * tenant read carries for it: the landing resolves the switcher's row from
 * that read and must announce the read's value, never the identifier. */
const INVITED_TENANT_NAME = 'acme-renamed'

/** The acceptView answer for a pending invitation: pending and still ahead of
 * its deadline, so the landing offers the accept action. The deadline is
 * relative to the run because a fixed instant would one day pass and turn the
 * landing into its invalid state. */
const VIEW = {
  state: 'pending',
  invitation: {
    tenant: 'acme',
    tenant_display_name: 'Acme',
    invited_by_name: 'Ada Lovelace',
    email: 'invitee@example.com',
    expires_at: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
  },
}

/** A pending invitation that carries a role. */
const VIEW_WITH_ROLE = {
  state: 'pending',
  invitation: {
    ...VIEW.invitation,
    role: 'tenant-admin',
  },
}

/** The same pending answer for an organization that never chose a display
 * name, as the demo seed's organization does. */
const VIEW_WITHOUT_DISPLAY_NAME = {
  state: 'pending',
  invitation: {
    tenant: 'acme',
    tenant_display_name: '',
    invited_by_name: 'Ada Lovelace',
    email: 'invitee@example.com',
    expires_at: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
    accepted_at: null,
    created_at: '2026-10-15T12:00:00Z',
  },
}

/** The eligibility answer a signed-in account that proved the address reads. */
const PROVED = { state: 'pending', email_proved: true }

/** The scripted invitation endpoints: a GET answers the pending invitation
 * and a POST accepts it (recording that it was redeemed). The caller tenant
 * read answers `selfBefore` until the acceptance commits and `selfAfter`
 * from then on, so a case can tell the landing's own read from the one the
 * app issued on mount. The eligibility endpoint answers `eligibility`
 * (proved by default) whenever the signed-in surface asks. */
function makeInvitationBackend(
  options: {
    readonly selfBefore?: () => unknown
    readonly selfAfter?: () => unknown
    readonly view?: unknown
    readonly viewError?: unknown
    readonly eligibility?: () => unknown
  } = {},
) {
  const accepted: string[] = []
  const eligibilityReads: string[] = []
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
    if (path === `/api/v1/auth/invitations/${TOKEN}/eligibility` && method === 'GET') {
      eligibilityReads.push(TOKEN)
      return Promise.resolve(
        (options.eligibility ?? (() => PROVED))() as T,
      )
    }
    if (path === `/api/v1/auth/invitations/${TOKEN}` && method === 'GET') {
      if (options.viewError !== undefined) {
        return Promise.reject(options.viewError)
      }
      return Promise.resolve((options.view ?? VIEW) as T)
    }
    if (path === `/api/v1/auth/invitations/${TOKEN}` && method === 'POST') {
      accepted.push(TOKEN)
      acceptedOnce = true
      return Promise.resolve({
        account: 'acc-1',
        tenant: INVITED_TENANT_NAME,
        active: true,
        source: 'invite',
        sso_exempt: false,
      } as T)
    }
    // The app reads the caller's profile on every authenticated mount; the
    // real route answers 200 with this body (never an empty one), and the
    // home it navigates to also reads the unread count.
    if (path === '/api/v1/auth/me' && method === 'GET') {
      return Promise.resolve(principal() as T)
    }
    if (
      path === '/api/v1/notifications/messages/unread-count' &&
      method === 'GET'
    ) {
      return Promise.resolve({ unread: 0 } as T)
    }
    return Promise.resolve(undefined as T)
  }
  return { fn, accepted, eligibilityReads, selfReads }
}

beforeEach(() => {
  window.history.replaceState({}, '', PATH)
  clearSelfTenants()
  setCurrentTenant(null)
  setTenantNames(new Map())
})

/** Opens the landing's sign-in action and completes the SMS challenge the
 * harness scripts, leaving the session's completion answer to the caller. */
async function signInWithSms(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  await user.click(
    await screen.findByRole('button', { name: authEn.acceptInvitation.signIn }),
  )
  await user.type(
    await screen.findByLabelText(authEn.smsSignIn.phoneLabel),
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
}

/** Holds the invitation read the landing issues after the in-place sign-in
 * closes. Reads answer at once until `hold` is called; from then on they wait
 * for `release`, so a case can observe the landing while its heading is still
 * absent. */
function heldInvitationRead(): {
  readonly handler: () => Promise<unknown>
  readonly hold: () => void
  readonly release: () => void
} {
  let holding = false
  let release = (): void => undefined
  const held = new Promise<void>((resolve) => {
    release = resolve
  })
  return {
    handler: () => (holding ? held.then(() => VIEW) : Promise.resolve(VIEW)),
    hold: () => {
      holding = true
    },
    release: () => release(),
  }
}

describe('the invitation landing', () => {
  it('shows the invitation to an anonymous viewer, not a generic sign-in page', async () => {
    // The feature document is readable only to an authenticated caller, so
    // the landing declaration carries no gate: an anonymous visitor sees the
    // invitation itself rather than falling through to the generic sign-in
    // surface.
    const backend = makeInvitationBackend()
    bindRequestFn(backend.fn)
    renderDemoApp()

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
    // The landing renders outside both shells.
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument()
    expect(
      screen.getByText(
        authEn.acceptInvitation.invitedBy.replace(
          '{{inviter}}',
          'Ada Lovelace',
        ).replace('{{tenant}}', 'Acme'),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        authEn.acceptInvitation.email.replace(
          '{{email}}',
          'invitee@example.com',
        ),
      ),
    ).toBeInTheDocument()
    // The two entries replace the generic sign-in surface, which is not on
    // screen until one of them is used.
    expect(
      screen.getByRole('button', { name: authEn.acceptInvitation.signIn }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', {
        name: authEn.acceptInvitation.createAccount,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).not.toBeInTheDocument()
  })

  it('opens the sign-in surface in place and returns to the landing', async () => {
    const backend = makeInvitationBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderDemoApp()

    await user.click(
      await screen.findByRole('button', {
        name: authEn.acceptInvitation.signIn,
      }),
    )
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).toBeInTheDocument()
    // The URL and its token survived the switch.
    expect(window.location.pathname).toBe(PATH)

    await user.click(screen.getByRole('button', { name: demoEnUS.signIn.back }))
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
  })

  it('shows the invited role in the landing sentence', async () => {
    const backend = makeInvitationBackend({ view: VIEW_WITH_ROLE })
    bindRequestFn(backend.fn)
    renderDemoApp()

    expect(
      await screen.findByText(
        authEn.acceptInvitation.invitedByWithRole
          .replace('{{inviter}}', 'Ada Lovelace')
          .replace('{{role}}', 'tenant-admin')
          .replace('{{tenant}}', 'Acme'),
      ),
    ).toBeInTheDocument()
  })

  it('renders the expired and revoked terminal states apart', async () => {
    const expired = makeInvitationBackend({
      view: { state: 'expired' },
    })
    bindRequestFn(expired.fn)
    const first = renderDemoApp()
    expect(
      await screen.findByText(authEn.acceptInvitation.expired.title),
    ).toBeInTheDocument()
    first.unmount()

    const revoked = makeInvitationBackend({
      view: { state: 'revoked' },
    })
    bindRequestFn(revoked.fn)
    renderDemoApp()
    expect(
      await screen.findByText(authEn.acceptInvitation.revoked.title),
    ).toBeInTheDocument()
  })

  it('renders the invalid-link hint for an unknown token', async () => {
    const backend = makeInvitationBackend({
      viewError: { status: 404, code: 'client.http.404' },
    })
    bindRequestFn(backend.fn)
    renderDemoApp()
    expect(
      await screen.findByText(authEn.acceptInvitation.missing.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(authEn.acceptInvitation.missing.hint),
    ).toBeInTheDocument()
  })

  it('shows the proof step on arrival for a signed-in viewer who has not proved the address', async () => {
    const backend = makeInvitationBackend({
      eligibility: () => ({ state: 'pending', email_proved: false }),
    })
    bindRequestFn(backend.fn)
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    expect(
      await screen.findByText(authEn.acceptInvitation.unproved.title),
    ).toBeInTheDocument()
    expect(backend.eligibilityReads).toEqual([TOKEN])
    expect(
      screen.queryByRole('button', { name: authEn.acceptInvitation.accept }),
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
    // The organization's display name is what the viewer sees when it is set.
    expect(
      screen.getByText(
        authEn.acceptInvitation.invitedBy
          .replace('{{inviter}}', 'Ada Lovelace')
          .replace('{{tenant}}', 'Acme'),
      ),
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

  it('falls back to the tenant name when the organization set no display name', async () => {
    const backend = makeInvitationBackend({
      view: VIEW_WITHOUT_DISPLAY_NAME,
    })
    bindRequestFn(backend.fn)
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The display name is free text an organization need not have chosen; the
    // name is always there, and the landing must not address the viewer with
    // an empty organization.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        authEn.acceptInvitation.invitedBy
          .replace('{{inviter}}', 'Ada Lovelace')
          .replace('{{tenant}}', 'acme'),
      ),
    ).toBeInTheDocument()
  })

  it('offers the organization when a member opens an accepted link again', async () => {
    const backend = makeInvitationBackend({
      view: {
        state: 'accepted',
        invitation: { tenant: 'acme', tenant_display_name: 'Acme' },
      },
      selfBefore: () => ({
        tenants: [{ name: 'acme', displayName: 'Acme' }],
      }),
    })
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    expect(
      await screen.findByText(
        authEn.acceptInvitation.member.title.replace('{{tenant}}', 'Acme'),
      ),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: authEn.acceptInvitation.member.open }),
    )
    // The organization became current and the viewer landed in it.
    expect(getCurrentTenant()).toBe('acme')
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
  })

  it('says the accepted link was used when the session is not a member', async () => {
    const backend = makeInvitationBackend({
      view: {
        state: 'accepted',
        invitation: { tenant: 'acme', tenant_display_name: 'Acme' },
      },
      selfBefore: () => ({ tenants: [] }),
    })
    bindRequestFn(backend.fn)
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    expect(
      await screen.findByText(authEn.acceptInvitation.used.title),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: authEn.acceptInvitation.member.open }),
    ).not.toBeInTheDocument()
  })

  it('lands in the invited tenant and resolves its name from the caller tenant read', async () => {
    const backend = makeInvitationBackend({
      selfBefore: () => ({ tenants: [{ name: 'other' }] }),
      selfAfter: () => ({
        tenants: [
          { name: 'other' },
          { name: INVITED_TENANT_NAME },
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

    // The acceptance names the tenant; the display-name source is the caller
    // tenant list, which the landing reads after it and the switcher keeps.
    expect(backend.selfReads).toContain('after')
    expect(getCurrentTenant()).toBe(INVITED_TENANT_NAME)
    expect(getCurrentTenantName()).toBe(INVITED_TENANT_NAME)
  })

  it('navigates home with no current tenant when the name read fails', async () => {
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
    // name read failed, so the tenant stays unknown and tenant-scoped reads
    // stay disabled rather than naming a placeholder.
    expect(backend.selfReads).toContain('after')
    expect(getCurrentTenant()).toBeNull()
    expect(getCurrentTenantName()).toBeNull()
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
      demoEnUS.home.notice.membershipTitle,
    )
    expect(confirmation).toBeInTheDocument()

    // The landing unmounted on the way here, so the confirmation takes the
    // focus: keyboard and screen-reader users otherwise land on the document
    // body with no signal that the page changed.
    await waitFor(() => expect(document.activeElement).not.toBe(document.body))
    expect(document.activeElement).toHaveTextContent(
      demoEnUS.home.notice.membershipTitle,
    )

    // Dismissing it removes the confirmation.
    await user.click(
      screen.getByRole('button', {
        name: demoEnUS.home.notice.dismiss,
      }),
    )
    expect(
      screen.queryByText(demoEnUS.home.notice.membershipTitle),
    ).not.toBeInTheDocument()

    // A later visit to the home page in the same session does not bring it
    // back.
    await user.click(screen.getByTestId('account-entry-trigger'))
    await user.click(
      screen.getByRole('menuitem', {
        name: demoEnUS.account.sections.profile,
      }),
    )
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.profile,
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
      screen.queryByText(demoEnUS.home.notice.membershipTitle),
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
      name: demoEnUS.home.notice.region,
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
        name: demoEnUS.home.notice.region,
      }),
    ).toBe(region)
    expect(region).toHaveTextContent(demoEnUS.home.notice.membershipTitle)
  })

  it('returns a password registration to the invitation after its address is verified', async () => {
    // An anonymous visitor on the invitation URL sees the landing, and its
    // create-account action opens the host's registration surface in place. A
    // password registration must first prove the address on the account page,
    // and the invitation token has to survive that detour: the visitor comes
    // back to the link and can accept it.
    let verified = false
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'org.personal'],
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
            : { state: 'pending', email: VIEW.invitation.email, since: null },
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
      [`GET /api/v1/auth/invitations/${TOKEN}/eligibility`]: () => PROVED,
      [`POST /api/v1/auth/invitations/${TOKEN}`]: () => ({
        account: 'acc-1',
        tenant: INVITED_TENANT_NAME,
        active: true,
        source: 'invite',
        sso_exempt: false,
      }),
      ['GET /api/v1/auth/me/tenants']: () => ({
        tenants: [{ name: INVITED_TENANT_NAME }],
      }),
    })
    const user = userEvent.setup()
    renderDemoApp({ session: harness.session })

    // The anonymous landing's create-account action.
    await user.click(
      await screen.findByRole('button', {
        name: authEn.acceptInvitation.createAccount,
      }),
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
        demoEnUS.home.notice.membershipTitle,
      ),
    ).toBeInTheDocument()
  })

  it('returns an SMS registration made on the invitation URL to its accept page', async () => {
    // The create-account action opens the host's sign-in surface in place, so
    // its register mode must leave the URL alone: the session flip re-renders
    // the landing onto its signed-in surface, with the token still in the
    // path.
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'org.personal'],
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
      [`GET /api/v1/auth/invitations/${TOKEN}/eligibility`]: () => PROVED,
    })
    const user = userEvent.setup()
    renderDemoApp({ session: harness.session })

    // The anonymous landing, then the register mode of the in-place surface.
    await user.click(
      await screen.findByRole('button', {
        name: authEn.acceptInvitation.createAccount,
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

  it('returns an SMS sign-in made on the invitation URL to its accept page', async () => {
    // The ordinary sign-in path: no register entry is picked, and the
    // deployment's SMS method opens an account for a number it does not know.
    // The sign-in lane never takes the register mode's landing, so the
    // invitation URL must survive the session flip.
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'org.personal'],
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
      [`GET /api/v1/auth/invitations/${TOKEN}/eligibility`]: () => PROVED,
    })
    const user = userEvent.setup()
    renderDemoApp({ session: harness.session })

    await user.click(
      await screen.findByRole('button', {
        name: authEn.acceptInvitation.signIn,
      }),
    )
    await user.type(
      await screen.findByLabelText(authEn.smsSignIn.phoneLabel),
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

    await waitFor(() => expect(window.location.pathname).toBe(PATH))
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
  })

  it('moves focus to the landing when the in-place sign-in closes', async () => {
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'org.personal'],
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
      [`GET /api/v1/auth/invitations/${TOKEN}/eligibility`]: () => PROVED,
    })
    const user = userEvent.setup()
    renderDemoApp({ session: harness.session })

    await signInWithSms(user)

    // The in-place surface closes with the control it held, and the landing
    // it re-renders onto takes the focus instead of leaving it on the body.
    await waitFor(() => expect(window.location.pathname).toBe(PATH))
    // The landing's own entry is its heading, not the landmark that stands
    // in while the signed-in read is still pending.
    const heading = await screen.findByRole('heading', {
      level: 1,
      name: authEn.acceptInvitation.title,
    })
    await waitFor(() => expect(heading).toHaveFocus())
  })

  it('lands on the main landmark first and moves to the heading when the read answers', async () => {
    const held = heldInvitationRead()
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'org.personal'],
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
      [`GET /api/v1/auth/invitations/${TOKEN}`]: held.handler,
      [`GET /api/v1/auth/invitations/${TOKEN}/eligibility`]: () => PROVED,
    })
    const user = userEvent.setup()
    renderDemoApp({ session: harness.session })
    await screen.findByRole('heading', {
      level: 1,
      name: authEn.acceptInvitation.title,
    })
    // Only the read the close issues is held: the anonymous landing's own
    // read has already answered.
    held.hold()

    await signInWithSms(user)

    // The heading does not exist until the read answers, so the landing takes
    // the focus on its landmark first.
    const main = screen.getByRole('main')
    await waitFor(() => expect(main).toHaveFocus())
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      }),
    ).toBeNull()

    await act(async () => {
      held.release()
    })
    const heading = await screen.findByRole('heading', {
      level: 1,
      name: authEn.acceptInvitation.title,
    })
    // The heading is the landing's own entry: the focus the landmark stood in
    // for moves on to it once it arrives, without the operator moving it.
    await waitFor(() => expect(heading).toHaveFocus())
  })

  it('keeps waiting for a visible heading when a hidden one appears first', async () => {
    const held = heldInvitationRead()
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'org.personal'],
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
      [`GET /api/v1/auth/invitations/${TOKEN}`]: held.handler,
      [`GET /api/v1/auth/invitations/${TOKEN}/eligibility`]: () => PROVED,
    })
    const user = userEvent.setup()
    renderDemoApp({ session: harness.session })
    await screen.findByRole('heading', {
      level: 1,
      name: authEn.acceptInvitation.title,
    })
    // Only the read the close issues is held: the anonymous landing's own
    // read has already answered.
    held.hold()

    await signInWithSms(user)

    const main = screen.getByRole('main')
    await waitFor(() => expect(main).toHaveFocus())

    // A heading the landing cannot show does not complete the hand-off: the
    // page entry is the visible heading the read supplies afterwards.
    await act(async () => {
      const hidden = document.createElement('h1')
      hidden.setAttribute('hidden', '')
      hidden.textContent = 'Hidden heading'
      main.prepend(hidden)
    })
    expect(document.activeElement).toBe(main)

    await act(async () => {
      held.release()
    })
    const heading = await screen.findByRole('heading', {
      level: 1,
      name: authEn.acceptInvitation.title,
    })
    await waitFor(() => expect(heading).toHaveFocus())
  })

  it('lands on the invitation heading when the way back closes the sign-in', async () => {
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'org.personal'],
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
      [`GET /api/v1/auth/invitations/${TOKEN}/eligibility`]: () => PROVED,
    })
    const user = userEvent.setup()
    renderDemoApp({ session: harness.session })

    await user.click(
      await screen.findByRole('button', {
        name: authEn.acceptInvitation.signIn,
      }),
    )
    await user.click(
      await screen.findByRole('button', { name: demoEnUS.signIn.back }),
    )

    // The way back closes the same in-place surface; the landing it returns
    // to is the invitation heading, not the document body.
    const heading = await screen.findByRole('heading', {
      level: 1,
      name: authEn.acceptInvitation.title,
    })
    await waitFor(() => expect(heading).toHaveFocus())
  })

  it('leaves focus where the operator moved it while the way back re-reads', async () => {
    const held = heldInvitationRead()
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'org.personal'],
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
      [`GET /api/v1/auth/invitations/${TOKEN}`]: held.handler,
      [`GET /api/v1/auth/invitations/${TOKEN}/eligibility`]: () => PROVED,
    })
    const user = userEvent.setup()
    renderDemoApp({ session: harness.session })
    await screen.findByRole('heading', {
      level: 1,
      name: authEn.acceptInvitation.title,
    })
    // Only the read the way back issues is held: the anonymous landing's own
    // read has already answered.
    held.hold()

    await user.click(
      await screen.findByRole('button', {
        name: authEn.acceptInvitation.signIn,
      }),
    )
    await user.click(
      await screen.findByRole('button', { name: demoEnUS.signIn.back }),
    )
    const main = screen.getByRole('main')
    await waitFor(() => expect(main).toHaveFocus())

    // The operator moves on before the re-read supplies the heading; that
    // place belongs to the operator, not to the heading's arrival.
    const elsewhere = document.createElement('button')
    document.body.appendChild(elsewhere)
    try {
      elsewhere.focus()
      expect(elsewhere).toHaveFocus()

      await act(async () => {
        held.release()
      })
      const heading = await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      })
      expect(elsewhere).toHaveFocus()
      expect(heading).not.toHaveFocus()
    } finally {
      elsewhere.remove()
      held.release()
    }
  })

  it('leaves focus where the operator moved it while the in-place sign-in closes', async () => {
    let completionStarted = false
    let releaseCompletion: (() => void) | undefined
    const held = new Promise<void>((resolve) => {
      releaseCompletion = resolve
    })
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['authn.invitations', 'org.personal'],
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
      [LOGIN_ANSWER]: async (call) => {
        if ((call.options?.body as { step?: string }).step === 'complete') {
          completionStarted = true
          await held
          return authenticatedCompletion()
        }
        return completeFlow(2)
      },
      [PROFILE_GET]: () => principal(),
      [`GET /api/v1/auth/invitations/${TOKEN}`]: () => VIEW,
      [`GET /api/v1/auth/invitations/${TOKEN}/eligibility`]: () => PROVED,
    })
    const user = userEvent.setup()
    renderDemoApp({ session: harness.session })

    // A connected control the operator moves to while the flow completes: the
    // swap must leave it alone.
    const elsewhere = document.createElement('button')
    document.body.appendChild(elsewhere)
    try {
      await signInWithSms(user)
      await waitFor(() => expect(completionStarted).toBe(true))
      elsewhere.focus()
      expect(elsewhere).toHaveFocus()

      await act(async () => {
        releaseCompletion?.()
      })
      await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      })
      expect(window.location.pathname).toBe(PATH)
      expect(elsewhere).toHaveFocus()
      expect(screen.getByRole('main')).not.toHaveFocus()
    } finally {
      elsewhere.remove()
      releaseCompletion?.()
    }
  })
})
