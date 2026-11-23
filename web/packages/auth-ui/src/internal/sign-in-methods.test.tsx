/**
 * EnterpriseSignIn's discovery notices: the entry's one notice region
 * stands for its whole life, born empty, and each discovery result fills
 * the existing node. A live region announces only content changes that
 * follow its own insertion, so a notice that mounted together with its
 * region -- the no-match notice, or the providers title -- would be
 * silent. Text expectations read the bundle values, never inline
 * language.
 */

import { describe, expect, it } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  DescriptorSignInForm,
  EnterpriseSignIn,
  PasskeySignIn,
} from './sign-in-methods.js'
import { renderWithProviders } from '../../test-utils/render.js'
import {
  CAPTCHA_CHALLENGE,
  LOGIN_START,
  SSO_DISCOVER,
  apiError,
  captchaChallenge,
  makeHarness,
  openFlow,
  ssoProviders,
} from '@speed/test-utils/session-harness'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import type { AuthnFlowState, AuthnHumanGate } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import uiKitZhCN from '../../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }

const MAIL = 'nobody@example.com'

/** A harness whose SSO discovery answers with the given providers. */
function harnessFor(
  providers: readonly { id: string; name: string; protocol: string }[],
) {
  return makeHarness({ [SSO_DISCOVER]: () => ssoProviders(providers) })
}

/** Submit an email through the entry's first step. */
async function discoverEmail(): Promise<void> {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText(zhCN.enterprise.emailLabel), MAIL)
  await user.click(
    screen.getByRole('button', { name: zhCN.enterprise.continue }),
  )
}

/** The status region whose text is `text`, once one carries it. */
async function findNotice(text: string): Promise<HTMLElement> {
  let notice: HTMLElement | undefined
  await waitFor(() => {
    notice = screen
      .getAllByRole('status')
      .find((region) => region.textContent === text)
    expect(notice).toBeDefined()
  })
  return notice as HTMLElement
}

/** The failure banner's sentence naming a whole-second wait. */
function waitSentence(seconds: number): string {
  const [before = '', after = ''] = zhCN.throttle.retryAfter.split('{{seconds}}')
  return `${before}${seconds}${after}`
}

/** The start requests the harness received. */
function starts(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter((call) => call.path === '/api/v1/auth/flows')
}

/** A gate that requires no proof on either surface. */
const NO_GATE: AuthnHumanGate = {
  phone: { required: false, provider: 'off' },
  other: { required: false, provider: 'off' },
}

/** A request the test settles by hand, so the in-flight window can be
 * inspected before it lands. */
function heldRequest<T>(): {
  readonly promise: Promise<T>
  readonly refuse: (error: unknown) => void
} {
  let refuse: ((error: unknown) => void) | null = null
  const promise = new Promise<T>((_, reject) => {
    refuse = reject
  })
  return {
    promise,
    refuse: (error) => {
      refuse?.(error)
    },
  }
}

describe('EnterpriseSignIn throttled answers', () => {
  it('name the wait of a throttled discovery in the entry banner', async () => {
    const harness = makeHarness({
      [SSO_DISCOVER]: () => {
        throw apiError(429, 'authn.throttled', 3000)
      },
    })
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    await discoverEmail()
    const alert = await screen.findByRole('alert')
    // The wait the answer stated is named, not a bare "try later".
    expect(alert).toHaveTextContent(waitSentence(3))
  })

  it('name the wait of a throttled provider start', async () => {
    const harness = makeHarness({
      [SSO_DISCOVER]: () =>
        ssoProviders([{ id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' }]),
      [LOGIN_START]: () => {
        throw apiError(429, 'authn.throttled', 5000)
      },
    })
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    await discoverEmail()
    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: 'Acme Okta' }),
    )
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(waitSentence(5))
  })
})

describe('EnterpriseSignIn discovery notices', () => {
  it('fill a status region that stood empty with the no-match notice', async () => {
    const harness = harnessFor([])
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    // The regions in the tree before the interaction are silent: the
    // notice must fill one of them, not mount with its text.
    const before = screen.getAllByRole('status')
    expect(before.every((region) => region.textContent === '')).toBe(true)
    await discoverEmail()
    const notice = await findNotice(zhCN.enterprise.noProviders)
    // The node that announced the result was already in the tree.
    expect(before).toContain(notice)
    expect(screen.getAllByRole('status')).toContain(notice)
  })

  it('fill the same region with the providers title', async () => {
    const harness = harnessFor([
      { id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' },
    ])
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    const before = screen.getAllByRole('status')
    expect(before.every((region) => region.textContent === '')).toBe(true)
    await discoverEmail()
    const notice = await findNotice(zhCN.enterprise.providersTitle)
    expect(before).toContain(notice)
    expect(
      screen.getByRole('button', { name: 'Acme Okta' }),
    ).toBeInTheDocument()
  })

  it('empties the notice again when the viewer returns to the email field', async () => {
    // A repeated identical result must be a change, not a rewrite: going
    // back to the email field clears the region so the next discovery's
    // notice is announced again.
    const harness = harnessFor([])
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    await discoverEmail()
    const notice = await findNotice(zhCN.enterprise.noProviders)
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.enterprise.editEmail }),
    )
    await waitFor(() => expect(notice.textContent).toBe(''))
  })
})

/** A reply the test settles by hand, so a window can be inspected before it
 * lands. */
function heldReply<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let settle: ((value: T) => void) | null = null
  const promise = new Promise<T>((resolve) => {
    settle = resolve
  })
  return {
    promise,
    resolve: (value) => {
      settle?.(value)
    },
  }
}

describe('EnterpriseSignIn focus when the email step is replaced', () => {
  it('lands on the first provider button when the discovery lists providers', async () => {
    const harness = harnessFor([
      { id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' },
      { id: 'idp-2', name: 'Acme Azure', protocol: 'oidc' },
    ])
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    await discoverEmail()
    const first = await screen.findByRole('button', { name: 'Acme Okta' })
    // The focused continue button unmounted with the form; the new surface's
    // first control takes the focus it left behind instead of the body.
    await waitFor(() => expect(first).toHaveFocus())
  })

  it('lands on the way back to the email field when nothing matched', async () => {
    const harness = harnessFor([])
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    await discoverEmail()
    const edit = await screen.findByRole('button', {
      name: zhCN.enterprise.editEmail,
    })
    await waitFor(() => expect(edit).toHaveFocus())
  })

  it('lands on the email field when the viewer returns to the form', async () => {
    const harness = harnessFor([])
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    await discoverEmail()
    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: zhCN.enterprise.editEmail }),
    )
    const email = await screen.findByLabelText(zhCN.enterprise.emailLabel)
    await waitFor(() => expect(email).toHaveFocus())
  })

  it('leaves focus where the viewer moved it when the discovery lands', async () => {
    const held = heldReply<ReturnType<typeof ssoProviders>>()
    const harness = makeHarness({ [SSO_DISCOVER]: () => held.promise })
    renderWithProviders(
      <>
        <button type="button">elsewhere</button>
        <EnterpriseSignIn session={harness.session} />
      </>,
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.enterprise.emailLabel), MAIL)
    await user.click(
      screen.getByRole('button', { name: zhCN.enterprise.continue }),
    )
    await waitFor(() =>
      expect(
        harness.calls.filter(
          (call) => call.path === '/api/v1/auth/sso/discover',
        ),
      ).toHaveLength(1),
    )
    const elsewhere = screen.getByRole('button', { name: 'elsewhere' })
    elsewhere.focus()
    await act(async () => {
      held.resolve(
        ssoProviders([{ id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' }]),
      )
    })
    const first = await screen.findByRole('button', { name: 'Acme Okta' })
    expect(elsewhere).toHaveFocus()
    expect(first).not.toHaveFocus()
  })

  it('leaves focus where the viewer moved it when returning to the form', async () => {
    const harness = harnessFor([])
    renderWithProviders(
      <>
        <button type="button">elsewhere</button>
        <EnterpriseSignIn session={harness.session} />
      </>,
    )
    await discoverEmail()
    const edit = await screen.findByRole('button', {
      name: zhCN.enterprise.editEmail,
    })
    const elsewhere = screen.getByRole('button', { name: 'elsewhere' })
    elsewhere.focus()
    // An activation that does not move focus (a programmatic click, or a
    // pointer click in a browser that leaves buttons unfocused): the
    // operator's focus stays where it is, the returning form does not take it.
    fireEvent.click(edit)
    const email = await screen.findByLabelText(zhCN.enterprise.emailLabel)
    expect(elsewhere).toHaveFocus()
    expect(email).not.toHaveFocus()
  })
})

/** A gate whose phone surface runs the captcha and whose other surface is
 * off. */
const PHONE_CAPTCHA: AuthnHumanGate = {
  phone: { required: true, provider: 'captcha' },
  other: { required: false, provider: 'off' },
}

/** A gate whose other surface runs the captcha and whose phone surface is
 * off. */
const OTHER_CAPTCHA: AuthnHumanGate = {
  phone: { required: false, provider: 'off' },
  other: { required: true, provider: 'captcha' },
}

/** A gate whose other surface runs Turnstile and whose phone surface is
 * off. */
const OTHER_TURNSTILE: AuthnHumanGate = {
  phone: { required: false, provider: 'off' },
  other: { required: true, provider: 'turnstile', site_key: 'site-1' },
}

describe('DescriptorSignInForm human-verification surface', () => {
  it('derives the phone surface from a phone field', async () => {
    const harness = makeHarness({
      [CAPTCHA_CHALLENGE]: () => captchaChallenge(),
    })
    renderWithProviders(
      <DescriptorSignInForm
        session={harness.session}
        method="sms-code"
        fields={[{ name: 'phone', kind: 'phone', required: true }]}
        humanGate={PHONE_CAPTCHA}
      />,
    )
    // Entering the form reads and solves nothing.
    expect(
      harness.calls.filter(
        (call) => call.path === '/api/v1/auth/human/captcha/challenge',
      ),
    ).toHaveLength(0)
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.smsSignIn.phoneLabel),
      '13800000001',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )
    // The form's first step sends SMS, so asking to start opens the phone
    // surface's captcha.
    await screen.findByLabelText(
      uiKitZhCN.humanVerification.captcha.answerLabel,
    )
    expect(
      harness.calls.filter(
        (call) => call.path === '/api/v1/auth/human/captcha/challenge',
      ),
    ).toHaveLength(1)
  })

  it('keeps the other surface for a form that does not name a phone', async () => {
    const harness = makeHarness({
      [CAPTCHA_CHALLENGE]: () => captchaChallenge(),
    })
    renderWithProviders(
      <DescriptorSignInForm
        session={harness.session}
        method="email-code"
        fields={[{ name: 'email', kind: 'text', required: true }]}
        humanGate={OTHER_CAPTCHA}
      />,
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.register.emailLabel), MAIL)
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.submit }),
    )
    await screen.findByLabelText(
      uiKitZhCN.humanVerification.captcha.answerLabel,
    )
  })

  it('carries a send surface the host names', async () => {
    const harness = makeHarness({
      [CAPTCHA_CHALLENGE]: () => captchaChallenge(),
    })
    renderWithProviders(
      <DescriptorSignInForm
        session={harness.session}
        method="one-time-code"
        fields={[{ name: 'identifier', kind: 'text', required: true }]}
        humanGate={PHONE_CAPTCHA}
        humanScope="phone"
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
    await screen.findByLabelText(
      uiKitZhCN.humanVerification.captcha.answerLabel,
    )
  })

  it('keeps the submit disabled while the deferred start is in flight', async () => {
    const fake = installFakeTurnstile()
    try {
      let releaseStart: ((reply: AuthnFlowState) => void) | null = null
      const harness = makeHarness({
        [LOGIN_START]: () =>
          new Promise<AuthnFlowState>((resolve) => {
            releaseStart = resolve
          }),
      })
      renderWithProviders(
        <DescriptorSignInForm
          session={harness.session}
          method="email-code"
          fields={[{ name: 'email', kind: 'text', required: true }]}
          humanGate={OTHER_TURNSTILE}
        />,
      )
      const user = userEvent.setup()
      await user.type(screen.getByLabelText(zhCN.register.emailLabel), MAIL)
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      )
      // The start waits on its proof: the submit gives way to the block.
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('token-1')
      })
      // The proof arrived and the deferred start left; its request is still
      // in flight, so the submit that came back must stay busy.
      const submit = await screen.findByRole('button', {
        name: zhCN.signInFlow.submit,
      })
      await waitFor(() =>
        expect(submit).toHaveAttribute('aria-disabled', 'true'),
      )
      expect(submit).toHaveAttribute('aria-busy', 'true')
      // A stray activation cannot begin a second flow while the first
      // start is in flight.
      fireEvent.click(submit)
      expect(
        harness.calls.filter((call) => call.path === '/api/v1/auth/flows'),
      ).toHaveLength(1)
      act(() => {
        releaseStart?.(openFlow())
      })
    } finally {
      fake.restore()
    }
  })
})

describe('sign-in entries keep the initiating control through a write', () => {
  it('keeps focus on the descriptor submit through a refused start', async () => {
    const held = heldRequest<AuthnFlowState>()
    const harness = makeHarness({ [LOGIN_START]: () => held.promise })
    renderWithProviders(
      <DescriptorSignInForm
        session={harness.session}
        method="email-code"
        fields={[{ name: 'email', kind: 'text', required: true }]}
        humanGate={NO_GATE}
      />,
    )
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.register.emailLabel), MAIL)
    const submit = screen.getByRole('button', {
      name: zhCN.signInFlow.submit,
    })
    await user.click(submit)
    await waitFor(() => expect(starts(harness)).toHaveLength(1))
    // The write is in flight: the control that started it keeps focus and
    // reports both states instead of becoming natively disabled.
    expect(submit).toHaveFocus()
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAttribute('aria-busy', 'true')
    await act(async () => {
      held.refuse(apiError(400, 'authn.login_failed'))
    })
    await screen.findByRole('alert')
    // The refusal lands under the same control.
    expect(submit).toHaveFocus()
    expect(submit).not.toHaveAttribute('aria-disabled')
    expect(submit).not.toHaveAttribute('aria-busy')
  })

  it('keeps focus on the enterprise submit through a refused discovery', async () => {
    const held = heldRequest<{ providers: never[] }>()
    const harness = makeHarness({ [SSO_DISCOVER]: () => held.promise })
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.enterprise.emailLabel), MAIL)
    const submit = screen.getByRole('button', {
      name: zhCN.enterprise.continue,
    })
    await user.click(submit)
    await waitFor(() =>
      expect(
        harness.calls.filter((call) => call.path === '/api/v1/auth/sso/discover'),
      ).toHaveLength(1),
    )
    expect(submit).toHaveFocus()
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAttribute('aria-busy', 'true')
    await act(async () => {
      held.refuse(apiError(429, 'authn.throttled', 3000))
    })
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(waitSentence(3))
    expect(submit).toHaveFocus()
    expect(submit).not.toHaveAttribute('aria-disabled')
    expect(submit).not.toHaveAttribute('aria-busy')
  })

  it('keeps focus on the clicked provider and reports its siblings unavailable', async () => {
    const held = heldRequest<AuthnFlowState>()
    const harness = makeHarness({
      [SSO_DISCOVER]: () =>
        ssoProviders([
          { id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' },
          { id: 'idp-2', name: 'Acme Azure', protocol: 'oidc' },
        ]),
      [LOGIN_START]: () => held.promise,
    })
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    await discoverEmail()
    const user = userEvent.setup()
    const clicked = await screen.findByRole('button', { name: 'Acme Okta' })
    const sibling = screen.getByRole('button', { name: 'Acme Azure' })
    const edit = screen.getByRole('button', {
      name: zhCN.enterprise.editEmail,
    })
    await user.click(clicked)
    await waitFor(() => expect(starts(harness)).toHaveLength(1))
    // Only the initiating provider is busy; the rest of the list, and the
    // control that would leave the list, are unavailable without a reason.
    expect(clicked).toHaveFocus()
    expect(clicked).toHaveAttribute('aria-disabled', 'true')
    expect(clicked).toHaveAttribute('aria-busy', 'true')
    expect(sibling).toHaveAttribute('aria-disabled', 'true')
    expect(sibling).not.toHaveAttribute('aria-busy')
    expect(sibling).not.toHaveAccessibleDescription()
    expect(edit).toHaveAttribute('aria-disabled', 'true')
    expect(edit).not.toHaveAttribute('aria-busy')
    expect(edit).not.toHaveAccessibleDescription()
    // Neither a pointer nor a keyboard activation starts a second flow.
    await user.click(sibling)
    sibling.focus()
    await user.keyboard('{Enter}')
    expect(starts(harness)).toHaveLength(1)
    // The viewer moves back to the provider that started the write; the
    // refusal lands under it and leaves the focus where it was.
    clicked.focus()
    await act(async () => {
      held.refuse(apiError(400, 'authn.login_failed'))
    })
    await screen.findByRole('alert')
    expect(clicked).toHaveFocus()
    expect(clicked).not.toHaveAttribute('aria-disabled')
    expect(clicked).not.toHaveAttribute('aria-busy')
    expect(sibling).not.toHaveAttribute('aria-disabled')
  })

  it('keeps focus on the passkey entry through a refused start', async () => {
    const held = heldRequest<AuthnFlowState>()
    const harness = makeHarness({ [LOGIN_START]: () => held.promise })
    renderWithProviders(
      <PasskeySignIn
        session={harness.session}
        method={{ name: 'passkey', kind: 'webauthn', label: 'Passkey' }}
      />,
    )
    const user = userEvent.setup()
    const entry = screen.getByRole('button', { name: 'Passkey' })
    await user.click(entry)
    await waitFor(() => expect(starts(harness)).toHaveLength(1))
    expect(entry).toHaveFocus()
    expect(entry).toHaveAttribute('aria-disabled', 'true')
    expect(entry).toHaveAttribute('aria-busy', 'true')
    await act(async () => {
      held.refuse(apiError(401, 'authn.login_failed'))
    })
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.signInFlow.passkeyRefused)
    expect(entry).toHaveFocus()
    expect(entry).not.toHaveAttribute('aria-disabled')
    expect(entry).not.toHaveAttribute('aria-busy')
  })
})
