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

const accountEn = accountUiResources['en-US'] as unknown as {
  readonly verification: {
    readonly codeLabel: string
    readonly verify: string
  }
}

const REGISTER = 'POST /api/v1/auth/register'

const TOKEN = 'tok-123'
const PATH = `${INVITATION_PREFIX}/${TOKEN}`

const INVITED_TENANT_NAME = 'acme-renamed'

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

const VIEW_WITH_ROLE = {
  state: 'pending',
  invitation: {
    ...VIEW.invitation,
    role: 'tenant-admin',
  },
}

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

const PROVED = { state: 'pending', email_proved: true }

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
    const backend = makeInvitationBackend()
    bindRequestFn(backend.fn)
    renderDemoApp()

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
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
          .replace('{{tenant}}', 'Acme'),
      ),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: authEn.acceptInvitation.accept }),
    )
    await waitFor(() => {
      expect(backend.accepted).toEqual([TOKEN])
    })
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

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
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

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    const confirmation = await within(screen.getByRole('main')).findByText(
      demoEnUS.home.notice.membershipTitle,
    )
    expect(confirmation).toBeInTheDocument()

    await waitFor(() => expect(document.activeElement).not.toBe(document.body))
    expect(document.activeElement).toHaveTextContent(
      demoEnUS.home.notice.membershipTitle,
    )

    await user.click(
      screen.getByRole('button', {
        name: demoEnUS.home.notice.dismiss,
      }),
    )
    expect(
      screen.queryByText(demoEnUS.home.notice.membershipTitle),
    ).not.toBeInTheDocument()

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

    expect(
      screen.getByRole('status', {
        name: demoEnUS.home.notice.region,
      }),
    ).toBe(region)
    expect(region).toHaveTextContent(demoEnUS.home.notice.membershipTitle)
  })

  it('returns a password registration to the invitation after its address is verified', async () => {
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

    await waitFor(() => expect(window.location.pathname).toBe('/account'))
    const codeField = await screen.findByLabelText(
      accountEn.verification.codeLabel,
    )
    await user.type(codeField, '123456')
    await user.click(
      screen.getByRole('button', { name: accountEn.verification.verify }),
    )

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

    await waitFor(() => expect(window.location.pathname).toBe(PATH))
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: authEn.acceptInvitation.title,
      }),
    ).toBeInTheDocument()
  })

  it('returns an SMS sign-in made on the invitation URL to its accept page', async () => {
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

    await waitFor(() => expect(window.location.pathname).toBe(PATH))
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
    held.hold()

    await signInWithSms(user)

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
    held.hold()

    await signInWithSms(user)

    const main = screen.getByRole('main')
    await waitFor(() => expect(main).toHaveFocus())

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
