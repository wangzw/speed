/**
 * FlowStep behaviour: the guarded factor-kind vocabulary of a flow step.
 *
 * What this suite pins: a factor option renders its server-supplied label
 * when one arrives, otherwise a known kind's translated name, and an
 * unknown kind shows the kind as given; an enrollment kind follows the
 * same known/unknown rule. The package keeps its vocabulary locally
 * (auth-ui, account-ui and console-ui keep their own copies), so this
 * suite is the console-ui side of the registered sync obligation.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthnStep } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStep } from './flow-step.js'

function render(step: AuthnStep, onSubmit = vi.fn()) {
  const rendered = renderWithProviders(
    <FlowStep
      step={step}
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
