import { describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import { flushSync } from 'react-dom'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthSession, AuthnMethodList } from '@speed/auth-core'
import { SignInScreen } from './SignInScreen.js'
import type { SignInMode } from './SignInScreen.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  LOGIN_ANSWER,
  LOGIN_METHODS,
  LOGIN_START,
  PROFILE_GET,
  SSO_DISCOVER,
  apiError,
  authenticatedCompletion,
  completeFlow,
  makeHarness,
  methodList,
  openFlow,
  principal,
  ssoProviders,
} from '@speed/test-utils/session-harness'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { waitForCondition } from '@speed/test-utils/wait'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const PASS = 's3cret-pass'
const MAIL = 'alice@example.com'

async function submitPasswordRegistration(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  await user.type(screen.getByLabelText(zhCN.register.usernameLabel), 'ada')
  await user.type(screen.getByLabelText(zhCN.register.emailLabel), MAIL)
  await user.type(screen.getByLabelText(zhCN.register.passwordLabel), PASS)
  await user.type(
    screen.getByLabelText(zhCN.register.confirmPasswordLabel),
    PASS,
  )
  await user.click(screen.getByRole('button', { name: zhCN.register.submit }))
}

function ControlledModeHost({
  session,
  discovery,
  control,
}: {
  readonly session: AuthSession
  readonly discovery: AuthnMethodList
  readonly control: { hold: boolean }
}) {
  const [mode, setMode] = useState<SignInMode>('register')
  return (
    <SignInScreen
      session={session}
      discovery={discovery}
      mode={mode}
      onModeChange={(next) => {
        if (!control.hold) {
          setMode(next)
        }
      }}
    />
  )
}

const BOTH_FORMS = methodList([
  { name: 'password', kind: 'form' },
  { name: 'sms', kind: 'form' },
])

const PASSKEY_ONLY = methodList([
  { name: 'passkey', kind: 'webauthn', label: 'Passkey' },
])

function stubCredentials(value: unknown): () => void {
  const original = Object.getOwnPropertyDescriptor(window.navigator, 'credentials')
  Object.defineProperty(window.navigator, 'credentials', {
    value,
    configurable: true,
  })
  return () => {
    if (original === undefined) {
      delete (window.navigator as { credentials?: unknown }).credentials
    } else {
      Object.defineProperty(window.navigator, 'credentials', original)
    }
  }
}

function assertionCredential(): PublicKeyCredential {
  return {
    id: 'AQID',
    rawId: new Uint8Array([1, 2, 3]).buffer,
    type: 'public-key',
    response: {
      clientDataJSON: new Uint8Array([4]).buffer,
      authenticatorData: new Uint8Array([5]).buffer,
      signature: new Uint8Array([6]).buffer,
      userHandle: null,
    },
    getClientExtensionResults: () => ({}),
    authenticatorAttachment: 'platform',
  } as unknown as PublicKeyCredential
}

function makePasskeyHarness() {
  return makeHarness({
    [LOGIN_START]: () =>
      openFlow({
        seq: 1,
        state: 'first',
        next: { kind: 'webauthn', options: { challenge: 'abc', rpId: 'x' } },
      }),
  })
}

function makeSignInHarness() {
  return makeHarness({
    [LOGIN_START]: (call) => {
      const method = (call.options?.body as { method?: string }).method
      return method === 'sms'
        ? openFlow({
            seq: 1,
            state: 'first',
            next: {
              kind: 'challenge',
              fields: [
                { name: 'code', kind: 'code', required: true, secret: true },
              ],
            },
          })
        : completeFlow(1)
    },
    [LOGIN_ANSWER]: (call) => {
      const step = (call.options?.body as { step?: string }).step
      return step === 'complete' ? authenticatedCompletion() : completeFlow(2)
    },
    [PROFILE_GET]: () => principal(),
  })
}

describe('SignInScreen', () => {
  it('render the discovery-driven form channels behind a tab strip', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen session={harness.session} discovery={BOTH_FORMS} />,
    )
    expect(
      screen.getByRole('tab', { name: zhCN.passwordSignIn.title }),
    ).toHaveAttribute('aria-selected', 'true')
    expect(
      screen.getByRole('tab', { name: zhCN.smsSignIn.title }),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
    expect(harness.calls).toHaveLength(0)
  })

  it('render the code a failed social callback landed with', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={BOTH_FORMS}
        initialError="authn.link_required"
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.link_required,
    )
  })

  it('render the masked conflicting address beside a link-required error', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={BOTH_FORMS}
        initialError="authn.link_required"
        initialErrorEmail="a***@home.org"
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.social.linkRequiredEmail.replace('{{email}}', 'a***@home.org'),
    )
  })

  it('render no address line when no address is set', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={BOTH_FORMS}
        initialError="authn.link_required"
      />,
    )
    const copy = zhCN.social.linkRequiredEmail
    const prefix = copy.slice(0, copy.indexOf('{{email}}'))
    expect(screen.queryByText(prefix, { exact: false })).toBeNull()
  })

  it('render no address line for a code that never names one', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={BOTH_FORMS}
        initialError="authn.login_failed"
        initialErrorEmail="a***@home.org"
      />,
    )
    const copy = zhCN.social.linkRequiredEmail
    const prefix = copy.slice(0, copy.indexOf('{{email}}'))
    expect(screen.queryByText(prefix, { exact: false })).toBeNull()
  })

  it('fall back to the generic message for a code it does not know', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={BOTH_FORMS}
        initialError="not-a-code-this-surface-knows"
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(zhCN.errors.unknown)
  })

  it('drop the host landing refusal when another channel is chosen', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={BOTH_FORMS}
        initialError="authn.link_required"
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.link_required,
    )

    const user = userEvent.setup()
    await user.click(screen.getByRole('tab', { name: zhCN.smsSignIn.title }))
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('drop the host landing refusal when another mode is chosen', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'sms', kind: 'form', opens_account: true }],
          { signup: true },
        )}
        initialError="authn.link_required"
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.link_required,
    )

    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    expect(
      screen.getByText(zhCN.mode.registerTitle),
    ).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('drop the host landing refusal when a new attempt starts', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => {
        throw apiError(503, 'authn.unavailable')
      },
    })
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={BOTH_FORMS}
        initialError="authn.link_required"
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      MAIL,
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      PASS,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordSignIn.submit }),
    )

    await waitForCondition(() => {
      const alerts = screen.getAllByRole('alert')
      expect(alerts).toHaveLength(1)
      expect(alerts[0]).toHaveTextContent(zhCN.errors.authn.unavailable)
    })
    expect(
      screen.queryByText(zhCN.errors.authn.link_required),
    ).toBeNull()
  })

  it('start the attempt even when the host dismissal callback throws', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', required: true }],
          },
        }),
    })
    const onInitialErrorDismiss = vi.fn(() => {
      throw new Error('host dismissal failed')
    })
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={BOTH_FORMS}
        initialError="authn.link_required"
        onInitialErrorDismiss={onInitialErrorDismiss}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      MAIL,
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      PASS,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordSignIn.submit }),
    )
    expect(onInitialErrorDismiss).toHaveBeenCalledTimes(1)
    expect(
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel),
    ).toBeInTheDocument()
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows'),
    ).toHaveLength(1)
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('fire onSignedIn once from the password channel login', async () => {
    const harness = makeSignInHarness()
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={BOTH_FORMS}
        onSignedIn={onSignedIn}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      MAIL,
    )
    await user.type(screen.getByLabelText(zhCN.passwordSignIn.passwordLabel), PASS)
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordSignIn.submit }),
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(onSignedIn).toHaveBeenCalledWith({
      mode: 'sign-in',
      channel: 'password',
    })
    expect(harness.store.get()).toBe('access-1')
    expect(harness.calls[0]?.options?.body).toMatchObject({ method: 'password' })
  })

  it('report the register mode and the SMS channel that completed', async () => {
    const harness = makeSignInHarness()
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'sms', kind: 'form', opens_account: true }],
          { signup: true },
        )}
        onSignedIn={onSignedIn}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      '+14155550123',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.smsSignIn.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.smsSignIn.submit }),
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(onSignedIn).toHaveBeenCalledWith({
      mode: 'register',
      channel: 'sms',
    })
  })

  it('render a single form channel without a tab strip', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([{ name: 'password', kind: 'form' }])}
      />,
    )
    expect(screen.queryByRole('tab')).not.toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
  })

  it('show the loading notice while the discovery read is in flight', () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () => new Promise(() => {}),
    })
    renderWithProviders(<SignInScreen session={harness.session} />)
    expect(screen.getByText(zhCN.discovery.loading)).toBeInTheDocument()
  })

  it('show the retry notice when the discovery read fails, and read again on retry', async () => {
    let attempts = 0
    const harness = makeHarness({
      [LOGIN_METHODS]: () => {
        attempts += 1
        if (attempts === 1) {
          throw apiError(500, 'authn.unavailable')
        }
        return BOTH_FORMS
      },
    })
    renderWithProviders(<SignInScreen session={harness.session} />)
    expect(
      await screen.findByText(zhCN.errors.authn.unavailable),
    ).toBeInTheDocument()
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.discovery.retry }),
    )
    expect(
      await screen.findByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
    expect(attempts).toBe(2)
  })

  it('render redirect methods from discovery as buttons that hand over the authorize URL', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          next: {
            kind: 'redirect',
            url: 'https://accounts.google.com/o/oauth2/v2/auth',
            method: 'GET',
          },
        }),
    })
    const onAuthorizeUrl = vi.fn()
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'password', kind: 'form' },
          { name: 'google', kind: 'redirect', protocol: 'oidc', label: 'Google' },
        ])}
        social={{ onAuthorizeUrl }}
      />,
    )
    expect(screen.getByText(zhCN.social.title)).toBeInTheDocument()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Google' }))
    await waitFor(() => expect(onAuthorizeUrl).toHaveBeenCalledTimes(1))
    expect(onAuthorizeUrl).toHaveBeenCalledWith(
      'google',
      'https://accounts.google.com/o/oauth2/v2/auth',
    )
  })

  it('give the two WeChat methods distinct visible and accessible names', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'password', kind: 'form' },
          {
            name: 'wechat.mp',
            kind: 'redirect',
            protocol: 'oauth2',
            label: 'WeChat',
          },
          {
            name: 'wechat.web',
            kind: 'redirect',
            protocol: 'oauth2',
            label: 'WeChat',
          },
        ])}
      />,
    )
    const officialAccount = screen.getByRole('button', {
      name: zhCN.social.provider.wechatOfficialAccount,
    })
    const web = screen.getByRole('button', {
      name: zhCN.social.provider.wechatWeb,
    })
    expect(officialAccount).not.toBe(web)
    expect(screen.queryByRole('button', { name: 'WeChat' })).toBeNull()
  })

  it('render the enterprise email-first entry that discovers the providers', async () => {
    const harness = makeHarness({
      [SSO_DISCOVER]: (call) => {
        expect(call.options?.body).toEqual({ email: MAIL })
        return ssoProviders([
          { id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' },
        ])
      },
      [LOGIN_START]: () =>
        openFlow({
          next: {
            kind: 'redirect',
            url: 'https://idp.example/auth',
            method: 'GET',
          },
        }),
    })
    const onAuthorizeUrl = vi.fn()
    const startFlow = vi.spyOn(harness.session, 'startFlow')
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'password', kind: 'form' },
          { name: 'oidc', kind: 'enterprise', protocol: 'oidc' },
        ])}
        social={{ returnTo: '/api/v1/auth/sso/callback', onAuthorizeUrl }}
      />,
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.enterprise.emailLabel), MAIL)
    await user.click(
      screen.getByRole('button', { name: zhCN.enterprise.continue }),
    )
    await user.click(await screen.findByRole('button', { name: 'Acme Okta' }))
    await waitFor(() => expect(onAuthorizeUrl).toHaveBeenCalledTimes(1))
    expect(onAuthorizeUrl).toHaveBeenCalledWith('oidc', 'https://idp.example/auth')
    const start = harness.calls.find((call) => call.path === '/api/v1/auth/flows')
    expect(start?.options?.body).toMatchObject({
      method: 'oidc',
      input: { provider: 'idp-1' },
      return_to: '/api/v1/auth/sso/callback',
    })
    expect(startFlow.mock.calls[0]?.[0]?.delivery).not.toBe('token')
  })

  it('drive the passkey ceremony from its options to a sign-in', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: { kind: 'webauthn', options: { challenge: 'abc', rpId: 'x' } },
        }),
      [LOGIN_ANSWER]: (call) =>
        (call.options?.body as { step?: string }).step === 'complete'
          ? authenticatedCompletion()
          : completeFlow(2),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    const startFlow = vi.spyOn(harness.session, 'startFlow')
    const credential = assertionCredential()
    const get = vi.fn().mockResolvedValue(credential)
    const original = Object.getOwnPropertyDescriptor(window.navigator, 'credentials')
    Object.defineProperty(window.navigator, 'credentials', {
      value: { get, create: vi.fn() },
      configurable: true,
    })
    try {
      renderWithProviders(
        <SignInScreen
          session={harness.session}
          discovery={methodList([
            { name: 'password', kind: 'form' },
            { name: 'passkey', kind: 'webauthn', label: 'Passkey' },
          ])}
          onSignedIn={onSignedIn}
        />,
      )
      const user = userEvent.setup()
      await user.click(screen.getByRole('button', { name: 'Passkey' }))
      expect(
        screen.queryByRole('button', { name: zhCN.signInFlow.submit }),
      ).not.toBeInTheDocument()
      await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    } finally {
      if (original === undefined) {
        delete (window.navigator as { credentials?: unknown }).credentials
      } else {
        Object.defineProperty(window.navigator, 'credentials', original)
      }
    }
    expect(get).toHaveBeenCalledTimes(1)
    const answered = harness.calls.find(
      (call) =>
        call.path === '/api/v1/auth/flows/answer' &&
        (call.options?.body as { step?: string }).step === 'answer',
    )
    expect(answered?.options?.body).toMatchObject({
      step: 'answer',
      input: { credential: expect.stringContaining('AQID') },
    })
    expect(harness.store.get()).toBe('access-1')
    expect(harness.calls[0]?.options?.body).toMatchObject({ method: 'passkey' })
    expect(startFlow.mock.calls[0]?.[0]?.delivery).not.toBe('token')
  })

  it('render the passkey ceremony message when the browser has no WebAuthn', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: { kind: 'webauthn', options: { challenge: 'abc', rpId: 'x' } },
        }),
      [LOGIN_ANSWER]: () => completeFlow(2),
    })
    const original = Object.getOwnPropertyDescriptor(window.navigator, 'credentials')
    delete (window.navigator as { credentials?: unknown }).credentials
    try {
      renderWithProviders(
        <SignInScreen
          session={harness.session}
          discovery={methodList([{ name: 'passkey', kind: 'webauthn', label: 'Passkey' }])}
          onSignedIn={vi.fn()}
        />,
      )
      const user = userEvent.setup()
      await user.click(screen.getByRole('button', { name: 'Passkey' }))
      expect(
        await screen.findByText(zhCN.signInFlow.passkeyUnsupported),
      ).toBeInTheDocument()
    } finally {
      if (original !== undefined) {
        Object.defineProperty(window.navigator, 'credentials', original)
      }
    }
    expect(
      harness.calls.find((call) => call.path === '/api/v1/auth/flows/answer'),
    ).toBeUndefined()
  })

  it('show the waiting message and a cancel control while the ceremony is pending', async () => {
    const harness = makePasskeyHarness()
    const pending = new Promise<PublicKeyCredential>(() => {})
    const get = vi.fn().mockReturnValue(pending)
    const restore = stubCredentials({ get, create: vi.fn() })
    try {
      renderWithProviders(
        <SignInScreen
          session={harness.session}
          discovery={PASSKEY_ONLY}
          onSignedIn={vi.fn()}
        />,
      )
      const user = userEvent.setup()
      await user.click(screen.getByRole('button', { name: 'Passkey' }))
      expect(
        await screen.findByText(zhCN.signInFlow.passkeyWaiting),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyCancel }),
      ).toBeEnabled()
      expect(
        screen.queryByRole('button', { name: zhCN.signInFlow.submit }),
      ).not.toBeInTheDocument()
    } finally {
      restore()
    }
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('turn a cancelled ceremony into an explicit failure with a retry that re-issues it', async () => {
    const harness = makePasskeyHarness()
    const pending = new Promise<PublicKeyCredential>(() => {})
    const get = vi.fn().mockReturnValue(pending)
    const restore = stubCredentials({ get, create: vi.fn() })
    try {
      renderWithProviders(
        <SignInScreen
          session={harness.session}
          discovery={PASSKEY_ONLY}
          onSignedIn={vi.fn()}
        />,
      )
      const user = userEvent.setup()
      await user.click(screen.getByRole('button', { name: 'Passkey' }))
      await screen.findByText(zhCN.signInFlow.passkeyWaiting)
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyCancel }),
      )
      expect(
        screen.getByText(zhCN.signInFlow.passkeyCancelled),
      ).toBeInTheDocument()
      expect(
        screen.queryByText(zhCN.signInFlow.passkeyWaiting),
      ).not.toBeInTheDocument()
      const first = get.mock.calls[0]?.[0] as { signal?: AbortSignal }
      expect(first.signal?.aborted).toBe(true)
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyRetry }),
      )
      expect(
        await screen.findByText(zhCN.signInFlow.passkeyWaiting),
      ).toBeInTheDocument()
      expect(
        screen.queryByText(zhCN.signInFlow.passkeyCancelled),
      ).not.toBeInTheDocument()
      const second = get.mock.calls[1]?.[0] as { signal?: AbortSignal }
      expect(second.signal?.aborted).toBe(false)
    } finally {
      restore()
    }
    expect(get).toHaveBeenCalledTimes(2)
    expect(
      harness.calls.find((call) => call.path === '/api/v1/auth/flows/answer'),
    ).toBeUndefined()
  })

  it('render the explicit failure when the ceremony rejects with no authenticator', async () => {
    const harness = makePasskeyHarness()
    const get = vi
      .fn()
      .mockRejectedValue(new DOMException('no authenticator', 'NotAllowedError'))
    const restore = stubCredentials({ get, create: vi.fn() })
    try {
      renderWithProviders(
        <SignInScreen
          session={harness.session}
          discovery={PASSKEY_ONLY}
          onSignedIn={vi.fn()}
        />,
      )
      const user = userEvent.setup()
      await user.click(screen.getByRole('button', { name: 'Passkey' }))
      expect(
        await screen.findByText(zhCN.signInFlow.passkeyCancelled),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyRetry }),
      ).toBeEnabled()
    } finally {
      restore()
    }
    expect(
      harness.calls.find((call) => call.path === '/api/v1/auth/flows/answer'),
    ).toBeUndefined()
  })

  it('discard a passkey ceremony that settles after the surface unmounted', async () => {
    const harness = makePasskeyHarness()
    let resolveGet: (value: unknown) => void = () => {}
    const pending = new Promise((resolve) => {
      resolveGet = resolve
    })
    const get = vi.fn().mockReturnValue(pending)
    const restore = stubCredentials({ get, create: vi.fn() })
    const onSignedIn = vi.fn()
    try {
      const { unmount } = renderWithProviders(
        <SignInScreen
          session={harness.session}
          discovery={PASSKEY_ONLY}
          onSignedIn={onSignedIn}
        />,
      )
      const user = userEvent.setup()
      await user.click(screen.getByRole('button', { name: 'Passkey' }))
      await screen.findByText(zhCN.signInFlow.passkeyWaiting)
      const signal = (
        get.mock.calls[0]?.[0] as { signal?: AbortSignal } | undefined
      )?.signal
      unmount()
      expect(signal?.aborted).toBe(true)
      await act(async () => {
        resolveGet(assertionCredential())
        await new Promise((resolve) => setTimeout(resolve, 0))
      })
      expect(
        harness.calls.find((call) => call.path === '/api/v1/auth/flows/answer'),
      ).toBeUndefined()
      expect(onSignedIn).not.toHaveBeenCalled()
    } finally {
      restore()
    }
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('mount the passkey waiting notice empty, fill it a commit later, and focus cancel', async () => {
    const harness = makePasskeyHarness()
    const pending = new Promise<PublicKeyCredential>(() => {})
    const get = vi.fn().mockReturnValue(pending)
    const restore = stubCredentials({ get, create: vi.fn() })
    try {
      renderWithProviders(
        <SignInScreen session={harness.session} discovery={PASSKEY_ONLY} />,
      )
      const user = userEvent.setup()
      await user.click(screen.getByRole('button', { name: 'Passkey' }))
      await screen.findByText(zhCN.signInFlow.passkeyWaiting)
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyCancel }),
      )
      const before = new Set(screen.getAllByRole('status'))
      const records: MutationRecord[] = []
      const observer = new MutationObserver((batch) => {
        records.push(...batch)
      })
      observer.observe(document.body, {
        childList: true,
        subtree: true,
        characterData: true,
      })
      try {
        act(() => {
          flushSync(() => {
            screen
              .getByRole('button', { name: zhCN.signInFlow.passkeyRetry })
              .click()
          })
        })
        const region = screen
          .getAllByRole('status')
          .find((candidate) => !before.has(candidate))
        if (region === undefined) {
          throw new Error('the retry inserted no waiting region')
        }
        expect(region).toHaveTextContent('')

        await waitFor(() =>
          expect(region).toHaveTextContent(zhCN.signInFlow.passkeyWaiting),
        )
        expect(
          records.some((record) =>
            Array.from(record.addedNodes).some(
              (node) =>
                node === region ||
                (node instanceof Element && node.contains(region)),
            ),
          ),
        ).toBe(true)
        expect(
          records.some((record) => region.contains(record.target)),
        ).toBe(true)
      } finally {
        observer.disconnect()
      }
      expect(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyCancel }),
      ).toHaveFocus()
    } finally {
      restore()
    }
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('prompt for registration on a passkey enrollment step', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'enroll',
          next: {
            kind: 'webauthn',
            options: { challenge: 'abc', user: { id: 'dXNlcg' } },
          },
        }),
    })
    renderWithProviders(
      <SignInScreen session={harness.session} discovery={PASSKEY_ONLY} />,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Passkey' }))
    expect(
      await screen.findByText(zhCN.signInFlow.passkeyRegisterPrompt),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.signInFlow.passkeyPrompt),
    ).not.toBeInTheDocument()
  })

  it('present the register entry only where a first login may open an account', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'sms', kind: 'form', opens_account: true }],
          { signup: true },
        )}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    expect(
      screen.getByText(zhCN.mode.registerHint),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.mode.signInEntry }))
    expect(screen.queryByText(zhCN.mode.registerHint)).not.toBeInTheDocument()
  })

  it('hide the register entry when the deployment opens no accounts', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'password', kind: 'form' }],
          { signup: false },
        )}
      />,
    )
    expect(
      screen.queryByRole('button', { name: zhCN.mode.registerEntry }),
    ).not.toBeInTheDocument()
  })

  it('offer the forgot-password entry under the password channel when the deployment resets passwords', async () => {
    const harness = makeHarness({})
    const onForgotPassword = vi.fn()
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([{ name: 'password', kind: 'form' }], {
          passwordReset: true,
          passwordResetMethods: ['email'],
        })}
        onForgotPassword={onForgotPassword}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordSignIn.forgotPassword }),
    )
    expect(onForgotPassword).toHaveBeenCalledTimes(1)
    expect(harness.calls).toHaveLength(0)
  })

  it('render its link-shaped actions as text buttons carrying the copy as written', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([{ name: 'password', kind: 'form' }], {
          signup: true,
          signupPassword: true,
          passwordReset: true,
          passwordResetMethods: ['email'],
        })}
        onForgotPassword={() => undefined}
      />,
    )
    const registerEntry = screen.getByRole('button', {
      name: zhCN.mode.registerEntry,
    })
    expect(registerEntry.className).toContain('MuiButton-text')
    const forgot = screen.getByRole('button', {
      name: zhCN.passwordSignIn.forgotPassword,
    })
    expect(forgot.className).toContain('MuiButton-text')
  })

  it('hide the forgot-password entry when the deployment does not offer it', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([{ name: 'password', kind: 'form' }])}
        onForgotPassword={() => undefined}
      />,
    )
    expect(
      screen.queryByRole('button', { name: zhCN.passwordSignIn.forgotPassword }),
    ).not.toBeInTheDocument()
  })

  it('hide the forgot-password entry when the host binds no navigation', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([{ name: 'password', kind: 'form' }], {
          passwordReset: true,
          passwordResetMethods: ['email'],
        })}
      />,
    )
    expect(
      screen.queryByRole('button', { name: zhCN.passwordSignIn.forgotPassword }),
    ).not.toBeInTheDocument()
  })

  it('hide the forgot-password entry off the password channel and in register mode', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'password', kind: 'form' }, { name: 'sms', kind: 'form', opens_account: true }],
          { signup: true, passwordReset: true, passwordResetMethods: ['email'] },
        )}
        onForgotPassword={() => undefined}
      />,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('tab', { name: zhCN.smsSignIn.title }))
    expect(
      screen.queryByRole('button', { name: zhCN.passwordSignIn.forgotPassword }),
    ).not.toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: zhCN.passwordSignIn.title }))
    expect(
      screen.getByRole('button', { name: zhCN.passwordSignIn.forgotPassword }),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    expect(
      screen.queryByRole('button', { name: zhCN.passwordSignIn.forgotPassword }),
    ).not.toBeInTheDocument()
  })

  it('name the register-mode toggle apart from the channel submit', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', required: true }],
          },
        }),
    })
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [
            { name: 'password', kind: 'form' },
            { name: 'sms', kind: 'form', opens_account: true },
          ],
          { signup: true },
        )}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    expect(
      screen.getByRole('heading', { name: zhCN.mode.registerTitle }),
    ).toBeInTheDocument()
    expect(
      screen.getAllByRole('button', { name: zhCN.mode.signInEntry }),
    ).toHaveLength(1)
    expect(
      screen.getAllByRole('button', { name: zhCN.smsSignIn.sendCode }),
    ).toHaveLength(1)
    expect(zhCN.mode.signInEntry).not.toBe(zhCN.smsSignIn.submit)
    expect(zhCN.mode.signInEntry).not.toBe(zhCN.smsSignIn.sendCode)
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      '+14155550123',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.smsSignIn.sendCode }),
    )
    await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    expect(
      screen.getAllByRole('button', { name: zhCN.mode.signInEntry }),
    ).toHaveLength(1)
    expect(
      screen.getAllByRole('button', { name: zhCN.smsSignIn.submit }),
    ).toHaveLength(1)
    expect(zhCN.mode.signInEntry).not.toBe(zhCN.smsSignIn.submit)
  })

  it('list only account-opening channels in register mode, first one selected', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [
            { name: 'password', kind: 'form' },
            { name: 'sms', kind: 'form', opens_account: true },
            {
              name: 'magic',
              kind: 'form',
              opens_account: true,
              fields: [{ name: 'identifier', kind: 'text', required: true }],
            },
          ],
          { signup: true },
        )}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    expect(
      screen.queryByRole('tab', { name: zhCN.passwordSignIn.title }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('tab', { name: zhCN.smsSignIn.title }),
    ).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: 'magic' })).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.mode.signInEntry }))
    expect(
      screen.getByRole('tab', { name: zhCN.passwordSignIn.title }),
    ).toHaveAttribute('aria-selected', 'true')
  })

  it('name the register mode and offer the way back with the account the viewer has', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'sms', kind: 'form', opens_account: true }],
          { signup: true },
        )}
      />,
    )
    const user = userEvent.setup()
    expect(
      screen.queryByRole('heading', { name: zhCN.mode.registerTitle }),
    ).not.toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    expect(
      screen.getByRole('heading', { name: zhCN.mode.registerTitle }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.mode.signInEntry }),
    ).toBeInTheDocument()
    expect(enUS.mode.signInEntry).toBe('Already have an account? Sign in')
    expect(zhCN.mode.signInEntry).toContain(zhCN.passwordSignIn.submit)
  })

  it('say in sign-in mode that an unregistered number creates an account', () => {
    const harness = makeHarness({})
    expect(zhCN.mode.signInAccountHint).not.toMatch(/\{\{/)
    expect(enUS.mode.signInAccountHint).not.toMatch(/\{\{/)

    const { unmount } = renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'sms', kind: 'form', opens_account: true }],
          { signup: true },
        )}
      />,
    )
    expect(screen.getByText(zhCN.mode.signInAccountHint)).toBeInTheDocument()
    unmount()

    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'password', kind: 'form' }],
          { signup: true },
        )}
      />,
    )
    expect(
      screen.queryByText(zhCN.mode.signInAccountHint),
    ).not.toBeInTheDocument()
  })

  it('keep the account sentence under the SMS channel alone', async () => {
    const harness = makeHarness({})
    const discovery = methodList(
      [
        { name: 'password', kind: 'form' },
        { name: 'sms', kind: 'form', opens_account: true },
      ],
      { signup: true },
    )
    renderWithProviders(
      <SignInScreen session={harness.session} discovery={discovery} />,
    )
    const hint = zhCN.mode.signInAccountHint
    expect(screen.queryByText(hint)).not.toBeInTheDocument()

    const user = userEvent.setup()
    await user.click(
      screen.getByRole('tab', { name: zhCN.smsSignIn.title }),
    )
    expect(screen.getByText(hint)).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    expect(screen.queryByText(hint)).not.toBeInTheDocument()
  })

  it('list only account-opening methods on every channel in register mode', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [
            { name: 'sms', kind: 'form', opens_account: true },
            {
              name: 'google',
              kind: 'redirect',
              protocol: 'oidc',
              label: 'Google',
              opens_account: true,
            },
            { name: 'github', kind: 'redirect', protocol: 'oauth2', label: 'GitHub' },
            { name: 'passkey', kind: 'webauthn', label: 'Passkey' },
            {
              name: 'okta',
              kind: 'enterprise',
              protocol: 'oidc',
              opens_account: true,
            },
          ],
          { signup: true },
        )}
      />,
    )
    const user = userEvent.setup()
    expect(screen.getByRole('button', { name: 'GitHub' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Passkey' })).toBeInTheDocument()
    expect(screen.getByLabelText(zhCN.enterprise.emailLabel)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: zhCN.mode.registerEntry }))

    expect(screen.getByRole('button', { name: 'Google' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'GitHub' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Passkey' })).not.toBeInTheDocument()
    expect(screen.getByLabelText(zhCN.enterprise.emailLabel)).toBeInTheDocument()
    expect(screen.getByLabelText(zhCN.smsSignIn.phoneLabel)).toBeInTheDocument()
  })

  it('narrow the deprecated provider override in register mode too', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [
            { name: 'sms', kind: 'form', opens_account: true },
            {
              name: 'google',
              kind: 'redirect',
              protocol: 'oidc',
              label: 'Google',
              opens_account: true,
            },
            { name: 'github', kind: 'redirect', protocol: 'oauth2', label: 'GitHub' },
          ],
          { signup: true },
        )}
        social={{
          providers: [
            { provider: 'google', label: 'Google', redirectUri: '' },
            { provider: 'github', label: 'GitHub', redirectUri: '' },
          ],
        }}
      />,
    )
    const user = userEvent.setup()
    expect(screen.getByRole('button', { name: 'GitHub' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.mode.registerEntry }))
    expect(screen.getByRole('button', { name: 'Google' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'GitHub' })).not.toBeInTheDocument()
  })

  it('omit an enterprise entry that opens no account in register mode', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [
            { name: 'sms', kind: 'form', opens_account: true },
            { name: 'corp', kind: 'enterprise', protocol: 'oidc' },
          ],
          { signup: true },
        )}
      />,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: zhCN.mode.registerEntry }))
    expect(
      screen.queryByLabelText(zhCN.enterprise.emailLabel),
    ).not.toBeInTheDocument()
  })

  it('show no phone-number sentence where only a social method opens accounts', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [
            { name: 'password', kind: 'form' },
            {
              name: 'google',
              kind: 'redirect',
              protocol: 'oidc',
              label: 'Google',
              opens_account: true,
            },
          ],
          { signup: true },
        )}
      />,
    )
    expect(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.mode.signInAccountHint),
    ).not.toBeInTheDocument()
  })

  it('count only rendered methods for the register entry', () => {
    const harness = makeHarness({})
    const { unmount } = renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [
            { name: 'password', kind: 'form' },
            { name: 'sms', kind: 'form', opens_account: true },
          ],
          { signup: true },
        )}
        channels={['password']}
      />,
    )
    expect(
      screen.queryByRole('button', { name: zhCN.mode.registerEntry }),
    ).not.toBeInTheDocument()
    unmount()

    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [
            { name: 'password', kind: 'form' },
            { name: 'quantum', kind: 'telepathy', opens_account: true },
          ],
          { signup: true },
        )}
      />,
    )
    expect(
      screen.queryByRole('button', { name: zhCN.mode.registerEntry }),
    ).not.toBeInTheDocument()
  })

  it('ignore an unknown method kind without crashing', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'password', kind: 'form' },
          { name: 'quantum', kind: 'telepathy' },
        ])}
      />,
    )
    expect(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
  })

  it('drive an unknown form method from its own descriptor fields', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          {
            name: 'magic',
            kind: 'form',
            fields: [
              { name: 'identifier', kind: 'text', required: true },
              { name: 'token', kind: 'password', secret: true },
            ],
          },
        ])}
      />,
    )
    expect(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
    expect(screen.getByLabelText('token')).toBeInTheDocument()
  })

  it('name no token delivery from a descriptor-driven form -- the session delivery is auth-core’s', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const startFlow = vi.spyOn(harness.session, 'startFlow')
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          {
            name: 'magic',
            kind: 'form',
            fields: [{ name: 'identifier', kind: 'text', required: true }],
          },
        ])}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      MAIL,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )
    await waitFor(() => expect(startFlow).toHaveBeenCalledTimes(1))
    expect(startFlow.mock.calls[0]?.[0]?.delivery).not.toBe('token')
  })

  it('unmount the previous descriptor form when switching between descriptor channels', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          {
            name: 'alpha',
            kind: 'form',
            label: 'Alpha',
            fields: [{ name: 'identifier', kind: 'text', required: true }],
          },
          {
            name: 'beta',
            kind: 'form',
            label: 'Beta',
            fields: [{ name: 'identifier', kind: 'text', required: true }],
          },
        ])}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      'typed-under-alpha',
    )
    await user.click(screen.getByRole('tab', { name: 'Beta' }))
    expect(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).toHaveValue('')
  })

  it('drop a parked descriptor flow when switching to another descriptor channel', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'alpha_code', kind: 'code' }],
          },
        }),
    })
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          {
            name: 'alpha',
            kind: 'form',
            label: 'Alpha',
            fields: [{ name: 'identifier', kind: 'text', required: true }],
          },
          {
            name: 'beta',
            kind: 'form',
            label: 'Beta',
            fields: [{ name: 'recovery', kind: 'text', required: true }],
          },
        ])}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      'stale-value',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )
    await screen.findByLabelText('alpha_code')
    await user.click(screen.getByRole('tab', { name: 'Beta' }))
    expect(screen.queryByLabelText('alpha_code')).not.toBeInTheDocument()
    const recovery = screen.getByLabelText('recovery')
    expect(recovery).toHaveValue('')
    await user.type(recovery, 'fresh-value')
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )
    await waitFor(() => expect(harness.calls).toHaveLength(2))
    expect(harness.calls[1]?.options?.body).toMatchObject({
      method: 'beta',
      input: { recovery: 'fresh-value' },
    })
    expect(
      (harness.calls[1]?.options?.body as { input?: Record<string, string> })
        .input,
    ).not.toHaveProperty('identifier')
  })

  it('honor the deprecated channels override to narrow the form strip', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={BOTH_FORMS}
        channels={['password']}
      />,
    )
    expect(
      screen.queryByRole('tab', { name: zhCN.smsSignIn.title }),
    ).not.toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
  })

  it('render the en-US bundle on an English-starting instance', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen session={harness.session} discovery={BOTH_FORMS} />,
      { language: 'en-US' },
    )
    expect(
      screen.getByRole('tab', { name: enUS.passwordSignIn.title }),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(enUS.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
  })

  it('pass axe with no violations', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <div>
        <h1>Sign in</h1>
        <SignInScreen
          session={harness.session}
          discovery={methodList(
            [
              { name: 'password', kind: 'form' },
              { name: 'google', kind: 'redirect', protocol: 'oidc', label: 'Google' },
              { name: 'passkey', kind: 'webauthn', label: 'Passkey' },
            ],
            { signup: true },
          )}
        />
      </div>,
    )
    await expectNoAxeViolations()
  })

  it('report the mode switch to the host and never switch itself when controlled', async () => {
    const harness = makeHarness({})
    const onModeChange = vi.fn()
    const first = renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'sms', kind: 'form', opens_account: true }],
          { signup: true },
        )}
        onModeChange={onModeChange}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    expect(onModeChange).toHaveBeenCalledWith('register')
    expect(screen.queryByText(zhCN.mode.registerHint)).not.toBeInTheDocument()
    first.unmount()

    const controlled = makeHarness({})
    const back = vi.fn()
    renderWithProviders(
      <SignInScreen
        session={controlled.session}
        discovery={methodList(
          [{ name: 'sms', kind: 'form', opens_account: true }],
          { signup: true },
        )}
        mode="register"
        onModeChange={back}
      />,
    )
    expect(screen.getByText(zhCN.mode.registerHint)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.mode.signInEntry }))
    expect(back).toHaveBeenCalledWith('sign-in')
  })

  it('render the host’s above-channels and below-forms slots', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={BOTH_FORMS}
        slots={{
          aboveChannels: <p>host-above</p>,
          belowForms: <p>host-below</p>,
        }}
      />,
    )
    expect(screen.getByText('host-above')).toBeInTheDocument()
    expect(screen.getByText('host-below')).toBeInTheDocument()
  })

  it('render the channel classes in the host’s order, heading the first without “or”', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'password', kind: 'form' },
          { name: 'google', kind: 'redirect', protocol: 'oidc', label: 'Google' },
        ])}
        order={['redirect', 'form']}
      />,
    )
    const social = screen.getByText(zhCN.social.titleFirst)
    expect(screen.queryByText(zhCN.social.title)).not.toBeInTheDocument()
    const form = screen.getByLabelText(zhCN.passwordSignIn.identifierLabel)
    expect(
      social.compareDocumentPosition(form) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('keep an unlisted channel class after the listed ones', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'password', kind: 'form' },
          { name: 'passkey', kind: 'webauthn', label: 'Passkey' },
        ])}
        order={['form']}
      />,
    )
    expect(
      screen.getByRole('button', { name: 'Passkey' }),
    ).toBeInTheDocument()
    expect(screen.getByText(zhCN.passkey.title)).toBeInTheDocument()
  })

  it('head the enterprise class with its own divider', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'password', kind: 'form' },
          { name: 'oidc', kind: 'enterprise', protocol: 'oidc' },
        ])}
      />,
    )
    expect(screen.getByText(zhCN.enterprise.title)).toBeInTheDocument()
    expect(screen.getByLabelText(zhCN.enterprise.emailLabel)).toBeInTheDocument()
  })

  it('head the surface without “or” when the enterprise class comes first', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'password', kind: 'form' },
          { name: 'oidc', kind: 'enterprise', protocol: 'oidc' },
        ])}
        order={['enterprise', 'form']}
      />,
    )
    expect(screen.getByText(zhCN.enterprise.titleFirst)).toBeInTheDocument()
    expect(screen.queryByText(zhCN.enterprise.title)).not.toBeInTheDocument()
  })

  it('render no enterprise divider when the deployment offers no enterprise method', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([{ name: 'password', kind: 'form' }])}
      />,
    )
    expect(screen.queryByText(zhCN.enterprise.title)).not.toBeInTheDocument()
    expect(screen.queryByText(zhCN.enterprise.titleFirst)).not.toBeInTheDocument()
  })

  it('head the surface without “or” when the passkey class comes first', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'password', kind: 'form' },
          { name: 'passkey', kind: 'webauthn', label: 'Passkey' },
        ])}
        order={['webauthn', 'form']}
      />,
    )
    expect(screen.getByText(zhCN.passkey.titleFirst)).toBeInTheDocument()
    expect(screen.queryByText(zhCN.passkey.title)).not.toBeInTheDocument()
  })

  it('render the register title as the host’s heading level', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'sms', kind: 'form', opens_account: true }],
          { signup: true },
        )}
        mode="register"
        headingLevel="h3"
      />,
    )
    expect(
      screen.getByRole('heading', {
        level: 3,
        name: zhCN.mode.registerTitle,
      }),
    ).toBeInTheDocument()
  })

  it('render each redirect method’s mark from the host lookup', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'google', kind: 'redirect', protocol: 'oidc', label: 'Google' },
        ])}
        providerIcon={(provider) => <span data-testid={`mark-${provider}`} />}
      />,
    )
    const button = screen.getByRole('button', { name: 'Google' })
    expect(
      button.querySelector('[data-testid="mark-google"]'),
    ).not.toBeNull()
  })

  it('refuse the password start at the gate: nothing sent, no error', async () => {
    const harness = makeHarness({})
    const beforeStart = vi.fn(() => false)
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([{ name: 'password', kind: 'form' }])}
        beforeStart={beforeStart}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      MAIL,
    )
    await user.type(screen.getByLabelText(zhCN.passwordSignIn.passwordLabel), PASS)
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordSignIn.submit }),
    )
    await waitFor(() => expect(beforeStart).toHaveBeenCalledTimes(1))
    expect(beforeStart).toHaveBeenCalledWith({
      method: 'password',
      mode: 'sign-in',
    })
    expect(harness.calls).toHaveLength(0)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('await an asynchronous gate answer before starting', async () => {
    const harness = makeHarness({})
    const beforeStart = vi.fn(async () => false)
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([{ name: 'password', kind: 'form' }])}
        beforeStart={beforeStart}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      MAIL,
    )
    await user.type(screen.getByLabelText(zhCN.passwordSignIn.passwordLabel), PASS)
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordSignIn.submit }),
    )
    await waitFor(() => expect(beforeStart).toHaveBeenCalledTimes(1))
    expect(harness.calls).toHaveLength(0)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('not gate the flow’s later steps, only its start', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', required: true }],
          },
        }),
      [LOGIN_ANSWER]: (call) =>
        (call.options?.body as { step?: string }).step === 'complete'
          ? authenticatedCompletion()
          : completeFlow(2),
      [PROFILE_GET]: () => principal(),
    })
    let allowed = true
    const beforeStart = vi.fn(() => allowed)
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([{ name: 'password', kind: 'form' }])}
        beforeStart={beforeStart}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      MAIL,
    )
    await user.type(screen.getByLabelText(zhCN.passwordSignIn.passwordLabel), PASS)
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordSignIn.submit }),
    )
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    allowed = false
    await user.type(code, '123456')
    await user.click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))
    await waitFor(() =>
      expect(
        harness.calls.some(
          (call) => call.path === '/api/v1/auth/flows/answer',
        ),
      ).toBe(true),
    )
    expect(beforeStart).toHaveBeenCalledTimes(1)
  })

  it('word a refused passkey answer itself instead of the password text', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: { kind: 'webauthn', options: { challenge: 'abc', rpId: 'x' } },
        }),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    const get = vi.fn().mockResolvedValue(assertionCredential())
    const restore = stubCredentials({ get, create: vi.fn() })
    try {
      renderWithProviders(
        <SignInScreen session={harness.session} discovery={PASSKEY_ONLY} />,
      )
      const user = userEvent.setup()
      await user.click(screen.getByRole('button', { name: 'Passkey' }))
      const banner = await screen.findByRole('alert')
      expect(banner).toHaveTextContent(zhCN.signInFlow.passkeyRefused)
      expect(banner).not.toHaveTextContent(zhCN.errors.authn.login_failed)
    } finally {
      restore()
    }
  })

  it('clear a refused passkey answer once the viewer turns to another channel', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: { kind: 'webauthn', options: { challenge: 'abc', rpId: 'x' } },
        }),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    const get = vi.fn().mockResolvedValue(assertionCredential())
    const restore = stubCredentials({ get, create: vi.fn() })
    try {
      renderWithProviders(
        <SignInScreen
          session={harness.session}
          discovery={methodList([
            { name: 'password', kind: 'form' },
            { name: 'passkey', kind: 'webauthn', label: 'Passkey' },
          ])}
        />,
      )
      const user = userEvent.setup()
      await user.click(screen.getByRole('button', { name: 'Passkey' }))
      expect(await screen.findByRole('alert')).toHaveTextContent(
        zhCN.signInFlow.passkeyRefused,
      )
      await user.type(
        screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
        MAIL,
      )
      await waitFor(() =>
        expect(screen.queryByRole('alert')).not.toBeInTheDocument(),
      )
    } finally {
      restore()
    }
  })

  it('keep the ceremony failure and the server failure banner mutually exclusive', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: { kind: 'webauthn', options: { challenge: 'abc', rpId: 'x' } },
        }),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    const get = vi
      .fn()
      .mockResolvedValueOnce(assertionCredential())
      .mockRejectedValueOnce(
        new DOMException('no authenticator', 'NotAllowedError'),
      )
      .mockResolvedValueOnce(assertionCredential())
    const restore = stubCredentials({ get, create: vi.fn() })
    try {
      renderWithProviders(
        <SignInScreen
          session={harness.session}
          discovery={PASSKEY_ONLY}
          onSignedIn={vi.fn()}
        />,
      )
      const user = userEvent.setup()
      await user.click(screen.getByRole('button', { name: 'Passkey' }))
      expect(await screen.findByRole('alert')).toHaveTextContent(
        zhCN.signInFlow.passkeyRefused,
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyRetry }),
      )
      const ceremony = await screen.findByRole('alert')
      expect(ceremony).toHaveTextContent(zhCN.signInFlow.passkeyCancelled)
      expect(ceremony).not.toHaveTextContent(zhCN.signInFlow.passkeyRefused)
      expect(screen.getAllByRole('alert')).toHaveLength(1)
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyRetry }),
      )
      const server = await screen.findByRole('alert')
      expect(server).toHaveTextContent(zhCN.signInFlow.passkeyRefused)
      expect(server).not.toHaveTextContent(zhCN.signInFlow.passkeyCancelled)
      expect(screen.getAllByRole('alert')).toHaveLength(1)
    } finally {
      restore()
    }
  })

  it('keep each channel’s failure banner in its own feedback scope', async () => {
    const harness = makeHarness({
      [LOGIN_START]: (call) => {
        const method = (call.options?.body as { method?: string }).method
        if (method === 'password') {
          throw apiError(503, 'authn.unavailable')
        }
        if (method === 'google') {
          throw apiError(400, 'authn.method_disabled')
        }
        return completeFlow(1)
      },
    })
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'password', kind: 'form' },
          { name: 'sms', kind: 'form' },
          { name: 'google', kind: 'redirect', label: 'Google' },
        ])}
      />,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
      MAIL,
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      PASS,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.passwordSignIn.submit }),
    )
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.unavailable,
      ),
    )
    await user.click(screen.getByRole('button', { name: 'Google' }))
    await waitFor(() => expect(screen.getAllByRole('alert')).toHaveLength(2))
    expect(
      screen.getByText(zhCN.errors.authn.unavailable),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.errors.authn.method_disabled),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: zhCN.smsSignIn.title }))
    expect(
      screen.queryByText(zhCN.errors.authn.unavailable),
    ).not.toBeInTheDocument()
    expect(
      screen.getByText(zhCN.errors.authn.method_disabled),
    ).toBeInTheDocument()
  })

  it('refuse a social start at the gate', async () => {
    const harness = makeHarness({})
    const beforeStart = vi.fn(() => false)
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'google', kind: 'redirect', protocol: 'oidc', label: 'Google' },
        ])}
        beforeStart={beforeStart}
      />,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Google' }))
    await waitFor(() =>
      expect(beforeStart).toHaveBeenCalledWith({
        method: 'google',
        mode: 'sign-in',
      }),
    )
    expect(harness.calls).toHaveLength(0)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('refuse an enterprise start at the gate after discovery', async () => {
    const harness = makeHarness({
      [SSO_DISCOVER]: () =>
        ssoProviders([{ id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' }]),
    })
    const beforeStart = vi.fn(() => false)
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'oidc', kind: 'enterprise', protocol: 'oidc' },
        ])}
        beforeStart={beforeStart}
      />,
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.enterprise.emailLabel), MAIL)
    await user.click(
      screen.getByRole('button', { name: zhCN.enterprise.continue }),
    )
    await user.click(await screen.findByRole('button', { name: 'Acme Okta' }))
    await waitFor(() =>
      expect(beforeStart).toHaveBeenCalledWith({
        method: 'oidc',
        mode: 'sign-in',
      }),
    )
    expect(harness.calls).toHaveLength(1)
    expect(harness.calls[0]?.path).toBe('/api/v1/auth/sso/discover')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('offer the register entry where password registration is enabled, even with no account-opening method', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'password', kind: 'form' }],
          { signup: true, signupPassword: true },
        )}
      />,
    )
    expect(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    ).toBeInTheDocument()
  })

  it('select the synthetic password-registration channel by default in register mode', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [
            { name: 'password', kind: 'form' },
            { name: 'sms', kind: 'form', opens_account: true },
          ],
          { signup: true, signupPassword: true },
        )}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    expect(
      screen.getByRole('tab', { name: zhCN.register.title }),
    ).toHaveAttribute('aria-selected', 'true')
    expect(
      screen.queryByRole('tab', { name: zhCN.passwordSignIn.title }),
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('tab', { name: zhCN.smsSignIn.title }),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.register.usernameLabel),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.register.confirmPasswordLabel),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).not.toBeInTheDocument()
  })

  it('start register mode on the password-registration channel after an SMS pick in sign-in mode', async () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [
            { name: 'password', kind: 'form' },
            { name: 'sms', kind: 'form', opens_account: true },
          ],
          { signup: true, signupPassword: true },
        )}
      />,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('tab', { name: zhCN.smsSignIn.title }))
    expect(
      screen.getByRole('tab', { name: zhCN.smsSignIn.title }),
    ).toHaveAttribute('aria-selected', 'true')
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    expect(
      screen.getByRole('tab', { name: zhCN.register.title }),
    ).toHaveAttribute('aria-selected', 'true')
    expect(
      screen.getByLabelText(zhCN.register.usernameLabel),
    ).toBeInTheDocument()
  })

  it('report the register mode and the password channel after a registration', async () => {
    const harness = makeHarness({
      'POST /api/v1/auth/register': () => ({
        state: 'registered',
        account_id: 'account-1',
        verification: { state: 'pending', email: MAIL },
      }),
      [LOGIN_START]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'password', kind: 'form' }],
          { signup: true, signupPassword: true },
        )}
        onSignedIn={onSignedIn}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    await user.type(screen.getByLabelText(zhCN.register.usernameLabel), 'ada')
    await user.type(screen.getByLabelText(zhCN.register.emailLabel), MAIL)
    await user.type(screen.getByLabelText(zhCN.register.passwordLabel), PASS)
    await user.type(
      screen.getByLabelText(zhCN.register.confirmPasswordLabel),
      PASS,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.register.submit }),
    )

    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(onSignedIn).toHaveBeenCalledWith({
      mode: 'register',
      channel: 'password',
    })
    expect(harness.store.get()).toBe('access-1')
  })

  it('return to the sign-in form with the registered username after a refused auto-login', async () => {
    const harness = makeHarness({
      'POST /api/v1/auth/register': () => ({
        state: 'registered',
        account_id: 'account-1',
        verification: { state: 'pending', email: MAIL },
      }),
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'password', kind: 'form' }],
          { signup: true, signupPassword: true },
        )}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    await user.type(screen.getByLabelText(zhCN.register.usernameLabel), 'ada')
    await user.type(screen.getByLabelText(zhCN.register.emailLabel), MAIL)
    await user.type(screen.getByLabelText(zhCN.register.passwordLabel), PASS)
    await user.type(
      screen.getByLabelText(zhCN.register.confirmPasswordLabel),
      PASS,
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.register.submit }),
    )

    const identifier = await screen.findByLabelText(
      zhCN.passwordSignIn.identifierLabel,
    )
    expect(identifier).toHaveValue('ada')
    expect(
      screen.queryByRole('heading', { name: zhCN.mode.registerTitle }),
    ).not.toBeInTheDocument()
  })

  it("land on the sign-in form's first field when a refused auto-login unmounts the registration form", async () => {
    const harness = makeHarness({
      'POST /api/v1/auth/register': () => ({
        state: 'registered',
        account_id: 'account-1',
        verification: { state: 'pending', email: MAIL },
      }),
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [
            { name: 'password', kind: 'form' },
            { name: 'sms', kind: 'form', opens_account: true },
          ],
          { signup: true, signupPassword: true },
        )}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    await submitPasswordRegistration(user)

    const identifier = await screen.findByLabelText(
      zhCN.passwordSignIn.identifierLabel,
    )
    expect(identifier).toHaveFocus()
    expect(document.activeElement).not.toBe(document.body)
  })

  it('leave the focus the operator moved during the auto-login where it is', async () => {
    let rejectLogin: (reason?: unknown) => void = () => {}
    const loginGate = new Promise((_resolve, reject) => {
      rejectLogin = reject
    })
    const harness = makeHarness({
      'POST /api/v1/auth/register': () => ({
        state: 'registered',
        account_id: 'account-1',
        verification: { state: 'pending', email: MAIL },
      }),
      [LOGIN_START]: () => loginGate,
    })
    renderWithProviders(
      <>
        <SignInScreen
          session={harness.session}
          discovery={methodList(
            [
              { name: 'password', kind: 'form' },
              { name: 'sms', kind: 'form', opens_account: true },
            ],
            { signup: true, signupPassword: true },
          )}
        />
        <button type="button">outside</button>
      </>,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.registerEntry }),
    )
    await submitPasswordRegistration(user)
    await waitFor(() =>
      expect(
        harness.calls.some(
          (call) => `${call.method} ${call.path}` === LOGIN_START,
        ),
      ).toBe(true),
    )
    const outside = screen.getByRole('button', { name: 'outside' })
    await user.click(outside)
    expect(outside).toHaveFocus()

    await act(async () => {
      rejectLogin(apiError(401, 'authn.login_failed'))
      await Promise.resolve()
    })
    const identifier = await screen.findByLabelText(
      zhCN.passwordSignIn.identifierLabel,
    )
    expect(outside).toHaveFocus()
    expect(identifier).not.toHaveFocus()
  })

  it('drop a held handoff at the commit after the controlled host declines to switch', async () => {
    const harness = makeHarness({
      'POST /api/v1/auth/register': () => ({
        state: 'registered',
        account_id: 'account-1',
        verification: { state: 'pending', email: MAIL },
      }),
      [LOGIN_START]: () => {
        throw apiError(401, 'authn.login_failed')
      },
    })
    const control = { hold: true }
    renderWithProviders(
      <ControlledModeHost
        session={harness.session}
        discovery={methodList(
          [{ name: 'password', kind: 'form' }],
          { signup: true, signupPassword: true },
        )}
        control={control}
      />,
    )
    const user = userEvent.setup()
    await submitPasswordRegistration(user)

    await screen.findByText(zhCN.register.signInRequired)
    expect(
      screen.queryByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).not.toBeInTheDocument()

    control.hold = false
    await user.click(
      screen.getByRole('button', { name: zhCN.mode.signInEntry }),
    )
    const identifier = await screen.findByLabelText(
      zhCN.passwordSignIn.identifierLabel,
    )
    expect(identifier).not.toHaveFocus()
  })

  it('keep the register entry hidden when password registration and account-opening methods are both off', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'password', kind: 'form' }],
          { signup: true, signupPassword: false },
        )}
      />,
    )
    expect(
      screen.queryByRole('button', { name: zhCN.mode.registerEntry }),
    ).not.toBeInTheDocument()
  })

  it('keep the register entry hidden when signup itself is closed', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList(
          [{ name: 'password', kind: 'form' }],
          { signup: false, signupPassword: true },
        )}
      />,
    )
    expect(
      screen.queryByRole('button', { name: zhCN.mode.registerEntry }),
    ).not.toBeInTheDocument()
  })

  it('keep an enterprise provider disabled while an async start gate is pending', async () => {
    const harness = makeHarness({
      [SSO_DISCOVER]: () =>
        ssoProviders([{ id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' }]),
    })
    let release: (allowed: boolean) => void = () => {}
    const beforeStart = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          release = resolve
        }),
    )
    renderWithProviders(
      <SignInScreen
        session={harness.session}
        discovery={methodList([
          { name: 'oidc', kind: 'enterprise', protocol: 'oidc' },
        ])}
        beforeStart={beforeStart}
      />,
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.enterprise.emailLabel), MAIL)
    await user.click(
      screen.getByRole('button', { name: zhCN.enterprise.continue }),
    )
    const provider = await screen.findByRole('button', { name: 'Acme Okta' })
    await user.click(provider)
    await waitFor(() => expect(beforeStart).toHaveBeenCalledTimes(1))
    expect(provider).toHaveAttribute('aria-disabled', 'true')
    expect(provider).toHaveAttribute('aria-busy', 'true')
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows'),
    ).toHaveLength(0)
    await act(async () => {
      release(true)
    })
    await waitFor(() =>
      expect(
        harness.calls.filter((call) => call.path === '/api/v1/auth/flows'),
      ).toHaveLength(1),
    )
  })
})
