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
 * answer renders its code text and stays retryable; an account with no
 * active factor is recognised from the caller's own factor read and the
 * dialog names the missing second factor, renders no link and starts no
 * flow at all; a 401 start refusal on an established
 * session (factors exist, none may strengthen it) offers a re-login; a
 * 400 or any other start failure renders as its code, never as either of
 * those explanations; and cancel fires onCancel without running the gated
 * action.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthSession, AuthSnapshot } from '@speed/auth-core'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { isApiError } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../../test-utils/real-client.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { captureRefreshWarning } from '../../test-utils/refresh-warning.js'
import { StepUpChallenge } from './step-up-challenge.js'

const FLOW_START = '/auth/flows/authenticated'
const FLOW_ANSWER = '/auth/flows/answer'

/** One active factor as the caller's own factor read answers it. */
const ACTIVE_FACTOR = {
  id: 'factor-1',
  kind: 'totp',
  label: 'Authenticator app',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
}

/** Binds a scripted request function that answers the caller's own factor
 * read with `factors`; any other leg is a defect of the case. For the
 * cases that hand the dialog a session directly and never touch the wire. */
function bindFactors(factors: readonly unknown[]): void {
  bindRequestFn((async (path: string, options?: RequestOptions) => {
    if ((options?.method ?? 'GET') === 'GET' && path === '/auth/factors') {
      return { factors }
    }
    throw new Error(`unexpected ${options?.method ?? 'GET'} ${path}`)
  }) as RequestFn)
}

/** A responder whose step-up flow offers one TOTP factor, challenges for a
 * code, and elevates on the code answer; the caller's own factor read
 * answers one active factor. */
function makeStepUpResponder(overrides: {
  readonly codeAnswer?: () => Response
} = {}): RealResponder {
  return (call: RealCall) => {
    if (call.method === 'GET' && call.path === '/auth/factors') {
      return jsonResponse(200, { factors: [ACTIVE_FACTOR] })
    }
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
  features: { state: 'ready', names: new Set() },
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
    retryFeatures: unused,
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
    const warn = captureRefreshWarning()
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
    // The refused answer's 401 is the expected refresh-failure diagnostic.
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.login_failed',
        trace_id: 'trace-1',
      }),
    )
    // Exactly one answer was refused; a duplicated answer would warn again.
    expect(warn).toHaveBeenCalledTimes(1)
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

  it('names the missing second factor instead of starting a flow that cannot come', async () => {
    // The caller's own factor read says the account has no active factor,
    // so no step-up can ever raise a single-factor session: the dialog
    // names that and renders no link at all -- these sections are the
    // account settings surface, so nothing here navigates -- and no flow
    // start is sent at all.
    const { rig } = mount(
      (call: RealCall) => {
        if (call.method === 'GET' && call.path === '/auth/factors') {
          return jsonResponse(200, { factors: [] })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      },
    )

    expect(await screen.findByText(zhCN.mfa.stepUp.noFactor)).toBeTruthy()
    expect(screen.queryAllByRole('link')).toHaveLength(0)
    expect(
      screen.queryByRole('button', { name: zhCN.mfa.stepUp.relogin }),
    ).toBeNull()
    expect(rig.calls.some((call) => call.path === FLOW_START)).toBe(false)
  })

  it('renders a 400 start refusal as an ordinary error when a factor is registered', async () => {
    // A 400 is not, by itself, "no factor to elevate with": the account
    // has an active factor here, so the dialog shows the refusal's own
    // code instead of either of the two explanations.
    bindFactors([ACTIVE_FACTOR])
    mountSession(
      refusingStartSession({
        status: 400,
        code: 'authn.bad_request',
        message: 'refused',
        auth: false,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    expect(screen.queryByText(zhCN.mfa.stepUp.cannotElevate)).toBeNull()
    expect(screen.queryByText(zhCN.mfa.stepUp.noFactor)).toBeNull()
  })

  it('treats a 401 start refusal on an established session as unavailable', async () => {
    // The account has factors, but none whose methods may strengthen this
    // session: the start answers 401 insufficient_user_authentication and
    // the session stays authenticated -- a refusal no step can satisfy.
    bindFactors([ACTIVE_FACTOR])
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
    // (not a 401 shortfall), so it must not be merged into the unavailable
    // phase: the dialog shows its resolved code text and still offers the
    // flow.
    bindFactors([ACTIVE_FACTOR])
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
