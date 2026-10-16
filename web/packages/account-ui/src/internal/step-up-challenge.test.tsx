/**
 * StepUpChallenge behaviour: the dialog driving one `purpose: 'step-up'`
 * flow (POST /auth/flows/authenticated, then POST /auth/flows/answer per
 * step).
 *
 * What this suite pins: opening the dialog starts the flow and renders
 * its first step; choosing a factor answers with `step: 'factor'` and
 * the factor identifier; the flow's challenge step then asks for a code
 * answered under the same `factor` step, the chosen factor re-named
 * beside it; a completion in state 'elevated' fires onSuccess; a refused
 * answer renders its code text and stays retryable; a start refused while
 * the session stays established -- a 400, or a 401
 * insufficient_user_authentication -- parks the dialog on 'unavailable'
 * and offers a re-login rather than a step that cannot come, while a
 * start failure of another kind renders as an ordinary banner; and cancel
 * fires onCancel without running the gated action.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthSession, AuthSnapshot } from '@speed/auth-core'
import { isApiError } from '@speed/api-client'
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

/** Mounts the dialog over a session the test hands in directly, for the
 * phase cases that never touch the wire. */
function mountSession(
  session: AuthSession,
  handlers: { readonly onSuccess?: () => void; readonly onCancel?: () => void } = {},
) {
  const onSuccess = vi.fn(handlers.onSuccess)
  const onCancel = vi.fn(handlers.onCancel)
  renderWithProviders(
    <StepUpChallenge
      open
      session={session}
      onSuccess={onSuccess}
      onCancel={onCancel}
    />,
  )
  return { onSuccess, onCancel }
}

/** An established session: the snapshot the step-up shortfall classifier
 * reads -- a start refusal is only "unavailable" while the session is
 * still authenticated. */
const AUTHENTICATED_SNAPSHOT: AuthSnapshot = {
  state: 'authenticated',
  principal: {
    account_id: 'account-1',
    kind: 'user',
    display_name: 'Ada',
    mfa_required: false,
    password_set: true,
    created_at: '2026-01-01T00:00:00Z',
    attributes: {},
    locale: 'zh-CN',
    time_zone: 'UTC',
    identifiers: [],
    links: [],
  },
  permissionSets: { tenant: null, system: null },
}

function unused(): never {
  throw new Error('the step-up dialog drives no such operation')
}

/** A session over an authenticated snapshot whose step-up start is
 * refused by `refusal`. The dialog reads only getSnapshot,
 * isStepUpShortfall, startAuthenticatedFlow and abandonFlow; every other
 * operation is unused. */
function refusingStartSession(
  refusal: unknown,
  logout: () => Promise<void> = () => Promise.resolve(),
): AuthSession {
  return {
    getSnapshot: () => AUTHENTICATED_SNAPSHOT,
    subscribe: () => () => undefined,
    isStepUpShortfall: (error) =>
      isApiError(error) &&
      error.status === 401 &&
      AUTHENTICATED_SNAPSHOT.state === 'authenticated',
    listMethods: unused,
    discoverSso: unused,
    startFlow: unused,
    startAuthenticatedFlow: () => Promise.reject(refusal),
    answerFlow: unused,
    readFlow: unused,
    abandonFlow: () => Promise.resolve(),
    logout,
    restore: unused,
    refresh: unused,
    setPermissionSet: unused,
  }
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
    // The code answer re-names the chosen factor: a challenge reply carries
    // only the field the factor asks for, and the server re-reads the
    // factor from it.
    expect(answers[1]?.body).toEqual({
      seq: 2,
      step: 'factor',
      input: { code: '123456', factor: 'factor-1' },
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

  it('offers a re-login when the deployment cannot raise the session', async () => {
    const user = userEvent.setup()
    const logout = vi.fn(() => Promise.resolve())
    // The start names no method the deployment could take (no factor to
    // elevate with): a 400 while the session stays established. The
    // client reads this deployment's 400 as authn.bad_request.
    const { onCancel, onSuccess } = mountSession(
      refusingStartSession(
        {
          status: 400,
          code: 'authn.bad_request',
          message: 'refused',
          auth: false,
        },
        logout,
      ),
    )

    // The dialog names the situation and offers the way out -- not the
    // deployment's 400 re-read as "invalid request", and not a step that
    // cannot come.
    expect(await screen.findByText(zhCN.mfa.stepUp.cannotElevate)).toBeTruthy()
    expect(screen.queryByText(zhCN.errors.authn.bad_request)).toBeNull()
    expect(screen.queryByText(zhCN.mfa.stepUp.description)).toBeNull()

    // The re-login ends the session (the host's session gate converges on
    // its sign-in surface) and forgets the refused action, as a cancel does.
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    )
    expect(logout).toHaveBeenCalledTimes(1)
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onSuccess).not.toHaveBeenCalled()
  })

  it('treats a 401 start refusal on an established session as unavailable', async () => {
    // The account has factors, but none whose methods may strengthen this
    // session: the start answers 401 insufficient_user_authentication and
    // the session stays authenticated -- a refusal no step can satisfy.
    mountSession(
      refusingStartSession({
        status: 401,
        code: 'authn.insufficient_user_authentication',
        message: 'refused',
        auth: true,
      }),
    )

    expect(await screen.findByText(zhCN.mfa.stepUp.cannotElevate)).toBeTruthy()
    expect(
      screen.queryByText(zhCN.errors.authn.insufficient_user_authentication),
    ).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    ).toBeTruthy()
  })

  it('renders a start failure that is not a refusal as an ordinary banner', async () => {
    // A 503 is not the deployment declining to raise the session -- the
    // session is established, but the failure is not an elevation refusal
    // (not a 401 shortfall, not a 400), so it must not be merged into the
    // unavailable phase: the dialog shows its resolved code text and
    // still offers the flow.
    mountSession(
      refusingStartSession({
        status: 503,
        code: 'authn.unavailable',
        message: 'down',
        auth: false,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.unavailable)
    expect(screen.queryByText(zhCN.mfa.stepUp.cannotElevate)).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    ).toBeNull()
  })
})
