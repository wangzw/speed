/**
 * BindingCallbackHandler: completes a social-account binding at the
 * host's callback route.
 *
 * The host mounts this handler on the route its provider redirect sends
 * the browser back to. In the flow model the provider's callback is
 * handled server-side first (the authn module's own /auth/callback route
 * advances the flow bound by its cookie and redirects the browser on),
 * so the handler holds no code or state of its own: it reads the
 * `purpose: 'link'` flow back with session.readFlow() and drives it to
 * its terminal step.
 *
 * A ready flow (next.kind = 'complete') is taken with `step: 'complete'`;
 * a `link` completion (state 'linked') invalidates the profile read --
 * the bound row appears for any observer of that cache -- and fires
 * onBound, the host's cue to navigate back to the account surface. A
 * flow still parked on a step renders that step's continuation (FlowStep)
 * and answers it through session.answerFlow, so a link flow that needs
 * another answer is finished where it stands. A read that failed, and a
 * completion whose state is not 'linked', render their code text in the
 * InlineError banner under a retry. The `link` flow never signs anyone in
 * (a binding is not a login), so there is no signed-elsewhere outcome.
 * Nothing here navigates: the callbacks are the host's.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import { getAuthnProfileGetQueryKey } from '@speed/api-sdk'
import type { AuthSession, AuthnFlowState, AuthnStep } from '@speed/auth-core'
import { errorCodeOf } from '@speed/ui-kit'
import { FlowStep } from './internal/flow-step.js'
import type { FlowStepAnswer } from './internal/flow-step.js'
import { InlineError } from './internal/inline-error.js'
import { useAccountUiTranslation } from './internal/translation.js'

export interface BindingCallbackHandlerProps {
  /** The session whose link flow the handler reads back and completes. */
  readonly session: AuthSession
  /**
   * Fired once after a `link` completion commits (and the profile read
   * refetched); the host navigates back to the account surface. A
   * handler without one still completes the flow and refetches the
   * profile -- the bound row lands for any observer -- and rests on the
   * pending notice until the host reacts.
   */
  readonly onBound?: () => void
}

/**
 * The handler's local phases: reading (the flow is being read), step
 * (the flow is parked on a step the handler renders and answers), failed
 * (a read or an answer failed, under a retry) and done (a `link`
 * completion committed and the host was cued).
 */
type Phase = 'reading' | 'step' | 'failed' | 'done'

/** The answer step a challenge step's input travels under, by the flow's
 * own state: `answer` in the first factor, `factor` in the second,
 * `enroll` while registering. */
function challengeStepOf(state: string): string {
  if (state === 'second') {
    return 'factor'
  }
  if (state === 'enroll') {
    return 'enroll'
  }
  return 'answer'
}

export function BindingCallbackHandler({
  session,
  onBound,
}: BindingCallbackHandlerProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const [phase, setPhase] = useState<Phase>('reading')
  const [step, setStep] = useState<AuthnStep | null>(null)
  const [seq, setSeq] = useState<number | null>(null)
  const [challengeStep, setChallengeStep] = useState('answer')
  const [attempt, setAttempt] = useState(0)
  const [errorCode, setErrorCode] = useState<string | null>(null)
  const [answering, setAnswering] = useState(false)

  // A running read is not cancelled on cleanup: StrictMode's
  // mount/effect/cleanup/effect cycle would leave one start, so the first
  // must be allowed to finish. Each start takes a run token and a
  // resolving read only acts when its token is still current. The attempt
  // counter re-arms the effect for a retry.
  const runRef = useRef(0)

  /** Commits a settled answer: a `link` completion refetches the profile
   * and cues the host; a further step is rendered; anything else is a
   * failure. */
  const applySettled = useCallback(
    async (settled: AuthnFlowState, run: number): Promise<void> => {
      if ('next' in settled) {
        setStep(settled.next)
        setSeq(settled.seq)
        setChallengeStep(challengeStepOf(settled.state))
        setPhase('step')
        return
      }
      if (settled.state !== 'linked') {
        setErrorCode('client.unknown')
        setPhase('failed')
        return
      }
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
      if (run === runRef.current) {
        setPhase('done')
        onBound?.()
      }
    },
    [queryClient, onBound],
  )

  /** Applies a flow state read off the callback route: a ready flow is
   * taken, a parked one rendered, a settled non-link state a failure. */
  const applyState = useCallback(
    async (state: AuthnFlowState | null, run: number): Promise<void> => {
      if (state === null || !('next' in state)) {
        // No binding (the flow is done or gone), or a settled state
        // reached without the complete step: not a link outcome.
        setErrorCode('client.unknown')
        setPhase('failed')
        return
      }
      if (state.next.kind !== 'complete') {
        // Parked on a step: render its continuation.
        setStep(state.next)
        setSeq(state.seq)
        setChallengeStep(challengeStepOf(state.state))
        setPhase('step')
        return
      }
      const settled = await session.answerFlow({
        seq: state.seq,
        step: 'complete',
      })
      if (run !== runRef.current) {
        return
      }
      await applySettled(settled, run)
    },
    [session, applySettled],
  )

  // The pending live region must never mount together with its text: a
  // role="status" region announces only content changes that follow its
  // own insertion, so the pending text born in the same commit as the
  // region would be silent. The text is gated on `pendingCommitted`,
  // which lags the reading phase by one commit.
  const reading = phase === 'reading'
  const [pendingCommitted, setPendingCommitted] = useState(false)
  useEffect(() => {
    setPendingCommitted(reading)
  }, [reading])

  useEffect(() => {
    const run = ++runRef.current
    setPhase('reading')
    setErrorCode(null)
    setStep(null)
    setSeq(null)
    void (async () => {
      try {
        const state = await session.readFlow()
        if (run !== runRef.current) {
          return
        }
        await applyState(state, run)
      } catch (error) {
        if (run === runRef.current) {
          setErrorCode(errorCodeOf(error))
          setPhase('failed')
        }
      }
    })()
  }, [attempt, session, applyState])

  const handleAnswer = useCallback(
    async (answer: FlowStepAnswer): Promise<void> => {
      if (seq === null) {
        return
      }
      setAnswering(true)
      setErrorCode(null)
      const run = runRef.current
      try {
        const settled = await session.answerFlow({
          seq,
          step: answer.step,
          input: answer.input,
        })
        if (run !== runRef.current) {
          return
        }
        await applySettled(settled, run)
      } catch (error) {
        if (run === runRef.current) {
          setErrorCode(errorCodeOf(error))
          setPhase('failed')
        }
      } finally {
        setAnswering(false)
      }
    },
    [seq, session, applySettled],
  )

  const retry = (): void => {
    setAttempt((value) => value + 1)
  }

  if (phase === 'failed') {
    return (
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'flex-start',
          gap: 1.5,
          width: '100%',
        }}
      >
        <InlineError code={errorCode} />
        <Button variant="outlined" onClick={retry}>
          {t('bindingCallback.retry')}
        </Button>
      </Box>
    )
  }

  if (phase === 'step' && step !== null) {
    return (
      <Box sx={{ width: '100%' }}>
        <Typography variant="body2" color="text.secondary">
          {t('bindingCallback.pending')}
        </Typography>
        <FlowStep
          key={`${seq ?? 0}-${step.kind}`}
          step={step}
          busy={answering}
          answerStep={challengeStep}
          submitLabel={t('bindingCallback.continueLabel')}
          onSubmit={(answer) => void handleAnswer(answer)}
        />
      </Box>
    )
  }

  return (
    <Box
      role="status"
      sx={{ display: 'flex', alignItems: 'center', gap: 1.5, width: '100%' }}
    >
      {/* Decorative: the pending text on its own announces the state from
          the role=status container; naming the spinner too would read the
          notice twice. The text renders only once `pendingCommitted`
          arms it, one commit after this region mounts. */}
      <CircularProgress size={18} aria-hidden={true} />
      <Typography>
        {pendingCommitted ? t('bindingCallback.pending') : ''}
      </Typography>
    </Box>
  )
}
