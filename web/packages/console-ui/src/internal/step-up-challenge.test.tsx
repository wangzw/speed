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
import { AUTHENTICATED_SNAPSHOT } from '../../test-utils/session.js'

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

/** A session whose step-up flow is refused at the start with a 401 while the
 * session is still established: the deployment offers no factor that may raise
 * it (a single-factor-kind assembly), so no step can ever come. */
function refusingStartSession(): AuthSession {
  return {
    ...flowSession().session,
    isStepUpShortfall: () => true,
    startAuthenticatedFlow: () =>
      Promise.reject({
        status: 401,
        code: 'client.http.401',
        message: 'refused',
        auth: true,
      }),
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

  it('says the session must be re-established when a step-up cannot raise it', async () => {
    const session = refusingStartSession()
    renderWithProviders(
      <StepUpChallenge
        open
        session={session}
        onSuccess={() => undefined}
        onCancel={() => undefined}
      />,
    )
    // The start is refused: the dialog tells the operator to re-establish the
    // session and does not present the generic retryable 401 line.
    expect(
      await screen.findByText(/本部署没有可提升当前会话的验证步骤/),
    ).toBeInTheDocument()
    expect(screen.queryByText(/HTTP 401/)).toBeNull()
  })
})
