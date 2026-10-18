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
import { authUiResources } from '@speed/auth-ui'
import type { AuthnFlowState, AuthnMethodList, AuthSession } from '@speed/auth-core'
import {
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
} from '@speed/test-utils/session-harness'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import demoZhCN from '../locales/zh-CN.json' with { type: 'json' }
import { ACCOUNT_SETUP_PATH } from '../paths.js'
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
  signup: { enabled: false },
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
  readonly mode: { readonly registerEntry: string }
  readonly smsSignIn: {
    readonly phoneLabel: string
    readonly sendCode: string
    readonly codeLabel: string
    readonly submit: string
  }
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  vi.mocked(navigateToAuthorizationUrl).mockClear()
})

describe('the sign-in surface', () => {
  it('renders the sign-in channels without deployment-operation guidance', async () => {
    const { container } = renderDemoApp()

    expect(
      await screen.findByLabelText('Email, phone, or username'),
    ).toBeInTheDocument()
    // The end-user surface states nothing about how the deployment is
    // operated: no seeding instruction, no repository-document pointer.
    expect(container.textContent).not.toMatch(/seed|AGENTS\.md/)
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
      await screen.findByText(demoEnUS.accountSetup.password.loginNameRequired),
    ).toBeInTheDocument()
  })
})
