// @vitest-environment jsdom
/**
 * useStepUpGate: the generic step-up gate.
 *
 * The suite drives the exported hook and asserts the observable contract
 * only: what `open` reads, whether the remembered action is replayed
 * (exactly once, with the inputs it was armed with), that a cancel replays
 * nothing, and which failures the default and a caller-supplied shortfall
 * classifier arm on.
 */

import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@speed/api-client'
import { useStepUpGate } from './step-up-gate.js'

afterEach(cleanup)

/** The platform's step-up refusal as the wire shapes it. */
function stepUpRefusal(): ApiError {
  return new ApiError({
    status: 401,
    code: 'authn.insufficient_user_authentication',
    attempts: 1,
  })
}

describe('useStepUpGate', () => {
  it('starts closed, arms an action, and replays it exactly once', () => {
    const onRetry = vi.fn()
    const { result } = renderHook(() => useStepUpGate<string>(onRetry))

    expect(result.current.open).toBe(false)

    act(() => result.current.arm('row-1'))
    expect(result.current.open).toBe(true)

    act(() => result.current.succeed())
    expect(result.current.open).toBe(false)
    expect(onRetry).toHaveBeenCalledTimes(1)
    expect(onRetry).toHaveBeenCalledWith('row-1')

    act(() => result.current.succeed())
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('replays nothing after a cancel', () => {
    const onRetry = vi.fn()
    const { result } = renderHook(() => useStepUpGate<string>(onRetry))

    act(() => result.current.arm('row-1'))
    act(() => result.current.cancel())
    expect(result.current.open).toBe(false)

    act(() => result.current.succeed())
    expect(onRetry).not.toHaveBeenCalled()
  })

  it('replays through the latest onRetry closure', () => {
    const first = vi.fn()
    const second = vi.fn()
    const { result, rerender } = renderHook(
      ({ retry }: { retry: (action: string) => void }) => useStepUpGate<string>(retry),
      { initialProps: { retry: first } },
    )

    rerender({ retry: second })
    act(() => result.current.arm('row-1'))
    act(() => result.current.succeed())

    expect(first).not.toHaveBeenCalled()
    expect(second).toHaveBeenCalledWith('row-1')
  })

  it('arms on the platform step-up refusal and answers true', () => {
    const onRetry = vi.fn()
    const { result } = renderHook(() => useStepUpGate<string>(onRetry))

    let intercepted = false
    act(() => {
      intercepted = result.current.intercept(stepUpRefusal(), 'row-1')
    })

    expect(intercepted).toBe(true)
    expect(result.current.open).toBe(true)

    act(() => result.current.succeed())
    expect(onRetry).toHaveBeenCalledWith('row-1')
  })

  it('leaves the gate untouched for any other failure', () => {
    const onRetry = vi.fn()
    const { result } = renderHook(() => useStepUpGate<string>(onRetry))

    const failures: unknown[] = [
      new ApiError({ status: 401, code: 'client.http.401', attempts: 1 }),
      new ApiError({
        status: 401,
        code: 'authn.session_expired',
        attempts: 1,
      }),
      new ApiError({ status: 403, code: 'authn.permission_denied', attempts: 1 }),
      new Error('boom'),
      null,
    ]

    for (const failure of failures) {
      let intercepted = true
      act(() => {
        intercepted = result.current.intercept(failure, 'row-1')
      })
      expect(intercepted).toBe(false)
    }
    expect(result.current.open).toBe(false)

    act(() => result.current.succeed())
    expect(onRetry).not.toHaveBeenCalled()
  })

  it('lets a caller replace the shortfall classifier', () => {
    const onRetry = vi.fn()
    const isShortfall = vi.fn((error: unknown) => error === 'shortfall')
    const { result } = renderHook(() => useStepUpGate<string>(onRetry, isShortfall))

    let intercepted = false
    act(() => {
      intercepted = result.current.intercept(stepUpRefusal(), 'row-1')
    })
    expect(intercepted).toBe(false)

    act(() => {
      intercepted = result.current.intercept('shortfall', 'row-2')
    })
    expect(intercepted).toBe(true)
    expect(result.current.open).toBe(true)

    act(() => result.current.succeed())
    expect(onRetry).toHaveBeenCalledWith('row-2')
  })
})
