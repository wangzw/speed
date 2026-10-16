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

describe('useStepUp', () => {
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

  it('parks on unavailable for a 400 start while a real session stays authenticated', async () => {
    // A real session, restored from a valid cookie, that meets a 400 when
    // it starts the step-up: the deployment declined to raise it (an
    // account with no factor at all), which no step can satisfy. The
    // 'unavailable' branch reads the snapshot at the moment of the
    // refusal, so the case pins what that read is made of -- the session
    // the 400 did not end is still authenticated, and this is what tells
    // the deployment-declined 400 apart from an ordinary failure the
    // caller may retry.
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
      expect(result.current.phase).toBe('unavailable')
    })
    expect(session.getSnapshot().state).toBe('authenticated')
    expect(onElevated).not.toHaveBeenCalled()
  })
})
