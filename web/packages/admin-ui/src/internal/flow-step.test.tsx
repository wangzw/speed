import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthnStep } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  expectNoAxeViolations,
} from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStep } from './flow-step.js'
import type { FlowStepProps } from './flow-step.js'

function render(
  step: AuthnStep,
  onSubmit = vi.fn(),
  props: Partial<FlowStepProps> = {},
) {
  const rendered = renderWithProviders(
    <FlowStep
      step={step}
      sequence={1}
      answerStep="answer"
      submitLabel="Continue"
      onSubmit={onSubmit}
      {...props}
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

const ASSERT_STEP: AuthnStep = {
  kind: 'webauthn',
  options: { challenge: 'Y2hhbGxlbmdl' },
}

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
  it('reads each factor as its kind, ignoring a sole factor’s name', () => {
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'totp', label: 'My app' },
        { id: 'f2', kind: 'sms' },
      ],
    })

    expect(
      screen.getByRole('button', { name: zhCN.stepUp.factorKind.totp }),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.stepUp.factorKind.sms }),
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
      screen.getByRole('button', { name: zhCN.stepUp.factorKind.webauthn }),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.stepUp.factorKind.totp }),
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

    const kind = zhCN.stepUp.factorKind.webauthn
    const named = zhCN.stepUp.factorOptionNamed
      .replace('{{kind}}', kind)
      .replace('{{name}}', 'Work MacBook')
    const ordinal = zhCN.stepUp.factorOptionOrdinal
      .replace('{{kind}}', kind)
      .replace('{{position}}', '2')
    expect(screen.getByRole('button', { name: named })).toBeTruthy()
    expect(screen.getByRole('button', { name: ordinal })).toBeTruthy()
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
  it('mounts the waiting region empty and fills it on a later frame', async () => {
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      render(ASSERT_STEP)
      const region = screen.getByRole('status')
      expect(region.textContent).toBe('')
      await waitFor(() =>
        expect(region).toHaveTextContent(zhCN.stepUp.passkeyWaiting),
      )
    } finally {
      restore()
    }
  })

  it('starts an assertion ceremony on arrival, without a second click', async () => {
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      render(ASSERT_STEP)
      expect(await screen.findByText(zhCN.stepUp.passkeyWaiting)).toBeTruthy()
      expect(screen.queryByRole('button', { name: 'Continue' })).toBeNull()
      expect(get).toHaveBeenCalledTimes(1)
    } finally {
      restore()
    }
  })

  // The sequence number distinguishes a re-sent challenge from an ordinary re-render.
  it('restarts the arrival ceremony when the server re-sends the same challenge', async () => {
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
      await screen.findByText(zhCN.stepUp.passkeyWaiting)
      const user = userEvent.setup()
      await user.click(
        screen.getByRole('button', { name: zhCN.stepUp.passkeyCancel }),
      )
      expect(await screen.findByRole('alert')).toHaveTextContent(
        zhCN.stepUp.passkeyCancelled,
      )
      const retry = screen.getByRole('button', { name: zhCN.stepUp.passkeyRetry })
      expect(retry).toHaveClass('MuiButton-contained')
      await user.click(retry)
      expect(await screen.findByText(zhCN.stepUp.passkeyWaiting)).toBeTruthy()
      expect(get).toHaveBeenCalledTimes(2)
    } finally {
      restore()
    }
  })

  it('waits for a click before a registration ceremony starts', async () => {
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
    let received: { signal?: AbortSignal } | undefined
    const get = vi.fn((options: { signal?: AbortSignal }) => {
      received = options
      return new Promise(() => {})
    })
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      const { unmount } = render(ASSERT_STEP)

      await screen.findByText(zhCN.stepUp.passkeyWaiting)
      expect(received?.signal?.aborted).toBe(false)

      unmount()
      expect(received?.signal?.aborted).toBe(true)
    } finally {
      restore()
    }
  })

  it('discards a late credential from an abandoned attempt', async () => {
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
    // Label in Name needs canvas text measurement, which jsdom lacks; the browser case decides it.
    await expectNoAxeViolations({
      decidedInBrowser: 'flowStepManualDisclosure',
    })
  })
})

describe('FlowStep passkey failure wording', () => {
  it('names the already-registered remedy when the device holds the credential', async () => {
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

const FACTOR_STEP: AuthnStep = {
  kind: 'factor',
  factors: [
    { id: 'f1', kind: 'totp' },
    { id: 'f2', kind: 'sms' },
  ],
}

describe('FlowStep held actions', () => {
  it('holds the TOTP confirm until a code is typed and names the missing code', async () => {
    const user = userEvent.setup()
    const { onSubmit } = render(TOTP_STEP)

    const confirm = screen.getByRole('button', { name: 'Continue' })
    expect(confirm).toHaveAttribute('aria-disabled', 'true')
    expect(confirm).toHaveAccessibleDescription(zhCN.stepUp.codeRequired)

    await user.click(confirm)
    expect(onSubmit).not.toHaveBeenCalled()

    await user.type(screen.getByLabelText(zhCN.stepUp.codeLabel), '123456')
    expect(confirm).not.toHaveAttribute('aria-disabled', 'true')
    await user.click(confirm)
    expect(onSubmit).toHaveBeenCalledTimes(1)
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
          answerStep="answer"
          submitLabel="Continue"
          busy={busy}
          onSubmit={() => {
            setBusy(true)
            onSubmit()
          }}
        />
      )
    }
    renderWithProviders(<Harness />)

    const totp = screen.getByRole('button', {
      name: zhCN.stepUp.factorKind.totp,
    })
    const sms = screen.getByRole('button', {
      name: zhCN.stepUp.factorKind.sms,
    })
    await user.click(totp)

    expect(totp).toHaveAttribute('aria-busy', 'true')
    expect(sms).toHaveAttribute('aria-disabled', 'true')
    expect(sms).not.toHaveAttribute('aria-busy')
    expect(sms).not.toHaveAccessibleDescription()
    await user.click(sms)
    expect(onSubmit).toHaveBeenCalledTimes(1)
  })

  it('refuses an implicit submit from the field while the step is held', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(
      { kind: 'challenge', fields: [{ name: 'code', kind: 'code' }] },
      onSubmit,
      { unavailable: true },
    )

    await user.type(screen.getByLabelText('code'), '123456{Enter}')
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('refuses a keyboard activation of a held option', async () => {
    const user = userEvent.setup()
    const onSubmit = vi.fn()
    render(FACTOR_STEP, onSubmit, { unavailable: true })

    const totp = screen.getByRole('button', {
      name: zhCN.stepUp.factorKind.totp,
    })
    expect(totp).toHaveAttribute('aria-disabled', 'true')
    totp.focus()
    expect(totp).toHaveFocus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('refuses a programmatic form submit while the step is held', () => {
    const onSubmit = vi.fn()
    render(
      { kind: 'challenge', fields: [{ name: 'code', kind: 'code' }] },
      onSubmit,
      { unavailable: true },
    )
    fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('refuses a programmatic form submit while an answer is in flight', () => {
    const onSubmit = vi.fn()
    render(
      { kind: 'challenge', fields: [{ name: 'code', kind: 'code' }] },
      onSubmit,
      { busy: true },
    )
    fireEvent.submit(document.querySelector('form') as HTMLFormElement)
    expect(onSubmit).not.toHaveBeenCalled()
  })
})
