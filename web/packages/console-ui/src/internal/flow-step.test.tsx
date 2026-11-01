/**
 * FlowStep behaviour: the guarded factor-kind vocabulary of a flow step, the
 * TOTP enrolment block and the passkey ceremony's failure wording.
 *
 * What this suite pins: a factor option renders its server-supplied label
 * when one arrives, otherwise a known kind's translated name, and an
 * unknown kind shows the kind as given; an enrollment kind follows the
 * same known/unknown rule. The package keeps its vocabulary locally
 * (auth-ui and account-ui keep their own copies), so this suite is the
 * console-ui side of the registered sync obligation. The TOTP cases pin
 * the step's URI becoming the shipped panel's QR symbol and manual secret,
 * never text; the failure cases pin the already-registered remedy and the
 * generic sentence naming the ceremony it ran.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
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

/** A step whose options ask the browser for an assertion. */
const ASSERT_STEP: AuthnStep = {
  kind: 'webauthn',
  options: { challenge: 'Y2hhbGxlbmdl' },
}

/** An assertion credential shaped as the browser's API returns one. */
function assertionCredential(id: string) {
  return {
    id,
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
}

describe('FlowStep labels', () => {
  it('lets a server label win and translates a known factor kind', () => {
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'totp', label: 'My app' },
        { id: 'f2', kind: 'sms' },
      ],
    })

    expect(screen.getByRole('button', { name: 'My app' })).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.stepUp.factorKind.sms }),
    ).toBeTruthy()
  })

  it('translates known enroll kinds and shows an unknown kind as given', () => {
    render({ kind: 'enroll', kinds: ['webauthn', 'magic'] })

    expect(
      screen.getByRole('button', { name: zhCN.stepUp.factorKind.webauthn }),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: 'magic' })).toBeTruthy()
  })
})

describe('FlowStep passkey ceremony', () => {
  it('starts an assertion ceremony on arrival, without a second click', async () => {
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      render(ASSERT_STEP)
      // The waiting state is the first thing the step shows: the Continue
      // control never renders, because the assertion started itself.
      expect(await screen.findByText(zhCN.stepUp.passkeyWaiting)).toBeTruthy()
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
              answerStep="answer"
              submitLabel="Continue"
              onSubmit={vi.fn()}
            />
          </>
        )
      }
      renderWithProviders(<Harness />)

      expect(await screen.findByText(zhCN.stepUp.passkeyWaiting)).toBeTruthy()
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
              answerStep="answer"
              submitLabel="Continue"
              onSubmit={vi.fn()}
            />
          </>
        )
      }
      renderWithProviders(<Harness />)

      expect(await screen.findByText(zhCN.stepUp.passkeyWaiting)).toBeTruthy()
      expect(get).toHaveBeenCalledTimes(1)

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'different' }))
      expect(get).toHaveBeenCalledTimes(1)
      expect(screen.getByLabelText('code')).toBeTruthy()
    } finally {
      restore()
    }
  })

  it('cancels the arrival ceremony into a retry that drives it again', async () => {
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      render(ASSERT_STEP)
      // The waiting state is reachable without a click; cancel then fails
      // at once and offers a retry that starts the ceremony again.
      await screen.findByText(zhCN.stepUp.passkeyWaiting)
      const user = userEvent.setup()
      await user.click(
        screen.getByRole('button', { name: zhCN.stepUp.passkeyCancel }),
      )
      expect(await screen.findByRole('alert')).toHaveTextContent(
        zhCN.stepUp.passkeyCancelled,
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.stepUp.passkeyRetry }),
      )
      expect(await screen.findByText(zhCN.stepUp.passkeyWaiting)).toBeTruthy()
      expect(get).toHaveBeenCalledTimes(2)
    } finally {
      restore()
    }
  })

  it('waits for a click before a registration ceremony starts', async () => {
    // A creation ceremony needs the user activation some browsers require,
    // so a registration step stays parked until the viewer clicks.
    const create = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get: vi.fn(), create })
    try {
      render({
        kind: 'webauthn',
        options: {
          challenge: 'Y2hhbGxlbmdl',
          user: { id: 'AQID', name: 'ada' },
        },
      })
      expect(
        screen.getByText(zhCN.stepUp.passkeyRegisterPrompt),
      ).toBeInTheDocument()
      expect(create).not.toHaveBeenCalled()

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'Continue' }))
      await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    } finally {
      restore()
    }
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
      await screen.findByText(zhCN.stepUp.passkeyWaiting)
      expect(received?.signal?.aborted).toBe(false)

      unmount()
      expect(received?.signal?.aborted).toBe(true)
    } finally {
      restore()
    }
  })

  it('discards a late credential from an abandoned attempt', async () => {
    // The cancelled attempt stays pending in the browser and settles later;
    // its answer no longer matches the live attempt, so it travels nowhere.
    const settle: Array<(value: unknown) => void> = []
    const get = vi.fn(
      () =>
        new Promise((resolve) => {
          settle.push(resolve)
        }),
    )
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      const onSubmit = vi.fn()
      render(ASSERT_STEP, onSubmit)
      await screen.findByText(zhCN.stepUp.passkeyWaiting)
      const user = userEvent.setup()

      await user.click(
        screen.getByRole('button', { name: zhCN.stepUp.passkeyCancel }),
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.stepUp.passkeyRetry }),
      )
      expect(await screen.findByText(zhCN.stepUp.passkeyWaiting)).toBeTruthy()
      expect(get).toHaveBeenCalledTimes(2)

      await act(async () => {
        settle[0]?.(assertionCredential('late'))
        await new Promise((resolve) => setTimeout(resolve, 0))
      })
      expect(onSubmit).not.toHaveBeenCalled()

      await act(async () => {
        settle[1]?.(assertionCredential('second'))
        await new Promise((resolve) => setTimeout(resolve, 0))
      })
      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1))
      expect(onSubmit).toHaveBeenCalledWith({
        step: 'answer',
        input: { credential: expect.stringContaining('second') },
      })
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

/** A step whose options ask the browser to create a credential. */
const REGISTER_STEP: AuthnStep = {
  kind: 'webauthn',
  options: {
    challenge: 'Y2hhbGxlbmdl',
    user: { id: 'AQID', name: 'ada' },
  },
}

describe('FlowStep TOTP enrolment', () => {
  it('renders the otpauth URI as a QR symbol, never as text', () => {
    const { getByRole } = render(TOTP_STEP)
    const image = getByRole('img', { name: zhCN.stepUp.totpQrLabel })
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
      name: zhCN.stepUp.totpManualToggle,
    })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText(OTPAUTH_SECRET_GROUPED)).toBeNull()

    await user.click(toggle)

    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText(OTPAUTH_SECRET_GROUPED)).toBeTruthy()
    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
    expect(screen.getByText(zhCN.stepUp.totpAlgorithm)).toBeTruthy()
    expect(screen.getByText('SHA1')).toBeTruthy()
    expect(screen.getByText(zhCN.stepUp.totpDigits)).toBeTruthy()
    expect(screen.getByText('6')).toBeTruthy()
    expect(screen.getByText(zhCN.stepUp.totpPeriod)).toBeTruthy()
    expect(screen.getByText('30')).toBeTruthy()
  })

  it('answers only when the confirm control is clicked, not while typing', async () => {
    const user = userEvent.setup()
    const { onSubmit } = render(TOTP_STEP)

    const input = screen.getByLabelText(zhCN.stepUp.codeLabel)
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

    expect(screen.queryByText(zhCN.stepUp.enrollTotpHelp)).toBeNull()
    expect(screen.queryByRole('img')).toBeNull()
    expect(screen.getByLabelText(zhCN.stepUp.codeLabel)).toBeTruthy()
  })

  it('renders no symbol or manual entry for a URI that does not parse', () => {
    render({
      kind: 'webauthn',
      options: { otpauth_uri: 'https://example.com/not-an-otpauth-uri' },
    })

    expect(screen.queryByRole('img')).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.stepUp.totpManualToggle }),
    ).toBeNull()
    expect(screen.queryByText(zhCN.stepUp.enrollTotpHelp)).toBeNull()
    expect(screen.getByLabelText(zhCN.stepUp.codeLabel)).toBeTruthy()
  })

  it('passes axe with the manual disclosure open', async () => {
    const user = userEvent.setup()
    // The scan needs the page heading context the dialog supplies in a real
    // host: an isolated widget render has no h1 and fails page-has-heading-one.
    renderWithProviders(
      <>
        <h1>Verify it's you</h1>
        <FlowStep
          step={TOTP_STEP}
          sequence={1}
          answerStep="answer"
          submitLabel="Continue"
          onSubmit={vi.fn()}
        />
      </>,
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.stepUp.totpManualToggle }),
    )
    await expectNoAxeViolations()
  })
})

describe('FlowStep passkey failure wording', () => {
  it('names the already-registered remedy when the device holds the credential', async () => {
    // The server sends excludeCredentials for the account's existing passkeys,
    // and an authenticator that already holds one refuses the creation with
    // InvalidStateError. That answer names its own remedy instead of the
    // generic failure the viewer would retry forever.
    const create = vi
      .fn()
      .mockRejectedValue(
        new DOMException(
          'the authenticator already holds this credential',
          'InvalidStateError',
        ),
      )
    const restore = installCredentials({ create, get: vi.fn() })
    try {
      render(REGISTER_STEP)
      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'Continue' }))
      const alert = await screen.findByRole('alert')
      expect(alert.textContent).toBe(zhCN.stepUp.passkeyAlreadyRegistered)
    } finally {
      restore()
    }
  })

  it('names a failed registration as a registration', async () => {
    const restore = installCredentials({
      create: vi.fn().mockRejectedValue(new Error('boom')),
      get: vi.fn(),
    })
    try {
      render(REGISTER_STEP)
      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'Continue' }))
      const alert = await screen.findByRole('alert')
      expect(alert.textContent).toBe(zhCN.stepUp.passkeyFailedRegister)
      expect(alert.textContent).not.toBe(zhCN.stepUp.passkeyFailedSignIn)
    } finally {
      restore()
    }
  })

  it('names a failed assertion as a sign-in', async () => {
    // A failed registration and a failed sign-in read differently, so the
    // viewer is not told a sign-in failed while they were enrolling a factor.
    const restore = installCredentials({
      create: vi.fn(),
      get: vi.fn().mockRejectedValue(new Error('boom')),
    })
    try {
      render(ASSERT_STEP)
      const alert = await screen.findByRole('alert')
      expect(alert.textContent).toBe(zhCN.stepUp.passkeyFailedSignIn)
      expect(alert.textContent).not.toBe(zhCN.stepUp.passkeyFailedRegister)
    } finally {
      restore()
    }
  })
})
