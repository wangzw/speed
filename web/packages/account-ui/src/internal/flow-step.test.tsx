/**
 * FlowStep checks the label vocabulary, the TOTP block and the passkey ceremony's states.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { configure, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthnStep } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  expectNoAxeViolations,
} from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStep } from './flow-step.js'

function render(
  step: AuthnStep,
  onSubmit = vi.fn(),
  options: {
    readonly busy?: boolean
    readonly submitDisabled?: boolean
  } = {},
) {
  const rendered = renderWithProviders(
    <FlowStep
      step={step}
      sequence={1}
      busy={options.busy ?? false}
      submitDisabled={options.submitDisabled}
      answerStep="answer"
      submitLabel="Continue"
      onSubmit={onSubmit}
    />,
  )
  return { ...rendered, onSubmit }
}

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

const ASSERT_STEP: AuthnStep = {
  kind: 'webauthn',
  options: { challenge: 'Y2hhbGxlbmdl' },
}

/** The `user` member is what tells a registration from an assertion. */
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

    expect(
      screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.mfa.factor.kind.sms }),
    ).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'My app' })).toBeNull()
  })

  it('labels same-named factors of different kinds by their kinds', () => {
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

  it('mounts the waiting region empty and fills it on a later frame', async () => {
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      render(ASSERT_STEP)
      const region = screen.getByRole('status')
      expect(region.textContent).toBe('')
      await waitFor(() =>
        expect(region).toHaveTextContent(zhCN.flowStep.passkeyWaiting),
      )
    } finally {
      restore()
    }
  })

  it('waits on the arrival ceremony, cancels into a retry, and retries the ceremony', async () => {
    const get = vi
      .fn()
      .mockImplementationOnce(() => new Promise(() => {}))
      .mockImplementationOnce(() => Promise.resolve(ASSERTION_CREDENTIAL))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      const { onSubmit } = render(ASSERT_STEP)
      const user = userEvent.setup()

      expect(screen.getByText(zhCN.flowStep.passkeyPrompt)).toBeTruthy()
      expect(await screen.findByText(zhCN.flowStep.passkeyWaiting)).toBeTruthy()
      const cancel = screen.getByRole('button', {
        name: zhCN.flowStep.passkeyCancel,
      })
      expect(document.activeElement).toBe(cancel)

      await user.click(cancel)
      expect(await screen.findByRole('alert')).toHaveTextContent(
        zhCN.flowStep.passkeyCancelled,
      )

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
      expect(await screen.findByText(zhCN.flowStep.passkeyWaiting)).toBeTruthy()
      expect(screen.queryByRole('button', { name: 'Continue' })).toBeNull()
      expect(get).toHaveBeenCalledTimes(1)
    } finally {
      restore()
    }
  })

  it('restarts the arrival ceremony when the server re-sends the same challenge', async () => {
    // The challenge string alone does not identify a ceremony; the sequence
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
    const get = vi
      .fn()
      .mockImplementationOnce(() => new Promise(() => {}))
      .mockResolvedValueOnce(ASSERTION_CREDENTIAL)
    const restore = installCredentials({ get, create: vi.fn() })
    // RTL does not mount a StrictMode root by default.
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
    // A creation ceremony needs the user activation some browsers require.
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
    // An unmounted step has no flow to answer, so its signal is aborted.
    let received: { signal?: AbortSignal } | undefined
    const get = vi.fn((options: { signal?: AbortSignal }) => {
      received = options
      return new Promise(() => {})
    })
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      const { unmount } = render(ASSERT_STEP)

      await screen.findByText(zhCN.flowStep.passkeyWaiting)
      expect(received?.signal?.aborted).toBe(false)

      unmount()
      expect(received?.signal?.aborted).toBe(true)
    } finally {
      restore()
    }
  })

  it('reports a device that already holds this account as already registered', async () => {
    // An authenticator holding an excluded credential answers InvalidStateError;
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
    // Label in Name needs canvas text measurement; the browser case decides it.
    await expectNoAxeViolations({
      decidedInBrowser: 'flowStepAccountManualDisclosure',
    })
  })
})

const CHALLENGE_STEP: AuthnStep = {
  kind: 'challenge',
  fields: [{ name: 'code', kind: 'code' }],
}

const FACTOR_STEP: AuthnStep = {
  kind: 'factor',
  factors: [
    { id: 'f1', kind: 'totp' },
    { id: 'f2', kind: 'sms' },
  ],
}

describe('FlowStep held actions', () => {
  it('keeps the submitted control focused while the answer is in flight', async () => {
    // A native disable would drop focus to the body, so the fields turn
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    function Harness() {
      const [busy, setBusy] = useState(false)
      return (
        <FlowStep
          step={CHALLENGE_STEP}
          sequence={1}
          busy={busy}
          answerStep="answer"
          submitLabel="Continue"
          onSubmit={() => {
            setBusy(true)
            onSubmit()
          }}
        />
      )
    }
    renderWithProviders(<Harness />)

    const field = screen.getByLabelText(zhCN.flowStep.codeLabel)
    await user.type(field, '123456')
    const submit = screen.getByRole('button', { name: 'Continue' })
    await user.click(submit)

    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(submit).toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(document.activeElement).toBe(submit)
    expect(field).toHaveAttribute('readonly')
    expect(field).not.toBeDisabled()
  })

  it('keeps the activated option busy and holds the rest of its group', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    function Harness() {
      const [busy, setBusy] = useState(false)
      return (
        <FlowStep
          step={FACTOR_STEP}
          sequence={1}
          busy={busy}
          answerStep="answer"
          submitLabel="Continue"
          onSubmit={() => {
            setBusy(true)
            onSubmit()
          }}
        />
      )
    }
    renderWithProviders(<Harness />)

    const totp = screen.getByRole('button', {
      name: zhCN.mfa.factor.kind.totp,
    })
    const sms = screen.getByRole('button', {
      name: zhCN.mfa.factor.kind.sms,
    })
    await user.click(totp)

    expect(totp).toHaveAttribute('aria-busy', 'true')
    expect(totp).toHaveAttribute('aria-disabled', 'true')
    expect(document.activeElement).toBe(totp)
    expect(sms).toHaveAttribute('aria-disabled', 'true')
    expect(sms).not.toHaveAttribute('aria-busy')
    expect(sms).not.toHaveAccessibleDescription()
    await user.click(sms)
    expect(onSubmit).toHaveBeenCalledTimes(1)
  })

  it('holds the TOTP confirm without a code and names the missing code', async () => {
    const user = userEvent.setup()
    const { onSubmit } = render(TOTP_STEP)

    const confirm = screen.getByRole('button', { name: 'Continue' })
    expect(confirm).toHaveAttribute('aria-disabled', 'true')
    expect(confirm).toHaveAccessibleDescription(zhCN.flowStep.codeRequired)
    expect(confirm).not.toBeDisabled()

    await user.click(confirm)
    expect(onSubmit).not.toHaveBeenCalled()

    await user.type(screen.getByLabelText(zhCN.flowStep.codeLabel), '123456')
    expect(confirm).not.toHaveAttribute('aria-disabled', 'true')
    await user.click(confirm)
    expect(onSubmit).toHaveBeenCalledTimes(1)
  })

  it('refuses a keyboard activation of a held option', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(FACTOR_STEP, onSubmit, { submitDisabled: true })

    const totp = screen.getByRole('button', {
      name: zhCN.mfa.factor.kind.totp,
    })
    expect(totp).toHaveAttribute('aria-disabled', 'true')
    totp.focus()
    expect(totp).toHaveFocus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('refuses a programmatic form submit while the step is held', () => {
    // The form's handler is guarded too, for a submit its button does not carry.
    const onSubmit = vi.fn()
    render(CHALLENGE_STEP, onSubmit, { submitDisabled: true })
    fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('refuses a programmatic form submit while an answer is in flight', () => {
    const onSubmit = vi.fn()
    render(CHALLENGE_STEP, onSubmit, { busy: true })
    fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    expect(onSubmit).not.toHaveBeenCalled()
  })
})
