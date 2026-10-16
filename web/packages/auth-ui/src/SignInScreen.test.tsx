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
import { screen, waitFor } from '@testing-library/react'
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
    expect(harness.store.get()).toBe('access-1')
    // The password flow started with the password method.
    expect(harness.calls[0]?.options?.body).toMatchObject({ method: 'password' })
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

  it('render the passkey button and drive its ceremony step to a sign-in', async () => {
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
    // The ceremony step renders its own answer field.
    const credential = await screen.findByLabelText(zhCN.signInFlow.credentialLabel)
    await user.type(credential, 'attestation-response')
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(harness.store.get()).toBe('access-1')
    expect(harness.calls[0]?.options?.body).toMatchObject({ method: 'passkey' })
    // The passkey button names no token delivery of its own: auth-core
    // forces the cookie session.
    expect(startFlow.mock.calls[0]?.[0]?.delivery).not.toBe('token')
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
