/**
 * useStepUpGate: the one place the step-up-gated surfaces of the account
 * page remember a refused action and re-run exactly it after the
 * challenge is won.
 *
 * The authn account routes that change what proves the account (the
 * profile write, the password change, the phone add and verify, the
 * identifier removal) ask for a fresh authentication: an unelevated
 * caller is answered 401 authn.insufficient_user_authentication. A
 * surface that meets that answer opens the step-up dialog (StepUpChallenge)
 * with the action's inputs remembered as `action`, and the dialog fires
 * `succeed` once its `purpose: 'step-up'` flow has elevated the session;
 * `succeed` re-runs the remembered action through the caller's `onRetry`
 * and clears the slot, so a success re-runs exactly once and a cancel
 * (`cancel`, or the dialog's own onCancel wired to it) re-runs nothing.
 *
 * The action is whatever the caller needs to replay the refused request
 * with -- a form's field values, a row's identifier, a tagged union of a
 * multi-step flow's inputs. The hook holds it and no more: it never
 * decides whether an answer was a step-up refusal (the caller reads the
 * code off the error first) and never opens the dialog on its own.
 *
 * `open` reads "an action is remembered". `onRetry` is read through a
 * ref, so a caller may pass a fresh closure every render without changing
 * `succeed`'s identity -- a step-up dialog keyed on its `onSuccess` prop
 * then never restarts its flow because the parent re-rendered.
 */

import { useCallback, useRef, useState } from 'react'

export interface StepUpGate<A> {
  /** Whether a refused action is remembered (the dialog's `open`). */
  readonly open: boolean
  /** Remember the inputs to re-run once the challenge is won. */
  readonly arm: (action: A) => void
  /** Forget the remembered action (the dialog's onCancel). */
  readonly cancel: () => void
  /** Re-run the remembered action once and clear it (the dialog's onSuccess). */
  readonly succeed: () => void
}

export function useStepUpGate<A>(onRetry: (action: A) => void): StepUpGate<A> {
  const [open, setOpen] = useState(false)
  const actionRef = useRef<A | null>(null)
  const retryRef = useRef(onRetry)
  retryRef.current = onRetry

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

  return { open, arm, cancel, succeed }
}
