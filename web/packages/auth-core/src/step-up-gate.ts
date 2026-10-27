/**
 * useStepUpGate -- the one place a step-up-gated surface remembers a
 * refused action and re-runs exactly it once the challenge is won.
 *
 * A write whose route asks for a stronger or fresher authentication than
 * the session proves is refused `401 authn.insufficient_user_authentication`
 * (pkg/authn/internal/mount's StepUpRequired), or `401 authn.sso_required`
 * when only a fresh sign-in through the tenant's identity provider will do
 * (the tenant admission layer). A surface that meets either answer arms the
 * gate with whatever it needs to replay the refused request -- a form's field
 * values, a row's identifier, the closure that re-fires the write -- opens the
 * step-up dialog, and the dialog's success re-runs the remembered action
 * through `onRetry` exactly once and clears the slot. A cancel re-runs
 * nothing. The gate also remembers which of the two was asked for, so the
 * dialog can offer a local factor challenge or the SSO sign-in exit.
 *
 * `intercept` is the failure side of the same wiring: hand it a caught
 * failure and the action to replay, and it arms the gate and answers true
 * exactly when the failure is a step-up shortfall, so the caller can clear
 * its own failure banner while the dialog opens. It answers false for every
 * other failure and leaves the gate untouched. A null action is never
 * remembered: the gate reads null as "nothing is armed".
 *
 * The default shortfall classifier recognises the refusals this platform
 * names: an error-shaped value whose `status` is 401 and whose `code` is one
 * of `authn.insufficient_user_authentication` and `authn.sso_required`.
 * Status alone is deliberately not enough. A 401 that ended the session (a
 * bare `client.http.401`, an expired or revoked session) leaves no session to
 * elevate, and arming a step-up for it would offer a verification that cannot
 * help. A caller whose session already tells the two apart -- auth-core's own
 * `AuthSession.isStepUpShortfall`, which adds the still-authenticated check --
 * passes that predicate as `isShortfall` instead; such a caller's error still
 * carries one of the codes, so the reason is read from it and any other code
 * falls back to the local challenge.
 *
 * `open` reads "an action is remembered". `onRetry` and `isShortfall` are
 * read through refs, so a caller may pass fresh closures every render
 * without changing `succeed`'s identity -- a dialog keyed on its
 * `onSuccess` prop then never restarts its flow because the parent
 * re-rendered.
 */

import { useCallback, useRef, useState } from 'react'

/** What a step-up refusal asks for: a local multi-factor authentication, or a
 * fresh sign-in through the tenant's identity provider, which no local factor
 * can satisfy. */
export type StepUpReason = 'mfa' | 'sso'

/** The reason a step-up refusal code names, or null when the code is not one.
 * The two codes are the platform's: pkg/authn/internal/mount's
 * insufficient_user_authentication and the tenant admission layer's
 * sso_required. Structural callers pass an error's `code`. */
export function stepUpReasonOf(code: string | undefined): StepUpReason | null {
  switch (code) {
    case 'authn.insufficient_user_authentication':
      return 'mfa'
    case 'authn.sso_required':
      return 'sso'
    default:
      return null
  }
}

/** The state and commands a step-up-gated surface reads and drives. */
export interface StepUpGate<A> {
  /** Whether a refused action is remembered (the dialog's `open`). */
  readonly open: boolean
  /** What the remembered refusal asks for; null while nothing is armed. */
  readonly reason: StepUpReason | null
  /** Remember the inputs to re-run once the challenge is won. A caller that
   * classified the failure itself names its reason; the default is the local
   * multi-factor challenge. */
  readonly arm: (action: A, reason?: StepUpReason) => void
  /** Forget the remembered action (the dialog's onCancel). */
  readonly cancel: () => void
  /** Re-run the remembered action once and clear it (the dialog's onSuccess). */
  readonly succeed: () => void
  /** Arm the gate with `action` and answer true when `error` is a step-up
   * shortfall; answer false and change nothing for any other failure. */
  readonly intercept: (error: unknown, action: A) => boolean
}

/** The step-up reason an error's code names, or null when the code names
 * none. Structural, so an error that crossed a package boundary or a realm
 * still classifies. */
function reasonOf(error: unknown): StepUpReason | null {
  if (typeof error !== 'object' || error === null) {
    return null
  }
  const code = (error as { readonly code?: unknown }).code
  return typeof code === 'string' ? stepUpReasonOf(code) : null
}

/** The default shortfall classifier: the platform's own step-up refusal.
 * Structural, so an error that crossed a package boundary or a realm still
 * classifies. */
function isStepUpRefusal(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const candidate = error as { readonly status?: unknown }
  return candidate.status === 401 && reasonOf(error) !== null
}

/**
 * Holds one remembered action. `onRetry` replays it after the challenge is
 * won; `isShortfall` decides which failures arm the gate, defaulting to
 * the platform's step-up refusal codes.
 */
export function useStepUpGate<A>(
  onRetry: (action: A) => void,
  isShortfall: (error: unknown) => boolean = isStepUpRefusal,
): StepUpGate<A> {
  const [open, setOpen] = useState(false)
  const [reason, setReason] = useState<StepUpReason | null>(null)
  const actionRef = useRef<A | null>(null)
  const retryRef = useRef(onRetry)
  retryRef.current = onRetry
  const shortfallRef = useRef(isShortfall)
  shortfallRef.current = isShortfall

  const arm = useCallback((action: A, armedReason: StepUpReason = 'mfa') => {
    actionRef.current = action
    setReason(armedReason)
    setOpen(true)
  }, [])

  const cancel = useCallback(() => {
    actionRef.current = null
    setReason(null)
    setOpen(false)
  }, [])

  const succeed = useCallback(() => {
    const action = actionRef.current
    actionRef.current = null
    setReason(null)
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
      // A caller whose classifier accepts any 401 (the session-based one) still
      // gets the local factor challenge for a code that names no reason, which
      // is what it offered before the SSO code existed.
      arm(action, reasonOf(error) ?? 'mfa')
      return true
    },
    [arm],
  )

  return { open, reason, arm, cancel, succeed, intercept }
}
