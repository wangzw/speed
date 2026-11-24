/** The shared stepping engine of the sign-in family. */

import { useCallback, useRef, useState } from 'react'
import type { AuthSession, AuthnFlowState } from '@speed/auth-core'
import { isOperationSuperseded } from '@speed/auth-core'
import { errorCodeOf } from '@speed/ui-kit'
import type { OpenFlowState, SettledFlowState } from './flow.js'
import { answerStepFor, isOpenFlow } from './flow.js'
import { allowedToStart } from './start-gate.js'
import type { SignInBeforeStart } from './start-gate.js'

/** The identity keys a multi-answer step carries into its next answer. A
 * `factor` step is chosen with `{factor: id}` and the challenge it opens is
 * answered with the challenge's own fields plus that same `factor` (the
 * server reads it on every `second` answer); an `enroll` step carries `kind`
 * the same way. Remembering only these two keys lets a challenge field ride
 * with its factor and nothing else. */
const CARRY_KEYS = ['factor', 'kind'] as const

const FLOW_INVALID = 'authn.flow_invalid'

function pickCarry(input: Record<string, string>): Record<string, string> {
  const carry: Record<string, string> = {}
  for (const key of CARRY_KEYS) {
    const value = input[key]
    if (value !== undefined) {
      carry[key] = value
    }
  }
  return carry
}

export interface FlowDriverCallbacks {
  /** A failure's code and the rejection it came from; the rejection may carry
   * facts the component reads (a 429's stated wait). */
  readonly onFailure: (code: string, error: unknown) => void
  /** The flow settled with a completion outcome (fired once). */
  readonly onSettled: (state: SettledFlowState) => void
  /**
   * This submit lost a concurrent commit race; the session is authenticated
   * under the winner, so no failure is reported.
   */
  readonly onSuperseded?: () => void
  /** Decides whether the flow may begin; gates `start` only. */
  readonly beforeStart?: SignInBeforeStart
}

export interface FlowDriver {
  /** The open step the view renders, or null when idle or settled. */
  readonly step: OpenFlowState | null
  /** The step parked right now, read synchronously: an awaited handler may
   * resolve after the render that parked it, so `step` is not yet it. */
  readonly currentStep: () => OpenFlowState | null
  /** A request is in flight. */
  readonly busy: boolean
  /** Begin a login flow and drive its first reply. Resolves true when the
   * reply advanced, false when a failure or lost race was reported instead. */
  start(request: Parameters<AuthSession['startFlow']>[0]): Promise<boolean>
  /** Drive an externally obtained reply (a readFlow's step). */
  absorb(reply: AuthnFlowState): Promise<boolean>
  /** Answer the current step, with the human-verification proof when the step
   * may send a verification message. */
  answer(input: Record<string, string>, humanProof?: string): Promise<boolean>
  /** Drop the current step and its carried identity keys. */
  reset(): void
}

/** The driver state and operations that advance one flow from start to
 * settlement, reporting a failure or a settlement upward. */
export function useFlowDriver(
  session: AuthSession,
  callbacks: FlowDriverCallbacks,
): FlowDriver {
  const [step, setStep] = useState<OpenFlowState | null>(null)
  const [busy, setBusy] = useState(false)
  // The step a click will answer, read synchronously so a render that parked
  // it and a handler that consumes it do not race through state.
  const stepRef = useRef<OpenFlowState | null>(null)
  const carryRef = useRef<Record<string, string>>({})
  // Behind a ref so the driver's handlers stay identity-stable across renders.
  const callbacksRef = useRef(callbacks)
  callbacksRef.current = callbacks

  const park = useCallback((value: OpenFlowState | null): void => {
    stepRef.current = value
    setStep(value)
  }, [])

  const advance = useCallback(
    async (reply: AuthnFlowState): Promise<void> => {
      let current = reply
      for (;;) {
        if (!isOpenFlow(current)) {
          park(null)
          callbacksRef.current.onSettled(current)
          return
        }
        if (current.next.kind === 'complete') {
          // Park the ready step and its sequence before taking the result: a
          // refused complete is retried with this sequence, never with the
          // previously answered step whose one-time credential is spent.
          stepRef.current = current
          const seq = current.seq
          try {
            current = await session.answerFlow({
              seq,
              step: 'complete',
              input: {},
            })
          } catch (error) {
            if (isOperationSuperseded(error)) {
              carryRef.current = {}
              park(null)
              throw error
            }
            if (errorCodeOf(error) === FLOW_INVALID) {
              throw error
            }
            // Either the complete's reply was lost (the flow may still be ready)
            // or the completion landed and a later read failed (terminal). Ask the
            // flow itself; the answered step must not be offered again either way.
            let reread: AuthnFlowState | null
            try {
              reread = await session.readFlow()
            } catch {
              setStep(null)
              throw error
            }
            if (reread === null || !isOpenFlow(reread)) {
              // The flow is terminal: the completion landed and the failure is
              // the read that followed it, not a refused login.
              carryRef.current = {}
              park(null)
              throw error
            }
            // The flow is still open: keep its step and sequence as the retry
            // target, and drop the answered step so it cannot be sent again.
            stepRef.current = reread
            setStep(null)
            throw error
          }
          continue
        }
        park(current)
        return
      }
    },
    [park, session],
  )

  const run = useCallback(
    async (operation: () => Promise<AuthnFlowState>): Promise<boolean> => {
      setBusy(true)
      try {
        await advance(await operation())
        return true
      } catch (error) {
        if (isOperationSuperseded(error)) {
          callbacksRef.current.onSuperseded?.()
          return false
        }
        const code = errorCodeOf(error)
        if (code === FLOW_INVALID) {
          // Drop the parked step so the channel's own first step renders again
          // and the viewer starts a fresh flow.
          carryRef.current = {}
          park(null)
        }
        callbacksRef.current.onFailure(code, error)
        return false
      } finally {
        setBusy(false)
      }
    },
    [advance, park],
  )

  const start = useCallback(
    async (request: Parameters<AuthSession['startFlow']>[0]) => {
      // The busy flag is taken before the gate resolves too, so an async gate
      // suspends under the pending indicator and a second click cannot pass it
      // and start a second flow.
      setBusy(true)
      const allowed = await allowedToStart(callbacksRef.current.beforeStart, {
        method: request.method ?? '',
      })
      if (!allowed) {
        setBusy(false)
        return false
      }
      return run(() => session.startFlow(request))
    },
    [run, session],
  )

  const absorb = useCallback(
    (reply: AuthnFlowState) => run(() => Promise.resolve(reply)),
    [run],
  )

  const answer = useCallback(
    (input: Record<string, string>, humanProof?: string): Promise<boolean> => {
      const current = stepRef.current
      if (current === null) {
        return Promise.resolve(false)
      }
      const merged = { ...carryRef.current, ...input }
      carryRef.current = pickCarry(merged)
      return run(() =>
        session.answerFlow({
          seq: current.seq,
          step: answerStepFor(current),
          input: merged,
          ...(humanProof === undefined ? {} : { human_proof: humanProof }),
        }),
      )
    },
    [run, session],
  )

  const reset = useCallback((): void => {
    carryRef.current = {}
    park(null)
  }, [park])

  const currentStep = useCallback((): OpenFlowState | null => stepRef.current, [])

  return { step, currentStep, busy, start, absorb, answer, reset }
}
