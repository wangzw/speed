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
 * does nothing -- a flow this mount opened and left open is abandoned on
 * the way out, so a stale binding never lingers. Unmounting is the same
 * departure: a caller whose surface goes away instead of closing (a
 * dialog that renders nothing while closed, a restart that remounts the
 * driver) gets the same release without a cleanup of its own. A mount
 * that opened no flow (its `active` never turned true) sends no abandon
 * request at all, because there is no binding to release; nor does a
 * flow that already settled, which the server has consumed. When
 * `active` turns true the hook starts one `purpose: 'step-up'` flow on
 * the session and parks on its first step. Each `answer` forwards one step's input; a reply that
 * settles the elevation -- `state: 'elevated'` for a token-delivered
 * session, the login-shaped `state: 'authenticated'` (the cookie
 * rotated) for the cookie-delivered one -- is the completion the caller
 * re-runs its refused action on, and `onElevated` fires once. A refusal
 * of an answer resolves to its code and renders as one banner; a fresh
 * answer is retryable, and the flow stays open. A refusal that states a
 * wait -- a 429 or 503 answer carrying Retry-After, read from the
 * api-client's `retryAfterMs` -- also arms `retryAfterSeconds`: the caller
 * holds its retry control for that many whole seconds, the value ticks
 * down once a second, and when it reaches zero the wait clears and the
 * step is answerable again; the failure code stays, rendered as its own
 * wait-free text. A refusal of the start
 * itself is the deployment declining to raise this session at all, which
 * no step can satisfy: it answers 401 insufficient_user_authentication --
 * the account has factors, but none whose methods may strengthen this
 * session or none it can prove again without a password -- and parks the
 * challenge on 'unavailable' so the caller tells
 * the operator to re-establish the session rather than retry a
 * verification that cannot come. Every other refusal of the start,
 * a 400 included, is an ordinary failure and stays on 'failed' with its
 * code: a 400 by itself does not say the account has no factor, because
 * an account with no factor is recognised by reading its factors, and a
 * caller that knows that renders the enrollment it needs.
 *
 * By default the step-up flow is named with no method: an account that has
 * a usable factor enters at the factor step, which is the case a step-up-
 * gated privilege action meets (the account's factors are what the route's
 * requirement asks about). A caller may instead name the `method` that
 * established the session's first factor (`sms` for an SMS session,
 * `password` for a password one): an account with no enabled factor is
 * held to a fresh first factor, and the server accepts a step-up that
 * re-proves that factor and refreshes the authentication time. The flow
 * then parks on that method's first step, whose answers travel under the
 * flow's `answer` step. The hook never signs in and never navigates.
 *
 * A step-up sends a verification message when the chosen second factor is
 * an SMS one, or when the first-factor re-proof supplies the phone an SMS
 * method asks for. Those answers present the deployment's
 * human-verification proof. The hook obtains the gate and the proof
 * through auth-core's useHumanVerification and exposes them as `human`, so
 * the caller renders the proof block on the step that may send; a
 * presented proof is consumed, and an answer that sends nothing leaves it
 * unspent.
 *
 * A flow that reaches its ready state parks on a `complete` step -- one
 * that collects nothing and only confirms, taking the result with
 * `step: 'complete'`. The hook answers that step on its own, once per
 * sequence number, so an elevation already settled on the server never
 * leaves the caller a contextless second confirmation to render. The
 * automatic answer waits for an in-flight answer to clear, because the
 * hook drops an answer sent while one is out, and it shows through the
 * same `submitting` flag. A refused automatic answer parks the flow on
 * 'failed' and leaves the step in place, so the caller renders that step's
 * own confirmation button for a manual retry.
 *
 * A running start is not cancelled on a re-render: the flow is keyed to
 * the `active` transition (one run per opening), and a resolving start or
 * answer only acts while its opening is still current, so a stale reply
 * never lands on a reopened challenge.
 */

import { useEffect, useRef, useState } from 'react'
import { isApiError } from '@speed/api-client'
import type { AuthSession, AuthnFlowState, AuthnStep } from './session.js'
import { humanGateNeedsProof, useHumanVerification } from './human.js'
import type { HumanVerification } from './human.js'

/**
 * The code a refusal carries when the flow the request named is gone. The
 * record is what the open claim below releases, so an answer saying it is
 * gone ends the claim: there is nothing left to release, and releasing
 * anyway would delete whatever flow the cookie names next.
 */
const FLOW_INVALID_CODE = 'authn.flow_invalid'

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
  /** Whether an answer is in flight. An answer sent while this is true is
   * ignored, so a caller that must send reliably waits for it to clear. */
  readonly submitting: boolean
  /** The last failure's resolved code, or null. */
  readonly errorCode: string | null
  /**
   * The remaining wait the last failure stated, in whole seconds, or null
   * when it stated none. A 429 (or 503) answer carries the api-client's
   * `retryAfterMs`, and the caller holds its retry control until this
   * counts down to zero. The value ticks once a second while it is set and
   * clears when no time remains -- it never reads zero -- so the failure
   * code stays, the caller renders that code's wait-free text, and its form
   * becomes submittable again in the frame the wait ends.
   */
  readonly retryAfterSeconds: number | null
  /** The deployment's human-verification gate and the proof this challenge
   * holds. A step that may send a verification message renders its block;
   * an answer that sends presents the proof. */
  readonly human: HumanVerification
  /** Answer the current step. Ignored while no step is parked. An answer
   * that may send a verification message carries the proof the gate
   * requires. */
  readonly answer: (answer: AuthnFlowAnswer) => void
}

/**
 * Whether answering this step with this input may send a verification
 * message. A step-up sends when the chosen second factor is an SMS one,
 * or when the first-factor re-proof supplies the phone an SMS method
 * asks for. It mirrors the server's own rule (pkg/authn/login's
 * applyFactor and challengeFactor), so a proof is presented exactly when
 * the answer may spend one.
 */
function answerSends(
  step: AuthnStep | null,
  value: AuthnFlowAnswer,
): boolean {
  if (step === null) {
    return false
  }
  const input = value.input ?? {}
  if (step.kind === 'factor') {
    return step.factors.some(
      (factor) => factor.id === input.factor && factor.kind === 'sms',
    )
  }
  if (step.kind === 'challenge') {
    return step.fields.some(
      (field) => field.kind === 'phone' && (input[field.name] ?? '') !== '',
    )
  }
  return false
}

/** Resolves an answer's failure to a code, the reserved client.unknown
 * for a non-ApiError. */
function codeOf(error: unknown): string {
  return isApiError(error) ? error.code : 'client.unknown'
}

/**
 * The wait a failure stated, in whole seconds: the api-client's
 * `retryAfterMs` (set from a 429 or 503 answer's Retry-After) rounded up,
 * or undefined when the failure carried none. Read structurally, so a
 * non-ApiError or a malformed value starts no countdown.
 */
function waitSecondsOf(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null) {
    return undefined
  }
  let value: unknown
  try {
    value = (error as { retryAfterMs?: unknown }).retryAfterMs
  } catch {
    return undefined
  }
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
    return undefined
  }
  return Math.max(1, Math.ceil(value / 1000))
}

/**
 * Whether a refusal of the step-up start is this deployment declining to
 * raise the session at all. One answer carries that meaning, and no step
 * can satisfy it: a 401 `insufficient_user_authentication` -- the account
 * has factors, but none whose methods may strengthen this session or none
 * it can prove again without a password, so the
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
 * `method` names the login method that established the session's first
 * factor, for an account that has no factor to raise the session with; it
 * is omitted when the account has a factor, and when it is omitted the
 * flow enters at the factor step. `returnPath` is where a redirect method's
 * round trip lands the browser -- the surface that opened the challenge, so
 * that one is the one that reads the refusal; omitted, the deployment's
 * default return target applies.
 */
export function useStepUp(
  session: AuthSession,
  active: boolean,
  onElevated: () => void,
  method?: string,
  returnPath?: string,
): StepUpState {
  const [phase, setPhase] = useState<StepUpPhase>('idle')
  const [step, setStep] = useState<AuthnStep | null>(null)
  const [seq, setSeq] = useState<number | null>(null)
  const [errorCode, setErrorCode] = useState<string | null>(null)
  // A stream of answers is in flight; a further answer is ignored, so a
  // step cannot be double-submitted.
  const [submitting, setSubmitting] = useState(false)
  // The deadline of the wait the last refusal stated, and the clock the
  // remaining seconds are read against. Both are null/now-free when no wait
  // is armed. The deadline (not a decremented counter) is the state, so a
  // timer that fires late -- a throttled tab, a slow machine -- still reads
  // the true remaining time instead of drifting.
  const [waitUntil, setWaitUntil] = useState<number | null>(null)
  const [now, setNow] = useState(() => Date.now())
  // The remaining wait, or null once no time remains. A deadline that has
  // passed publishes no wait -- never zero -- so a caller holding its retry
  // control on a non-null value frees it in the same frame the deadline is
  // crossed instead of showing "0 seconds" for one more render.
  const remainingSeconds =
    waitUntil === null ? null : Math.ceil((waitUntil - now) / 1000)
  const retryAfterSeconds =
    remainingSeconds === null || remainingSeconds <= 0 ? null : remainingSeconds
  // The elevated callback is read through a ref, so a caller may pass a
  // fresh closure every render without restarting the flow.
  const elevatedRef = useRef(onElevated)
  elevatedRef.current = onElevated
  // Each opening takes a run token; a resolving start or answer only acts
  // while its token is still current, so a stale reply never lands on a
  // reopened challenge.
  const runRef = useRef(0)
  // Whether this mount holds an open flow of its own to release: set when
  // the opening's start request is issued, cleared when that flow settles
  // (the server consumed it) or is abandoned. Abandoning on every close
  // would send a request for a binding that never existed -- a mount that
  // stays closed opened no flow.
  const openRef = useRef(false)
  // The sequence number of the ready step this hook answered on its own
  // (see the automatic completion effect below). Cleared on every opening so
  // a sequence number from a previous flow never suppresses the next one's.
  const autoAnsweredSeq = useRef<number | null>(null)
  // The unmount release is deferred by one microtask, so a remount in the
  // same commit cancels it (see the release effect below). The flag is the
  // pending release.
  const releasePendingRef = useRef(false)
  // The session the release targets, read through a ref so the cleanup
  // below needs no dependency and never re-registers.
  const sessionRef = useRef(session)
  sessionRef.current = session
  // The deployment's gate and this challenge's proof. A step that may send
  // (an SMS second factor, or the SMS first factor a step-up re-proves)
  // renders the block; the answer that sends presents the proof. The gate
  // is read here because a caller that opens the challenge has no other
  // discovery document to hand it, and it is read only while the challenge
  // is active: a closed challenge reads and solves nothing.
  const human = useHumanVerification(session, { enabled: active })

  useEffect(() => {
    // A new opening answers its own ready step afresh.
    autoAnsweredSeq.current = null
    if (!active) {
      // Close: bump the token so a start still in flight is dropped, and
      // abandon the binding this mount opened, so no stale flow lingers
      // on the server. Nothing was opened, nothing is sent.
      runRef.current += 1
      setPhase('idle')
      setStep(null)
      setSeq(null)
      setErrorCode(null)
      setSubmitting(false)
      setWaitUntil(null)
      if (openRef.current) {
        openRef.current = false
        void session.abandonFlow().catch(() => undefined)
      }
      return
    }
    const run = ++runRef.current
    setPhase('starting')
    setStep(null)
    setSeq(null)
    setErrorCode(null)
    setSubmitting(false)
    setWaitUntil(null)
    void (async () => {
      try {
        openRef.current = true
        const state = await session.startAuthenticatedFlow({
          purpose: 'step-up',
          ...(method === undefined ? {} : { method }),
          ...(returnPath === undefined ? {} : { return_to: returnPath }),
        })
        if (run !== runRef.current) {
          return
        }
        settle(state)
      } catch (error) {
        if (run === runRef.current) {
          // The start created no flow to release.
          openRef.current = false
          if (startUnavailable(session, error)) {
            // The deployment declined to raise this session: no factor may
            // strengthen it. No step can be rendered and no answer retried
            // -- the session must be re-established instead.
            setPhase('unavailable')
          } else {
            armWait(error)
            setErrorCode(codeOf(error))
            setPhase('failed')
          }
        }
      }
    })()
  }, [active, session, method, returnPath])

  // Unmounting is a departure like `active` turning false: a flow this
  // mount opened and has not seen settle is released, so no stale binding
  // lingers on the server; a refused start or a settled flow has nothing
  // to release. The release waits one microtask so StrictMode's double
  // effect invocation -- a cleanup immediately followed by a remount,
  // both in the same task -- cancels it in the remount's own setup: the
  // flow the remounted hook starts is the one that must live, and a
  // DELETE sent while that start is in flight would race it.
  useEffect(() => {
    releasePendingRef.current = false
    return () => {
      if (!openRef.current) {
        return
      }
      releasePendingRef.current = true
      void Promise.resolve().then(() => {
        if (!releasePendingRef.current) {
          return
        }
        releasePendingRef.current = false
        openRef.current = false
        void sessionRef.current.abandonFlow().catch(() => undefined)
      })
    }
  }, [])

  // The countdown a refusal stated: tick once a second while a wait is
  // armed, and stop the timer at its own deadline instead of leaving it
  // running for the rest of the mount.
  useEffect(() => {
    if (waitUntil === null) {
      return undefined
    }
    const armed = Date.now()
    if (armed >= waitUntil) {
      // The deadline passed before the timer could be set: read the clock
      // once so the derived remaining value settles at zero.
      setNow(armed)
      return undefined
    }
    const timer = setInterval(() => {
      const current = Date.now()
      setNow(current)
      if (current >= waitUntil) {
        clearInterval(timer)
      }
    }, 1000)
    return () => {
      clearInterval(timer)
    }
  }, [waitUntil])

  // The wait elapsed: the caller's retry control is free again. The failure
  // code stays -- it still names what the last attempt hit -- and renders
  // its own wait-free text; a retry or a reopen clears it. A wait that has
  // elapsed reads as null, so this clears the deadline the moment the
  // remaining time reaches zero rather than publishing a zero-second wait.
  useEffect(() => {
    if (waitUntil === null || retryAfterSeconds !== null) {
      return
    }
    setWaitUntil(null)
  }, [waitUntil, retryAfterSeconds])

  /** Arm the wait a refusal stated, or disarm any wait when it stated
   * none. */
  function armWait(error: unknown): void {
    const seconds = waitSecondsOf(error)
    if (seconds === undefined) {
      setWaitUntil(null)
      return
    }
    // The clock is reset with the deadline, so a wait armed long after the
    // mount starts its countdown from the full interval instead of the
    // stale value read at mount.
    const armed = Date.now()
    setNow(armed)
    setWaitUntil(armed + seconds * 1000)
  }

  function settle(state: AuthnFlowState): void {
    if ('next' in state) {
      setPhase('step')
      setStep(state.next)
      setSeq(state.seq)
      return
    }
    // The flow settled on the server, which consumed it: no binding is
    // left for a later close to abandon.
    openRef.current = false
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
    // The proof the answer presents, when it may send. A single-use proof
    // is consumed once presented, whatever the server answered, so the
    // block obtains a fresh one for the next attempt.
    const presented =
      humanGateNeedsProof(human.gate) && answerSends(step, value)
        ? human.proof
        : null
    setSubmitting(true)
    setErrorCode(null)
    // A retry disarms the previous refusal's wait.
    setWaitUntil(null)
    const run = runRef.current
    void (async () => {
      try {
        const state = await session.answerFlow({
          seq,
          step: value.step,
          input: value.input,
          ...(presented === null ? {} : { human_proof: presented }),
        })
        if (run !== runRef.current) {
          return
        }
        settle(state)
      } catch (error) {
        if (run === runRef.current) {
          const code = codeOf(error)
          if (code === FLOW_INVALID_CODE) {
            // The record this flow named is gone, so there is no binding
            // left to release. Releasing it anyway -- on a close, or on
            // the remount a restart performs -- deletes whatever flow the
            // cookie names by then, which is the fresh one the restart
            // just opened. The server has said there is nothing here.
            openRef.current = false
          }
          setErrorCode(code)
          armWait(error)
          setPhase('failed')
        }
      } finally {
        if (run === runRef.current) {
          if (presented !== null) {
            human.consume()
          }
          setSubmitting(false)
        }
      }
    })()
  }

  // A `complete` step collects nothing and only confirms, so the hook
  // answers it on its own rather than leaving the caller a contextless
  // second confirmation. `submitting` is read because the hook drops an
  // answer sent while another is out, and `autoAnsweredSeq` records the
  // sequence this hook answered: one automatic answer per step, whatever
  // the re-render or StrictMode's double effect invocation. A refusal moves
  // the flow to 'failed' and leaves the step parked, where the caller's own
  // confirmation button retries it by hand.
  useEffect(() => {
    if (step === null || step.kind !== 'complete' || phase !== 'step') {
      return
    }
    if (submitting || seq === null || autoAnsweredSeq.current === seq) {
      return
    }
    autoAnsweredSeq.current = seq
    answer({ step: 'complete' })
  }, [step, phase, submitting, seq, answer])

  return {
    phase,
    step,
    seq,
    submitting,
    errorCode,
    retryAfterSeconds,
    human,
    answer,
  }
}
