// @vitest-environment jsdom
/**
 * useStepUp + session.isStepUpShortfall: the session operation behind a
 * step-up-gated surface. A real session is driven over the shared
 * scripting harness (a fake request function bound through the api-sdk
 * runtime seam), and the assertions are the observable state only: the
 * discriminator's answer, the hook's phase/step, and the one elevation
 * callback.
 */

import { StrictMode } from 'react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { ApiError } from '@speed/api-client'
import {
  LOGIN_ABANDON,
  LOGIN_ANSWER,
  LOGIN_START_AUTHENTICATED,
  PROFILE_GET,
  SESSION_GET,
  apiError,
  completeFlow,
  makeCookieHarness,
  openFlow,
  principal,
  sessionCompletion,
  sessionView,
} from '@speed/test-utils/session-harness'
import type { AuthSession } from './session.js'
import { useStepUp } from './step-up.js'

/** A session established by a restore of a valid cookie. `script` adds
 * handlers on top of the restore reads, so a test scripts the operation
 * its surface drives against a real authenticated session. */
async function authenticatedSession(
  script: Parameters<typeof makeCookieHarness>[0] = {},
): Promise<AuthSession> {
  const { session } = makeCookieHarness({
    [SESSION_GET]: () => sessionView({ authenticated: true }),
    [PROFILE_GET]: () => principal(),
    ...script,
  })
  await session.restore()
  return session
}

describe('session.isStepUpShortfall', () => {
  it('is true for a 401 while the session is still established', async () => {
    const session = await authenticatedSession()
    expect(session.getSnapshot().state).toBe('authenticated')
    expect(session.isStepUpShortfall(apiError(401, 'client.http.401'))).toBe(true)
  })

  it('is false for a non-401 or a non-ApiError', async () => {
    const session = await authenticatedSession()
    expect(session.isStepUpShortfall(apiError(403, 'client.http.403'))).toBe(false)
    expect(session.isStepUpShortfall(new Error('boom'))).toBe(false)
  })

  it('is false for a 401 when the session is anonymous', () => {
    const { session } = makeCookieHarness({})
    expect(session.getSnapshot().state).toBe('anonymous')
    expect(session.isStepUpShortfall(apiError(401, 'client.http.401'))).toBe(false)
  })
})

/** The abandon requests (`DELETE /api/v1/auth/flows/current`) a harness recorded. */
function abandons(
  calls: readonly { readonly method: string; readonly path: string }[],
) {
  return calls.filter(
    (call) => call.method === 'DELETE' && call.path === '/api/v1/auth/flows/current',
  )
}

describe('useStepUp', () => {
  it('issues no abandon request when it mounts closed', () => {
    // A surface may render the challenge closed and never open it (the
    // account sections do). The mount started no flow, so there is no
    // binding to release: closing must not invent one.
    const { session, calls } = makeCookieHarness({})
    renderHook(() => useStepUp(session, false, () => undefined))
    expect(calls).toEqual([])
  })

  it('abandons exactly once when the surface unmounts with its flow still open', async () => {
    // A surface may unmount instead of turning `active` false: the
    // console's StepUpChallenge renders nothing while closed, and a
    // restart remounts its driver. The flow this mount opened and left
    // open is released on the way out.
    const { session, calls } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      [LOGIN_ABANDON]: () => ({}),
    })
    const { result, unmount } = renderHook(() =>
      useStepUp(session, true, () => undefined),
    )
    await waitFor(() => {
      expect(result.current.phase).toBe('step')
    })
    expect(abandons(calls)).toHaveLength(0)

    unmount()
    await waitFor(() => {
      expect(abandons(calls)).toHaveLength(1)
    })
  })

  it('sends the surface’s path as the flow’s return_to', async () => {
    // A redirect method's round trip must land back on the page that
    // started the challenge, so that page is the one that reads a refusal.
    const { session, calls } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      [LOGIN_ABANDON]: () => ({}),
    })
    renderHook(() =>
      useStepUp(session, true, () => undefined, undefined, '/account'),
    )
    const start = await waitFor(() => {
      const call = calls.find(
        (entry) => entry.path === '/api/v1/auth/flows/authenticated',
      )
      expect(call).toBeDefined()
      return call
    })
    expect(start?.options?.body).toMatchObject({
      purpose: 'step-up',
      return_to: '/account',
    })
  })

  it('omits return_to when the surface gives no path', async () => {
    const { session, calls } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      [LOGIN_ABANDON]: () => ({}),
    })
    renderHook(() => useStepUp(session, true, () => undefined))
    const start = await waitFor(() => {
      const call = calls.find(
        (entry) => entry.path === '/api/v1/auth/flows/authenticated',
      )
      expect(call).toBeDefined()
      return call
    })
    expect(start?.options?.body).not.toHaveProperty('return_to')
  })

  it('sends no abandon when a settled flow unmounts', async () => {
    // The server consumed the settled flow; unmounting afterwards has no
    // binding to release.
    const { session, calls } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      [LOGIN_ANSWER]: () => sessionCompletion(),
      [LOGIN_ABANDON]: () => ({}),
    })
    const { result, unmount } = renderHook(() =>
      useStepUp(session, true, () => undefined),
    )
    await waitFor(() => {
      expect(result.current.phase).toBe('step')
    })
    act(() => {
      result.current.answer({ step: 'factor', input: { factor: 'f1' } })
    })
    await waitFor(() => {
      expect(result.current.phase).toBe('elevated')
    })

    unmount()
    await act(async () => {
      await Promise.resolve()
    })
    expect(abandons(calls)).toHaveLength(0)
  })

  it('sends no abandon when StrictMode remounts the effect', async () => {
    // The development double effect invocation cleans up and remounts in
    // the same task. The deferred release is cancelled by the remount's
    // setup, so opening a challenge never sends a DELETE of its own.
    const { session, calls } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      [LOGIN_ABANDON]: () => ({}),
    })
    const wrapper = ({ children }: { readonly children: ReactNode }) => (
      <StrictMode>{children}</StrictMode>
    )
    const { result, unmount } = renderHook(
      () => useStepUp(session, true, () => undefined),
      { wrapper },
    )
    await waitFor(() => {
      expect(result.current.phase).toBe('step')
    })
    await act(async () => {
      await Promise.resolve()
    })
    expect(abandons(calls)).toHaveLength(0)

    unmount()
    await waitFor(() => {
      expect(abandons(calls)).toHaveLength(1)
    })
  })

  it('abandons exactly once when a flow it started turns inactive', async () => {
    const { session, calls } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      [LOGIN_ABANDON]: () => ({}),
    })
    const { result, rerender } = renderHook(
      ({ active }: { active: boolean }) => useStepUp(session, active, () => undefined),
      { initialProps: { active: false } },
    )
    expect(abandons(calls)).toHaveLength(0)

    rerender({ active: true })
    await waitFor(() => {
      expect(result.current.phase).toBe('step')
    })

    rerender({ active: false })
    expect(abandons(calls)).toHaveLength(1)
  })

  it('sends no abandon when a settled flow closes', async () => {
    // A completed flow is consumed by the server; closing the challenge
    // afterwards has no binding to release.
    const { session, calls } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      [LOGIN_ANSWER]: () => sessionCompletion(),
      [LOGIN_ABANDON]: () => ({}),
    })
    const { result, rerender } = renderHook(
      ({ active }: { active: boolean }) => useStepUp(session, active, () => undefined),
      { initialProps: { active: false } },
    )
    rerender({ active: true })
    await waitFor(() => {
      expect(result.current.phase).toBe('step')
    })
    act(() => {
      result.current.answer({ step: 'factor', input: { factor: 'f1' } })
    })
    await waitFor(() => {
      expect(result.current.phase).toBe('elevated')
    })

    rerender({ active: false })
    expect(abandons(calls)).toHaveLength(0)
  })

  it('sends no abandon when a refused start closes', async () => {
    // The start was refused, so the server created no flow: closing the
    // dialog has no binding to release.
    const { session, calls } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () => {
        throw apiError(400, 'authn.bad_request')
      },
      [LOGIN_ABANDON]: () => ({}),
    })
    const { result, rerender } = renderHook(
      ({ active }: { active: boolean }) => useStepUp(session, active, () => undefined),
      { initialProps: { active: false } },
    )

    rerender({ active: true })
    await waitFor(() => {
      expect(result.current.phase).toBe('failed')
    })

    rerender({ active: false })
    expect(abandons(calls)).toHaveLength(0)
  })

  it('sends no abandon when an unavailable start closes', async () => {
    // The unraisable-session refusal (401 while the session stays
    // established) is also a refused start -- no flow was created -- and
    // the dialog it leaves on screen offers a re-login, which cancels it.
    // Closing must not release a binding that never existed.
    const { session, calls } = makeCookieHarness({
      [SESSION_GET]: () => sessionView({ authenticated: true }),
      [PROFILE_GET]: () => principal(),
      [LOGIN_START_AUTHENTICATED]: () => {
        throw apiError(401, 'authn.insufficient_user_authentication')
      },
      [LOGIN_ABANDON]: () => ({}),
    })
    await session.restore()
    const { result, rerender } = renderHook(
      ({ active }: { active: boolean }) => useStepUp(session, active, () => undefined),
      { initialProps: { active: false } },
    )

    rerender({ active: true })
    await waitFor(() => {
      expect(result.current.phase).toBe('unavailable')
    })

    rerender({ active: false })
    expect(abandons(calls)).toHaveLength(0)
  })

  it('starts on opening, parks on the step, and reports the elevation once', async () => {
    const { session, calls } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      [LOGIN_ANSWER]: () => sessionCompletion(),
      [LOGIN_ABANDON]: () => ({}),
    })
    const onElevated = vi.fn()
    const { result, rerender } = renderHook(
      ({ active }: { active: boolean }) => useStepUp(session, active, onElevated),
      { initialProps: { active: false } },
    )
    expect(result.current.phase).toBe('idle')

    rerender({ active: true })
    await waitFor(() => {
      expect(result.current.phase).toBe('step')
    })
    expect(result.current.step?.kind).toBe('factor')

    act(() => {
      result.current.answer({ step: 'factor', input: { factor: 'f1' } })
    })
    await waitFor(() => {
      expect(result.current.phase).toBe('elevated')
    })
    expect(onElevated).toHaveBeenCalledTimes(1)

    // The start named the step-up purpose alone (no method): the flow
    // enters at the factor step for an account that has one.
    const start = calls.find((c) => c.path === '/api/v1/auth/flows/authenticated')
    expect(start?.options?.body).toEqual({ purpose: 'step-up' })
  })

  it('names the first-factor method on the start when one is given', async () => {
    // An account with no factor cannot be raised through a second factor;
    // its route asks for a fresh first factor, and the server accepts a
    // step-up that re-proves the method the session was established with.
    // The hook forwards that method on the start and parks on the method's
    // own first step.
    const { session, calls } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          state: 'first',
          next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
        }),
      [LOGIN_ANSWER]: () => sessionCompletion(),
      [LOGIN_ABANDON]: () => ({}),
    })
    const onElevated = vi.fn()
    const { result, rerender } = renderHook(
      ({ active }: { active: boolean }) =>
        useStepUp(session, active, onElevated, 'sms'),
      { initialProps: { active: false } },
    )

    rerender({ active: true })
    await waitFor(() => {
      expect(result.current.phase).toBe('step')
    })
    expect(result.current.step?.kind).toBe('challenge')

    act(() => {
      result.current.answer({ step: 'answer', input: { code: '123456' } })
    })
    await waitFor(() => {
      expect(result.current.phase).toBe('elevated')
    })
    expect(onElevated).toHaveBeenCalledTimes(1)

    const start = calls.find((c) => c.path === '/api/v1/auth/flows/authenticated')
    expect(start?.options?.body).toEqual({ purpose: 'step-up', method: 'sms' })
  })

  it('arms the wait a throttled answer stated and clears it at its deadline', async () => {
    // A 429 carrying Retry-After is a refusal that names how long the caller
    // must hold off. The hook exposes that wait in whole seconds, counts it
    // down, and once it elapses clears both the wait and the refusal that
    // stated it, leaving the parked step answerable.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const { session } = makeCookieHarness({
        [LOGIN_START_AUTHENTICATED]: () =>
          openFlow({ next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] } }),
        [LOGIN_ANSWER]: () => {
          throw new ApiError({
            status: 429,
            code: 'ratelimit.throttled',
            attempts: 1,
            retryAfterMs: 30_000,
          })
        },
        [LOGIN_ABANDON]: () => ({}),
      })
      const onElevated = vi.fn()
      const { result, rerender } = renderHook(
        ({ active }: { active: boolean }) => useStepUp(session, active, onElevated),
        { initialProps: { active: false } },
      )
      rerender({ active: true })
      await waitFor(() => {
        expect(result.current.phase).toBe('step')
      })
      expect(result.current.retryAfterSeconds).toBeNull()

      act(() => {
        result.current.answer({ step: 'factor', input: { factor: 'f1' } })
      })
      await waitFor(() => {
        expect(result.current.phase).toBe('failed')
      })
      expect(result.current.errorCode).toBe('ratelimit.throttled')
      expect(result.current.retryAfterSeconds).toBe(30)

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000)
      })
      expect(result.current.retryAfterSeconds).toBe(29)

      await act(async () => {
        await vi.advanceTimersByTimeAsync(29_000)
      })
      await waitFor(() => {
        expect(result.current.retryAfterSeconds).toBeNull()
      })
      // The wait is spent, so the retry control is free again; the failure
      // code stays and names what the attempt hit.
      expect(result.current.errorCode).toBe('ratelimit.throttled')
      expect(result.current.step?.kind).toBe('factor')
      expect(onElevated).not.toHaveBeenCalled()
    } finally {
      vi.useRealTimers()
    }
  })

  it('surfaces a failed start as a resolved code', async () => {
    const { session } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () => {
        throw apiError(400, 'authn.bad_request')
      },
    })
    const onElevated = vi.fn()
    const { result, rerender } = renderHook(
      ({ active }: { active: boolean }) => useStepUp(session, active, onElevated),
      { initialProps: { active: false } },
    )
    rerender({ active: true })
    await waitFor(() => {
      expect(result.current.phase).toBe('failed')
    })
    expect(result.current.errorCode).toBe('authn.bad_request')
    expect(onElevated).not.toHaveBeenCalled()
  })

  it('keeps a 400 start on failed while a real session stays authenticated', async () => {
    // A real session, restored from a valid cookie, that meets a 400 when
    // it starts the step-up. The 400 says the start named something the
    // server cannot take; it does not say the account has no factor (that
    // is read from the account's factor list), so the refusal is an
    // ordinary failure rendered as its code, and the session it did not
    // end staying authenticated changes nothing about that.
    const session = await authenticatedSession({
      [LOGIN_START_AUTHENTICATED]: () => {
        throw apiError(400, 'authn.bad_request')
      },
    })
    const onElevated = vi.fn()
    const { result, rerender } = renderHook(
      ({ active }: { active: boolean }) => useStepUp(session, active, onElevated),
      { initialProps: { active: false } },
    )
    rerender({ active: true })
    await waitFor(() => {
      expect(result.current.phase).toBe('failed')
    })
    expect(result.current.errorCode).toBe('authn.bad_request')
    expect(session.getSnapshot().state).toBe('authenticated')
    expect(onElevated).not.toHaveBeenCalled()
  })

  it('parks on unavailable for a 401 start while the session stays authenticated', async () => {
    // The account has factors, but none whose methods may strengthen this
    // session: the start answers 401 insufficient_user_authentication and
    // the session stays authenticated -- the one refusal no step can
    // satisfy, which the caller answers by re-establishing the session.
    const session = await authenticatedSession({
      [LOGIN_START_AUTHENTICATED]: () => {
        throw apiError(401, 'authn.insufficient_user_authentication')
      },
    })
    const onElevated = vi.fn()
    const { result, rerender } = renderHook(
      ({ active }: { active: boolean }) => useStepUp(session, active, onElevated),
      { initialProps: { active: false } },
    )
    rerender({ active: true })
    await waitFor(() => {
      expect(result.current.phase).toBe('unavailable')
    })
    expect(session.getSnapshot().state).toBe('authenticated')
    expect(onElevated).not.toHaveBeenCalled()
  })

  it('answers a ready complete step on its own and elevates once', async () => {
    // The factor proof parks the flow on its ready state, whose next step is
    // a bare confirmation: it collects nothing, so the hook answers it
    // without a second caller action and the elevation fires once.
    const { session, calls } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      [LOGIN_ANSWER]: (call) => {
        const body = call.options?.body as { step?: string } | undefined
        return body?.step === 'complete' ? sessionCompletion() : completeFlow(2)
      },
      [LOGIN_ABANDON]: () => ({}),
    })
    const onElevated = vi.fn()
    const { result, rerender } = renderHook(
      ({ active }: { active: boolean }) => useStepUp(session, active, onElevated),
      { initialProps: { active: false } },
    )
    rerender({ active: true })
    await waitFor(() => {
      expect(result.current.phase).toBe('step')
    })

    act(() => {
      result.current.answer({ step: 'factor', input: { factor: 'f1' } })
    })
    await waitFor(() => {
      expect(result.current.phase).toBe('elevated')
    })
    expect(onElevated).toHaveBeenCalledTimes(1)

    // The automatic answer carries the ready step's own sequence number and
    // the complete step, and it is the only completion sent.
    const answers = calls.filter(
      (call) => call.path === '/api/v1/auth/flows/answer',
    )
    expect(answers.map((call) => call.options?.body)).toEqual([
      { seq: 1, step: 'factor', input: { factor: 'f1' } },
      { seq: 2, step: 'complete' },
    ])
  })

  it('leaves a refused automatic completion on the step for a manual retry', async () => {
    // A refused automatic answer must not strand the flow on its pending
    // state: it parks on 'failed' with the code and keeps the step, so the
    // caller renders that step's own confirmation button, which retries it.
    let completions = 0
    const { session } = makeCookieHarness({
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'totp' }] },
        }),
      [LOGIN_ANSWER]: (call) => {
        const body = call.options?.body as { step?: string } | undefined
        if (body?.step !== 'complete') {
          return completeFlow(2)
        }
        completions += 1
        if (completions === 1) {
          throw apiError(401, 'authn.login_failed')
        }
        return sessionCompletion()
      },
      [LOGIN_ABANDON]: () => ({}),
    })
    const onElevated = vi.fn()
    const { result, rerender } = renderHook(
      ({ active }: { active: boolean }) => useStepUp(session, active, onElevated),
      { initialProps: { active: false } },
    )
    rerender({ active: true })
    await waitFor(() => {
      expect(result.current.phase).toBe('step')
    })

    act(() => {
      result.current.answer({ step: 'factor', input: { factor: 'f1' } })
    })
    await waitFor(() => {
      expect(result.current.phase).toBe('failed')
    })
    expect(result.current.errorCode).toBe('authn.login_failed')
    expect(result.current.step?.kind).toBe('complete')
    expect(onElevated).not.toHaveBeenCalled()

    act(() => {
      result.current.answer({ step: 'complete' })
    })
    await waitFor(() => {
      expect(result.current.phase).toBe('elevated')
    })
    expect(onElevated).toHaveBeenCalledTimes(1)
  })
})
