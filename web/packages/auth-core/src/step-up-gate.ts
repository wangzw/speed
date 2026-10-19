/**
 * useStepUpGate -- the one place a step-up-gated surface remembers a
 * refused action and re-runs exactly it once the challenge is won.
 *
 * A write whose route asks for a stronger or fresher authentication than
 * the session proves is refused `401 authn.insufficient_user_authentication`
 * (pkg/authn/internal/mount's StepUpRequired). A surface that meets that
 * answer arms the gate with whatever it needs to replay the refused
 * request -- a form's field values, a row's identifier, the closure that
 * re-fires the write -- opens the step-up dialog, and the dialog's success
 * re-runs the remembered action through `onRetry` exactly once and clears
 * the slot. A cancel re-runs nothing.
 *
 * `intercept` is the failure side of the same wiring: hand it a caught
 * failure and the action to replay, and it arms the gate and answers true
 * exactly when the failure is a step-up shortfall, so the caller can clear
 * its own failure banner while the dialog opens. It answers false for every
 * other failure and leaves the gate untouched. A null action is never
 * remembered: the gate reads null as "nothing is armed".
 *
 * The default shortfall classifier recognises the refusal this platform
 * names: an error-shaped value whose `status` is 401 and whose `code` is
 * `authn.insufficient_user_authentication`. Status alone is deliberately
 * not enough. A 401 that ended the session (a bare `client.http.401`, an
 * expired or revoked session) leaves no session to elevate, and arming a
 * step-up for it would offer a verification that cannot help. A caller
 * whose session already tells the two apart -- auth-core's own
 * `AuthSession.isStepUpShortfall`, which adds the still-authenticated
 * check -- passes that predicate as `isShortfall` instead.
 *
 * `open` reads "an action is remembered". `onRetry` and `isShortfall` are
 * read through refs, so a caller may pass fresh closures every render
 * without changing `succeed`'s identity -- a dialog keyed on its
 * `onSuccess` prop then never restarts its flow because the parent
 * re-rendered.
 */

import { useCallback, useRef, useState } from 'react'

/** The code pkg/authn answers a route that asks for a stronger
 * authentication than the session proves. */
const STEP_UP_REQUIRED_CODE = 'authn.insufficient_user_authentication'

/** The state and commands a step-up-gated surface reads and drives. */
export interface StepUpGate<A> {
  /** Whether a refused action is remembered (the dialog's `open`). */
  readonly open: boolean
  /** Remember the inputs to re-run once the challenge is won. */
  readonly arm: (action: A) => void
  /** Forget the remembered action (the dialog's onCancel). */
  readonly cancel: () => void
  /** Re-run the remembered action once and clear it (the dialog's onSuccess). */
  readonly succeed: () => void
  /** Arm the gate with `action` and answer true when `error` is a step-up
   * shortfall; answer false and change nothing for any other failure. */
  readonly intercept: (error: unknown, action: A) => boolean
}

/** The default shortfall classifier: the platform's own step-up refusal.
 * Structural, so an error that crossed a package boundary or a realm still
 * classifies. */
function isStepUpRefusal(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const candidate = error as { readonly status?: unknown; readonly code?: unknown }
  return candidate.status === 401 && candidate.code === STEP_UP_REQUIRED_CODE
}

/**
 * Holds one remembered action. `onRetry` replays it after the challenge is
 * won; `isShortfall` decides which failures arm the gate, defaulting to
 * the platform's step-up refusal code.
 */
export function useStepUpGate<A>(
  onRetry: (action: A) => void,
  isShortfall: (error: unknown) => boolean = isStepUpRefusal,
): StepUpGate<A> {
  const [open, setOpen] = useState(false)
  const actionRef = useRef<A | null>(null)
  const retryRef = useRef(onRetry)
  retryRef.current = onRetry
  const shortfallRef = useRef(isShortfall)
  shortfallRef.current = isShortfall

  const arm = useCallback((action: A) => {
    actionRef.current = action
    setOpen(true)
  }, [])

  const cancel = useCallback(() => {
    actionRef.current = null
    setOpen(false)
  }, [])

  const succeed = useCallback(() => {
    const action = actionRef.current
    actionRef.current = null
    setOpen(false)
    if (action !== null) {
      retryRef.current(action)
    }
  }, [])

  const intercept = useCallback(
    (error: unknown, action: A): boolean => {
      if (!shortfallRef.current(error)) {
        return false
      }
      arm(action)
      return true
    },
    [arm],
  )

  return { open, arm, cancel, succeed, intercept }
}
