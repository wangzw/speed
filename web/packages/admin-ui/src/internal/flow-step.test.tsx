/**
 * FlowStep behaviour: the guarded factor-kind vocabulary of a flow step, the
 * TOTP enrolment block and the passkey ceremony's failure wording.
 *
 * What this suite pins: a factor option reads as its known kind's
 * translated name, a repeated kind distinguishes its entries by name
 * ("Kind · Name") and an unnamed entry from the second on by ordinal
 * ("Kind (2)"), and an unknown kind shows the kind as given; an enrollment
 * kind follows the same known/unknown rule. The package keeps its vocabulary locally
 * (auth-ui and account-ui keep their own copies), so this suite is the
 * admin-ui side of the registered sync obligation. The TOTP cases pin
 * the step's URI becoming the shipped panel's QR symbol and manual secret,
 * never text; the failure cases pin the already-registered remedy and the
 * generic sentence naming the ceremony it ran.
 */

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
      screen.getByRole('button', { name: zhCN.stepUp.factorKind.totp }),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.stepUp.factorKind.sms }),
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
      // The region is inserted by the waiting state's first commit empty.
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
      // The retry uses the one primary action mapping every action row
      // shares, so the three retry surfaces cannot drift apart.
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
    // Label in Name needs canvas text measurement, which jsdom lacks; the
    // named browser case decides it.
    await expectNoAxeViolations({
      decidedInBrowser: 'flowStepManualDisclosure',
    })
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

/** A factor step offering two kinds, so a case can watch one of them. */
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

    // The control stays focusable; activating it answers nothing.
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

    // The option that was activated carries the progress state; the rest of
    // the group refuses another answer without a reason of its own.
    expect(totp).toHaveAttribute('aria-busy', 'true')
    expect(sms).toHaveAttribute('aria-disabled', 'true')
    expect(sms).not.toHaveAttribute('aria-busy')
    expect(sms).not.toHaveAccessibleDescription()
    await user.click(sms)
    expect(onSubmit).toHaveBeenCalledTimes(1)
  })

  it('refuses an implicit submit from the field while the step is held', async () => {
    // Enter in a field synthesizes a click on the form's submit button; the
    // form's own guard refuses it as well, so no answer leaves.
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
    // The submit button's own guard swallows the implicit submission it
    // carries; the form's handler is guarded too, for a submit the button
    // does not carry (a programmatic one, or a form without its button).
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
