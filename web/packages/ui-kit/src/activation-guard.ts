/** The hook a write-starting control uses to decide whether an activation reaches its handler. */

import { useCallback, useLayoutEffect, useRef } from 'react'

/** The state one guarded control judges its activation by. */
export interface ActivationGuardState {
  /** The action this control started is in flight. */
  readonly busy: boolean
  /** The precondition is unmet, or a write is in flight in a scope whose context the control changes. */
  readonly unavailable?: boolean
}

interface SubmitLikeEvent {
  readonly type: 'submit'
  preventDefault(): void
}

function isSubmitEvent(value: unknown): value is SubmitLikeEvent {
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const candidate = value as {
    readonly type?: unknown
    readonly preventDefault?: unknown
  }
  return (
    candidate.type === 'submit' &&
    typeof candidate.preventDefault === 'function'
  )
}

/**
 * Wraps a control's activation handler so activation is swallowed while
 * `busy` or `unavailable` is true; a swallowed submit is cancelled and the
 * handler judges the latest committed state with one stable identity.
 */
export function useActivationGuard<A extends readonly unknown[]>(
  handler: (...args: A) => void,
  state: ActivationGuardState,
): (...args: A) => void {
  const latest = useRef({ handler, state })
  useLayoutEffect(() => {
    latest.current = { handler, state }
  })
  return useCallback((...args: A): void => {
    const current = latest.current
    if (current.state.busy || current.state.unavailable === true) {
      const firstArgument: unknown = args[0]
      if (isSubmitEvent(firstArgument)) {
        firstArgument.preventDefault()
      }
      return
    }
    current.handler(...args)
  }, [])
}
