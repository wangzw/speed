// @vitest-environment jsdom

import { StrictMode } from 'react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { act, renderHook, waitFor } from '@testing-library/react'
import { ApiError } from '@speed/api-client'
import {
  LOGIN_ABANDON,
  LOGIN_ANSWER,
  LOGIN_METHODS,
  LOGIN_START_AUTHENTICATED,
  PROFILE_GET,
  SESSION_GET,
  apiError,
  completeFlow,
  makeCookieHarness,
  methodList,
  openFlow,
  principal,
  sessionCompletion,
  sessionView,
} from '@speed/test-utils/session-harness'
import type { AuthSession } from './session.js'
import { useStepUp } from './step-up.js'

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

function abandons(
  calls: readonly { readonly method: string; readonly path: string }[],
) {
  return calls.filter(
    (call) => call.method === 'DELETE' && call.path === '/api/v1/auth/flows/current',
  )
}

describe('useStepUp', () => {
  it('issues no abandon request when it mounts closed', () => {
    const { session, calls } = makeCookieHarness({})
    renderHook(() => useStepUp(session, false, () => undefined))
    expect(calls).toEqual([])
  })

  it('abandons exactly once when the surface unmounts with its flow still open', async () => {
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

    const start = calls.find((c) => c.path === '/api/v1/auth/flows/authenticated')
    expect(start?.options?.body).toEqual({ purpose: 'step-up' })
  })

  it('names the first-factor method on the start when one is given', async () => {
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

  it('holds a sending answer until its proof arrives, then forwards it once', async () => {
    const { session, calls } = makeCookieHarness({
      [LOGIN_METHODS]: () =>
        methodList([], {
          humanPhoneProvider: 'turnstile',
          humanPhoneRequired: true,
          humanPhoneSiteKey: 'site-1',
        }),
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'sms' }] },
        }),
      [LOGIN_ANSWER]: () => sessionCompletion(),
      [LOGIN_ABANDON]: () => ({}),
    })
    const onElevated = vi.fn()
    const { result } = renderHook(() =>
      useStepUp(session, true, onElevated),
    )
    await waitFor(() => {
      expect(result.current.phase).toBe('step')
    })
    expect(result.current.pending).toBe(false)
    expect(result.current.humanScope).toBe('phone')

    act(() => {
      result.current.answer({ step: 'factor', input: { factor: 'f1' } })
    })

    await waitFor(() => {
      expect(result.current.pending).toBe(true)
    })
    expect(
      calls.filter((call) => call.path === '/api/v1/auth/flows/answer'),
    ).toHaveLength(0)

    act(() => {
      result.current.human.adoptToken('token-1')
    })

    await waitFor(() => {
      expect(result.current.phase).toBe('elevated')
    })
    expect(onElevated).toHaveBeenCalledTimes(1)
    const answers = calls.filter(
      (call) => call.path === '/api/v1/auth/flows/answer',
    )
    expect(answers.map((call) => call.options?.body)).toEqual([
      {
        seq: 1,
        step: 'factor',
        input: { factor: 'f1' },
        human_proof: 'token-1',
      },
    ])
    expect(result.current.human.proof).toBeNull()
  })

  it('forwards a gate-free send with no proof', async () => {
    const { session, calls } = makeCookieHarness({
      [LOGIN_METHODS]: () => methodList([]),
      [LOGIN_START_AUTHENTICATED]: () =>
        openFlow({
          next: { kind: 'factor', factors: [{ id: 'f1', kind: 'sms' }] },
        }),
      [LOGIN_ANSWER]: () => sessionCompletion(),
      [LOGIN_ABANDON]: () => ({}),
    })
    const onElevated = vi.fn()
    const { result } = renderHook(() =>
      useStepUp(session, true, onElevated),
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
    expect(onElevated).toHaveBeenCalledTimes(1)
    const body = calls.find(
      (call) => call.path === '/api/v1/auth/flows/answer',
    )?.options?.body
    expect(body).toMatchObject({
      seq: 1,
      step: 'factor',
      input: { factor: 'f1' },
    })
    expect(body).not.toHaveProperty('human_proof')
  })

  it('arms the wait a throttled answer stated and clears it at its deadline', async () => {
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
      expect(result.current.errorCode).toBe('ratelimit.throttled')
      expect(result.current.step?.kind).toBe('factor')
      expect(onElevated).not.toHaveBeenCalled()
    } finally {
      await restoreRealTimers()
    }
  })

  it('never publishes a zero-second wait at its deadline', async () => {
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
      const rendered: Array<number | null> = []
      const { result, rerender } = renderHook(
        ({ active }: { active: boolean }) => {
          const state = useStepUp(session, active, onElevated)
          rendered.push(state.retryAfterSeconds)
          return state
        },
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
      expect(result.current.retryAfterSeconds).toBe(30)

      await act(async () => {
        await vi.advanceTimersByTimeAsync(30_000)
      })
      await waitFor(() => {
        expect(result.current.retryAfterSeconds).toBeNull()
      })
      expect(result.current.step?.kind).toBe('factor')
      expect(rendered).not.toContain(0)
      expect(
        rendered.filter((value) => value !== null).every((value) => value > 0),
      ).toBe(true)
    } finally {
      await restoreRealTimers()
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

    const answers = calls.filter(
      (call) => call.path === '/api/v1/auth/flows/answer',
    )
    expect(answers.map((call) => call.options?.body)).toEqual([
      { seq: 1, step: 'factor', input: { factor: 'f1' } },
      { seq: 2, step: 'complete' },
    ])
  })

  it('leaves a refused automatic completion on the step for a manual retry', async () => {
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
