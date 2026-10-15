/**
 * StepUpChallenge behaviour: the dialog driving one `purpose: 'step-up'`
 * flow (POST /auth/flows/authenticated, then POST /auth/flows/answer per
 * step).
 *
 * What this suite pins: opening the dialog starts the flow and renders
 * its first step; choosing a factor answers with `step: 'factor'` and
 * the factor identifier; the flow's challenge step then asks for a code
 * answered under the same `factor` step; a completion in state 'elevated'
 * fires onSuccess; a refused answer renders its code text and stays
 * retryable; and cancel fires onCancel without running the gated action.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../../test-utils/real-client.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { StepUpChallenge } from './step-up-challenge.js'

const FLOW_START = '/auth/flows/authenticated'
const FLOW_ANSWER = '/auth/flows/answer'

/** A responder whose step-up flow offers one TOTP factor, challenges for a
 * code, and elevates on the code answer. */
function makeStepUpResponder(overrides: {
  readonly codeAnswer?: () => Response
} = {}): RealResponder {
  return (call: RealCall) => {
    if (call.method === 'POST' && call.path === FLOW_START) {
      return jsonResponse(200, {
        seq: 1,
        state: 'second',
        next: {
          kind: 'factor',
          factors: [{ id: 'factor-1', kind: 'totp', label: 'Authenticator app' }],
        },
      })
    }
    if (call.method === 'POST' && call.path === FLOW_ANSWER) {
      const body = call.body as { seq: number; step: string; input?: Record<string, string> }
      if (body.seq === 1) {
        return jsonResponse(200, {
          seq: 2,
          state: 'second',
          next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
        })
      }
      if (overrides.codeAnswer !== undefined) {
        return overrides.codeAnswer()
      }
      return jsonResponse(200, {
        state: 'elevated',
        token_type: 'Bearer',
        access_token: 'access-2',
        expires_in: 3600,
      })
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  }
}

function mount(
  respond: RealResponder,
  handlers: { readonly onSuccess?: () => void; readonly onCancel?: () => void } = {},
) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const onSuccess = vi.fn(handlers.onSuccess)
  const onCancel = vi.fn(handlers.onCancel)
  renderWithProviders(
    <StepUpChallenge
      open
      session={rig.session}
      onSuccess={onSuccess}
      onCancel={onCancel}
    />,
  )
  return { rig, onSuccess, onCancel }
}

describe('StepUpChallenge', () => {
  it('starts the flow, drives the factor then the code, and elevates', async () => {
    const { rig, onSuccess } = mount(makeStepUpResponder())
    const user = userEvent.setup()

    // The factor step offers the flow's factor.
    await user.click(
      await screen.findByRole('button', { name: 'Authenticator app' }),
    )
    // The chosen factor is answered under the `factor` step.
    await waitFor(() =>
      expect(
        rig.calls.find(
          (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
        )?.body,
      ).toEqual({ seq: 1, step: 'factor', input: { factor: 'factor-1' } }),
    )

    await user.type(await screen.findByLabelText('code'), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    const answers = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === FLOW_ANSWER,
    )
    expect(answers[1]?.body).toEqual({
      seq: 2,
      step: 'factor',
      input: { code: '123456' },
    })
  })

  it('renders a refused answer as code text and stays retryable', async () => {
    const { rig, onSuccess } = mount(
      makeStepUpResponder({
        codeAnswer: () => errorResponse(401, 'authn.login_failed'),
      }),
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: 'Authenticator app' }),
    )
    await user.type(await screen.findByLabelText('code'), '000000')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.login_failed)
    expect(onSuccess).not.toHaveBeenCalled()
    // The flow is still open: the answer stayed parked on the code step.
    expect(screen.getByLabelText('code')).toBeTruthy()
    expect(rig.calls.length).toBeGreaterThan(0)
  })

  it('fires onCancel on the cancel button without elevating', async () => {
    const { onSuccess, onCancel } = mount(makeStepUpResponder())
    const user = userEvent.setup()

    await screen.findByRole('button', { name: 'Authenticator app' })
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.cancelLabel }),
    )
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onSuccess).not.toHaveBeenCalled()
  })
})
