/** Resumes a social sign-in at the host's callback route. */

import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import type { AuthSession, AuthnMethodList } from '@speed/auth-core'
import { AnnouncementRegion, errorCodeOf } from '@speed/ui-kit'
import { useAuthUiTranslation } from './internal/translation.js'
import {
  ChannelFeedbackRegion,
  useChannelFeedback,
} from './internal/write-feedback.js'
import { RetryAction } from './internal/retry-action.js'
import {
  HumanGatedFlowStep,
  signupFieldRefusal,
} from './internal/flow-step.js'
import { FlowExpired } from './internal/flow-expired.js'
import { retryWaitMs } from './internal/retry-wait.js'
import { stepKey, useStepFocus } from './internal/step-focus.js'
import { useFlowDriver } from './internal/use-flow-driver.js'

/** The host supplies the session that resumes the flow, and optionally the
 * card and its exit; the handler owns the read-back and failure state. */
export interface SocialCallbackHandlerProps {
  /** The session that resumes the flow. */
  readonly session: AuthSession
  /** Fired once after the login lands -- and again on a later mount that
   * finds the session already authenticated (the re-entry case where no
   * read is started); the host navigates. */
  readonly onSignedIn?: () => void
  /**
   * Rendered when this deployment offers neither a `redirect` nor an
   * `enterprise` method, so nothing ever returns to this route. Supplying it
   * makes the handler read `listMethods` in that case, and only then.
   */
  readonly absent?: ReactNode
  /**
   * The host's card around this handler's own content (the pending notice, a
   * step, a refusal, the terminal notice). The `absent` view is returned
   * unwrapped. Omitted, the content renders bare.
   */
  readonly frame?: (content: ReactNode) => ReactNode
  /**
   * The host's exit to its sign-in surface, called by the terminal notice's one
   * action. Omitted, those states keep the failure banner and its re-read.
   */
  readonly onSignIn?: () => void
}

function offersCallbackReturn(doc: AuthnMethodList): boolean {
  return doc.methods.some(
    (method) => method.kind === 'redirect' || method.kind === 'enterprise',
  )
}

/** The code a gone flow carries: a read that finds none, or an answer refused
 * as a gone flow. */
const NO_FLOW = 'authn.flow_invalid'

/**
 * Reads the flow back and resumes it: a parked step is rendered and advanced
 * in place, a ready flow's result is taken, and a re-entry with an
 * authenticated session fires `onSignedIn` again.
 */
export function SocialCallbackHandler({
  session,
  onSignedIn,
  absent,
  frame,
  onSignIn,
}: SocialCallbackHandlerProps) {
  const { t } = useAuthUiTranslation()
  // The handler's one failure slot; a parked step's banner is this same slot
  // rendered beneath the step, never in place of it.
  const feedback = useChannelFeedback()
  // Stable identities, read by the mount effect without re-arming it.
  const { fail: failFeedback, reset: resetFeedback } = feedback
  const [failed, setFailed] = useState(false)
  const [flowExpired, setFlowExpired] = useState(false)
  const [absentShown, setAbsentShown] = useState(false)
  const [attempt, setAttempt] = useState(0)

  const driver = useFlowDriver(session, {
    onFailure: (code, error) => {
      if (code === NO_FLOW && onSignIn !== undefined) {
        // No answer or re-read can change a gone flow, so show the terminal
        // notice instead of a banner whose retry reads the same dead record.
        setFlowExpired(true)
        feedback.reset()
        return
      }
      // A parked step keeps its surface with the banner beside it. A ready
      // step or a gone flow has no step, so it falls back to the surface-wide
      // banner with the re-read.
      const parked = driverRef.current.currentStep()
      if (parked === null || parked.next.kind === 'complete') {
        setFailed(true)
      }
      feedback.fail(code, retryWaitMs(error))
    },
    onSettled: () => {
      try {
        onSignedIn?.()
      } catch {
        // A throwing host callback is not a failure of the flow, and the
        // failed state's retry would read a flow that is already gone.
      }
    },
  })
  // Read through refs so the read effect depends only on the session and the
  // retry counter: an inline host callback would otherwise re-arm it and
  // re-read a flow that is already gone.
  const driverRef = useRef(driver)
  driverRef.current = driver
  const onSignInRef = useRef(onSignIn)
  onSignInRef.current = onSignIn

  // One read per attempt: the ref survives StrictMode's double effect
  // invocation, so the second run starts nothing.
  const handledRef = useRef<number | null>(null)
  useEffect(() => {
    if (handledRef.current === attempt) {
      return
    }
    handledRef.current = attempt
    void (async () => {
      // A re-entry after a completed login: no read, and `onSignedIn` again.
      // With `absent`, first settle whether any method could have returned
      // here; a failed discovery read keeps the old answer.
      if (session.getSnapshot().state === 'authenticated') {
        if (absent !== undefined) {
          let offers: boolean | null = null
          try {
            offers = offersCallbackReturn(await session.listMethods())
          } catch {
            offers = null
          }
          if (offers === false) {
            setAbsentShown(true)
            return
          }
        }
        try {
          onSignedIn?.()
        } catch {
        }
        return
      }
      let reply
      try {
        reply = await session.readFlow()
      } catch (error) {
        failFeedback(errorCodeOf(error), retryWaitMs(error))
        setFailed(true)
        return
      }
      if (reply === null) {
        // With `absent`, no redirect/enterprise method means nothing returns
        // to this route and the host's 404 answers; otherwise (or when the
        // discovery read fails) the failure banner stays.
        if (absent !== undefined) {
          let offers: boolean | null = null
          try {
            offers = offersCallbackReturn(await session.listMethods())
          } catch {
            offers = null
          }
          if (offers === false) {
            setAbsentShown(true)
            return
          }
        }
        if (onSignInRef.current !== undefined) {
          setFlowExpired(true)
          return
        }
        failFeedback(NO_FLOW)
        setFailed(true)
        return
      }
      await driverRef.current.absorb(reply)
    })()
  }, [absent, attempt, session, onSignedIn, failFeedback])

  const retry = (): void => {
    resetFeedback()
    setFailed(false)
    setFlowExpired(false)
    setAttempt((value) => value + 1)
  }

  // The step read back after readFlow is a first appearance and moves no
  // focus; a later step does. With no step left, the page's heading takes the
  // dropped focus.
  const errorHeadingRef = useRef<HTMLHeadingElement>(null)
  const surfaceRef = useStepFocus(stepKey(driver.step), {
    focusFirstStep: false,
    emptyTarget: errorHeadingRef,
  })
  // A signup step's field refusals render under their field, not as the banner.
  const failureCode =
    feedback.outcome === null || feedback.outcome.kind !== 'failure'
      ? null
      : feedback.outcome.code
  const fieldRefusal =
    driver.step !== null && failureCode !== null
      ? signupFieldRefusal(driver.step, failureCode)
      : null
  const bannerFailure =
    failureCode === null || fieldRefusal !== null ? null : failureCode

  const framed = (content: ReactNode): ReactNode =>
    frame === undefined ? content : frame(content)

  if (absentShown && absent !== undefined) {
    return <>{absent}</>
  }

  // A redirect step belongs to the host's navigation, so with the host's exit
  // it is the same terminal state as a gone flow.
  const unrenderableStep =
    driver.step !== null && driver.step.next.kind === 'redirect'

  if (onSignIn !== undefined && (flowExpired || unrenderableStep)) {
    return (
      <>
        {framed(
          <Box
            ref={surfaceRef}
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'flex-start',
              gap: 1.5,
              width: '100%',
            }}
          >
            {/* The focus landing when the answered step's control went with
                it: a programmatic target, not a tab stop. */}
            <Typography
              ref={errorHeadingRef}
              component="h2"
              variant="h6"
              tabIndex={-1}
            >
              {t('socialCallback.incompleteTitle')}
            </Typography>
            <FlowExpired onRestart={onSignIn} />
          </Box>,
        )}
      </>
    )
  }

  if (failed) {
    return (
      <>
        {framed(
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'flex-start',
              gap: 1.5,
              width: '100%',
            }}
          >
            <Typography
              ref={errorHeadingRef}
              component="h2"
              variant="h6"
              tabIndex={-1}
            >
              {t('socialCallback.incompleteTitle')}
            </Typography>
            <ChannelFeedbackRegion feedback={feedback} />
            <RetryAction label={t('socialCallback.retry')} onClick={retry} />
          </Box>,
        )}
      </>
    )
  }

  if (driver.step !== null) {
    return (
      <>
        {framed(
          <Box ref={surfaceRef} sx={{ display: 'contents' }}>
            <HumanGatedFlowStep
              session={session}
              step={driver.step}
              busy={driver.busy}
              onAttemptStart={resetFeedback}
              onAnswer={(input, proof) => {
                resetFeedback()
                void driver.answer(input, proof)
              }}
              onUnsupported={
                // With the host's exit it becomes the terminal notice; without
                // one its own action re-reads the flow.
                onSignIn === undefined
                  ? retry
                  : () => setFlowExpired(true)
              }
              unsupportedLabel={t('socialCallback.retry')}
              failure={failureCode}
            />
            {/* A refused answer that left the flow on this step keeps its
                banner beneath the step; a signup field refusal renders under
                its field instead. */}
            {bannerFailure === null ? null : (
              <Box sx={{ mt: 1, width: '100%' }}>
                <ChannelFeedbackRegion feedback={feedback} />
              </Box>
            )}
          </Box>,
        )}
      </>
    )
  }

  return (
    <>
      {framed(
        // The spinner is decorative: the announcement region alone announces
        // the pending state, so naming the spinner would read it twice.
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 1.5,
            width: '100%',
          }}
        >
          <CircularProgress size={18} aria-hidden={true} />
          <AnnouncementRegion
            message={t('socialCallback.pending')}
            sx={{ typography: 'body1' }}
          />
        </Box>,
      )}
    </>
  )
}
