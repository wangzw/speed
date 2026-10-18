// @vitest-environment jsdom
/**
 * useStepUp + session.isStepUpShortfall: the session operation behind a
 * step-up-gated surface. A real session is driven over the shared
 * scripting harness (a fake request function bound through the api-sdk
 * runtime seam), and the assertions are the observable state only: the
 * discriminator's answer, the hook's phase/step, and the one elevation
 * callback.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import {
  LOGIN_ABANDON,
  LOGIN_ANSWER,
  LOGIN_START_AUTHENTICATED,
  PROFILE_GET,
  SESSION_GET,
  apiError,
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

/** The abandon requests (`DELETE /auth/flows/current`) a harness recorded. */
function abandons(
  calls: readonly { readonly method: string; readonly path: string }[],
) {
  return calls.filter(
    (call) => call.method === 'DELETE' && call.path === '/auth/flows/current',
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
    const start = calls.find((c) => c.path === '/auth/flows/authenticated')
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

    const start = calls.find((c) => c.path === '/auth/flows/authenticated')
    expect(start?.options?.body).toEqual({ purpose: 'step-up', method: 'sms' })
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
})
