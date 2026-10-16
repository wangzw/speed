/**
 * useStepUpGate behaviour: the remember-and-re-run-once machine the
 * step-up-gated account surfaces share.
 *
 * What this suite pins: arming remembers the action's inputs and opens
 * the gate; a won challenge (succeed) re-runs exactly that action once
 * through the caller's onRetry and closes the gate; cancelling closes the
 * gate and re-runs nothing; a second arm after a settle re-runs the new
 * action, not the first.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useStepUpGate } from './step-up-gate.js'

describe('useStepUpGate', () => {
  it('opens on arm and re-runs the remembered action exactly once on success', () => {
    const retry = vi.fn()
    const { result } = renderHook(() => useStepUpGate<{ id: string }>(retry))

    expect(result.current.open).toBe(false)

    act(() => result.current.arm({ id: 'ident-1' }))
    expect(result.current.open).toBe(true)
    expect(retry).not.toHaveBeenCalled()

    act(() => result.current.succeed())
    expect(retry).toHaveBeenCalledTimes(1)
    expect(retry).toHaveBeenCalledWith({ id: 'ident-1' })
    expect(result.current.open).toBe(false)

    // A second succeed with nothing remembered re-runs nothing.
    act(() => result.current.succeed())
    expect(retry).toHaveBeenCalledTimes(1)
  })

  it('closes without re-running when the challenge is cancelled', () => {
    const retry = vi.fn()
    const { result } = renderHook(() => useStepUpGate<string>(retry))

    act(() => result.current.arm('password-change'))
    act(() => result.current.cancel())

    expect(result.current.open).toBe(false)
    expect(retry).not.toHaveBeenCalled()
  })

  it('re-runs the most recently armed action', () => {
    const retry = vi.fn()
    const { result } = renderHook(() => useStepUpGate<string>(retry))

    act(() => result.current.arm('first'))
    act(() => result.current.succeed())
    act(() => result.current.arm('second'))
    act(() => result.current.succeed())

    expect(retry).toHaveBeenNthCalledWith(1, 'first')
    expect(retry).toHaveBeenNthCalledWith(2, 'second')
  })

  it('does not restart the challenge when the caller re-renders', () => {
    // succeed reads onRetry through a ref, so its identity is stable: a
    // step-up dialog keyed on its onSuccess prop never re-fires its flow
    // because the parent re-rendered while the dialog was open.
    const { result, rerender } = renderHook(
      ({ onRetry }: { readonly onRetry: (a: string) => void }) =>
        useStepUpGate<string>(onRetry),
      { initialProps: { onRetry: vi.fn() } },
    )
    const firstSucceed = result.current.succeed

    rerender({ onRetry: vi.fn() })

    expect(result.current.succeed).toBe(firstSucceed)
  })
})
