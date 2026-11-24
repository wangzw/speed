import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { accountUiResources } from '@speed/account-ui'
import { authUiResources } from '@speed/auth-ui'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn } from '@speed/api-client'
import type { AuthnFlowState, AuthnMethodList, AuthSession } from '@speed/auth-core'
import {
  LOGIN_ANSWER,
  LOGIN_METHODS,
  LOGIN_START,
  PROFILE_GET,
  authenticatedCompletion,
  completeFlow,
  FEATURES_GET,
  makeHarness,
  methodList,
  openFlow,
  principal,
} from '../test-utils/session-harness.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { pendingInvitationLanding } from '../invitation-return.js'
import {
  ACCOUNT_PATH,
  ACCOUNT_SECTION_PATHS,
  INVITATION_PREFIX,
  PASSWORD_RESET_PATH,
} from '../paths.js'
import { renderDemoApp } from '../test-utils/render.js'
import { createTestSession } from '../test-utils/session.js'
import { navigateToAuthorizationUrl } from '../sign-in-navigation.js'

vi.mock('../sign-in-navigation.js', () => ({
  navigateToAuthorizationUrl: vi.fn(),
}))

const AUTHORIZE_URL = 'https://github.com/login/oauth/authorize?client_id=demo'

const METHODS: AuthnMethodList = {
  methods: [
    { name: 'password', kind: 'form' },
    { name: 'github', kind: 'redirect', protocol: 'oauth2', label: 'GitHub' },
  ],
  signup: { enabled: false, password: { enabled: false } },
  mfa: { factors: [] },
  human: {
    phone: { required: false, provider: 'off' },
    other: { required: false, provider: 'off' },
  },
  password_reset: { enabled: false, methods: [] },
}

function redirectSession(): { session: AuthSession; startFlow: ReturnType<typeof vi.fn> } {
  const startFlow = vi.fn(
    async (): Promise<AuthnFlowState> => ({
      seq: 1,
      state: 'first',
      next: { kind: 'redirect', url: AUTHORIZE_URL, method: 'GET' },
    }),
  )
  const session: AuthSession = Object.assign(createTestSession(), {
    listMethods: () => Promise.resolve(METHODS),
    startFlow,
  })
  return { session, startFlow }
}

const authEn = authUiResources['en-US'] as unknown as {
  readonly acceptInvitation: { readonly signIn: string }
  readonly mode: {
    readonly registerEntry: string
    readonly signInEntry: string
  }
  readonly passwordSignIn: { readonly identifierLabel: string }
  readonly smsSignIn: {
    readonly phoneLabel: string
    readonly sendCode: string
    readonly codeLabel: string
    readonly submit: string
  }
}

const authRegisterEn = authUiResources['en-US'] as unknown as {
  readonly register: {
    readonly usernameLabel: string
    readonly emailLabel: string
    readonly passwordLabel: string
    readonly confirmPasswordLabel: string
    readonly submit: string
  }
}

const authLandingEn = authUiResources['en-US'] as unknown as {
  readonly errors: { readonly authn: { readonly link_required: string } }
  readonly social: { readonly linkRequiredEmail: string }
}

const authResetEn = authUiResources['en-US'] as unknown as {
  readonly passwordSignIn: { readonly forgotPassword: string }
  readonly passwordReset: {
    readonly title: string
    readonly emailLabel: string
  }
}

const REGISTER_PATH = '/api/v1/auth/register'
const REGISTER = `POST ${REGISTER_PATH}`

const accountEn = accountUiResources['en-US'] as unknown as {
  readonly verification: {
    readonly codeLabel: string
    readonly verify: string
    readonly verifiedDescription: string
  }
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  window.sessionStorage.clear()
  vi.mocked(navigateToAuthorizationUrl).mockClear()
})

describe('the sign-in surface', () => {
  it('renders the sign-in channels without deployment-operation guidance', async () => {
    const { container } = renderDemoApp()

    expect(
      await screen.findByLabelText(authEn.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
    expect(container.textContent).not.toMatch(/seed|AGENTS\.md/)
  })

  it('offers the forgot-password entry where reset is published and opens the reset page', async () => {
    const session: AuthSession = Object.assign(createTestSession(), {
      listMethods: (): Promise<AuthnMethodList> =>
        Promise.resolve({
          methods: [{ name: 'password', kind: 'form' }],
          signup: { enabled: false, password: { enabled: false } },
          mfa: { factors: [] },
          human: {
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          },
          password_reset: { enabled: true, methods: ['email'] },
        }),
    })
    renderDemoApp({ session })
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: authResetEn.passwordSignIn.forgotPassword,
      }),
    )

    await waitFor(() =>
      expect(window.location.pathname).toBe(PASSWORD_RESET_PATH),
    )
    expect(
      await screen.findByRole('heading', { name: authResetEn.passwordReset.title }),
    ).toBeInTheDocument()
    expect(
      await screen.findByRole('textbox', {
        name: authResetEn.passwordReset.emailLabel,
      }),
    ).toBeInTheDocument()
  })

  it('offers no forgot-password entry where the deployment publishes none', async () => {
    renderDemoApp()
    await screen.findByLabelText(authEn.passwordSignIn.identifierLabel)
    expect(
      screen.queryByRole('button', {
        name: authResetEn.passwordSignIn.forgotPassword,
      }),
    ).toBeNull()
  })

  it('follows the register / sign-in switch in the page heading', async () => {
    const session: AuthSession = Object.assign(createTestSession(), {
      listMethods: (): Promise<AuthnMethodList> =>
        Promise.resolve({
          methods: [{ name: 'password', kind: 'form', opens_account: true }],
          signup: { enabled: true, password: { enabled: false } },
          mfa: { factors: [] },
          human: {
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          },
          password_reset: { enabled: false, methods: [] },
        }),
    })
    renderDemoApp({ session })
    const user = userEvent.setup()

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: authEn.mode.registerEntry }),
    )
    expect(
      screen.getByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.registerHeading,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(demoEnUS.signIn.registerSubtitle),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: authEn.mode.signInEntry }),
    )
    expect(
      screen.getByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).toBeInTheDocument()
    expect(screen.getByText(demoEnUS.signIn.subtitle)).toBeInTheDocument()
  })

  it('navigates the current window to a redirect method authorization URL', async () => {
    const { session, startFlow } = redirectSession()
    renderDemoApp({ session })

    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'GitHub' }))

    await waitFor(() =>
      expect(navigateToAuthorizationUrl).toHaveBeenCalledWith(AUTHORIZE_URL),
    )
    expect(startFlow).toHaveBeenCalledWith({
      method: 'github',
      binding: 'cookie',
      return_to: '/sign-in/callback',
    })
  })

  it('remembers an invitation landing before leaving for a provider', async () => {
    const landing = `${INVITATION_PREFIX}/tok-invite`
    window.history.replaceState({}, '', landing)
    bindRequestFn((async (path: string) => {
      if (path === '/api/v1/auth/invitations/tok-invite') {
        return {
          state: 'pending',
          invitation: {
            tenant: 'acme',
            tenant_display_name: 'Acme',
            invited_by_name: 'Ada Lovelace',
            email: 'i***@demo.test',
            expires_at: new Date(
              Date.now() + 24 * 60 * 60 * 1000,
            ).toISOString(),
          },
        }
      }
      return undefined
    }) as RequestFn)
    const { session } = redirectSession()
    renderDemoApp({ session })

    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', {
        name: authEn.acceptInvitation.signIn,
      }),
    )
    await user.click(await screen.findByRole('button', { name: 'GitHub' }))

    await waitFor(() =>
      expect(navigateToAuthorizationUrl).toHaveBeenCalledWith(AUTHORIZE_URL),
    )
    expect(pendingInvitationLanding()).toBe(landing)
  })

  it('lands a registration completed through SMS on the account profile page', async () => {
    const harness = makeHarness({
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
    })
    renderDemoApp({ session: harness.session })

    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: authEn.mode.registerEntry }),
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

    await waitFor(() =>
      expect(window.location.pathname).toBe(ACCOUNT_SECTION_PATHS.profile),
    )
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.profile,
      }),
    ).toBeInTheDocument()
  })

  it('lands a password registration on the verification page', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([{ name: 'password', kind: 'form' }], {
          signup: true,
          signupPassword: true,
        }),
      [REGISTER]: () => ({
        state: 'registered',
        account_id: 'account-1',
        verification: { state: 'pending', email: 'ada@example.com' },
      }),
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    renderDemoApp({ session: harness.session })

    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: authEn.mode.registerEntry }),
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.usernameLabel),
      'ada',
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.emailLabel),
      'ada@example.com',
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.passwordLabel),
      's3cret-pass',
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.confirmPasswordLabel),
      's3cret-pass',
    )
    await user.click(
      screen.getByRole('button', { name: authRegisterEn.register.submit }),
    )

    await waitFor(() => {
      expect(window.location.pathname).toBe(ACCOUNT_SECTION_PATHS.profile)
      expect(window.location.search).toBe('?verify=1')
    })
    expect(
      harness.calls.some(
        (call) => call.method === 'POST' && call.path === REGISTER_PATH,
      ),
    ).toBe(true)
  })

  it('keeps a registration made away from an invitation on the account page after verification', async () => {
    let verified = false
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([{ name: 'password', kind: 'form' }], {
          signup: true,
          signupPassword: true,
        }),
      [REGISTER]: () => ({
        state: 'registered',
        account_id: 'account-1',
        verification: { state: 'pending', email: 'ada@example.com' },
      }),
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () =>
        principal({
          verification: verified
            ? {
                state: 'verified',
                email: 'ada@example.com',
                since: '2026-10-25T00:00:00Z',
              }
            : { state: 'pending', email: 'ada@example.com', since: null },
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
    })
    renderDemoApp({ session: harness.session })

    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: authEn.mode.registerEntry }),
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.usernameLabel),
      'ada',
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.emailLabel),
      'ada@example.com',
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.passwordLabel),
      's3cret-pass',
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.confirmPasswordLabel),
      's3cret-pass',
    )
    await user.click(
      screen.getByRole('button', { name: authRegisterEn.register.submit }),
    )

    await waitFor(() =>
      expect(window.location.pathname).toBe(ACCOUNT_SECTION_PATHS.profile),
    )
    await user.type(
      await screen.findByLabelText(accountEn.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: accountEn.verification.verify }),
    )

    expect(
      await screen.findByText(
        accountEn.verification.verifiedDescription.replace(
          '{{email}}',
          'ada@example.com',
        ),
      ),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe(ACCOUNT_SECTION_PATHS.profile)
  })

  it('lands a registration in the tenant area when the personal tenant is in force', async () => {
    window.history.replaceState({}, '', ACCOUNT_PATH)
    const harness = makeHarness({
      [FEATURES_GET]: () => ({ features: ['org.personal'] }),
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
    })
    renderDemoApp({ session: harness.session })

    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: authEn.mode.registerEntry }),
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

    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      await screen.findByRole('heading', { level: 1, name: demoEnUS.home.heading }),
    ).toBeInTheDocument()
  })

  it('lands a registration on the account page when the feature document failed', async () => {
    const harness = makeHarness({
      [FEATURES_GET]: () => {
        throw Object.assign(new Error('the feature read failed'), {
          status: 503,
        })
      },
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
    })
    renderDemoApp({ session: harness.session })

    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: authEn.mode.registerEntry }),
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

    await waitFor(() =>
      expect(window.location.pathname).toBe(ACCOUNT_SECTION_PATHS.profile),
    )
  })

  it('stays on the admin path when a registration completes in its session gate', async () => {
    window.history.replaceState({}, '', '/admin')
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['tenant.admin', 'org.personal'],
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
    })
    renderDemoApp({ session: harness.session })

    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: authEn.mode.registerEntry }),
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

    await waitFor(() =>
      expect(
        screen.queryByRole('heading', {
          level: 1,
          name: demoEnUS.signIn.heading,
        }),
      ).toBeNull(),
    )
    expect(window.location.pathname).toBe('/admin')
  })

  it('takes a password registration from the admin session gate to the verification page', async () => {
    window.history.replaceState({}, '', '/admin')
    const harness = makeHarness({
      [FEATURES_GET]: () => ({ features: ['tenant.admin'] }),
      [LOGIN_METHODS]: () =>
        methodList([{ name: 'password', kind: 'form' }], {
          signup: true,
          signupPassword: true,
        }),
      [REGISTER]: () => ({
        state: 'registered',
        account_id: 'account-1',
        verification: { state: 'pending', email: 'ada@example.com' },
      }),
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    renderDemoApp({ session: harness.session })

    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: authEn.mode.registerEntry }),
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.usernameLabel),
      'ada',
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.emailLabel),
      'ada@example.com',
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.passwordLabel),
      's3cret-pass',
    )
    await user.type(
      screen.getByLabelText(authRegisterEn.register.confirmPasswordLabel),
      's3cret-pass',
    )
    await user.click(
      screen.getByRole('button', { name: authRegisterEn.register.submit }),
    )

    await waitFor(() => {
      expect(window.location.pathname).toBe(ACCOUNT_SECTION_PATHS.profile)
      expect(window.location.search).toBe('?verify=1')
    })
    expect(pendingInvitationLanding()).toBeNull()
  })

  it('lands a refused callback with the refusal and its masked address', async () => {
    window.history.replaceState(
      {},
      '',
      '/#authn_error=link-required&authn_email=a***@home.org',
    )
    renderDemoApp()

    expect(
      await screen.findByText(authLandingEn.errors.authn.link_required, {
        exact: true,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        authLandingEn.social.linkRequiredEmail.replace(
          '{{email}}',
          'a***@home.org',
        ),
      ),
    ).toBeInTheDocument()
  })

  it('does not show an address a fragment pairs with another code', async () => {
    window.history.replaceState(
      {},
      '',
      '/#authn_error=flow-invalid&authn_email=a***@home.org',
    )
    renderDemoApp()

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    const prefix = authLandingEn.social.linkRequiredEmail.slice(
      0,
      authLandingEn.social.linkRequiredEmail.indexOf('{{email}}'),
    )
    expect(screen.queryByText(prefix, { exact: false })).toBeNull()
  })
})
