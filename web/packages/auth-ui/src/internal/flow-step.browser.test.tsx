import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page, userEvent as browserUserEvent } from 'vitest/browser'
import { makeHarness } from '@speed/test-utils/session-harness'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import type { AuthnField, AuthnHumanGate, AuthnStep } from '@speed/auth-core'
import enUS from '../locales/en-US.json' with { type: 'json' }
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStepFields, HumanGatedFlowStep } from './flow-step.js'
import type { OpenFlowState } from './flow.js'

const TOTP_URI =
  'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co'

function step(next: AuthnStep): OpenFlowState {
  return { seq: 1, state: 'enroll', next }
}

describe('FlowStepFields Label in Name in Chromium', () => {
  it('settles Label in Name with the FlowStepFields manual-entry disclosure open', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <h1>Two-factor setup</h1>
        <FlowStepFields
          step={step({ kind: 'webauthn', options: { otpauth_uri: TOTP_URI } })}
          busy={false}
          onAnswer={vi.fn()}
          onUnsupported={vi.fn()}
        />
      </>,
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.totpManualToggle }),
    )
    await expectNoAxeViolations({
      decidedInBrowser: 'flowStepFieldsManualDisclosure',
    })
  })
})

describe('FlowStepFields passkey waiting in Chromium', () => {
  it('inserts the waiting region empty and fills it a frame later', async () => {
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const original = Object.getOwnPropertyDescriptor(
      window.navigator,
      'credentials',
    )
    Object.defineProperty(window.navigator, 'credentials', {
      value: { get, create: vi.fn() },
      configurable: true,
    })
    const snapshots: string[] = []
    const observer = new MutationObserver(() => {
      const region = document.querySelector('[role="status"]')
      if (region !== null) {
        snapshots.push(region.textContent ?? '')
      }
    })
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      characterData: true,
    })
    try {
      await page.viewport(900, 700)
      renderWithProviders(
        <FlowStepFields
          step={step({
            kind: 'webauthn',
            options: { challenge: 'Y2hhbGxlbmdl' },
          })}
          busy={false}
          onAnswer={vi.fn()}
          onUnsupported={vi.fn()}
        />,
        { language: 'en-US' },
      )

      await waitFor(() => expect(snapshots.length).toBeGreaterThan(0))
      expect(snapshots[0]).toBe('')
      await waitFor(() =>
        expect(screen.getByRole('status')).toHaveTextContent(
          enUS.signInFlow.passkeyWaiting,
        ),
      )
      expect(snapshots).toContain(enUS.signInFlow.passkeyWaiting)
    } finally {
      observer.disconnect()
      if (original === undefined) {
        delete (window.navigator as { credentials?: unknown }).credentials
      } else {
        Object.defineProperty(window.navigator, 'credentials', original)
      }
    }
  })
})

function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
  readonly reject: (reason: unknown) => void
} {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle
    reject = fail
  })
  return { promise, resolve, reject }
}

function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const parts = [active.tagName.toLowerCase()]
  const label = active.getAttribute('aria-label')
  const text = (active.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 80)
  if (label !== null) {
    parts.push(`aria-label="${label}"`)
  }
  if (text !== '') {
    parts.push(`text="${text}"`)
  }
  return parts.join(' ')
}

function installCredentials(credentials: unknown): () => void {
  const original = Object.getOwnPropertyDescriptor(
    window.navigator,
    'credentials',
  )
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

const FACTOR_STEP: OpenFlowState = {
  seq: 2,
  state: 'second',
  next: {
    kind: 'factor',
    factors: [
      { id: 'mfa-1', kind: 'sms' },
      { id: 'mfa-2', kind: 'totp' },
    ],
  },
}

const CHALLENGE_CODE_FIELDS: AuthnField[] = [
  { name: 'code', kind: 'code', required: true },
]

const ACCOUNT_FIELDS: AuthnField[] = [
  { name: 'username', kind: 'text', required: true },
  { name: 'password', kind: 'password', secret: true, required: true },
]

describe('FlowStepFields focus retention in Chromium', () => {
  it('keeps focus on the challenge submit in flight and after the answer is refused', async () => {
    const answer = deferred<void>()
    function Harness() {
      const [busy, setBusy] = useState(false)
      return (
        <FlowStepFields
          step={{ seq: 2, state: 'second', next: { kind: 'challenge', fields: CHALLENGE_CODE_FIELDS } }}
          busy={busy}
          onAnswer={() => {
            setBusy(true)
            answer.promise.then(
              () => setBusy(false),
              () => setBusy(false),
            )
          }}
          onUnsupported={vi.fn()}
        />
      )
    }
    renderWithProviders(<Harness />)
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.smsSignIn.codeLabel), '123456')
    const submit = screen.getByRole('button', { name: zhCN.signInFlow.submit })

    await user.click(submit)
    await waitFor(() => expect(submit).toHaveAttribute('aria-busy', 'true'))
    await nextFrame()
    expect(
      submit,
      `focus while the answer is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    answer.reject(new Error('refused'))
    await waitFor(() => expect(submit).not.toHaveAttribute('aria-busy'))
    await nextFrame()
    expect(
      submit,
      `focus after the answer was refused; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })

  it('keeps focus on the account-opening submit in flight and after the answer is refused', async () => {
    const answer = deferred<void>()
    function Harness() {
      const [busy, setBusy] = useState(false)
      return (
        <FlowStepFields
          step={{ seq: 2, state: 'signup', next: { kind: 'challenge', fields: ACCOUNT_FIELDS } }}
          busy={busy}
          onAnswer={() => {
            setBusy(true)
            answer.promise.then(
              () => setBusy(false),
              () => setBusy(false),
            )
          }}
          onUnsupported={vi.fn()}
        />
      )
    }
    renderWithProviders(<Harness />)
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(zhCN.signInFlow.usernameLabel), 'ada')
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      'correct-horse',
    )
    await user.type(
      screen.getByLabelText(zhCN.register.confirmPasswordLabel),
      'correct-horse',
    )
    const submit = screen.getByRole('button', {
      name: zhCN.signInFlow.signupAccountSubmit,
    })

    await user.click(submit)
    await waitFor(() => expect(submit).toHaveAttribute('aria-busy', 'true'))
    await nextFrame()
    expect(
      submit,
      `focus while the answer is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    answer.reject(new Error('refused'))
    await waitFor(() => expect(submit).not.toHaveAttribute('aria-busy'))
    await nextFrame()
    expect(
      submit,
      `focus after the answer was refused; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })

  it('keeps the empty-code TOTP confirm reachable and focused, and holds focus through the write', async () => {
    const answer = deferred<void>()
    function Harness() {
      const [busy, setBusy] = useState(false)
      return (
        <FlowStepFields
          step={step({ kind: 'webauthn', options: { otpauth_uri: TOTP_URI } })}
          busy={busy}
          onAnswer={() => {
            setBusy(true)
            answer.promise.then(
              () => setBusy(false),
              () => setBusy(false),
            )
          }}
          onUnsupported={vi.fn()}
        />
      )
    }
    renderWithProviders(<Harness />)
    const user = userEvent.setup()
    const confirm = screen.getByRole('button', { name: zhCN.signInFlow.submit })
    expect(confirm).toHaveAttribute('aria-disabled', 'true')

    confirm.focus()
    await nextFrame()
    expect(
      confirm,
      `focus on the empty-code confirm; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    await user.type(screen.getByLabelText(zhCN.signInFlow.codeLabel), '123456')
    await user.click(confirm)
    await waitFor(() => expect(confirm).toHaveAttribute('aria-busy', 'true'))
    await nextFrame()
    expect(
      confirm,
      `focus while the answer is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    answer.reject(new Error('refused'))
  })

  it('keeps focus on the chosen factor while it is busy and after the answer is refused', async () => {
    const answer = deferred<void>()
    function Harness() {
      const [busy, setBusy] = useState(false)
      return (
        <FlowStepFields
          step={FACTOR_STEP}
          busy={busy}
          onAnswer={() => {
            setBusy(true)
            answer.promise.then(
              () => setBusy(false),
              () => setBusy(false),
            )
          }}
          onUnsupported={vi.fn()}
        />
      )
    }
    renderWithProviders(<Harness />)
    const user = userEvent.setup()
    const sms = screen.getByRole('button', { name: zhCN.signInFlow.factorKind.sms })
    const totp = screen.getByRole('button', {
      name: zhCN.signInFlow.factorKind.totp,
    })

    await user.click(sms)
    await waitFor(() => expect(sms).toHaveAttribute('aria-busy', 'true'))
    expect(totp).toHaveAttribute('aria-disabled', 'true')
    await nextFrame()
    expect(
      sms,
      `focus on the chosen factor; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    answer.reject(new Error('refused'))
    await waitFor(() => expect(sms).not.toHaveAttribute('aria-busy'))
    await nextFrame()
    expect(
      sms,
      `focus after the answer was refused; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })

  it('keeps focus on the passkey cancel control when an answer goes in flight', async () => {
    const create = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get: vi.fn(), create })
    let setBusy: ((busy: boolean) => void) | null = null
    try {
      function Harness() {
        const [busy, updateBusy] = useState(false)
        setBusy = updateBusy
        return (
          <FlowStepFields
            step={step({
              kind: 'webauthn',
              options: {
                challenge: 'Y2hhbGxlbmdl',
                user: { id: 'AQID', name: 'ada' },
              },
            })}
            busy={busy}
            onAnswer={vi.fn()}
            onUnsupported={vi.fn()}
            onClientFailure={vi.fn()}
          />
        )
      }
      renderWithProviders(<Harness />)
      const user = userEvent.setup()
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      )
      const cancel = await screen.findByRole('button', {
        name: zhCN.signInFlow.passkeyCancel,
      })
      await waitFor(() => expect(cancel).toHaveFocus())

      act(() => setBusy?.(true))
      await waitFor(() => expect(cancel).toHaveAttribute('aria-disabled', 'true'))
      await nextFrame()
      expect(
        cancel,
        `focus while the answer is in flight; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    } finally {
      restore()
    }
  })

  it('keeps the passkey start control reachable when its answer is in flight', async () => {
    const create = vi.fn()
    const restore = installCredentials({ get: vi.fn(), create })
    try {
      renderWithProviders(
        <FlowStepFields
          step={step({
            kind: 'webauthn',
            options: {
              challenge: 'Y2hhbGxlbmdl',
              user: { id: 'AQID', name: 'ada' },
            },
          })}
          busy
          onAnswer={vi.fn()}
          onUnsupported={vi.fn()}
        />,
      )
      const start = screen.getByRole('button', { name: zhCN.signInFlow.submit })
      expect(start).toHaveAttribute('aria-busy', 'true')
      start.focus()
      await nextFrame()
      expect(
        start,
        `focus on the busy start control; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    } finally {
      restore()
    }
  })
})

const PHONE_TURNSTILE: AuthnHumanGate = {
  phone: { required: true, provider: 'turnstile', site_key: 'site-1' },
  other: { required: false, provider: 'off' },
}

describe('HumanGatedFlowStep held-answer focus in Chromium', () => {
  it('returns focus to the answer control when the held answer is cancelled', async () => {
    const fake = installFakeTurnstile()
    try {
      const onAnswer = vi.fn()
      const harness = makeHarness({})
      const factorStep: OpenFlowState = {
        seq: 2,
        state: 'second',
        next: {
          kind: 'factor',
          factors: [{ id: 'mfa-1', kind: 'sms' }],
        },
      }
      renderWithProviders(
        <>
          <h1>Two-factor sign-in</h1>
          <HumanGatedFlowStep
            session={harness.session}
            humanGate={PHONE_TURNSTILE}
            step={factorStep}
            busy={false}
            onAnswer={onAnswer}
            onUnsupported={vi.fn()}
          />
        </>,
      )
      const smsChoice = screen.getByRole('button', {
        name: zhCN.signInFlow.factorKind.sms,
      })
      smsChoice.focus()
      expect(document.activeElement).toBe(smsChoice)
      await browserUserEvent.keyboard('{Enter}')
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(onAnswer).not.toHaveBeenCalled()

      const cancel = screen.getByRole('button', {
        name: zhCN.signInFlow.passkeyCancel,
      })
      cancel.focus()
      expect(document.activeElement).toBe(cancel)
      await browserUserEvent.keyboard('{Enter}')
      await waitFor(() => expect(document.activeElement).toBe(smsChoice))
      expect(onAnswer).not.toHaveBeenCalled()
      expect(
        screen.queryByRole('button', { name: zhCN.signInFlow.passkeyCancel }),
      ).toBeNull()
    } finally {
      fake.restore()
    }
  })
})
