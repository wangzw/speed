// @vitest-environment jsdom
/**
 * step-up.test.ts -- useStepUp, the headless driver of a re-authentication
 * challenge. The distinction this suite pins: a start the deployment declines
 * to raise (a 401 while the session is still established, isStepUpShortfall)
 * parks the challenge on 'unavailable'; any other start failure -- a 400, a
 * 503, a refusal on a session that is no longer established -- stays on
 * 'failed' with its resolved code, retryable. A 400 alone does not say the
 * account has no factor (an account with no factor is identified by reading
 * it), so it must not be read as the deployment's own refusal: a caller
 * renders 'unavailable' as "re-establish the session" and 'failed' as "the
 * request failed, and here is its code".
 *
 * The session is a minimal hand-rolled AuthSession so the start's refusal is
 * controlled directly; the flow operations the challenge does not reach are
 * stubs that throw.
 */

import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { isApiError } from '@speed/api-client'
import type { AuthSession, AuthnFlowState, AuthSnapshot } from './session.js'
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
    impersonating: false,
    permissionSets: { tenant: null, system: null },
    features: { state: 'none' },
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
    register: unused,
    startFlow: unused,
    logout: unused,
    restore: unused,
    beginIdentityTransition: () => () => undefined,
    refresh: unused,
    retryFeatures: unused,
    setPermissionSet: unused,
  }
}

const ANONYMOUS: AuthSnapshot = {
  state: 'anonymous',
  principal: null,
  impersonating: false,
  permissionSets: { tenant: null, system: null },
  features: { state: 'none' },
}

describe('useStepUp', () => {
  it('parks on unavailable when the start is a step-up shortfall', async () => {
    const refusal = {
      status: 401,
      code: 'client.http.401',
      message: 'refused',
      auth: true,
    }
    const driven = session(refusal)
    const { result } = renderHook(() => useStepUp(driven, true, () => undefined))
    await waitFor(() => {
      expect(result.current.phase).toBe('unavailable')
    })
  })

  it('stays on failed with its code when the start is a 400 on an established session', async () => {
    // A 400 is the start naming something the server cannot take. It is
    // not the deployment declining to raise the session -- an account
    // with no factor is recognised by reading its factors, not by the
    // status of this refusal -- so an ordinary error is not excused as
    // an unraisable session.
    const refusal = {
      status: 400,
      code: 'client.http.400',
      message: 'refused',
      auth: false,
    }
    const driven = session(refusal)
    const { result } = renderHook(() => useStepUp(driven, true, () => undefined))
    await waitFor(() => {
      expect(result.current.phase).toBe('failed')
    })
    expect(result.current.errorCode).toBe('client.http.400')
  })

  it('stays on failed with its code when a 400 lands on a session no longer established', async () => {
    const refusal = {
      status: 400,
      code: 'client.http.400',
      message: 'refused',
      auth: false,
    }
    const driven = session(refusal, ANONYMOUS)
    const { result } = renderHook(() =>
      useStepUp(driven, true, () => undefined),
    )
    await waitFor(() => {
      expect(result.current.phase).toBe('failed')
    })
    expect(result.current.errorCode).toBe('client.http.400')
  })

  it('releases nothing when an answer finds its flow gone', async () => {
    // An answer refused as flow_invalid says the record no longer exists.
    // Releasing the binding afterwards would name whatever flow the cookie
    // holds by then -- the fresh one a restart opens -- so the claim ends
    // with the refusal and the unmount sends no abandon.
    const openStep = {
      seq: 1,
      state: 'first',
      next: { kind: 'prompt', fields: [] },
    } as unknown as AuthnFlowState
    const abandon = vi.fn(() => Promise.resolve())
    const base = session(new Error('the start is scripted'))
    const driven: AuthSession = {
      ...base,
      startAuthenticatedFlow: () => Promise.resolve(openStep),
      answerFlow: () =>
        Promise.reject({
          status: 401,
          code: 'authn.flow_invalid',
          message: 'gone',
          auth: true,
        }),
      abandonFlow: abandon,
    }
    const { result, unmount } = renderHook(() => useStepUp(driven, true, () => undefined))
    await waitFor(() => {
      expect(result.current.phase).toBe('step')
    })

    act(() => {
      result.current.answer({ step: 'answer', input: { password: 'x' } })
    })
    await waitFor(() => {
      expect(result.current.phase).toBe('failed')
    })
    expect(result.current.errorCode).toBe('authn.flow_invalid')

    act(() => {
      unmount()
    })
    await act(async () => {
      await Promise.resolve()
    })
    expect(abandon).not.toHaveBeenCalled()
  })

  it('stays on failed with its code when the start is refused for another reason', async () => {
    const refusal = {
      status: 403,
      code: 'authn.forbidden',
      message: 'refused',
      auth: false,
    }
    const driven = session(refusal)
    const { result } = renderHook(() => useStepUp(driven, true, () => undefined))
    await waitFor(() => {
      expect(result.current.phase).toBe('failed')
    })
    expect(result.current.errorCode).toBe('authn.forbidden')
  })
})
