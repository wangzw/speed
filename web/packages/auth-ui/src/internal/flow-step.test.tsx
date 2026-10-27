/**
 * FlowStepFields behaviour: the factor and enroll label vocabulary.
 *
 * What this suite pins: a known factor or enroll kind renders its
 * translated name, a server-supplied factor label wins over it, and a
 * kind the family has no wording for shows the kind as given -- the
 * deployment's own vocabulary, never a generic stand-in. That last rule
 * is the one the three flow-step copies share: account-ui and console-ui
 * render an unknown kind verbatim, so this package does too, and this
 * suite is the auth-ui side of the registered sync obligation.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthnStep } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStepFields } from './flow-step.js'
import type { OpenFlowState } from './flow.js'

function step(next: AuthnStep): OpenFlowState {
  return { seq: 1, state: 'enroll', next }
}

function render(next: AuthnStep, onUnsupported: () => void = vi.fn()) {
  return renderWithProviders(
    <FlowStepFields
      step={step(next)}
      busy={false}
      onAnswer={vi.fn()}
      onUnsupported={onUnsupported}
    />,
  )
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

describe('FlowStepFields labels', () => {
  it('translates known factor kinds and shows an unknown kind as given', () => {
    render({
      kind: 'factor',
      factors: [
        { id: 'f1', kind: 'totp' },
        { id: 'f2', kind: 'magic' },
      ],
    })

    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.factorKind.totp }),
    ).toBeTruthy()
    // The unknown kind is the deployment's own name, not the generic
    // "other" label.
    expect(screen.getByRole('button', { name: 'magic' })).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: zhCN.signInFlow.factorKind.other }),
    ).toBeNull()
  })

  it('lets a server-supplied factor label win over the vocabulary', () => {
    render({
      kind: 'factor',
      factors: [{ id: 'f1', kind: 'totp', label: 'My app' }],
    })

    expect(screen.getByRole('button', { name: 'My app' })).toBeTruthy()
    expect(
      screen.queryByRole('button', { name: zhCN.signInFlow.factorKind.totp }),
    ).toBeNull()
  })

  it('translates a known enroll kind and shows an unknown kind as given', () => {
    render({ kind: 'enroll', kinds: ['webauthn', 'magic'] })

    expect(
      screen.getByRole('button', { name: zhCN.signInFlow.factorKind.webauthn }),
    ).toBeTruthy()
    expect(screen.getByRole('button', { name: 'magic' })).toBeTruthy()
  })
})

describe('FlowStepFields passkey arrival', () => {
  it('starts an assertion ceremony on arrival and does not restart it on a re-render', async () => {
    // The passkey entry the viewer clicked is the only control: the
    // assertion step it opens starts the browser's ceremony itself.
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const restore = installCredentials({ get, create: vi.fn() })
    try {
      const onAnswer = vi.fn()
      // A state update re-renders the renderer with a fresh step object,
      // which is what a parent's ordinary re-render looks like.
      function Harness() {
        const [, setTick] = useState(0)
        return (
          <>
            <button type="button" onClick={() => setTick((tick) => tick + 1)}>
              rerender
            </button>
            <FlowStepFields
              step={step({
                kind: 'webauthn',
                options: { challenge: 'Y2hhbGxlbmdl' },
              })}
              busy={false}
              onAnswer={onAnswer}
              onUnsupported={vi.fn()}
            />
          </>
        )
      }
      renderWithProviders(<Harness />)

      expect(
        await screen.findByText(zhCN.signInFlow.passkeyWaiting),
      ).toBeInTheDocument()
      // No second click: the start control is not offered at all.
      expect(
        screen.queryByRole('button', { name: zhCN.signInFlow.submit }),
      ).toBeNull()
      expect(get).toHaveBeenCalledTimes(1)

      // The same challenge in a fresh options object is the same ceremony:
      // an ordinary re-render leaves it running.
      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'rerender' }))
      expect(get).toHaveBeenCalledTimes(1)
      expect(onAnswer).not.toHaveBeenCalled()
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
        const [seq, setSeq] = useState(1)
        return (
          <>
            <button type="button" onClick={() => setSeq((value) => value + 1)}>
              resend
            </button>
            <FlowStepFields
              step={{
                seq,
                state: 'second',
                next: {
                  kind: 'webauthn',
                  options: { challenge: 'Y2hhbGxlbmdl' },
                },
              }}
              busy={false}
              onAnswer={vi.fn()}
              onUnsupported={vi.fn()}
            />
          </>
        )
      }
      renderWithProviders(<Harness />)

      expect(
        await screen.findByText(zhCN.signInFlow.passkeyWaiting),
      ).toBeInTheDocument()
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
        const [step, setStep] = useState<AuthnStep>({
          kind: 'webauthn',
          options: { challenge: 'Y2hhbGxlbmdl' },
        })
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
            <FlowStepFields
              step={{ seq: 2, state: 'second', next: step }}
              busy={false}
              onAnswer={vi.fn()}
              onUnsupported={vi.fn()}
            />
          </>
        )
      }
      renderWithProviders(<Harness />)

      expect(
        await screen.findByText(zhCN.signInFlow.passkeyWaiting),
      ).toBeInTheDocument()
      expect(get).toHaveBeenCalledTimes(1)

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: 'different' }))
      expect(get).toHaveBeenCalledTimes(1)
      expect(
        screen.getByLabelText(zhCN.smsSignIn.codeLabel),
      ).toBeInTheDocument()
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
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      ).toBeTruthy()
      expect(create).not.toHaveBeenCalled()

      await userEvent
        .setup()
        .click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))
      await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    } finally {
      restore()
    }
  })
})

describe('FlowStepFields unsupported step', () => {
  it('render the notice with an exit control that reports upward', async () => {
    const user = userEvent.setup()
    const onUnsupported = vi.fn()
    render(
      {
        kind: 'redirect',
        url: 'https://idp.example/authorize',
        method: 'oidc',
      },
      onUnsupported,
    )

    expect(
      screen.getByText(zhCN.signInFlow.unsupportedStep),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', {
        name: zhCN.signInFlow.unsupportedStepAction,
      }),
    )
    expect(onUnsupported).toHaveBeenCalledTimes(1)
  })
})
