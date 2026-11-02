/**
 * FlowStep behaviour: the controlled label vocabulary of a flow step, the
 * TOTP enrolment block and the passkey ceremony's own states.
 *
 * What this suite pins: a challenge field name the family knows renders
 * its translated label, an unknown name renders verbatim; a factor option
 * reads as its kind's translated name, a repeated kind distinguishes its
 * entries by name ("Kind · Name") and an unnamed entry from the second on
 * by ordinal ("Kind (2)"), and an unknown kind shows the kind as given; an
 * enrollment kind follows the same known/unknown rule. The
 * package keeps its vocabulary locally (auth-ui and console-ui keep their
 * own copies), so this suite is the account-ui side of the registered
 * sync obligation. The ceremony cases pin the assertion step starting its
 * ceremony on arrival (also under StrictMode's double mount, where the
 * abandoned first attempt gives way to the second), the waiting state and
 * its cancel control (with the focus the control that opened the step
 * held), the immediate failure a cancel renders and the retry that runs
 * the ceremony again, a registration step waiting for a click, the
 * registration prompt's own wording, the ceremony being abandoned when the
 * step unmounts, the already-registered refusal's own sentence, and the
 * generic failure sentence naming the ceremony it ran. The TOTP cases pin
 * this package's side of the shared panel: the step's URI becoming the
 * symbol, the parser's secret and parameters reaching the manual
 * disclosure, the confirm-only submission, and the symbol and guidance
 * being withheld when the step carries no URI or one the parser refuses.
 * The panel's own contract is covered beside it in @speed/ui-kit.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { configure, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthnStep } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStep } from './flow-step.js'

function render(step: AuthnStep, onSubmit = vi.fn()) {
  const rendered = renderWithProviders(
    <FlowStep
      step={step}
      sequence={1}
      busy={false}
      answerStep="answer"
      submitLabel="Continue"
      onSubmit={onSubmit}
    />,
  )
  return { ...rendered, onSubmit }
}

/** Installs a `navigator.credentials` stub, answering the restorer. */
function installCredentials(credentials: unknown): () => void {
  const original = Object.getOwnPropertyDescriptor(window.navigator, 'credentials')
  Object.defineProperty(window.navigator, 'credentials', {
    value: credentials,
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

/** An assertion credential shaped as the browser's API returns one. */
const ASSERTION_CREDENTIAL = {
  id: 'cred-1',
  rawId: new Uint8Array([1]).buffer,
  type: 'public-key',
  response: {
    clientDataJSON: new Uint8Array([2]).buffer,
    authenticatorData: new Uint8Array([3]).buffer,
    signature: new Uint8Array([4]).buffer,
    userHandle: null,
  },
  getClientExtensionResults: () => ({}),
}

/** A step whose options ask the browser for an assertion. */
const ASSERT_STEP: AuthnStep = {
  kind: 'webauthn',
  options: { challenge: 'Y2hhbGxlbmdl' },
}

/** A step whose options ask the browser for a registration. The `user`
 * member is what tells the ceremony apart from an assertion. */
const REGISTER_STEP: AuthnStep = {
  kind: 'webauthn',
  options: {
    challenge: 'Y2hhbGxlbmdl',
    user: { id: 'AQID', name: 'ada' },
    rp: { id: 'localhost', name: 'demo' },
    pubKeyCredParams: [],
  },
}

describe('FlowStep labels', () => {
  it('translates a known challenge field name and shows an unknown one as given', () => {
    render({
      kind: 'challenge',
      fields: [
        { name: 'code', kind: 'code' },
        { name: 'deployment_field', kind: 'text' },
      ],
    })

    expect(screen.getByLabelText(zhCN.flowStep.codeLabel)).toBeTruthy()
    expect(screen.getByLabelText('deployment_field')).toBeTruthy()
  })

  it('reads each factor as its kind, ignoring a sole factor’s name', () => {
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'totp', label: 'My app' },
        { id: 'f2', kind: 'sms' },
      ],
    })

    // Each kind is offered once, so the kind name is the whole label; the
    // registration-time name does not replace it.
    expect(
      screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.mfa.factor.kind.sms }),
    ).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'My app' })).toBeNull()
  })

  it('labels same-named factors of different kinds by their kinds', () => {
    // Two factors whose registration-time names match must not render two
    // identical buttons: the step answers "how do I prove it", so the kind
    // is the label.
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'webauthn', label: 'Chrome · macOS' },
        { id: 'f2', kind: 'totp', label: 'Chrome · macOS' },
      ],
    })

    expect(
      screen.getByRole('button', { name: zhCN.mfa.factor.kind.webauthn }),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }),
    ).toBeTruthy()
    expect(
      screen.queryAllByRole('button', { name: 'Chrome · macOS' }),
    ).toHaveLength(0)
  })

  it('distinguishes a repeated kind by name and by ordinal', () => {
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'webauthn', label: 'Work MacBook' },
        { id: 'f2', kind: 'webauthn' },
      ],
    })

    const kind = zhCN.mfa.factor.kind.webauthn
    const named = zhCN.mfa.factor.optionNamed
      .replace('{{kind}}', kind)
      .replace('{{name}}', 'Work MacBook')
    const ordinal = zhCN.mfa.factor.optionOrdinal
      .replace('{{kind}}', kind)
      .replace('{{position}}', '2')
    expect(screen.getByRole('button', { name: named })).toBeTruthy()
    expect(screen.getByRole('button', { name: ordinal })).toBeTruthy()
  })

  it('translates known enroll kinds and shows an unknown kind as given', () => {
    render({ kind: 'enroll', kinds: ['webauthn', 'magic'] })

    expect(
      screen.getByRole('button', { name: zhCN.mfa.factor.kind.webauthn }),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: 'magic' })).toBeTruthy()
  })

  it('waits on the arrival ceremony, cancels into a retry, and retries the ceremony', async () => {
    // The assertion ceremony starts on arrival and sits pending in the
    // browser: it shows an explicit waiting state whose cancel control takes
    // the focus the control that opened the step held, and cancel fails at
    // once and offers a retry that drives the ceremony again.
    const get = vi
      .fn()
      // The cancelled attempt never settles; its late answer is irrelevant.
      .mockImplementationOnce(() => new Promise(() => {}))
      .mockImplementationOnce(() => Promise.resolve(ASSERTION_CREDENTIAL))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      const { onSubmit } = render(ASSERT_STEP)
      const user = userEvent.setup()

      expect(screen.getByText(zhCN.flowStep.passkeyPrompt)).toBeTruthy()
      // No click starts the assertion: the step arrival did.
      expect(await screen.findByText(zhCN.flowStep.passkeyWaiting)).toBeTruthy()
      const cancel = screen.getByRole('button', {
        name: zhCN.flowStep.passkeyCancel,
      })
      expect(document.activeElement).toBe(cancel)

      await user.click(cancel)
      expect(await screen.findByRole('alert')).toHaveTextContent(
        zhCN.flowStep.passkeyCancelled,
      )

      // The retry uses the one primary action mapping every action row
      // shares, so the three retry surfaces cannot drift apart.
      const retry = screen.getByRole('button', {
        name: zhCN.flowStep.passkeyRetry,
      })
      expect(retry).toHaveClass('MuiButton-contained')
      await user.click(retry)
      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
      expect(get).toHaveBeenCalledTimes(2)
      expect(onSubmit).toHaveBeenCalledWith({
        step: 'answer',
        input: { credential: expect.stringContaining('cred-1') },
      })
    } finally {
      restore()
    }
  })

  it('starts an assertion ceremony on arrival, without a second click', async () => {
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      render(ASSERT_STEP)
      // The waiting state is the first thing the step shows: the Continue
      // control never renders, because the assertion started itself.
      expect(await screen.findByText(zhCN.flowStep.passkeyWaiting)).toBeTruthy()
      expect(screen.queryByRole('button', { name: 'Continue' })).toBeNull()
      expect(get).toHaveBeenCalledTimes(1)
    } finally {
      restore()
    }
  })

  it('restarts the arrival ceremony when the server re-sends the same challenge', async () => {
    // The challenge string alone does not identify a ceremony: the server can
    // send the same challenge again in the next step. The sequence number
    // advances with every reply the flow parks, so a re-sent challenge starts
    // the ceremony again while an ordinary re-render leaves it alone.
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      function Harness() {
        const [sequence, setSequence] = useState(1)
        return (
          <>
            <button
              type="button"
              onClick={() => setSequence((value) => value + 1)}
            >
              resend
            </button>
            <FlowStep
              step={ASSERT_STEP}
              sequence={sequence}
              busy={false}
              answerStep="answer"
              submitLabel="Continue"
              onSubmit={vi.fn()}
            />
          </>
        )
      }
      renderWithProviders(<Harness />)

      expect(await screen.findByText(zhCN.flowStep.passkeyWaiting)).toBeTruthy()
      expect(get).toHaveBeenCalledTimes(1)

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'resend' }))
      await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
    } finally {
      restore()
    }
  })

  it('starts no assertion ceremony when a genuinely different step arrives', async () => {
    // Only an assertion step starts on arrival. A different step the flow
    // parks is not an assertion ceremony, whatever sequence it carries.
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      function Harness() {
        const [step, setStep] = useState<AuthnStep>(ASSERT_STEP)
        return (
          <>
            <button
              type="button"
              onClick={() =>
                setStep({
                  kind: 'challenge',
                  fields: [{ name: 'code', kind: 'code' }],
                })
              }
            >
              different
            </button>
            <FlowStep
              step={step}
              sequence={2}
              busy={false}
              answerStep="answer"
              submitLabel="Continue"
              onSubmit={vi.fn()}
            />
          </>
        )
      }
      renderWithProviders(<Harness />)

      expect(await screen.findByText(zhCN.flowStep.passkeyWaiting)).toBeTruthy()
      expect(get).toHaveBeenCalledTimes(1)

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'different' }))
      expect(get).toHaveBeenCalledTimes(1)
      expect(screen.getByLabelText(zhCN.flowStep.codeLabel)).toBeTruthy()
    } finally {
      restore()
    }
  })

  it('starts the second ceremony after StrictMode abandons the first', async () => {
    // StrictMode mounts, unmounts and mounts again in development: the
    // first arrival's ceremony is abandoned by the cleanup, and the second
    // one must start cleanly and be the only one that answers.
    const get = vi
      .fn()
      .mockImplementationOnce(() => new Promise(() => {}))
      .mockResolvedValueOnce(ASSERTION_CREDENTIAL)
    const restore = installCredentials({ get, create: vi.fn() })
    // The StrictMode root is what double-invokes the effects; RTL does not
    // mount one by default.
    configure({ reactStrictMode: true })
    try {
      const onSubmit = vi.fn()
      renderWithProviders(
        <FlowStep
          step={ASSERT_STEP}
          sequence={1}
          busy={false}
          answerStep="answer"
          submitLabel="Continue"
          onSubmit={onSubmit}
        />,
      )

      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
      expect(get).toHaveBeenCalledTimes(2)
      const first = get.mock.calls[0]?.[0] as { signal?: AbortSignal }
      expect(first.signal?.aborted).toBe(true)
      const second = get.mock.calls[1]?.[0] as { signal?: AbortSignal }
      expect(second.signal?.aborted).toBe(false)
      expect(onSubmit).toHaveBeenCalledWith({
        step: 'answer',
        input: { credential: expect.stringContaining('cred-1') },
      })
    } finally {
      configure({ reactStrictMode: false })
      restore()
    }
  })

  it('waits for a click before a registration ceremony starts', async () => {
    // A creation ceremony needs the user activation some browsers require,
    // so a registration step stays parked until the viewer clicks.
    const create = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get: vi.fn(), create })
    try {
      const step: AuthnStep = {
        kind: 'webauthn',
        options: {
          challenge: 'Y2hhbGxlbmdl',
          user: { id: 'AQID', name: 'ada' },
        },
      }
      render(step)
      expect(screen.getByRole('button', { name: 'Continue' })).toBeTruthy()
      expect(create).not.toHaveBeenCalled()

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'Continue' }))
      await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    } finally {
      restore()
    }
  })

  it('starts no registration ceremony on mount and one per click', async () => {
    // A registration ceremony is click-driven, not effect-driven: the mount
    // starts none, and one click asks the browser once. The run is under RTL's
    // reactStrictMode, so the mount effects are double-invoked -- as the
    // assertion-ceremony case beside this one also relies on -- and the
    // ceremony still has no effect firing behind it.
    const create = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get: vi.fn(), create })
    configure({ reactStrictMode: true })
    try {
      render(REGISTER_STEP)
      expect(create).not.toHaveBeenCalled()

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'Continue' }))
      await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    } finally {
      configure({ reactStrictMode: false })
      restore()
    }
  })

  it('renders the registration prompt for a creation step', () => {
    // A creation step (its options carry the user) says it registers a
    // passkey rather than reusing the assertion's wording.
    const step: AuthnStep = {
      kind: 'webauthn',
      options: {
        challenge: 'Y2hhbGxlbmdl',
        user: { id: 'AQID', name: 'ada' },
      },
    }
    render(step)

    expect(screen.getByText(zhCN.flowStep.passkeyRegisterPrompt)).toBeTruthy()
    expect(screen.queryByText(zhCN.flowStep.passkeyPrompt)).toBeNull()
  })

  it('abandons the ceremony when the step unmounts', async () => {
    // An unmounted step has no flow to answer, so the pending ceremony is
    // given up: the signal the browser's API received is aborted.
    let received: { signal?: AbortSignal } | undefined
    const get = vi.fn((options: { signal?: AbortSignal }) => {
      received = options
      return new Promise(() => {})
    })
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      const { unmount } = render(ASSERT_STEP)

      // The arrival started the ceremony; nothing was clicked.
      await screen.findByText(zhCN.flowStep.passkeyWaiting)
      expect(received?.signal?.aborted).toBe(false)

      unmount()
      expect(received?.signal?.aborted).toBe(true)
    } finally {
      restore()
    }
  })

  it('reports a device that already holds this account as already registered', async () => {
    // The server sends excludeCredentials for the account's existing passkeys,
    // and an authenticator that already holds one refuses the creation with
    // InvalidStateError. That answer names its own remedy instead of the
    // generic failure the viewer would retry forever.
    const create = vi
      .fn()
      .mockRejectedValue(
        new DOMException('the authenticator already holds this credential', 'InvalidStateError'),
      )
    const restore = installCredentials({ create, get: vi.fn() })
    try {
      render(REGISTER_STEP)
      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'Continue' }))
      const alert = await screen.findByRole('alert')
      expect(alert.textContent).toBe(zhCN.flowStep.passkeyAlreadyRegistered)
    } finally {
      restore()
    }
  })

  it('names the ceremony in the generic failure sentence', async () => {
    // A registration and a sign-in that fail the same way read differently:
    // the sentence says which ceremony did not complete, so the viewer is not
    // told a sign-in failed while they were enrolling a factor.
    const composed = (action: string): string =>
      zhCN.flowStep.passkeyFailed.replace('{{action}}', action)
    const boom = (): Promise<never> => Promise.reject(new Error('boom'))
    const restore = installCredentials({
      create: vi.fn().mockImplementation(boom),
      get: vi.fn().mockImplementation(boom),
    })
    try {
      const register = render(REGISTER_STEP)
      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'Continue' }))
      const registerText = (await screen.findByRole('alert')).textContent
      register.unmount()

      render(ASSERT_STEP)
      const signInText = (await screen.findByRole('alert')).textContent

      expect(registerText).toBe(composed(zhCN.flowStep.passkeyActionRegister))
      expect(signInText).toBe(composed(zhCN.flowStep.passkeyActionSignIn))
      expect(registerText).not.toBe(signInText)
    } finally {
      restore()
    }
  })
})

const OTPAUTH_URI =
  'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co'
const OTPAUTH_SECRET_GROUPED = 'JBSW Y3DP EHPK 3PXP'

const TOTP_STEP: AuthnStep = {
  kind: 'webauthn',
  options: { otpauth_uri: OTPAUTH_URI },
}

describe('FlowStep TOTP enrolment', () => {
  it('renders the otpauth URI as a QR symbol, never as text', () => {
    const { getByRole } = render(TOTP_STEP)
    const image = getByRole('img', { name: zhCN.flowStep.totpQrLabel })
    const path = image.querySelector('path')?.getAttribute('d')
    expect(path).toBeTruthy()

    // A different URI draws a different matrix: the symbol's payload is the
    // step's otpauth_uri, not a fixed decoration.
    const other = render({
      kind: 'webauthn',
      options: {
        otpauth_uri: 'otpauth://totp/speed:ada?secret=KRSXG5CTMVRXEZLU',
      },
    })
    const otherPath = other.container
      .querySelector('svg[role="img"] path')
      ?.getAttribute('d')
    expect(otherPath).toBeTruthy()
    expect(otherPath).not.toBe(path)

    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
  })

  it('passes the parsed secret and parameters to the manual disclosure', async () => {
    const user = userEvent.setup()
    render(TOTP_STEP)

    const toggle = screen.getByRole('button', {
      name: zhCN.flowStep.totpManualToggle,
    })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText(OTPAUTH_SECRET_GROUPED)).toBeNull()

    await user.click(toggle)

    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText(OTPAUTH_SECRET_GROUPED)).toBeTruthy()
    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
    expect(screen.getByText(zhCN.flowStep.totpAlgorithm)).toBeTruthy()
    expect(screen.getByText('SHA1')).toBeTruthy()
    expect(screen.getByText(zhCN.flowStep.totpDigits)).toBeTruthy()
    expect(screen.getByText('6')).toBeTruthy()
    expect(screen.getByText(zhCN.flowStep.totpPeriod)).toBeTruthy()
    expect(screen.getByText('30')).toBeTruthy()
  })

  it('answers only when the confirm control is clicked, not while typing', async () => {
    const user = userEvent.setup()
    const { onSubmit } = render(TOTP_STEP)

    const input = screen.getByLabelText(zhCN.flowStep.codeLabel)
    await user.type(input, '123456')
    expect(onSubmit).not.toHaveBeenCalled()

    await user.click(screen.getByRole('button', { name: 'Continue' }))
    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(onSubmit).toHaveBeenCalledWith({
      step: 'answer',
      input: { code: '123456' },
    })
  })

  it('withholds the scan guidance when the step carries no URI', () => {
    render({ kind: 'webauthn', options: { otpauth_uri: '' } })

    expect(screen.queryByText(zhCN.flowStep.enrollTotpHelp)).toBeNull()
    expect(screen.queryByRole('img')).toBeNull()
    expect(screen.getByLabelText(zhCN.flowStep.codeLabel)).toBeTruthy()
  })

  it('renders no symbol or manual entry for a URI that does not parse', () => {
    render({
      kind: 'webauthn',
      options: { otpauth_uri: 'https://example.com/not-an-otpauth-uri' },
    })

    expect(screen.queryByRole('img')).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.flowStep.totpManualToggle }),
    ).toBeNull()
    expect(screen.queryByText(zhCN.flowStep.enrollTotpHelp)).toBeNull()
    expect(screen.getByLabelText(zhCN.flowStep.codeLabel)).toBeTruthy()
  })

  it('passes axe with the manual disclosure open', async () => {
    const user = userEvent.setup()
    render(TOTP_STEP)

    await user.click(
      screen.getByRole('button', { name: zhCN.flowStep.totpManualToggle }),
    )
    await expectNoAxeViolations()
  })
})
