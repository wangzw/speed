/**
 * SocialCallbackHandler: resumes a social sign-in at the host's callback
 * route.
 *
 * The provider's round trip lands the browser back on the flow's
 * `return_to` -- the host's callback route -- because the server handed
 * the provider's redirect and answered it; the host renders this handler
 * there. On mount the handler reads the flow back
 * (`session.readFlow`): a flow that is ready (`next.kind = complete`) is
 * taken with `step: 'complete'`, the login lands, and the handler fires
 * onSignedIn once; a flow still parked on a step (a required second
 * factor, a factor to register) is rendered by the shared step renderer
 * and advanced in place. A mount that finds no flow -- the binding is
 * gone, the flow is done or expired -- is definitive: no further read can
 * change it, so with the host's `onSignIn` it becomes the terminal notice
 * and without one it renders the failure banner with a retry button that
 * reads once more. Nothing here navigates: the callback
 * route is the host's own and the success callback is the host's.
 *
 * A refused answer keeps its step when the flow stays parked on it: the
 * failure banner renders beside the still-parked step, its state is
 * unchanged, and the step's own controls are the retry -- answering again
 * clears the banner first, because an answer is a new attempt. A signup
 * step's refusals are the exception: its three name refusals render under
 * the username field, and a bad request on the face that asked for a
 * password is the password policy's message under the password field.
 *
 * A throttled refusal's stated wait is named in its banner and counted
 * down there, whichever placement the banner takes. The sentence assistive
 * technology reads stays fixed at the wait the answer first stated; only
 * the painted sentence follows the clock, and once the wait ends it drops
 * the number (the banner itself stays -- no timer clears a result).
 *
 * A refusal with no step left to keep -- the flow is gone
 * (`authn.flow_invalid`), whether the answer was a parked step's or a ready
 * flow's `complete` -- is definitive, as is a step this route cannot render
 * (a redirect step, whose navigation belongs to the host). With the host's
 * `onSignIn` each becomes the terminal notice whose one action returns the
 * viewer to the host's sign-in surface: this route knows neither the method
 * nor the provider that opened the flow, so it cannot start a new one
 * itself. Without `onSignIn`, a gone flow keeps the banner with the retry
 * that reads the flow back, and an unrenderable step's exit is the same
 * re-read, so the handler never returns to the pending state without
 * issuing a read. A ready flow's `complete` refused for any other reason
 * keeps that banner too: its flow may still be ready, so the read is not a
 * certain empty turn.
 *
 * A mount that finds the session already authenticated is a re-entry to
 * the callback route after a completed login; no second read is started
 * and the handler fires onSignedIn again so the host bounces the viewer
 * onward. A throwing onSignedIn is contained: it is not a failure of the
 * flow (which settled), so it never flips the handler to the failed state
 * -- whose retry would read a flow that is already gone -- and never
 * escapes as an unhandled rejection.
 *
 * The optional `absent` slot answers a deployment with no callback landing
 * point: when the handler finds no flow (or a re-entry) and the discovery
 * document offers neither a `redirect` nor an `enterprise` method, nothing
 * can return to this route, so the host's "no such page" renders instead
 * of a flow-not-found banner. The discovery document is read only when
 * `absent` is supplied. The `absent` view is returned unwrapped: it is the
 * host's own page, not this handler's surface.
 *
 * The optional `frame` slot is the host's card around this handler's own
 * content -- the pending notice, a step, a refusal, the terminal notice.
 * The host renders the sign-in surface's own card there, so the first
 * social login's account-opening step appears in the same card as the
 * sign-in page, brand and page heading included, while the step itself
 * supplies only the card's content.
 */

import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import type { AuthSession, AuthnMethodList } from '@speed/auth-core'
import { errorCodeOf } from '@speed/ui-kit'
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

export interface SocialCallbackHandlerProps {
  /** The session that resumes the flow. */
  readonly session: AuthSession
  /** Fired once after the login lands -- and again on a later mount that
   * finds the session already authenticated (the re-entry case where no
   * read is started); the host navigates. */
  readonly onSignedIn?: () => void
  /**
   * Rendered when this deployment offers neither a `redirect` nor an
   * `enterprise` method, so nothing ever returns to this route: an invitee
   * who hand-types the URL gets the host's "no such page" instead of a
   * flow-not-found banner. When supplied, the handler reads
   * `session.listMethods()` in that case (and only in that case) to decide;
   * without it the handler behaves exactly as before and reads no
   * discovery document.
   */
  readonly absent?: ReactNode
  /**
   * The host's card around this handler's own content (the pending notice,
   * a step, a refusal, the terminal notice). The host passes the sign-in
   * surface's card -- brand and page heading included -- so the
   * account-opening step a first social login parks at renders in the same
   * card as the sign-in page. The `absent` view is returned unwrapped: it
   * is the host's own page. Omitted, the content renders bare.
   */
  readonly frame?: (content: ReactNode) => ReactNode
  /**
   * The host's exit to its sign-in surface. When no flow can be resumed --
   * a read that finds none, an answer refused as `authn.flow_invalid`, or a
   * step this route cannot render -- the surface becomes a terminal notice
   * whose one action calls this: the callback route knows neither the method
   * nor the provider that opened the flow, so it cannot start a new one
   * itself, and the host navigates back to the surface that can. Omitted,
   * those states keep the failure banner with its re-read (and the
   * unrenderable step keeps its re-read exit).
   */
  readonly onSignIn?: () => void
}

/** Whether the deployment offers a method whose round trip can return to
 * this callback route. */
function offersCallbackReturn(doc: AuthnMethodList): boolean {
  return doc.methods.some(
    (method) => method.kind === 'redirect' || method.kind === 'enterprise',
  )
}

/** The code a gone flow carries: a read that finds no flow (the binding is
 * gone, or the flow is done or expired), or an answer it refuses. */
const NO_FLOW = 'authn.flow_invalid'

export function SocialCallbackHandler({
  session,
  onSignedIn,
  absent,
  frame,
  onSignIn,
}: SocialCallbackHandlerProps) {
  const { t } = useAuthUiTranslation()
  // The handler's one failure slot: a refusal of the read or of an answer
  // settles it, and the shared region names the wait a throttled answer
  // stated and counts it down. A parked step keeps its own surface; its
  // banner is this same slot rendered beneath the step, and the two
  // placements never stand together.
  const feedback = useChannelFeedback()
  // Stable identities, read by the mount effect without re-arming it.
  const { fail: failFeedback, reset: resetFeedback } = feedback
  const [failed, setFailed] = useState(false)
  // The flow turned out to be gone -- or parked on a step this route cannot
  // render -- so there is no answer or step left to retry and the terminal
  // notice takes the surface's place.
  const [flowExpired, setFlowExpired] = useState(false)
  const [absentShown, setAbsentShown] = useState(false)
  const [attempt, setAttempt] = useState(0)

  const driver = useFlowDriver(session, {
    onFailure: (code, error) => {
      if (code === NO_FLOW && onSignIn !== undefined) {
        // The flow is gone: no answer or re-read can change that, so render
        // the terminal notice instead of a banner whose retry would read the
        // same dead record again.
        setFlowExpired(true)
        feedback.reset()
        return
      }
      // A refusal that leaves the flow parked on its step keeps that step:
      // the banner rides beside it (the same slot, rendered below the step)
      // and the step's own controls are the retry. A ready step's result
      // renders nothing and has no control to retry with, and a gone flow
      // has no step at all: without the host's exit those fall back to the
      // surface-wide banner with the retry that reads the flow back.
      const parked = driverRef.current.currentStep()
      if (parked === null || parked.next.kind === 'complete') {
        setFailed(true)
      }
      // The wait a throttled answer stated rides into the banner, which
      // counts it down live (the slot owns the deadline).
      feedback.fail(code, retryWaitMs(error))
    },
    onSettled: () => {
      try {
        onSignedIn?.()
      } catch {
        // A throwing host callback is not a failure of the flow: it
        // settled, so nothing here flips to the failed state -- whose
        // retry would read a flow that is already gone -- and the
        // containment keeps the throw out of this fire-and-forget
        // promise.
      }
    },
  })
  // The driver is read inside the effect through a ref, so the effect
  // depends only on the host's session and the retry counter and does not
  // re-run on every driver re-render.
  const driverRef = useRef(driver)
  driverRef.current = driver
  // The host's exit is read through a ref inside the read effect: a host
  // passing it inline would otherwise re-arm the effect and re-read a flow
  // that is already gone.
  const onSignInRef = useRef(onSignIn)
  onSignInRef.current = onSignIn

  // The pending live region must never mount together with its text: a
  // role="status" region announces only content changes that follow its
  // own insertion, so the pending text born in the same commit as the
  // region -- the initial mount, or a retry's re-entry into the pending
  // branch -- would be silent. The text below is therefore gated on
  // `pendingCommitted`, which lags the pending phase by one commit (the
  // effect after the phase's first commit arms it, leaving the phase
  // disarms it): the region's first committed frame is empty whatever
  // transition entered the phase, and the text fills an existing region
  // a commit later.
  const pending = !failed && driver.step === null
  const [pendingCommitted, setPendingCommitted] = useState(false)
  useEffect(() => {
    setPendingCommitted(pending)
  }, [pending])

  // One read per attempt: the ref survives StrictMode's double effect
  // invocation, so the second run starts nothing.
  const handledRef = useRef<number | null>(null)
  useEffect(() => {
    if (handledRef.current === attempt) {
      return
    }
    handledRef.current = attempt
    void (async () => {
      // A mount that finds the session already authenticated is a re-entry
      // to the callback route after a completed login: start no read and
      // fire onSignedIn again. With an absent slot, first settle whether
      // the deployment offers any method that could have returned here; a
      // discovery read that fails keeps the old answer (fire onSignedIn).
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
          // Contained; see the driver's onSettled above.
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
        // No flow to resume. A host-provided absent view turns this into a
        // question about the deployment: when neither a redirect nor an
        // enterprise method exists, nothing returns to this route and the
        // host's 404 answers. A deployment that does offer one keeps the
        // failure banner (and a discovery read that fails cannot settle it,
        // so the banner stays).
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
          // The read found no flow and no deployment question overrides it:
          // the flow is definitively gone, so the terminal notice replaces
          // the banner whose retry would read the same nothing again.
          setFlowExpired(true)
          return
        }
        // The read found nothing and there is no host exit to offer: the
        // gone flow's banner carries no wait (nothing stated one).
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

  // A step the route parks after readFlow is its first appearance -- the
  // viewer was not working in a control it replaced -- so it moves no
  // focus. A later step, parked by answering the one before it, took the
  // answered step's place and does.
  const surfaceRef = useStepFocus(stepKey(driver.step), {
    focusFirstStep: false,
  })
  // A signup step's refusals are the step's own: they render under the
  // field they name (the username's three, or a password policy on the
  // face that asked for a password) and never as the whole-attempt banner.
  // Every other code, and every other step, keeps the banner.
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

  // The host's card around this handler's own content; the absent view is
  // the host's page and is returned unwrapped.
  const framed = (content: ReactNode): ReactNode =>
    frame === undefined ? content : frame(content)

  if (absentShown && absent !== undefined) {
    return <>{absent}</>
  }

  // A redirect step belongs to the host's navigation: this route can never
  // render it, so with the host's exit it is the same terminal state as a
  // gone flow.
  const unrenderableStep =
    driver.step !== null && driver.step.next.kind === 'redirect'

  if (onSignIn !== undefined && (flowExpired || unrenderableStep)) {
    return (
      <>
        {framed(
          <Box ref={surfaceRef} sx={{ display: 'contents' }}>
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
            <ChannelFeedbackRegion feedback={feedback} />
            <RetryAction label={t('socialCallback.retry')} onClick={retry} />
          </Box>,
        )}
      </>
    )
  }

  // A step the flow parked when it was read back is rendered and answered
  // in place, with the deployment's proof block above it when the step may
  // send; only the pending read shows the notice below. A refused answer
  // that left the flow on this step renders its banner beneath the step,
  // not in place of it, and answering again clears it first.
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
                // A step this route cannot render is the same dead end: with
                // the host's exit it becomes the terminal notice, and without
                // one its own action re-reads the flow.
                onSignIn === undefined
                  ? retry
                  : () => setFlowExpired(true)
              }
              unsupportedLabel={t('socialCallback.retry')}
              failure={failureCode}
            />
            {/* A refused answer that left the flow on this step keeps its
                banner beneath the step; the shared slot counts a stated
                wait down in place. A signup field refusal is the field's
                own and renders under it instead. */}
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
        <Box
          role="status"
          sx={{ display: 'flex', alignItems: 'center', gap: 1.5, width: '100%' }}
        >
          {/* Decorative: the notice on its own announces the pending state
              from the role=status container; naming the spinner too would
              read the notice twice. The text renders only once
              `pendingCommitted` arms it, one commit after this region mounts
              (see the comment on the state above). */}
          <CircularProgress size={18} aria-hidden={true} />
          <Typography>
            {pendingCommitted ? t('socialCallback.pending') : ''}
          </Typography>
        </Box>,
      )}
    </>
  )
}
