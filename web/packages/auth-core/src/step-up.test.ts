// @vitest-environment jsdom
/**
 * step-up.test.ts -- useStepUp, the headless driver of a re-authentication
 * challenge. The distinction this suite pins: a start the deployment declines
 * to raise -- a 401 while the session is still established (isStepUpShortfall),
 * or a 400 (the start named no method the deployment could take, which is an
 * account with no factor at all) -- parks the challenge on 'unavailable'; a
 * start refused for any other reason, or a 400 on a session that is no longer
 * established, stays on 'failed' with its resolved code, retryable. A caller
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
 * shortfall exactly as auth-core's own session does. The snapshot defaults to
 * established; an anonymous one models a refusal that ended the session. */
function session(
  startError: unknown,
  snapshot: AuthSnapshot = {
    state: 'authenticated',
    principal: null,
    permissionSets: { tenant: null, system: null },
  },
): AuthSession {
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

const ANONYMOUS: AuthSnapshot = {
  state: 'anonymous',
  principal: null,
  permissionSets: { tenant: null, system: null },
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

  it('parks on unavailable when the start names no method the deployment could take', async () => {
    // The deployment has no factor to raise the session with: the step-up
    // start, issued with no method, is answered 400 (pkg/authn/login's
    // stepUpStart, which reports a method not taken in).
    const refusal = {
      status: 400,
      code: 'client.http.400',
      message: 'refused',
      auth: false,
    }
    const { result } = renderHook(() => useStepUp(session(refusal), true, () => undefined))
    await waitFor(() => {
      expect(result.current.phase).toBe('unavailable')
    })
  })

  it('stays on failed with its code when a 400 lands on a session no longer established', async () => {
    const refusal = {
      status: 400,
      code: 'client.http.400',
      message: 'refused',
      auth: false,
    }
    const { result } = renderHook(() =>
      useStepUp(session(refusal, ANONYMOUS), true, () => undefined),
    )
    await waitFor(() => {
      expect(result.current.phase).toBe('failed')
    })
    expect(result.current.errorCode).toBe('client.http.400')
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
