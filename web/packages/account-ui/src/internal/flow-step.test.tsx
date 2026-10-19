/**
 * FlowStep behaviour: the controlled label vocabulary of a flow step and
 * the passkey ceremony's own states.
 *
 * What this suite pins: a challenge field name the family knows renders
 * its translated label, an unknown name renders verbatim; a factor
 * option renders its server-supplied label when one arrives, otherwise a
 * known kind's translated name, and an unknown kind shows the kind as
 * given; an enrollment kind follows the same known/unknown rule. The
 * package keeps its vocabulary locally (auth-ui and console-ui keep their
 * own copies), so this suite is the account-ui side of the registered
 * sync obligation. The ceremony cases pin the waiting state and its
 * cancel control (with the focus the unmounted button held), the
 * immediate failure a cancel renders and the retry that runs the
 * ceremony again, the registration prompt's own wording, and the
 * ceremony being abandoned when the step unmounts.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthnStep } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStep } from './flow-step.js'

function render(step: AuthnStep, onSubmit = vi.fn()) {
  const rendered = renderWithProviders(
    <FlowStep
      step={step}
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

  it('lets a server label win and translates a known factor kind', () => {
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'totp', label: 'My app' },
        { id: 'f2', kind: 'sms' },
      ],
    })

    // The server's own label wins over the bundle's wording; a factor
    // without one renders its kind's translated name.
    expect(screen.getByRole('button', { name: 'My app' })).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.mfa.factor.kind.sms }),
    ).toBeTruthy()
  })

  it('translates known enroll kinds and shows an unknown kind as given', () => {
    render({ kind: 'enroll', kinds: ['webauthn', 'magic'] })

    expect(
      screen.getByRole('button', { name: zhCN.mfa.factor.kind.webauthn }),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: 'magic' })).toBeTruthy()
  })

  it('waits on the ceremony, cancels into a retry, and retries the ceremony', async () => {
    // The ceremony sits pending in the browser: the click must move the
    // step into an explicit waiting state whose cancel control takes the
    // focus the unmounted button held, and cancel must fail at once and
    // offer a retry that drives the ceremony again.
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
      await user.click(screen.getByRole('button', { name: 'Continue' }))

      expect(await screen.findByText(zhCN.flowStep.passkeyWaiting)).toBeTruthy()
      const cancel = screen.getByRole('button', {
        name: zhCN.flowStep.passkeyCancel,
      })
      expect(document.activeElement).toBe(cancel)

      await user.click(cancel)
      expect(await screen.findByRole('alert')).toHaveTextContent(
        zhCN.flowStep.passkeyCancelled,
      )

      await user.click(
        screen.getByRole('button', { name: zhCN.flowStep.passkeyRetry }),
      )
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
      const user = userEvent.setup()

      await user.click(screen.getByRole('button', { name: 'Continue' }))
      await screen.findByText(zhCN.flowStep.passkeyWaiting)
      expect(received?.signal?.aborted).toBe(false)

      unmount()
      expect(received?.signal?.aborted).toBe(true)
    } finally {
      restore()
    }
  })
})
