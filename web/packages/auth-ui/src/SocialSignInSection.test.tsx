/**
 * SocialSignInSection behaviour: each configured provider renders its
 * bundle name and, on click, begins that method's login flow (asserted on
 * method, path and request body -- the provider is the flow's method and
 * the host callback route travels as the flow's return_to) and reports
 * the flow's redirect URL upward through onAuthorizeUrl; the section
 * itself never navigates. A refused start renders the answer's code text
 * in one alert and reports nothing; while one request is in flight its
 * button is busy -- focusable, announced unavailable and busy, with no
 * native disable -- and the others stay live. The en-US bundle renders on
 * an English-starting instance and the tree passes axe. Text expectations
 * read the bundle values, never inline language.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SocialSignInSection } from './SocialSignInSection.js'
import type { SocialProviderConfig } from './SocialSignInSection.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  LOGIN_START,
  apiError,
  makeHarness,
  openFlow,
} from '@speed/test-utils/session-harness'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const CALLBACK_URI = '/api/v1/auth/social/callback'

const PROVIDERS: readonly SocialProviderConfig[] = [
  { provider: 'google', redirectUri: CALLBACK_URI },
  { provider: 'github', redirectUri: CALLBACK_URI },
]

const GOOGLE_AUTH_URL = 'https://accounts.google.com/o/oauth2/v2/auth?client_id=c1'
const GITHUB_AUTH_URL = 'https://github.com/login/oauth/authorize'

function redirectFlow(url: string) {
  return openFlow({
    next: { kind: 'redirect', url, method: 'GET' },
  })
}

/** The clicked provider's flight is in flight: the button stays focusable
 * and announces its state instead of taking a native disable. */
function expectBusy(button: HTMLElement): void {
  expect(button).toHaveAttribute('aria-disabled', 'true')
  expect(button).toHaveAttribute('aria-busy', 'true')
  expect(button).not.toHaveAttribute('disabled')
  // Busy states give no reason: the flight is the button's own.
  expect(button).toHaveAccessibleDescription('')
}

/** A provider with no flight of its own stays live: another provider's
 * write never marks it unavailable. */
function expectLive(button: HTMLElement): void {
  expect(button).not.toHaveAttribute('aria-disabled', 'true')
  expect(button).not.toHaveAttribute('aria-busy', 'true')
  expect(button).not.toHaveAttribute('disabled')
  expect(button).toHaveAccessibleDescription('')
}

describe('SocialSignInSection', () => {
  it('begin one login flow per clicked provider and report its redirect URL upward', async () => {
    const harness = makeHarness({
      [LOGIN_START]: (call) => {
        const method = (call.options?.body as { method?: string }).method
        return method === 'github' ? redirectFlow(GITHUB_AUTH_URL) : redirectFlow(GOOGLE_AUTH_URL)
      },
    })
    const onAuthorizeUrl = vi.fn()
    renderWithProviders(
      <SocialSignInSection
        session={harness.session}
        providers={PROVIDERS}
        onAuthorizeUrl={onAuthorizeUrl}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.social.provider.google }),
    )
    await waitFor(() => expect(onAuthorizeUrl).toHaveBeenCalledTimes(1))
    expect(onAuthorizeUrl).toHaveBeenCalledWith('google', GOOGLE_AUTH_URL)
    await user.click(
      screen.getByRole('button', { name: zhCN.social.provider.github }),
    )
    await waitFor(() => expect(onAuthorizeUrl).toHaveBeenCalledTimes(2))
    expect(onAuthorizeUrl).toHaveBeenLastCalledWith('github', GITHUB_AUTH_URL)
    expect(harness.calls).toHaveLength(2)
    expect(harness.calls[0]?.method).toBe('POST')
    expect(harness.calls[0]?.path).toBe('/api/v1/auth/flows')
    // The provider is the flow's method; the host callback route is the
    // flow's return_to (the server builds the provider's own redirect_uri).
    expect(harness.calls[0]?.options?.body).toMatchObject({
      method: 'google',
      delivery: 'session',
      binding: 'cookie',
      return_to: CALLBACK_URI,
    })
    expect(harness.calls[1]?.options?.body).toMatchObject({
      method: 'github',
    })
  })

  it('name no token delivery on the start -- the session delivery is auth-core’s', async () => {
    // auth-core forces the cookie session (`delivery: 'session'`) on
    // startFlow, so the section names no delivery of its own. A `'token'`
    // here would be dead today and a live contradiction to the cookie model
    // the moment that override lifted.
    const harness = makeHarness({
      [LOGIN_START]: () => redirectFlow(GOOGLE_AUTH_URL),
    })
    const startFlow = vi.spyOn(harness.session, 'startFlow')
    renderWithProviders(
      <SocialSignInSection session={harness.session} providers={PROVIDERS} />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.social.provider.google }),
    )
    await waitFor(() => expect(startFlow).toHaveBeenCalledTimes(1))
    expect(startFlow.mock.calls[0]?.[0]?.delivery).not.toBe('token')
  })

  it('render a refused start in one alert and report nothing', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => {
        throw apiError(403, 'authn.method_disabled')
      },
    })
    const onAuthorizeUrl = vi.fn()
    renderWithProviders(
      <SocialSignInSection
        session={harness.session}
        providers={PROVIDERS}
        onAuthorizeUrl={onAuthorizeUrl}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.social.provider.google }),
    )
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.method_disabled,
      ),
    )
    expect(onAuthorizeUrl).not.toHaveBeenCalled()
    expect(harness.calls).toHaveLength(1)
  })

  it('render the unexpected-answer code when the start is not a redirect step', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => openFlow(),
    })
    const onAuthorizeUrl = vi.fn()
    renderWithProviders(
      <SocialSignInSection
        session={harness.session}
        providers={PROVIDERS}
        onAuthorizeUrl={onAuthorizeUrl}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.social.provider.google }),
    )
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.client.protocol,
      ),
    )
    expect(onAuthorizeUrl).not.toHaveBeenCalled()
  })

  it('mark only the clicked provider button busy while its flow is in flight', async () => {
    let resolveStart: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () =>
        new Promise((resolve) => {
          resolveStart = resolve
        }),
    })
    renderWithProviders(
      <SocialSignInSection session={harness.session} providers={PROVIDERS} />,
    )
    const user = userEvent.setup()
    const google = screen.getByRole('button', {
      name: zhCN.social.provider.google,
    })
    const github = screen.getByRole('button', {
      name: zhCN.social.provider.github,
    })
    await user.click(google)
    await waitFor(() => expectBusy(google))
    expectLive(github)
    resolveStart(redirectFlow(GOOGLE_AUTH_URL))
    await waitFor(() => expectLive(google))
  })

  it('keep focus on the clicked provider button while its flow is in flight and swallow a repeat activation', async () => {
    let rejectStart: (error: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_START]: () =>
        new Promise((_resolve, reject) => {
          rejectStart = reject
        }),
    })
    renderWithProviders(
      <SocialSignInSection session={harness.session} providers={PROVIDERS} />,
    )
    const user = userEvent.setup()
    const google = screen.getByRole('button', {
      name: zhCN.social.provider.google,
    })
    await user.click(google)
    // Wait for the flight to be in flight -- whichever state the button
    // uses for it -- so the focus assertion runs against the in-flight
    // control, not the idle one.
    await waitFor(() => {
      expect(
        google.hasAttribute('disabled') ||
          google.getAttribute('aria-disabled') === 'true',
      ).toBe(true)
    })
    // The initiating control keeps the keyboard user's place for the whole
    // flight instead of dropping it when the button becomes unavailable.
    expect(google).toHaveFocus()
    // A second activation of the busy button -- pointer or Enter -- sends
    // nothing: the flight already running owns this provider.
    await user.click(google)
    await user.keyboard('{Enter}')
    expect(harness.calls).toHaveLength(1)
    await act(async () => {
      rejectStart(apiError(403, 'authn.method_disabled'))
    })
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.method_disabled,
      ),
    )
    // The refusal settles in place: the banner appears, the button returns
    // live, and the viewer's focus never left the control they started it
    // from.
    expect(google).toHaveFocus()
    expectLive(google)
  })

  it('render the section title and the en-US provider names on an English-starting instance', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => redirectFlow(GOOGLE_AUTH_URL),
    })
    renderWithProviders(
      <SocialSignInSection session={harness.session} providers={PROVIDERS} />,
      { language: 'en-US' },
    )
    expect(screen.getByText(enUS.social.title)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: enUS.social.provider.google }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: enUS.social.provider.github }),
    ).toBeInTheDocument()
  })

  it('pass axe with no violations', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => redirectFlow(GOOGLE_AUTH_URL),
    })
    // Rendered under the page h1 a real host page supplies: the section
    // renders no heading of its own, and page-has-heading-one is
    // determinate in jsdom now (see the axe helper header) -- a scan
    // without that context fails instead of passing by indeterminacy.
    renderWithProviders(
      <div>
        <h1>Sign in</h1>
        <SocialSignInSection session={harness.session} providers={PROVIDERS} />
      </div>,
    )
    await expectNoAxeViolations()
  })

  it('contain a throwing onAuthorizeUrl: the built URL never looks like a failure', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => redirectFlow(GOOGLE_AUTH_URL),
    })
    const onAuthorizeUrl = vi.fn(() => {
      throw new Error('host navigation failed')
    })
    renderWithProviders(
      <SocialSignInSection
        session={harness.session}
        providers={PROVIDERS}
        onAuthorizeUrl={onAuthorizeUrl}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.social.provider.google }),
    )
    await waitFor(() => expect(onAuthorizeUrl).toHaveBeenCalledTimes(1))
    expect(onAuthorizeUrl).toHaveBeenCalledWith('google', GOOGLE_AUTH_URL)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.calls).toHaveLength(1)
  })

  it('keep both overlapping attempts locked until each settles on its own', async () => {
    // Two provider attempts in flight at once: each button must stay
    // locked until ITS OWN attempt settles -- an earlier return must
    // not re-enable the later provider's button mid-flight.
    let resolveGoogle: (value: unknown) => void = () => {}
    let resolveGithub: (value: unknown) => void = () => {}
    const googleGate = new Promise((resolve) => {
      resolveGoogle = resolve
    })
    const githubGate = new Promise((resolve) => {
      resolveGithub = resolve
    })
    const harness = makeHarness({
      [LOGIN_START]: (call) =>
        (call.options?.body as { method?: string }).method === 'github'
          ? (githubGate as Promise<never>)
          : (googleGate as Promise<never>),
    })
    renderWithProviders(
      <SocialSignInSection session={harness.session} providers={PROVIDERS} />,
    )
    const user = userEvent.setup()
    const google = screen.getByRole('button', {
      name: zhCN.social.provider.google,
    })
    const github = screen.getByRole('button', {
      name: zhCN.social.provider.github,
    })
    await user.click(google)
    await waitFor(() => expectBusy(google))
    await user.click(github)
    // Both in flight: both buttons busy.
    await waitFor(() => expectBusy(github))
    expectBusy(google)
    // The earlier attempt settles first: its own button returns live,
    // the still-pending later one stays busy.
    resolveGoogle(redirectFlow(GOOGLE_AUTH_URL))
    await waitFor(() => expectLive(google))
    expectBusy(github)
    // The later attempt settles: its button returns live too.
    resolveGithub(redirectFlow(GITHUB_AUTH_URL))
    await waitFor(() => expectLive(github))
    expect(harness.calls).toHaveLength(2)
  })

  it('never paint an earlier attempt failure over a newer attempt success', async () => {
    // Google's flight started first and fails only after the newer
    // GitHub attempt is in flight; GitHub then succeeds. The section's
    // failure banner must not show the stale Google failure after the
    // GitHub success -- a failure can only belong to the newest
    // attempt.
    let rejectGoogle: (error: unknown) => void = () => {}
    let resolveGithub: (value: unknown) => void = () => {}
    const googleGate = new Promise((_resolve, reject) => {
      rejectGoogle = reject
    })
    const githubGate = new Promise((resolve) => {
      resolveGithub = resolve
    })
    const harness = makeHarness({
      [LOGIN_START]: (call) =>
        (call.options?.body as { method?: string }).method === 'github'
          ? (githubGate as Promise<never>)
          : (googleGate as Promise<never>),
    })
    const onAuthorizeUrl = vi.fn()
    renderWithProviders(
      <SocialSignInSection
        session={harness.session}
        providers={PROVIDERS}
        onAuthorizeUrl={onAuthorizeUrl}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.social.provider.google }),
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.social.provider.github }),
    )
    // The older Google flight fails while GitHub is still in flight;
    // GitHub then succeeds.
    await act(async () => {
      rejectGoogle(apiError(403, 'authn.method_disabled'))
    })
    await act(async () => {
      resolveGithub(redirectFlow(GITHUB_AUTH_URL))
    })
    await waitFor(() => expect(onAuthorizeUrl).toHaveBeenCalledTimes(1))
    expect(onAuthorizeUrl).toHaveBeenCalledWith('github', GITHUB_AUTH_URL)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.calls).toHaveLength(2)
  })

  it('consult the host’s start gate: a refusal begins no flow and renders no error', async () => {
    const harness = makeHarness({})
    const beforeStart = vi.fn(() => false)
    const onAuthorizeUrl = vi.fn()
    renderWithProviders(
      <SocialSignInSection
        session={harness.session}
        providers={PROVIDERS}
        onAuthorizeUrl={onAuthorizeUrl}
        beforeStart={beforeStart}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.social.provider.google }),
    )
    await waitFor(() =>
      expect(beforeStart).toHaveBeenCalledWith({ method: 'google' }),
    )
    expect(harness.calls).toHaveLength(0)
    expect(onAuthorizeUrl).not.toHaveBeenCalled()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('keep the provider button busy while an async start gate is pending', async () => {
    const harness = makeHarness({
      [LOGIN_START]: () => redirectFlow(GOOGLE_AUTH_URL),
    })
    let release: (allowed: boolean) => void = () => {}
    const beforeStart = vi.fn(
      () =>
        new Promise<boolean>((resolve) => {
          release = resolve
        }),
    )
    const onAuthorizeUrl = vi.fn()
    renderWithProviders(
      <SocialSignInSection
        session={harness.session}
        providers={PROVIDERS}
        onAuthorizeUrl={onAuthorizeUrl}
        beforeStart={beforeStart}
      />,
    )
    const user = userEvent.setup()
    const button = screen.getByRole('button', {
      name: zhCN.social.provider.google,
    })
    await user.click(button)
    await waitFor(() => expect(beforeStart).toHaveBeenCalledTimes(1))
    // The gate has not answered yet: its own provider button is busy, so
    // a second click cannot pass the same gate and start another flow.
    expectBusy(button)
    expect(harness.calls).toHaveLength(0)
    await act(async () => {
      release(true)
    })
    await waitFor(() => expect(onAuthorizeUrl).toHaveBeenCalledTimes(1))
    expect(harness.calls).toHaveLength(1)
  })

  it('render a provider’s mark from its config, else from the host lookup', () => {
    const harness = makeHarness({})
    renderWithProviders(
      <SocialSignInSection
        session={harness.session}
        providers={[
          {
            provider: 'google',
            redirectUri: CALLBACK_URI,
            icon: <span data-testid="mark-config" />,
          },
          { provider: 'github', redirectUri: CALLBACK_URI },
        ]}
        providerIcon={(provider) => (
          <span data-testid={`mark-lookup-${provider}`} />
        )}
      />,
    )
    // The config's own icon wins; the lookup covers the rest.
    expect(
      screen
        .getByRole('button', { name: zhCN.social.provider.google })
        .querySelector('[data-testid="mark-config"]'),
    ).not.toBeNull()
    expect(
      screen
        .getByRole('button', { name: zhCN.social.provider.github })
        .querySelector('[data-testid="mark-lookup-github"]'),
    ).not.toBeNull()
  })
})
