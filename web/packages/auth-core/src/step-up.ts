import { useEffect, useRef, useState } from 'react'
import { isApiError } from '@speed/api-client'
import type { AuthSession, AuthnFlowState, AuthnStep } from './session.js'
import { humanGateNeedsProof, useHumanVerification } from './human.js'
import type { HumanScope, HumanVerification } from './human.js'

/** The code a refusal carries when the named flow is gone; the open claim is released without a DELETE. */
const FLOW_INVALID_CODE = 'authn.flow_invalid'

/** One answer to a flow step: the answer step name and its input. */
export interface AuthnFlowAnswer {
  readonly step: string
  readonly input?: Readonly<Record<string, string>>
}

/**
 * The challenge's local phase: `'idle'` inactive, `'starting'` opening the flow,
 * `'step'` owing an answer, `'failed'` after a refusal, `'unavailable'` when the
 * deployment cannot elevate this session, `'elevated'` once elevated.
 */
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
  /** Whether an answer is in flight; an answer sent while true is ignored. */
  readonly submitting: boolean
  /** Whether an answer is held while the gate is read and its proof obtained. */
  readonly pending: boolean
  /** The last failure's resolved code, or null. */
  readonly errorCode: string | null
  /** The remaining wait the last failure stated, in whole seconds, or null; it ticks once a second and never reads zero. */
  readonly retryAfterSeconds: number | null
  /** The deployment's human-verification gate and the proof this challenge holds. */
  readonly human: HumanVerification
  /** The surface the current step's send belongs to. */
  readonly humanScope: HumanScope
  /** Answer the current step. Ignored while no step is parked, while an answer is in flight, or while a previous answer waits for its proof. */
  readonly answer: (answer: AuthnFlowAnswer) => void
}

function stepSends(step: AuthnStep | null): boolean {
  if (step === null) {
    return false
  }
  if (step.kind === 'factor') {
    return step.factors.some((factor) => factor.kind === 'sms')
  }
  if (step.kind === 'challenge') {
    return step.fields.some((field) => field.kind === 'phone')
  }
  return false
}

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

function codeOf(error: unknown): string {
  return isApiError(error) ? error.code : 'client.unknown'
}

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

function startUnavailable(session: AuthSession, error: unknown): boolean {
  return session.isStepUpShortfall(error)
}

/**
 * Drives the step-up flow `active` opens; `onElevated` fires once on settlement.
 * `method` names the first-factor method for a factorless account, `returnPath`
 * is where a redirect lands, and `excludeFactorId` must not be offered.
 */
export function useStepUp(
  session: AuthSession,
  active: boolean,
  onElevated: () => void,
  method?: string,
  returnPath?: string,
  excludeFactorId?: string,
): StepUpState {
  const [phase, setPhase] = useState<StepUpPhase>('idle')
  const [step, setStep] = useState<AuthnStep | null>(null)
  const [seq, setSeq] = useState<number | null>(null)
  const [errorCode, setErrorCode] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [pendingAnswer, setPendingAnswer] = useState<AuthnFlowAnswer | null>(
    null,
  )
  // One answer at a time: the immediate and deferred paths can reach the send in the same tick.
  const sendingRef = useRef(false)
  // The deadline (not a counter) is the state, so a late timer reads the true remaining time.
  const [waitUntil, setWaitUntil] = useState<number | null>(null)
  const [now, setNow] = useState(() => Date.now())
  const remainingSeconds =
    waitUntil === null ? null : Math.ceil((waitUntil - now) / 1000)
  const retryAfterSeconds =
    remainingSeconds === null || remainingSeconds <= 0 ? null : remainingSeconds
  const elevatedRef = useRef(onElevated)
  elevatedRef.current = onElevated
  // A resolving start or answer acts only while its run token is current.
  const runRef = useRef(0)
  // Whether this mount holds an open flow to release; a closed mount opened none.
  const openRef = useRef(false)
  const autoAnsweredSeq = useRef<number | null>(null)
  // The unmount release is deferred one microtask so a same-commit remount cancels it.
  const releasePendingRef = useRef(false)
  const sessionRef = useRef(session)
  sessionRef.current = session
  // A step that may send reaches a phone number, so it runs the phone surface's mechanism.
  const humanScope: HumanScope = stepSends(step) ? 'phone' : 'other'
  const human = useHumanVerification(session, {
    scope: humanScope,
    enabled: active && pendingAnswer !== null,
  })

  useEffect(() => {
    autoAnsweredSeq.current = null
    if (!active) {
      runRef.current += 1
      setPhase('idle')
      setStep(null)
      setSeq(null)
      setErrorCode(null)
      setSubmitting(false)
      setPendingAnswer(null)
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
    setPendingAnswer(null)
    setWaitUntil(null)
    void (async () => {
      try {
        openRef.current = true
        const state = await session.startAuthenticatedFlow({
          purpose: 'step-up',
          ...(method === undefined ? {} : { method }),
          ...(returnPath === undefined ? {} : { return_to: returnPath }),
          ...(excludeFactorId === undefined
            ? {}
            : { input: { exclude_factor: excludeFactorId } }),
        })
        if (run !== runRef.current) {
          return
        }
        settle(state)
      } catch (error) {
        if (run === runRef.current) {
          openRef.current = false
          if (startUnavailable(session, error)) {
            setPhase('unavailable')
          } else {
            armWait(error)
            setErrorCode(codeOf(error))
            setPhase('failed')
          }
        }
      }
    })()
  }, [active, session, method, returnPath, excludeFactorId])

  // Unmount releases an open flow after one microtask, so a StrictMode remount cancels it.
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

  useEffect(() => {
    if (waitUntil === null) {
      return undefined
    }
    const armed = Date.now()
    if (armed >= waitUntil) {
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

  useEffect(() => {
    if (waitUntil === null || retryAfterSeconds !== null) {
      return
    }
    setWaitUntil(null)
  }, [waitUntil, retryAfterSeconds])

  function armWait(error: unknown): void {
    const seconds = waitSecondsOf(error)
    if (seconds === undefined) {
      setWaitUntil(null)
      return
    }
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
    // The server consumed the flow, so no binding is left to abandon.
    openRef.current = false
    if (state.state === 'elevated' || state.state === 'authenticated') {
      setPhase('elevated')
      setStep(null)
      setSeq(null)
      elevatedRef.current()
      return
    }
    setErrorCode('client.unknown')
    setPhase('failed')
  }

  function sendAnswer(value: AuthnFlowAnswer, presented: string | null): void {
    if (seq === null || sendingRef.current) {
      return
    }
    if (
      answerSends(step, value) &&
      humanGateNeedsProof(human.gate, humanScope) &&
      presented === null
    ) {
      return
    }
    sendingRef.current = true
    setSubmitting(true)
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
            // The named flow is gone, so there is no binding left to release.
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
        sendingRef.current = false
      }
    })()
  }

  const answer = (value: AuthnFlowAnswer): void => {
    if (submitting || seq === null || pendingAnswer !== null) {
      return
    }
    setErrorCode(null)
    setWaitUntil(null)
    const maySend = answerSends(step, value)
    const gateKnown = !human.pending && human.gate !== null
    const needsProof = gateKnown && humanGateNeedsProof(human.gate, humanScope)
    if (maySend && (!gateKnown || (needsProof && human.proof === null))) {
      setPendingAnswer(value)
      return
    }
    sendAnswer(value, needsProof ? human.proof : null)
  }

  useEffect(() => {
    if (!active || pendingAnswer === null || human.pending) {
      return
    }
    if (human.gate === null) {
      return
    }
    const needsProof = humanGateNeedsProof(human.gate, humanScope)
    if (needsProof && human.proof === null) {
      return
    }
    setPendingAnswer(null)
    sendAnswer(pendingAnswer, needsProof ? human.proof : null)
  }, [
    active,
    pendingAnswer,
    human.pending,
    human.gate,
    human.proof,
    humanScope,
    sendAnswer,
  ])

  // A `complete` step collects nothing, so the hook answers it on its own once per seq.
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
    pending: pendingAnswer !== null,
    errorCode,
    retryAfterSeconds,
    human,
    humanScope,
    answer,
  }
}
