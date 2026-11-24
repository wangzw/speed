/**
 * BindingCallbackHandler: completes a social-account binding at the host's
 * callback route. It reads the `purpose: 'link'` flow back and drives it to
 * its terminal step; it never signs anyone in and never navigates.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Alert from '@mui/material/Alert'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import { getAuthnProfileGetQueryKey } from '@speed/api-sdk'
import type { AuthSession, AuthnFlowState, AuthnStep } from '@speed/auth-core'
import {
  AnnouncementRegion,
  errorCodeOf,
  useWriteFeedback,
} from '@speed/ui-kit'
import { FlowStep } from './internal/flow-step.js'
import type { FlowStepAnswer } from './internal/flow-step.js'
import { InlineError } from './internal/inline-error.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

/**
 * The host supplies the session, the refused callback's problem code and
 * exit, and a fallback for an unreachable callback; the handler completes
 * the link flow and reports completion once.
 */
export interface BindingCallbackHandlerProps {
  /** The session whose link flow the handler reads back and completes. */
  readonly session: AuthSession
  /** Fired once after a `link` completion commits and the profile read
   * refetches; the host navigates back to the account surface. */
  readonly onBound?: () => void
  /** Rendered when no flow is bound and the deployment offers no `redirect`
   * method, so nothing can reach this callback. Only this case reads the
   * discovery document. */
  readonly absent?: ReactNode
  /** The problem code a refused callback landed with, read by the host from
   * the URL fragment. Given, the handler words it and offers no retry; the
   * flow is not read. */
  readonly initialError?: string
  /** The host's exit for the refused callback and the flow-gone notice, the
   * only action in both states. */
  readonly onDismiss?: () => void
}

type Phase = 'reading' | 'step' | 'failed' | 'done' | 'gone'

const FLOW_INVALID = 'authn.flow_invalid'

/** The answer step a challenge step's input travels under, by flow state. */
function challengeStepOf(state: string): string {
  if (state === 'second') {
    return 'factor'
  }
  if (state === 'enroll') {
    return 'enroll'
  }
  return 'answer'
}

/** Completes a link flow on the host's callback route. */
export function BindingCallbackHandler({
  session,
  onBound,
  absent,
  initialError,
  onDismiss,
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
  const [absentShown, setAbsentShown] = useState(false)
  const doneFeedback = useWriteFeedback()
  const succeedDone = doneFeedback.succeed

  // `t` and the host's props are read through refs inside the callbacks.
  // Hosts pass `onBound` and `absent` inline and `t` changes on a language
  // switch, so putting any in the callbacks' dependencies would re-arm the
  // read effect: the second read answers 401 (the flow is gone), which the
  // silent-refresh path turns into a restore whose notification re-renders
  // the host again, closing a loop that never reaches completion.
  const tRef = useRef(t)
  tRef.current = t
  const onBoundRef = useRef(onBound)
  onBoundRef.current = onBound
  const absentRef = useRef(absent)
  absentRef.current = absent
  const onDismissRef = useRef(onDismiss)
  onDismissRef.current = onDismiss

  // A running read is not cancelled on cleanup, because StrictMode's
  // mount/effect/cleanup/effect cycle would leave one start. Each start takes
  // a run token, and only the current token's read acts.
  const runRef = useRef(0)

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
        succeedDone(tRef.current('bindingCallback.done'))
        setPhase('done')
        onBoundRef.current?.()
      }
    },
    [queryClient, succeedDone],
  )

  const applyState = useCallback(
    async (state: AuthnFlowState | null, run: number): Promise<void> => {
      if (state === null) {
        // With an absent slot, ask whether any redirect method exists; a
        // failed discovery read cannot settle it, so the banner stays.
        if (absentRef.current !== undefined) {
          let offersRedirect = true
          try {
            const doc = await session.listMethods()
            offersRedirect = doc.methods.some(
              (method) => method.kind === 'redirect',
            )
          } catch {
            offersRedirect = true
          }
          if (run !== runRef.current) {
            return
          }
          if (!offersRedirect) {
            setAbsentShown(true)
            return
          }
        }
        if (run !== runRef.current) {
          return
        }
        if (onDismissRef.current !== undefined) {
          // No re-read can change a gone flow, so the terminal notice replaces
          // a retry that would spin on the same nothing.
          setPhase('gone')
          return
        }
        setErrorCode('client.unknown')
        setPhase('failed')
        return
      }
      if (!('next' in state)) {
        setErrorCode('client.unknown')
        setPhase('failed')
        return
      }
      if (state.next.kind !== 'complete') {
        setStep(state.next)
        setSeq(state.seq)
        setChallengeStep(challengeStepOf(state.state))
        setPhase('step')
        return
      }
      let settled: AuthnFlowState
      try {
        settled = await session.answerFlow({
          seq: state.seq,
          step: 'complete',
        })
      } catch (error) {
        if (run !== runRef.current) {
          return
        }
        if (
          errorCodeOf(error) === FLOW_INVALID &&
          onDismissRef.current !== undefined
        ) {
          // The completion can never land and a re-read is an empty turn.
          setPhase('gone')
          return
        }
        throw error
      }
      if (run !== runRef.current) {
        return
      }
      await applySettled(settled, run)
    },
    [session, applySettled],
  )

  useEffect(() => {
    if (initialError !== undefined) {
      return
    }
    const run = ++runRef.current
    setPhase('reading')
    setErrorCode(null)
    setStep(null)
    setSeq(null)
    setAbsentShown(false)
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
  }, [attempt, initialError, session, applyState])

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

  if (initialError !== undefined) {
    // A conflict reads here as "held by another account", not "the account
    // changed"; the way out is back, never a repeat of the round trip.
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
        <InlineError
          code={initialError}
          message={
            initialError === 'authn.conflict'
              ? t('bindingCallback.conflict')
              : undefined
          }
        />
        {onDismiss === undefined ? null : (
          <Button variant="contained" onClick={onDismiss}>
            {t('bindingCallback.backToAccount')}
          </Button>
        )}
      </Box>
    )
  }

  if (absentShown && absent !== undefined) {
    return <>{absent}</>
  }

  // The success region stands across every phase, so the confirmation fills a
  // live region the screen reader already knows rather than arriving with it.
  return (
    <Box sx={{ width: '100%' }}>
      <WriteFeedbackRegion outcome={doneFeedback.outcome} />
      {phase === 'done' ? null : phase === 'gone' ? (
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'flex-start',
            gap: 1.5,
            width: '100%',
          }}
        >
          <Alert severity="warning" role="alert" sx={{ width: '100%' }}>
            {t('bindingCallback.flowGone')}
          </Alert>
          <Button variant="contained" onClick={onDismiss}>
            {t('bindingCallback.backToAccount')}
          </Button>
        </Box>
      ) : phase === 'failed' ? (
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
      ) : phase === 'step' && step !== null ? (
        <Box sx={{ width: '100%' }}>
          <Typography variant="body2" color="text.secondary">
            {t('bindingCallback.pending')}
          </Typography>
          <FlowStep
            key={`${seq ?? 0}-${step.kind}`}
            step={step}
            sequence={seq ?? 0}
            busy={answering}
            answerStep={challengeStep}
            submitLabel={t('bindingCallback.continueLabel')}
            onSubmit={(answer) => void handleAnswer(answer)}
          />
        </Box>
      ) : (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 1.5,
            width: '100%',
          }}
        >
          {/* Decorative: the announcement region already names the state. */}
          <CircularProgress size={18} aria-hidden={true} />
          <AnnouncementRegion
            message={t('bindingCallback.pending')}
            sx={{ typography: 'body1' }}
          />
        </Box>
      )}
    </Box>
  )
}
