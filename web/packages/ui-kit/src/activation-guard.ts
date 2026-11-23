/**
 * activation-guard: the one place a write-starting control decides whether
 * an activation may reach its handler.
 *
 * A control that starts a write stays focusable while the write is in
 * flight or while its precondition is unmet; it cannot be natively
 * disabled without dropping a focused control's focus to the document
 * body. What is left to stop is activation, and this hook is the guard
 * that stops it:
 *
 *  - While `busy` or `unavailable` holds, a call never reaches the
 *    handler. If its first argument is a submit event -- a React
 *    `FormEvent` or a native `SubmitEvent` -- the guard calls
 *    `preventDefault()`, so the browser does not submit the form. Any
 *    other argument is left untouched: a change event is not cancelled,
 *    because a controlled value does not change when its handler is
 *    swallowed.
 *  - Otherwise the handler runs with the original arguments and the hook
 *    returns nothing. A promise the handler returns is not awaited and its
 *    rejection is not swallowed here; react-hook-form's
 *    `handleSubmit(...)` result can be handed over as it is.
 *
 * The guard holds no write state, reads no feedback scope and never
 * touches focus. The caller passes `busy` from `useWriteFeedback().pending`
 * or its own per-item in-flight state, and `unavailable` from whatever
 * makes the precondition false.
 *
 * The returned function keeps one identity for the component's lifetime;
 * each call judges by the handler and state of the latest committed
 * render, held in a ref that is not part of any dependency list. It
 * judges the same way `ActionButton`'s own click guard does, so a form
 * with both a submit button and an `onSubmit` guard refuses a double
 * submission at either layer without conflict.
 */

import { useCallback, useLayoutEffect, useRef } from 'react'

/** The state one guarded control judges its activation by. */
export interface ActivationGuardState {
  /** The action this control started is in flight. */
  readonly busy: boolean
  /** The precondition is unmet, or a write is in flight in a scope whose context the control changes. */
  readonly unavailable?: boolean
}

/** A first argument the guard may have to cancel a form submit through. */
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
 * Wraps a control's activation handler so that activation is swallowed
 * while the control is busy or unavailable.
 *
 * @param handler The handler an idle activation reaches.
 * @param state The control's busy and unavailable state.
 * @returns A stable handler that applies the current state on each call.
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
