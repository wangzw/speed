/**
 * The host's sign-in surface.
 *
 * The suite renders an anonymous viewer: the surface shows the deployment's
 * methods and carries no deployment-operation guidance. A redirect method's
 * flow hands the host an authorization URL, and the host navigates the
 * current window to it, returning to the app's callback route.
 *
 * It also carries the host half of registration: a registration that
 * completes through SMS lands on the account setup page, because the account
 * exists but has no login name or password yet.
 */

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { accountUiResources } from '@speed/account-ui'
import { authUiResources } from '@speed/auth-ui'
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
import { ACCOUNT_PATH, ACCOUNT_SETUP_PATH } from '../paths.js'
import { renderDemoApp } from '../test-utils/render.js'
import { createTestSession } from '../test-utils/session.js'
import { navigateToAuthorizationUrl } from '../sign-in-navigation.js'

vi.mock('../sign-in-navigation.js', () => ({
  navigateToAuthorizationUrl: vi.fn(),
}))

const AUTHORIZE_URL = 'https://github.com/login/oauth/authorize?client_id=review'

const METHODS: AuthnMethodList = {
  methods: [
    { name: 'password', kind: 'form' },
    { name: 'github', kind: 'redirect', protocol: 'oauth2', label: 'GitHub' },
  ],
  signup: { enabled: false, password: { enabled: false } },
  mfa: { factors: [] },
}

/** A session whose discovery names a redirect method and whose flow start
 * answers that method's authorization URL. */
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

/**
 * The auth-ui bundle the screen renders from, read as test data: the register
 * entry and the SMS form's field labels. The host test drives the package's
 * real bundle, so a renamed label fails here rather than in a selector.
 */
const authEn = authUiResources['en-US'] as unknown as {
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

/** The auth-ui password-registration labels, read from the bundle the
 * screen renders (both languages are pinned in that package's own suite). */
const authRegisterEn = authUiResources['en-US'] as unknown as {
  readonly register: {
    readonly usernameLabel: string
    readonly emailLabel: string
    readonly passwordLabel: string
    readonly confirmPasswordLabel: string
    readonly submit: string
  }
}

const REGISTER_PATH = '/api/v1/auth/register'
const REGISTER = `POST ${REGISTER_PATH}`

/** The account-ui explanation the setup page renders while the account has
 * no login name (PasswordSection owns that copy now). */
const accountEn = accountUiResources['en-US'] as unknown as {
  readonly password: { readonly loginNameRequired: string }
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  vi.mocked(navigateToAuthorizationUrl).mockClear()
})

describe('the sign-in surface', () => {
  it('renders the sign-in channels without deployment-operation guidance', async () => {
    const { container } = renderDemoApp()

    expect(
      await screen.findByLabelText(authEn.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
    // The end-user surface states nothing about how the deployment is
    // operated: no seeding instruction, no repository-document pointer.
    expect(container.textContent).not.toMatch(/seed|AGENTS\.md/)
  })

  it('follows the register / sign-in switch in the page heading', async () => {
    // A deployment whose password channel opens an account: the screen offers
    // its register entry, and the host page heading has to follow the switch
    // it reports.
    const session: AuthSession = Object.assign(createTestSession(), {
      listMethods: (): Promise<AuthnMethodList> =>
        Promise.resolve({
          methods: [{ name: 'password', kind: 'form', opens_account: true }],
          signup: { enabled: true, password: { enabled: false } },
          mfa: { factors: [] },
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

    // The screen's own way back reports the other mode the same way.
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
    // The provider's round trip returns to the app's callback route.
    expect(startFlow).toHaveBeenCalledWith({
      method: 'github',
      binding: 'cookie',
      return_to: '/sign-in/callback',
    })
  })

  it('lands a registration completed through SMS on the account setup page', async () => {
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
    // The register entry's own "sign in" toggle shares the submit's label,
    // so the submit is read inside the form.
    const form = screen
      .getByLabelText(authEn.smsSignIn.codeLabel)
      .closest('form') as HTMLFormElement
    await user.click(
      within(form).getByRole('button', { name: authEn.smsSignIn.submit }),
    )

    // The screen reported register + sms, so the host moved the just-opened
    // account onto the setup page. Its profile lists no login name, so the
    // password section explains what to add first.
    await waitFor(() =>
      expect(window.location.pathname).toBe(ACCOUNT_SETUP_PATH),
    )
    expect(
      await screen.findByText(accountEn.password.loginNameRequired),
    ).toBeInTheDocument()
  })

  it('lands a password registration on the verification page', async () => {
    // A password registration opens the account without proving its email
    // address, then signs in with the same credentials. The host sends the
    // just-opened account to the verification page whatever the deployment's
    // personal-tenant policy is.
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
      expect(window.location.pathname).toBe(ACCOUNT_PATH)
      expect(window.location.search).toBe('?verify=1')
    })
    expect(
      harness.calls.some(
        (call) => call.method === 'POST' && call.path === REGISTER_PATH,
      ),
    ).toBe(true)
  })

  it('lands a registration in the tenant area when the personal tenant is in force', async () => {
    // Start away from the root: the wait below can then only succeed when
    // the completion actually navigated to the tenant home, not because the
    // page happened to stay where it was.
    window.history.replaceState({}, '', ACCOUNT_PATH)
    const harness = makeHarness({
      // The deployment serves workspace.personal: the register mode's
      // landing is the tenant home rather than the setup page.
      [FEATURES_GET]: () => ({ features: ['workspace.personal'] }),
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

    // The registration landed on the tenant home rather than the setup page.
    // Whether the home page offers the setup notice is a question the ensure
    // answer and the profile settle, not the register mode -- the case below
    // drives it through an ordinary sign-in.
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      await screen.findByRole('heading', { level: 1, name: demoEnUS.home.heading }),
    ).toBeInTheDocument()
  })

  it('lands a registration on the setup page when the feature document failed', async () => {
    // A failed feature read is neither "personal tenant served" nor "absent":
    // the register mode must fall back to the setup page, and the setup page
    // still renders because it needs no feature.
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
      expect(window.location.pathname).toBe(ACCOUNT_SETUP_PATH),
    )
  })

  it('stays on the console path when a registration completes in its session gate', async () => {
    // The console area's session gate embeds this surface, so the console URL
    // is the viewer's destination; the register mode's landing belongs to the
    // application's own sign-in page.
    window.history.replaceState({}, '', '/console')
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['tenant.admin', 'workspace.personal'],
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
    expect(window.location.pathname).toBe('/console')
  })

  it('offers the setup notice to a new phone opened by a plain SMS sign-in', async () => {
    // No register entry is clicked: this is the ordinary sign-in form. The
    // deployment's SMS method opens an account for a number it does not know,
    // so the completion creates the account implicitly. The ensure answer is
    // what says the personal tenant was just created, and GET /api/v1/auth/me shows
    // no login name and no password -- the two facts the notice is made of.
    const harness = makeHarness({
      [FEATURES_GET]: () => ({
        features: ['tenant.admin', 'workspace.personal'],
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
      [PROFILE_GET]: () =>
        principal({
          password_set: false,
          identifiers: [
            {
              id: 'identifier-phone',
              kind: 'phone',
              value: '+14155550123',
              proven: true,
              verified_at: '2026-01-01T00:00:00Z',
              verified_via: 'sms-code',
            },
          ],
        }),
      // An ordinary member cannot read the tenant roster; the frame's failed
      // read leaves no current tenant, which is what lets the ensure run.
      'GET /api/v1/system/tenants': () => {
        throw Object.assign(new Error('the roster read was refused'), {
          status: 403,
        })
      },
      'POST /api/v1/workspace/personal': () => ({
        tenant: {
          id: 'personal-account-1',
          alias: 'personal-account-1',
          kind: 'personal',
        },
        created: true,
      }),
    })
    renderDemoApp({ session: harness.session })

    const user = userEvent.setup()
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

    expect(
      await screen.findByText(demoEnUS.home.setupNotice.title),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: demoEnUS.home.setupNotice.action }),
    ).toHaveAttribute('href', ACCOUNT_SETUP_PATH)
  })
})
