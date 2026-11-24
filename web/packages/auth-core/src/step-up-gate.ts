import { useCallback, useRef, useState } from 'react'

/** What a step-up refusal asks for: a local factor challenge, or a fresh sign-in through the tenant's identity provider. */
export type StepUpReason = 'mfa' | 'sso'

/** The reason a step-up refusal code names, or null when the code is not one. */
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
  /** Remember the inputs to re-run once the challenge is won. The default reason is the local multi-factor challenge. */
  readonly arm: (action: A, reason?: StepUpReason) => void
  /** Forget the remembered action (the dialog's onCancel). */
  readonly cancel: () => void
  /** Re-run the remembered action once and clear it (the dialog's onSuccess). */
  readonly succeed: () => void
  /** The action currently remembered, or null. */
  readonly peek: () => A | null
  /** Arm the gate with `action` and answer true when `error` is a step-up shortfall; answer false and change nothing otherwise. */
  readonly intercept: (error: unknown, action: A) => boolean
}

function reasonOf(error: unknown): StepUpReason | null {
  if (typeof error !== 'object' || error === null) {
    return null
  }
  const code = (error as { readonly code?: unknown }).code
  return typeof code === 'string' ? stepUpReasonOf(code) : null
}

function isStepUpRefusal(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const candidate = error as { readonly status?: unknown }
  return candidate.status === 401 && reasonOf(error) !== null
}

/**
 * Holds one remembered action; `onRetry` replays it after the challenge is
 * won, and `isShortfall` decides which failures arm the gate.
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

  const peek = useCallback(() => actionRef.current, [])

  const intercept = useCallback(
    (error: unknown, action: A): boolean => {
      if (!shortfallRef.current(error)) {
        return false
      }
      arm(action, reasonOf(error) ?? 'mfa')
      return true
    },
    [arm],
  )

  return { open, reason, arm, cancel, succeed, peek, intercept }
}
