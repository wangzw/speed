/**
 * BindingCallbackHandler: completes a social-account binding at the
 * host's callback route.
 *
 * The host mounts this handler on the route its provider redirect sends
 * the browser back to. In the flow model the provider's callback is
 * handled server-side first (the authn module's own /api/v1/auth/callback route
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
 * another answer is finished where it stands. A landed `link` completion
 * confirms itself through the write-feedback region, naming the bound
 * object, so a host that stays (or gave no onBound) is not left on the
 * pending notice; the region stands in the tree across every phase, empty
 * until the link settles, so its live region announces the confirmation
 * rather than being born with it. A read that finds no flow -- the binding
 * is gone or the completion already landed -- and a `complete` exchange
 * refused as `authn.flow_invalid` are definitive: re-reading would answer
 * the same nothing. With the host's `onDismiss` each renders a terminal
 * notice whose one action returns to the account surface; without one the
 * generic failure banner keeps its retry. A read that failed, and a
 * completion whose state is not 'linked', render their code text in the
 * InlineError banner under a retry.
 * A callback the server refused never reaches this handler as a flow: it
 * lands back on the callback route with its problem code in the URL
 * fragment (the same shape a refused login lands with), and the host hands
 * that code in as `initialError`. The handler words the code for this
 * surface, reads no flow and offers no retry -- the server already consumed
 * the flow, so the only action is `onDismiss`, back to the account surface.
 * The `link` flow never signs anyone in
 * (a binding is not a login), so there is no signed-elsewhere outcome.
 * Nothing here navigates: the callbacks are the host's.
 *
 * The optional `absent` slot answers a deployment with no social binding:
 * when no flow is bound and `session.listMethods()` reports no `redirect`
 * method, nothing can reach this callback, so the host's "no such page"
 * renders instead of a flow-not-found banner. The discovery document is
 * read only when `absent` is supplied.
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
import { errorCodeOf, useWriteFeedback } from '@speed/ui-kit'
import { FlowStep } from './internal/flow-step.js'
import type { FlowStepAnswer } from './internal/flow-step.js'
import { InlineError } from './internal/inline-error.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

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
  /**
   * Rendered when no flow is bound AND the deployment offers no
   * `redirect` method: nothing can reach this callback, so the host's
   * "no such page" answers instead of a flow-not-found banner. When
   * supplied, the handler reads `session.listMethods()` in that case
   * (and only in that case); without it the handler behaves exactly as
   * before and reads no discovery document.
   */
  readonly absent?: ReactNode
  /**
   * The problem code a refused callback landed with, read by the host from
   * the URL fragment. Given, the handler words that code for this surface
   * and offers no retry: the server already consumed the flow and said what
   * went wrong, so the same round trip cannot succeed again. The flow is
   * not read.
   */
  readonly initialError?: string
  /**
   * The host's exit, used in two terminal states: the refused callback's
   * only action, and the flow-gone notice's only action -- both return to
   * the account surface, because the server already consumed the flow and
   * the callback route cannot start another. Without one the refused
   * message stands alone and a gone flow falls back to the failure banner
   * with its retry.
   */
  readonly onDismiss?: () => void
}

/**
 * The handler's local phases: reading (the flow is being read), step
 * (the flow is parked on a step the handler renders and answers), failed
 * (a read or an answer failed, under a retry), done (a `link` completion
 * committed and the host was cued) and gone (the flow no longer exists, so
 * the terminal notice offers only the way back).
 */
type Phase = 'reading' | 'step' | 'failed' | 'done' | 'gone'

/** The code the server answers a flow that no longer exists. */
const FLOW_INVALID = 'authn.flow_invalid'

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
  // The done stage's settled success: the completion confirms itself through
  // the same write-feedback region as every other account write, naming the
  // bound object. A host that leaves on onBound never renders it; one that
  // stays (or gave no onBound) gets the confirmation instead of the pending
  // notice.
  const doneFeedback = useWriteFeedback()
  const succeedDone = doneFeedback.succeed

  // The translation function and the host's two props are read through refs
  // inside the callbacks. `t` changes identity on a language switch, and hosts
  // pass `onBound` and `absent` inline, so any unrelated parent re-render
  // hands the handler new values. Putting any of them in the callbacks'
  // dependencies would re-arm the read effect, re-reading a flow its done
  // stage already took: the second read answers 401 (the flow is gone), which
  // the silent-refresh path turns into a restore whose notification re-renders
  // the host again, closing a loop that never reaches the callback's
  // completion. The confirmation is translated with whatever language is
  // active when the link settles -- a snapshot, like every other success
  // message.
  const tRef = useRef(t)
  tRef.current = t
  const onBoundRef = useRef(onBound)
  onBoundRef.current = onBound
  const absentRef = useRef(absent)
  absentRef.current = absent
  // Read inside the callbacks for the same reason: the flow's terminal
  // decision must not re-arm the read effect when a host passes it inline.
  const onDismissRef = useRef(onDismiss)
  onDismissRef.current = onDismiss

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
        // Translated when the link settles; it names the bound object.
        succeedDone(tRef.current('bindingCallback.done'))
        setPhase('done')
        onBoundRef.current?.()
      }
    },
    [queryClient, succeedDone],
  )

  /** Applies a flow state read off the callback route: a ready flow is
   * taken, a parked one rendered, a settled non-link state a failure. */
  const applyState = useCallback(
    async (state: AuthnFlowState | null, run: number): Promise<void> => {
      if (state === null) {
        // No binding (the flow is done or gone). With an absent slot, ask
        // the discovery document whether this deployment offers any
        // redirect method: without one nothing can reach this callback and
        // the host's 404 answers. A discovery read that fails cannot settle
        // it, so the failure banner stays.
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
          // The flow is definitively gone (or already completed elsewhere):
          // no re-read can change that, so the terminal notice replaces the
          // banner whose retry would spin on the same nothing.
          setPhase('gone')
          return
        }
        setErrorCode('client.unknown')
        setPhase('failed')
        return
      }
      if (!('next' in state)) {
        // A settled state reached without the complete step: not a link
        // outcome.
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
          // The flow no longer exists: the completion can never land and a
          // re-read is a certain empty turn, so the notice is terminal.
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
    // A refused landing carries its code in the fragment: the server already
    // consumed the flow and said what went wrong, so there is no flow to
    // read and nothing a retry could change.
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
    // The server's own refusal: this surface says what it means here -- a
    // conflicting social identity is already held by another account, not
    // "the account changed" -- and the way out is back to the account
    // surface, never a repeat of the round trip.
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
          <Button variant="outlined" onClick={onDismiss}>
            {t('bindingCallback.backToAccount')}
          </Button>
        )}
      </Box>
    )
  }

  if (absentShown && absent !== undefined) {
    return <>{absent}</>
  }

  // The done stage's success region stands in the tree from the first
  // render, across every phase, so the confirmation fills a live region the
  // screen reader already knows rather than arriving in the commit that
  // mounts it. The slot is empty until a link settles; the phase body below
  // changes around it.
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
          role="status"
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 1.5,
            width: '100%',
          }}
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
      )}
    </Box>
  )
}
