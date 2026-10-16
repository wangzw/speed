// @vitest-environment jsdom
/**
 * step-up.test.ts -- useStepUp, the headless driver of a re-authentication
 * challenge. The distinction this suite pins: a start refused 401 while the
 * session is still established (isStepUpShortfall) is the deployment declining
 * to raise the session -- no factor may strengthen it in a single-factor-kind
 * assembly -- so the challenge parks on 'unavailable'; a start refused for any
 * other reason stays on 'failed' with its resolved code, retryable. A caller
 * renders the two differently (re-establish the session vs. retry).
 *
 * The session is a minimal hand-rolled AuthSession so the start's refusal is
 * controlled directly; the flow operations the challenge does not reach are
 * stubs that throw.
 */

import { cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { isApiError } from '@speed/api-client'
import type { AuthSession, AuthSnapshot } from './session.js'
import { useStepUp } from './step-up.js'

afterEach(() => {
  cleanup()
})

function unused(): never {
  throw new Error('the step-up test session drives no such operation')
}

/** A session whose step-up start rejects with `startError`, classifying a
 * shortfall exactly as auth-core's own session does. */
function session(startError: unknown): AuthSession {
  const snapshot: AuthSnapshot = {
    state: 'authenticated',
    principal: null,
    permissionSets: { tenant: null, system: null },
  }
  return {
    getSnapshot: () => snapshot,
    subscribe: () => () => undefined,
    isStepUpShortfall: (error) =>
      isApiError(error) && error.status === 401 && snapshot.state === 'authenticated',
    startAuthenticatedFlow: () => Promise.reject(startError),
    answerFlow: unused,
    abandonFlow: () => Promise.resolve(),
    readFlow: () => Promise.resolve(null),
    listMethods: unused,
    discoverSso: unused,
    startFlow: unused,
    logout: unused,
    restore: unused,
    refresh: unused,
    setPermissionSet: unused,
  }
}

describe('useStepUp', () => {
  it('parks on unavailable when the start is a step-up shortfall', async () => {
    const refusal = {
      status: 401,
      code: 'client.http.401',
      message: 'refused',
      auth: true,
    }
    const { result } = renderHook(() => useStepUp(session(refusal), true, () => undefined))
    await waitFor(() => {
      expect(result.current.phase).toBe('unavailable')
    })
  })

  it('stays on failed with its code when the start is refused for another reason', async () => {
    const refusal = {
      status: 403,
      code: 'authn.forbidden',
      message: 'refused',
      auth: false,
    }
    const { result } = renderHook(() => useStepUp(session(refusal), true, () => undefined))
    await waitFor(() => {
      expect(result.current.phase).toBe('failed')
    })
    expect(result.current.errorCode).toBe('authn.forbidden')
  })
})
