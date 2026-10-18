/**
 * SignInScreen behaviour: the surface renders the deployment's login
 * methods, read from the discovery document (injected here so no network
 * is needed), grouped by kind -- forms behind a tab strip (a single form
 * without one), redirect methods as buttons, the enterprise email-first
 * entry, the passkey button and its ceremony step. The discovery read's
 * loading and failure states, the register entry's visibility rule, the
 * deprecated channel override, the unknown-kind / unknown-name fallbacks,
 * the en-US bundle and an axe pass round it out. Text expectations read
 * the bundle values, never inline language.
 */

import { describe, expect, it, vi } from 'vitest'
import { flushSync } from 'react-dom'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SignInScreen } from './SignInScreen.js'
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
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const PASS = 's3cret-pass'
const MAIL = 'alice@example.com'

/** A discovery document with both form channels. */
const BOTH_FORMS = methodList([
  { name: 'password', kind: 'form' },
  { name: 'sms', kind: 'form' },
])

/** A discovery document with the passkey method alone, so the ceremony
 * step is the surface under test. */
const PASSKEY_ONLY = methodList([
  { name: 'passkey', kind: 'webauthn', label: 'Passkey' },
])

/** Replace navigator.credentials for one case; the result restores the
 * property. */
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

/** A credential shaped as navigator.credentials.get returns one. */
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

/** A harness whose passkey flow opens on its assertion step. No answer is
 * scripted: the cases below stop before one travels. */
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

/** A harness that completes a password flow and drives the SMS channel's
 * two steps, dispatching on the request method and step. */
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
    // The document's forms gather behind the strip; the password channel
    // is selected first and no read was fired (the document was injected).
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
    // The screen reports the mode it presented and the channel's own name.
    expect(onSignedIn).toHaveBeenCalledWith({
      mode: 'sign-in',
      channel: 'password',
    })
    expect(harness.store.get()).toBe('access-1')
    // The password flow started with the password method.
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
    // The register entry starts the same SMS flow; the mode travels with
    // the completion, and the channel is the method that drove it.
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
    // A tablist with nothing to switch between is not a choice.
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
    // The failure renders the namespace text for the code, retryable.
    expect(
      await screen.findByText(zhCN.errors.authn.unavailable),
    ).toBeInTheDocument()
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.discovery.retry }),
    )
    // The retry reads once more and the surface renders the forms.
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
    // The brand label from the descriptor names the button.
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
    // One brand reaches the deployment through two sign-in modes, and both
    // descriptors carry the same brand label. Each button renders the
    // mode's own wording, which is also its accessible name.
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
        social={{ returnTo: '/auth/sso/callback', onAuthorizeUrl }}
      />,
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.enterprise.emailLabel), MAIL)
    await user.click(
      screen.getByRole('button', { name: zhCN.enterprise.continue }),
    )
    // The discovered provider renders as its own button.
    await user.click(await screen.findByRole('button', { name: 'Acme Okta' }))
    await waitFor(() => expect(onAuthorizeUrl).toHaveBeenCalledTimes(1))
    expect(onAuthorizeUrl).toHaveBeenCalledWith('oidc', 'https://idp.example/auth')
    // The flow started with the provider's protocol and id, returning to
    // the host's callback route.
    const start = harness.calls.find((call) => call.path === '/auth/flows')
    expect(start?.options?.body).toMatchObject({
      method: 'oidc',
      input: { provider: 'idp-1' },
      return_to: '/auth/sso/callback',
    })
    // The entry names no token delivery of its own: auth-core forces the
    // cookie session (`delivery: 'session'`).
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
    // The ceremony is the browser's own API: stub the one method an
    // assertion step reaches and hand back a credential shaped as
    // navigator.credentials.get returns one.
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
      // The ceremony step drives navigator.credentials.get against the
      // step's options and reports the credential it returns.
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      )
      await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    } finally {
      if (original === undefined) {
        delete (window.navigator as { credentials?: unknown }).credentials
      } else {
        Object.defineProperty(window.navigator, 'credentials', original)
      }
    }
    expect(get).toHaveBeenCalledTimes(1)
    // The answer carries the serialized credential under the flow's
    // first-factor step, not a pasted free-text value.
    const answered = harness.calls.find(
      (call) =>
        call.path === '/auth/flows/answer' &&
        (call.options?.body as { step?: string }).step === 'answer',
    )
    expect(answered?.options?.body).toMatchObject({
      step: 'answer',
      input: { credential: expect.stringContaining('AQID') },
    })
    expect(harness.store.get()).toBe('access-1')
    expect(harness.calls[0]?.options?.body).toMatchObject({ method: 'passkey' })
    // The passkey button names no token delivery of its own: auth-core
    // forces the cookie session.
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
    // A browser without WebAuthn: the property is absent, as it is on a
    // page the credential API does not reach.
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
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      )
      expect(
        await screen.findByText(zhCN.signInFlow.passkeyUnsupported),
      ).toBeInTheDocument()
    } finally {
      if (original !== undefined) {
        Object.defineProperty(window.navigator, 'credentials', original)
      }
    }
    // No answer travelled: the browser could not run the ceremony.
    expect(
      harness.calls.find((call) => call.path === '/auth/flows/answer'),
    ).toBeUndefined()
  })

  it('show the waiting message and a cancel control while the ceremony is pending', async () => {
    const harness = makePasskeyHarness()
    // A ceremony that stays pending, as one does on a page with no
    // authenticator to answer it.
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
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      )
      expect(
        await screen.findByText(zhCN.signInFlow.passkeyWaiting),
      ).toBeInTheDocument()
      // The cancel control is interactive, and the Continue action gave way
      // to the waiting state rather than sitting there disabled.
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
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      )
      await screen.findByText(zhCN.signInFlow.passkeyWaiting)
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyCancel }),
      )
      // The failure renders with the click, not when the ceremony settles:
      // the stubbed ceremony never settles, and the assertions below are
      // read synchronously.
      expect(
        screen.getByText(zhCN.signInFlow.passkeyCancelled),
      ).toBeInTheDocument()
      expect(
        screen.queryByText(zhCN.signInFlow.passkeyWaiting),
      ).not.toBeInTheDocument()
      // The abandoned call was handed an abort signal, so a browser that
      // honours it closes its own prompt instead of leaving it behind.
      const first = get.mock.calls[0]?.[0] as { signal?: AbortSignal }
      expect(first.signal?.aborted).toBe(true)
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyRetry }),
      )
      // Retry re-issues the same step's ceremony under its own signal.
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
    // Neither the cancelled ceremony nor its retry answered the flow.
    expect(
      harness.calls.find((call) => call.path === '/auth/flows/answer'),
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
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      )
      expect(
        await screen.findByText(zhCN.signInFlow.passkeyCancelled),
      ).toBeInTheDocument()
      // The failure offers the way out: a retry, not a dead end.
      expect(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyRetry }),
      ).toBeEnabled()
    } finally {
      restore()
    }
    expect(
      harness.calls.find((call) => call.path === '/auth/flows/answer'),
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
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      )
      await screen.findByText(zhCN.signInFlow.passkeyWaiting)
      const signal = (
        get.mock.calls[0]?.[0] as { signal?: AbortSignal } | undefined
      )?.signal
      unmount()
      // The surface abandoned the ceremony with itself: the browser was
      // asked to give it up, and the attempt no longer matches.
      expect(signal?.aborted).toBe(true)
      await act(async () => {
        resolveGet(assertionCredential())
        await new Promise((resolve) => setTimeout(resolve, 0))
      })
      // The late success answered nothing and completed nothing for the
      // screen the viewer already left.
      expect(
        harness.calls.find((call) => call.path === '/auth/flows/answer'),
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
      act(() => {
        flushSync(() => {
          screen
            .getByRole('button', { name: zhCN.signInFlow.submit })
            .click()
        })
        // The region is born empty: a live region announces only content
        // changes that follow its own insertion, and the text fills the
        // existing region a commit later.
        expect(screen.getByRole('status').textContent).toBe('')
      })
      await waitFor(() =>
        expect(screen.getByRole('status')).toHaveTextContent(
          zhCN.signInFlow.passkeyWaiting,
        ),
      )
      // The Continue button that had focus unmounted with the phase; the
      // control the waiting state offers has it now.
      expect(
        screen.getByRole('button', { name: zhCN.signInFlow.passkeyCancel }),
      ).toHaveFocus()
    } finally {
      restore()
    }
    expect(get).toHaveBeenCalledTimes(1)
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
    // A creation ceremony registers a passkey; the prompt says so instead
    // of claiming a sign-in.
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
    // The entry appears (signup on and an account-opening method present)
    // and toggles to the register side and back.
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
    // Register mode shows the title, the mode toggle and the selected
    // channel's actions at once. No two buttons answer to one name: the
    // toggle says which mode it returns to, and each channel action keeps
    // its own.
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
    // The code step's submit is "Sign in"; the toggle that leaves the mode
    // is not, so the two never collide.
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
    // The password method opens no account, so the register strip drops it;
    // the first remaining channel -- SMS, in the document's own order -- is
    // selected without the viewer choosing anything.
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
    // The password channel returns with the sign-in mode's own default.
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
    // Register mode states its own title and asks about the account the
    // viewer already has, rather than offering a bare "sign in".
    expect(
      screen.getByRole('heading', { name: zhCN.mode.registerTitle }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.mode.signInEntry }),
    ).toBeInTheDocument()
    expect(enUS.mode.signInEntry).toBe('Already have an account? Sign in')
    expect(zhCN.mode.signInEntry).toBe('已有账户？登录')
  })

  it('say in sign-in mode that an unregistered number creates an account', () => {
    const harness = makeHarness({})
    // The sentence is the same for every number: no placeholder to fill in,
    // so it cannot reveal whether a number is registered.
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
    // An account-opening channel is on screen: the sentence sits below it.
    expect(screen.getByText(zhCN.mode.signInAccountHint)).toBeInTheDocument()
    unmount()

    // No account-opening channel: the sentence would be false, so it is
    // absent along with the register entry.
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

  it('state the register hint conditionally, not for every listed method', () => {
    // The password method does not open accounts, and it can be the tab the
    // hint sits above, so the hint must not claim that every listed method
    // creates an account.
    expect(zhCN.mode.registerHint).not.toMatch(/任一|每种|所有|这些方式/)
    expect(enUS.mode.registerHint).not.toMatch(
      /one of these|any of these|every/i,
    )
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
    // The account-opening SMS form is narrowed away, so no rendered channel
    // can open an account and the entry is absent.
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
    // An unknown kind renders nothing, so it cannot make the entry visible.
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
    // A known field shows translated wording, an unknown one its own name.
    expect(
      screen.getByLabelText(zhCN.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
    expect(screen.getByLabelText('token')).toBeInTheDocument()
  })

  it('name no token delivery from a descriptor-driven form -- the session delivery is auth-core’s', async () => {
    // A `form` method with no bespoke component is driven by its descriptor;
    // its start names no token delivery of its own (auth-core forces the
    // cookie session), like every other channel.
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
    // Both channels render the same element type at the same position, so
    // the reset comes from the panel's key: beta's form is a fresh
    // instance, not alpha's instance re-rendered with half-typed state.
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
    // Alpha's flow parks on its own challenge step.
    await screen.findByLabelText('alpha_code')
    await user.click(screen.getByRole('tab', { name: 'Beta' }))
    // The parked alpha step went with the unmounted alpha form; beta's
    // first step is the surface.
    expect(screen.queryByLabelText('alpha_code')).not.toBeInTheDocument()
    const recovery = screen.getByLabelText('recovery')
    expect(recovery).toHaveValue('')
    await user.type(recovery, 'fresh-value')
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )
    await waitFor(() => expect(harness.calls).toHaveLength(2))
    // Beta's submit carries beta's field only: alpha's value did not ride
    // along.
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
    // The override drops the SMS channel: one form, no tab strip.
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
})
