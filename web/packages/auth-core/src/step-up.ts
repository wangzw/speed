/**
 * useStepUp -- the headless driver of a re-authentication (step-up)
 * challenge over the authenticated flow surface of @speed/auth-core's
 * session.
 *
 * A write whose route asks for a stronger or fresher authentication
 * than the session proves is refused 401 with the authn problem code
 * insufficient_user_authentication (pkg/authn/internal/mount's
 * StepUpRequired). The refusal is a strength shortfall, not the end of
 * the session: the caller can elevate and retry. Raising the session is
 * the session's own business, so the driving of that flow belongs here,
 * beside the session it elevates -- a caller (a UI) opens the challenge,
 * renders whatever step comes back, and this hook forwards each answer.
 *
 * The challenge is controlled by `active`: while it is false the hook
 * does nothing -- a flow left open by a closing challenge is abandoned on
 * the way out, so a stale binding never lingers; when it turns true the
 * hook starts one `purpose: 'step-up'` flow on the session and parks on
 * its first step. Each `answer` forwards one step's input; a reply that
 * settles the elevation -- `state: 'elevated'` for a token-delivered
 * session, the login-shaped `state: 'authenticated'` (the cookie
 * rotated) for the cookie-delivered one -- is the completion the caller
 * re-runs its refused action on, and `onElevated` fires once. A refusal
 * of an answer resolves to its code and renders as one banner; a fresh
 * answer is retryable, and the flow stays open. A refusal of the start
 * itself is the deployment declining to raise this session at all, which
 * no step can satisfy: it answers 401 insufficient_user_authentication --
 * the account has factors, but none whose methods may strengthen this
 * session -- and parks the challenge on 'unavailable' so the caller tells
 * the operator to re-establish the session rather than retry a
 * verification that cannot come. Every other refusal of the start,
 * a 400 included, is an ordinary failure and stays on 'failed' with its
 * code: a 400 by itself does not say the account has no factor, because
 * an account with no factor is recognised by reading its factors, and a
 * caller that knows that renders the enrollment it needs.
 *
 * The step-up flow is named with no method: an account that has a usable
 * factor enters at the factor step, which is the case a step-up-gated
 * privilege action meets (the account's factors are what the route's
 * requirement asks about). The hook never signs in and never navigates.
 *
 * A running start is not cancelled on a re-render: the flow is keyed to
 * the `active` transition (one run per opening), and a resolving start or
 * answer only acts while its opening is still current, so a stale reply
 * never lands on a reopened challenge.
 */

import { useEffect, useRef, useState } from 'react'
import { isApiError } from '@speed/api-client'
import type { AuthSession, AuthnFlowState, AuthnStep } from './session.js'

/** One answer to a flow step: the answer step name and its input, the
 * shape the login answer operation carries. */
export interface AuthnFlowAnswer {
  readonly step: string
  readonly input?: Readonly<Record<string, string>>
}

/** The challenge's local phase: the flow is starting, is parked on a
 * step, the last answer failed, the deployment cannot raise the session,
 * or the flow settled the elevation. */
export type StepUpPhase =
  | 'idle'
  | 'starting'
  | 'step'
  | 'failed'
  | 'unavailable'
  | 'elevated'

/** The state and the one command a step-up-gated surface reads. */
export interface StepUpState {
  /** The phase of the challenge; 'idle' while `active` is false. */
  readonly phase: StepUpPhase
  /** The step to render, or null before the first one arrives. */
  readonly step: AuthnStep | null
  /** The open flow's sequence number, for the next answer. */
  readonly seq: number | null
  /** The last failure's resolved code, or null. */
  readonly errorCode: string | null
  /** Answer the current step. Ignored while no step is parked. */
  readonly answer: (answer: AuthnFlowAnswer) => void
}

/** Resolves an answer's failure to a code, the reserved client.unknown
 * for a non-ApiError. */
function codeOf(error: unknown): string {
  return isApiError(error) ? error.code : 'client.unknown'
}

/**
 * Whether a refusal of the step-up start is this deployment declining to
 * raise the session at all. One answer carries that meaning, and no step
 * can satisfy it: a 401 `insufficient_user_authentication` -- the account
 * has factors, but none whose methods may strengthen this session, so the
 * start names none (the session's own isStepUpShortfall). It is read only
 * while the session stays established: a refusal that ended the session is
 * a dead session, not an unraisable one, and its code text renders
 * instead. Every other status, a 400 included, is an ordinary failure the
 * caller renders as its code -- the fact that an account has no factor is
 * read from its factor list, not inferred from a 400.
 */
function startUnavailable(session: AuthSession, error: unknown): boolean {
  return session.isStepUpShortfall(error)
}

/**
 * Drives the step-up flow the `active` flag opens. `session` is the
 * session the challenge elevates; `onElevated` fires once when the flow
 * settles the elevation, so the caller re-runs the action it remembered.
 */
export function useStepUp(
  session: AuthSession,
  active: boolean,
  onElevated: () => void,
): StepUpState {
  const [phase, setPhase] = useState<StepUpPhase>('idle')
  const [step, setStep] = useState<AuthnStep | null>(null)
  const [seq, setSeq] = useState<number | null>(null)
  const [errorCode, setErrorCode] = useState<string | null>(null)
  // A stream of answers is in flight; a further answer is ignored, so a
  // step cannot be double-submitted.
  const [submitting, setSubmitting] = useState(false)
  // The elevated callback is read through a ref, so a caller may pass a
  // fresh closure every render without restarting the flow.
  const elevatedRef = useRef(onElevated)
  elevatedRef.current = onElevated
  // Each opening takes a run token; a resolving start or answer only acts
  // while its token is still current, so a stale reply never lands on a
  // reopened challenge.
  const runRef = useRef(0)

  useEffect(() => {
    if (!active) {
      // Close: bump the token so a start still in flight is dropped, and
      // abandon the binding so no stale flow lingers on the server.
      runRef.current += 1
      setPhase('idle')
      setStep(null)
      setSeq(null)
      setErrorCode(null)
      setSubmitting(false)
      void session.abandonFlow().catch(() => undefined)
      return
    }
    const run = ++runRef.current
    setPhase('starting')
    setStep(null)
    setSeq(null)
    setErrorCode(null)
    setSubmitting(false)
    void (async () => {
      try {
        const state = await session.startAuthenticatedFlow({
          purpose: 'step-up',
        })
        if (run !== runRef.current) {
          return
        }
        settle(state)
      } catch (error) {
        if (run === runRef.current) {
          if (startUnavailable(session, error)) {
            // The deployment declined to raise this session: no factor may
            // strengthen it. No step can be rendered and no answer retried
            // -- the session must be re-established instead.
            setPhase('unavailable')
          } else {
            setErrorCode(codeOf(error))
            setPhase('failed')
          }
        }
      }
    })()
  }, [active, session])

  function settle(state: AuthnFlowState): void {
    if ('next' in state) {
      setPhase('step')
      setStep(state.next)
      setSeq(state.seq)
      return
    }
    if (state.state === 'elevated' || state.state === 'authenticated') {
      setPhase('elevated')
      setStep(null)
      setSeq(null)
      elevatedRef.current()
      return
    }
    // 'linked' / 'enrolled': not a step-up outcome; the flow settled
    // without elevating.
    setErrorCode('client.unknown')
    setPhase('failed')
  }

  const answer = (value: AuthnFlowAnswer): void => {
    if (submitting || seq === null) {
      return
    }
    setSubmitting(true)
    setErrorCode(null)
    const run = runRef.current
    void (async () => {
      try {
        const state = await session.answerFlow({
          seq,
          step: value.step,
          input: value.input,
        })
        if (run !== runRef.current) {
          return
        }
        settle(state)
      } catch (error) {
        if (run === runRef.current) {
          setErrorCode(codeOf(error))
          setPhase('failed')
        }
      } finally {
        if (run === runRef.current) {
          setSubmitting(false)
        }
      }
    })()
  }

  return { phase, step, seq, errorCode, answer }
}
