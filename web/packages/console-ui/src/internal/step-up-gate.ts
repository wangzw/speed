/**
 * useStepUpGate: the one place the step-up-gated writes of the console
 * remember a refused action and re-run exactly it after the challenge is
 * won.
 *
 * A console write whose route asks for a stronger authentication than the
 * session proves is refused 401 (the session is still established -- a
 * strength shortfall, not the end of the session; @speed/auth-core's
 * session.isStepUpShortfall is the discriminator). The surface that meets
 * that answer arms this gate with the closure that re-fires the refused
 * write, opens the step-up dialog, and the dialog's success re-runs the
 * remembered closure once and clears the slot -- a success re-runs exactly
 * once, and a cancel (the dialog's own onCancel) re-runs nothing.
 *
 * The gate holds one closure and no more: it never decides whether an
 * answer was a step-up refusal (the caller reads isStepUpShortfall first)
 * and never opens the dialog on its own.
 */

import { useCallback, useRef, useState } from 'react'
import type { AuthSession } from '@speed/auth-core'
import { useConsoleSession } from './session.js'

export interface StepUpGate {
  /** Whether a refused action is remembered (the dialog's `open`). */
  readonly open: boolean
  /** Remember how to re-fire the refused write once the challenge is won. */
  readonly arm: (retry: () => void) => void
  /** Forget the remembered action (the dialog's onCancel). */
  readonly cancel: () => void
  /** Re-run the remembered action once and clear it (the dialog's onSuccess). */
  readonly succeed: () => void
}

export function useStepUpGate(): StepUpGate {
  const [open, setOpen] = useState(false)
  const retryRef = useRef<(() => void) | null>(null)

  const arm = useCallback((retry: () => void) => {
    retryRef.current = retry
    setOpen(true)
  }, [])

  const cancel = useCallback(() => {
    retryRef.current = null
    setOpen(false)
  }, [])

  const succeed = useCallback(() => {
    const retry = retryRef.current
    retryRef.current = null
    setOpen(false)
    if (retry !== null) {
      retry()
    }
  }, [])

  return { open, arm, cancel, succeed }
}

/** The page-facing step-up wiring: the session (or null), the gate, and
 * the failure classifier that arms the gate on a shortfall. */
export interface ConsoleStepUp {
  /** The console session, or null when none is mounted (no step-up). */
  readonly session: AuthSession | null
  /** The gate the dialog reads and the page arms. */
  readonly gate: StepUpGate
  /**
   * Inspect a write's failure against the session: when the session says
   * it is a step-up shortfall, remember `retry` and return true -- the
   * caller then clears its mutation's own error (mutation.reset()) so the
   * failure banner does not also render while the dialog opens; otherwise
   * return false and the caller renders the error as usual. A null
   * session never intercepts: without a session there is nothing to
   * elevate.
   */
  readonly intercept: (error: unknown, retry: () => void) => boolean
}

/**
 * Wires a page's step-up-gated writes to the console session: reads the
 * session, holds the gate, and classifies a failed write. The page arms
 * the gate through `intercept` in each mutation's onError and renders
 * StepUpChallenge from `gate` and `session`.
 */
export function useConsoleStepUp(): ConsoleStepUp {
  const session = useConsoleSession()
  const gate = useStepUpGate()
  const sessionRef = useRef(session)
  sessionRef.current = session

  const intercept = useCallback(
    (error: unknown, retry: () => void): boolean => {
      const current = sessionRef.current
      if (current !== null && current.isStepUpShortfall(error)) {
        gate.arm(retry)
        return true
      }
      return false
    },
    [gate],
  )

  return { session, gate, intercept }
}
