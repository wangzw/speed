
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, renderHook, screen } from '@testing-library/react'
import {
  useActivationGuard,
  type ActivationGuardState,
} from './activation-guard.js'

describe('useActivationGuard', () => {
  it('runs the handler while idle and swallows activation while busy or unavailable', () => {
    const calls: string[] = []
    const handler = vi.fn((value: string) => {
      calls.push(value)
    })
    const { result, rerender } = renderHook(
      ({ state }: { readonly state: ActivationGuardState }) =>
        useActivationGuard(handler, state),
      { initialProps: { state: { busy: false } as ActivationGuardState } },
    )

    result.current('idle')
    expect(handler).toHaveBeenCalledTimes(1)
    expect(handler).toHaveBeenLastCalledWith('idle')

    rerender({ state: { busy: true } })
    result.current('busy')
    expect(handler).toHaveBeenCalledTimes(1)

    rerender({ state: { busy: false, unavailable: true } })
    result.current('unavailable')
    expect(handler).toHaveBeenCalledTimes(1)

    rerender({ state: { busy: false, unavailable: false } })
    result.current('idle again')
    expect(handler).toHaveBeenCalledTimes(2)
    expect(calls).toEqual(['idle', 'idle again'])
  })

  it('cancels a swallowed submit event so the browser does not submit the form', () => {
    const received: unknown[][] = []
    const handler = vi.fn((...args: readonly unknown[]) => {
      received.push([...args])
    })
    const { result } = renderHook(() =>
      useActivationGuard(handler, { busy: true }),
    )

    const reactLikeSubmit = { type: 'submit', preventDefault: vi.fn() }
    result.current(reactLikeSubmit, 'submitter')
    expect(handler).not.toHaveBeenCalled()
    expect(reactLikeSubmit.preventDefault).toHaveBeenCalledTimes(1)
    expect(received).toEqual([])

    const nativeSubmit = new SubmitEvent('submit', { cancelable: true })
    result.current(nativeSubmit)
    expect(handler).not.toHaveBeenCalled()
    expect(nativeSubmit.defaultPrevented).toBe(true)
  })

  it('leaves every non-submit argument untouched when it swallows activation', () => {
    const received: unknown[][] = []
    const handler = vi.fn((...args: readonly unknown[]) => {
      received.push([...args])
    })
    const { result } = renderHook(() =>
      useActivationGuard(handler, { busy: false, unavailable: true }),
    )

    const click = { type: 'click', preventDefault: vi.fn() }
    const change = { type: 'change', preventDefault: vi.fn() }
    result.current(click, change)
    result.current('submit', null, undefined)
    result.current({ type: 'submit' })

    expect(handler).not.toHaveBeenCalled()
    expect(received).toEqual([])
    expect(click.preventDefault).not.toHaveBeenCalled()
    expect(change.preventDefault).not.toHaveBeenCalled()
  })

  it('keeps one handler identity and judges by the latest committed state and handler', () => {
    const firstCalls: string[] = []
    const secondCalls: string[] = []
    const first = vi.fn((value: string) => {
      firstCalls.push(value)
    })
    const second = vi.fn((value: string) => {
      secondCalls.push(value)
    })
    const { result, rerender } = renderHook(
      ({
        handler,
        state,
      }: {
        readonly handler: (...args: [string]) => void
        readonly state: ActivationGuardState
      }) => useActivationGuard(handler, state),
      { initialProps: { handler: first, state: { busy: false } } },
    )

    const stable = result.current
    rerender({ handler: second, state: { busy: true } })
    expect(result.current).toBe(stable)
    stable('swallowed')
    expect(first).not.toHaveBeenCalled()
    expect(second).not.toHaveBeenCalled()

    rerender({ handler: second, state: { busy: false } })
    expect(result.current).toBe(stable)
    stable('delivered')
    expect(first).not.toHaveBeenCalled()
    expect(second).toHaveBeenCalledTimes(1)
    expect(secondCalls).toEqual(['delivered'])
  })

  it('returns nothing and does not await a promise from the handler', () => {
    const pending = new Promise<never>(() => undefined)
    const handler = vi.fn((value: string) => {
      expect(value).toBe('argument')
      return pending
    })
    const { result } = renderHook(() =>
      useActivationGuard(handler, { busy: false }),
    )

    const returned = result.current('argument')
    expect(handler).toHaveBeenCalledWith('argument')
    expect(returned).toBeUndefined()
  })

  it('leaves a controlled switch value unchanged while its change is swallowed', () => {
    function GuardedSwitch({ busy }: { readonly busy: boolean }) {
      const [on, setOn] = useState(false)
      const onChange = useActivationGuard(() => {
        setOn((value) => !value)
      }, { busy })
      return (
        <label>
          <input type="checkbox" checked={on} onChange={onChange} />
          <span>{on ? 'on' : 'off'}</span>
        </label>
      )
    }

    const { rerender } = render(<GuardedSwitch busy />)
    const checkbox = screen.getByRole('checkbox')
    expect(checkbox).not.toBeChecked()

    fireEvent.click(checkbox)
    expect(checkbox).not.toBeChecked()

    rerender(<GuardedSwitch busy={false} />)
    fireEvent.click(checkbox)
    expect(checkbox).toBeChecked()
  })
})
