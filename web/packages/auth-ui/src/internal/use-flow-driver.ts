/**
 * useFlowDriver: the shared stepping engine of the sign-in family.
 *
 * Each channel form begins a login flow and then advances it step by
 * step, and the same loop serves them all: hand the reply to `advance`,
 * which either parks it as the current step for the view to render, or
 * -- when it is a ready step asking for the result -- takes the result
 * with `step: 'complete'`, or -- when it is a settled completion --
 * reports it upward once. `answer` sends one step's input and feeds the
 * reply back through the same loop.
 *
 * Only auth-core commits the session: `answerFlow` writes the tokens
 * when a completion settled a token issuance (a login completion opens
 * the session; a `link` or `enroll` completion does not touch it), so
 * the driver commits nothing itself and a component never holds login
 * state -- it observes a settled completion and fires its host callback.
 *
 * A submit whose own answer lost a concurrent commit race is not a
 * failure: auth-core rejects it with OperationSupersededError -- the
 * session is authenticated under the winner, not under this submit's
 * identity -- so the driver stays silent (no failure banner, no settled
 * callback) rather than reporting a stale success for an identity the
 * session is not running under. Every other rejection resolves to its
 * code and is reported through `onFailure`, which renders the
 * namespace's text for it.
 *
 * Second-factor state carrying: a `factor` step is chosen by sending
 * `{factor: id}`, and the challenge it opens is answered by sending the
 * challenge's own fields *plus* the same `factor` again (the server
 * reads the factor id on every answer of a `second` step). An `enroll`
 * step carries its `kind` the same way. The driver remembers only those
 * two identity keys from the last answer and merges them into the next
 * one, so a challenge field typed for a chosen factor rides with that
 * factor and nothing else does.
 *
 * An answer that may send a verification message (choosing an SMS second
 * factor, or a step that supplies a phone a method declared) also carries
 * the human-verification proof the deployment requires. The proof travels
 * in the answer body's own `human_proof` member, never the input map,
 * whose values the server caps far below a token's size.
 */

import { useCallback, useRef, useState } from 'react'
import type { AuthSession, AuthnFlowState } from '@speed/auth-core'
import { isOperationSuperseded } from '@speed/auth-core'
import { errorCodeOf } from '@speed/ui-kit'
import type { OpenFlowState, SettledFlowState } from './flow.js'
import { answerStepFor, isOpenFlow } from './flow.js'
import { allowedToStart } from './start-gate.js'
import type { SignInBeforeStart } from './start-gate.js'

/** The identity keys a multi-answer step carries across its sub-steps. */
const CARRY_KEYS = ['factor', 'kind'] as const

/** The code the server answers a flow that is gone -- expired, already
 * consumed by a concurrent submit, or bound to another browser. A step
 * that fails with it cannot be answered any more, so the view returns to
 * the channel's first step to start a fresh flow (the family's error
 * table, design-web-auth-ui.md's "流程已失"). */
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
  /** A failure's code; the component renders its namespace text. */
  readonly onFailure: (code: string) => void
  /** The flow settled with a completion outcome (fired once). */
  readonly onSettled: (state: SettledFlowState) => void
  /**
   * This submit lost a concurrent commit race. The session is
   * authenticated under the winner, so no failure is reported; a
   * caller that spent a one-shot credential in the losing submit (a
   * single-use SMS code) hears it here.
   */
  readonly onSuperseded?: () => void
  /**
   * Decides whether the flow may begin (see ./start-gate). It gates
   * `start` only: a parked step's answers are the flow's own later
   * steps, not a new start.
   */
  readonly beforeStart?: SignInBeforeStart
}

export interface FlowDriver {
  /** The open step the view renders, or null when idle or settled. */
  readonly step: OpenFlowState | null
  /** A request is in flight. */
  readonly busy: boolean
  /** Begin a login flow and drive its first reply. Resolves true when
   * the reply was advanced (a step parked or the flow settled), false
   * when a failure or a lost race was reported instead. */
  start(request: Parameters<AuthSession['startFlow']>[0]): Promise<boolean>
  /** Drive an externally obtained reply (a readFlow's step). */
  absorb(reply: AuthnFlowState): Promise<boolean>
  /** Answer the current step with the given input and, when the step may
   * send a verification message, the human-verification proof the
   * deployment requires. */
  answer(input: Record<string, string>, humanProof?: string): Promise<boolean>
  /** Drop the current step and its carried identity keys. */
  reset(): void
}

export function useFlowDriver(
  session: AuthSession,
  callbacks: FlowDriverCallbacks,
): FlowDriver {
  const [step, setStep] = useState<OpenFlowState | null>(null)
  const [busy, setBusy] = useState(false)
  // The step a click will answer, read synchronously: a render that
  // parked it and a handler that consumes it do not race through state.
  const stepRef = useRef<OpenFlowState | null>(null)
  const carryRef = useRef<Record<string, string>>({})
  // The host callbacks behind a ref, so the driver's own handlers stay
  // identity-stable across renders and an effect that depends on the
  // driver does not re-run on every render.
  const callbacksRef = useRef(callbacks)
  callbacksRef.current = callbacks

  const park = useCallback((value: OpenFlowState | null): void => {
    stepRef.current = value
    setStep(value)
  }, [])

  // Advance a reply: take a ready step's result, report a settlement, or
  // park a step for the view. The loop only ever runs a ready step
  // through `complete` once (a completion is a terminal outcome), so it
  // is not an unbounded retry.
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
          current = await session.answerFlow({
            seq: current.seq,
            step: 'complete',
            input: {},
          })
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
          // A lost concurrent commit is not this submit's failure -- the
          // winner already fired its own callback (see the file header).
          callbacksRef.current.onSuperseded?.()
          return false
        }
        const code = errorCodeOf(error)
        if (code === FLOW_INVALID) {
          // The flow is gone: drop the parked step so the channel's own
          // first step renders again and the viewer starts a fresh one.
          carryRef.current = {}
          park(null)
        }
        callbacksRef.current.onFailure(code)
        return false
      } finally {
        setBusy(false)
      }
    },
    [advance, park],
  )

  const start = useCallback(
    async (request: Parameters<AuthSession['startFlow']>[0]) => {
      // The gate decides whether the flow begins; a refusal starts
      // nothing and renders no error (see ./start-gate). It runs before
      // the request, so `answer` -- the flow's own later steps -- is
      // untouched. The busy flag is taken before the gate resolves too:
      // an async gate (a host consent wall) suspends under the same
      // pending indicator, so the channel's controls stay disabled for
      // its duration and a second click cannot pass the gate and start a
      // second flow.
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

  return { step, busy, start, absorb, answer, reset }
}
