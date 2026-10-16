/**
 * StepUpChallenge: the re-authentication dialog of the step-up-gated
 * console writes.
 *
 * The dialog drives the session's step-up flow (auth-core's useStepUp)
 * and forwards each answer; the assertion here is the one wire detail the
 * backend requires and a naive renderer misses: the second factor's code
 * challenge is answered under the `factor` step with the chosen factor's
 * identifier resent alongside the code (pkg/authn/login's applyFactor
 * re-reads the factor from the answer).
 *
 * The other assertion is the deployment that cannot raise the session: a
 * step-up start refused with a 400 (no factor to elevate with) parks the
 * flow on 'unavailable', and the dialog names the situation and offers a
 * re-login rather than a verification step.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type {
  AuthSession,
  AuthSessionListener,
  AuthSnapshot,
  AuthnFlowState,
} from '@speed/auth-core'
import { StepUpChallenge } from './step-up-challenge.js'
import { renderWithProviders } from '../../test-utils/render.js'
import {
  AUTHENTICATED_SNAPSHOT,
  createStepUpSession,
  type StepUpTestSession,
} from '../../test-utils/session.js'

/** A session whose step-up flow offers one factor, then asks for its code,
 * then settles elevated; it records every answer's input. */
function flowSession(): {
  readonly session: AuthSession
  readonly answers: Array<Readonly<Record<string, string>> | undefined>
} {
  const answers: Array<Readonly<Record<string, string>> | undefined> = []
  const snapshot: AuthSnapshot = AUTHENTICATED_SNAPSHOT
  const listeners = new Set<AuthSessionListener>()
  const session: AuthSession = {
    getSnapshot: () => snapshot,
    subscribe: (listener) => {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    isStepUpShortfall: () => true,
    startAuthenticatedFlow: () =>
      Promise.resolve<AuthnFlowState>({
        seq: 1,
        state: 'second',
        next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
      }),
    answerFlow: (request) => {
      answers.push(request.input)
      return Promise.resolve<AuthnFlowState>(
        answers.length === 1
          ? {
              seq: 2,
              state: 'second',
              next: {
                kind: 'challenge',
                fields: [{ name: 'code', kind: 'code', required: true }],
              },
            }
          : {
              state: 'authenticated',
              session: { id: 'sess-1', expires_at: '2026-10-17T00:00:00Z' },
            },
      )
    },
    abandonFlow: () => Promise.resolve(),
    readFlow: () => Promise.resolve(null),
    listMethods: () =>
      Promise.resolve({
        methods: [{ name: 'password', kind: 'form' }],
        signup: { enabled: false },
        mfa: { factors: [] },
      }),
    discoverSso: () => Promise.resolve([]),
    startFlow: () => Promise.reject(new Error('unused')),
    logout: () => Promise.reject(new Error('unused')),
    restore: () => Promise.resolve(false),
    refresh: () => Promise.resolve(false),
    setPermissionSet: () => undefined,
  }
  return { session, answers }
}

/** A session whose step-up start is refused with a 400 while the session
 * stays established: the deployment has no factor to raise it with (the
 * start names no method the deployment could take), so no step can ever
 * come. The shortfall classifier is auth-core's own, so a 400 is not read
 * as a strength shortfall -- only the hook's 400 branch reaches it. */
function refusingStartSession(logout: () => Promise<void>): StepUpTestSession {
  return {
    ...createStepUpSession(),
    startAuthenticatedFlow: () =>
      Promise.reject({
        status: 400,
        code: 'client.http.400',
        message: 'refused',
        auth: false,
      }),
    logout,
  }
}

describe('StepUpChallenge', () => {
  it('resends the chosen factor alongside the code of its challenge', async () => {
    const user = userEvent.setup()
    const { session, answers } = flowSession()
    const onSuccess = vi.fn()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={onSuccess}
        onCancel={() => undefined}
      />,
    )
    // The flow offers the factor; choosing it starts the code challenge.
    await user.click(await screen.findByRole('button', { name: 'totp' }))
    await user.type(await screen.findByLabelText(/code/), '123456')
    await user.click(screen.getByRole('button', { name: '验证' }))
    await vi.waitFor(() => {
      expect(onSuccess).toHaveBeenCalledTimes(1)
    })
    // The factor-choice answer carries the id; the code answer carries the
    // same id again beside the code.
    expect(answers).toEqual([{ factor: 'f1' }, { code: '123456', factor: 'f1' }])
  })

  it('answers a passkey step-up under the factor step, driven by its options', async () => {
    const user = userEvent.setup()
    const requests: Array<{ step: string | undefined; input?: Readonly<Record<string, string>> }> = []
    const session: AuthSession = {
      ...flowSession().session,
      startAuthenticatedFlow: () =>
        Promise.resolve<AuthnFlowState>({
          seq: 1,
          state: 'second',
          next: {
            kind: 'webauthn',
            options: { challenge: 'Y2hhbGxlbmdl', allowCredentials: [] },
          },
        }),
      answerFlow: (request) => {
        requests.push({ step: request.step, input: request.input })
        return Promise.resolve<AuthnFlowState>({
          state: 'authenticated',
          session: { id: 'sess-1', expires_at: '2026-10-17T00:00:00Z' },
        })
      },
    }
    const get = vi.fn().mockResolvedValue({
      id: 'AQID',
      rawId: new Uint8Array([1, 2, 3]).buffer,
      type: 'public-key',
      response: {
        clientDataJSON: new Uint8Array([4]).buffer,
        authenticatorData: new Uint8Array([5]).buffer,
        signature: new Uint8Array([6]).buffer,
        userHandle: null,
      },
      getClientExtensionResults: () => ({}),
      authenticatorAttachment: 'platform',
    })
    const original = Object.getOwnPropertyDescriptor(window.navigator, 'credentials')
    Object.defineProperty(window.navigator, 'credentials', {
      value: { get, create: vi.fn() },
      configurable: true,
    })
    try {
      const onSuccess = vi.fn()
      renderWithProviders(
        <StepUpChallenge
          open
          session={session}
          onSuccess={onSuccess}
          onCancel={() => undefined}
        />,
      )
      // The passkey step drives the ceremony; its answer travels under the
      // second factor's step (`factor`), not the enrollment one.
      await user.click(await screen.findByRole('button', { name: '验证' }))
      await vi.waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1))
    } finally {
      if (original === undefined) {
        delete (window.navigator as { credentials?: unknown }).credentials
      } else {
        Object.defineProperty(window.navigator, 'credentials', original)
      }
    }
    expect(requests).toHaveLength(1)
    expect(requests[0]?.step).toBe('factor')
    expect(requests[0]?.input?.credential).toContain('AQID')
  })

  it('offers a re-login when the deployment cannot complete the step-up', async () => {
    const user = userEvent.setup()
    const logout = vi.fn(() => Promise.resolve())
    const onCancel = vi.fn()
    renderWithProviders(
      <StepUpChallenge
        open
        session={refusingStartSession(logout)}
        onSuccess={() => undefined}
        onCancel={onCancel}
      />,
    )
    // The start is refused: the dialog names the situation and offers the
    // way out, not a verification the deployment cannot satisfy and not the
    // raw status the answer carried.
    expect(
      await screen.findByText(/本部署无法完成此次升级/),
    ).toBeInTheDocument()
    expect(screen.queryByText(/请先完成一次验证/)).toBeNull()
    expect(screen.queryByText(/HTTP 400/)).toBeNull()
    // The re-login ends the session (the host's session gate converges on its
    // sign-in surface) and forgets the refused write, as a cancel does.
    await user.click(screen.getByRole('button', { name: '重新登录' }))
    expect(logout).toHaveBeenCalledTimes(1)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })
})
