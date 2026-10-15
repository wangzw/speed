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
 * gone, the flow is done or expired -- renders the failure banner with a
 * retry button that reads once more. Nothing here navigates: the callback
 * route is the host's own and the success callback is the host's.
 *
 * A mount that finds the session already authenticated is a re-entry to
 * the callback route after a completed login; no second read is started
 * and the handler fires onSignedIn again so the host bounces the viewer
 * onward. A throwing onSignedIn is contained: it is not a failure of the
 * flow (which settled), so it never flips the handler to the failed state
 * -- whose retry would read a flow that is already gone -- and never
 * escapes as an unhandled rejection.
 */

import { useEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import type { AuthSession } from '@speed/auth-core'
import { errorCodeOf } from '@speed/ui-kit'
import { useAuthUiTranslation } from './internal/translation.js'
import { InlineError } from './internal/inline-error.js'
import { FlowStepFields } from './internal/flow-step.js'
import { useFlowDriver } from './internal/use-flow-driver.js'

export interface SocialCallbackHandlerProps {
  /** The session that resumes the flow. */
  readonly session: AuthSession
  /** Fired once after the login lands -- and again on a later mount that
   * finds the session already authenticated (the re-entry case where no
   * read is started); the host navigates. */
  readonly onSignedIn?: () => void
}

/** The code a read that finds no flow renders (the binding is gone, or
 * the flow is done or expired). */
const NO_FLOW = 'authn.flow_invalid'

export function SocialCallbackHandler({
  session,
  onSignedIn,
}: SocialCallbackHandlerProps) {
  const { t } = useAuthUiTranslation()
  const [errorCode, setErrorCode] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  const [attempt, setAttempt] = useState(0)

  const driver = useFlowDriver(session, {
    onFailure: (code) => {
      setErrorCode(code)
      setFailed(true)
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
      // fire onSignedIn again.
      if (session.getSnapshot().state === 'authenticated') {
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
        setErrorCode(errorCodeOf(error))
        setFailed(true)
        return
      }
      if (reply === null) {
        setErrorCode(NO_FLOW)
        setFailed(true)
        return
      }
      await driverRef.current.absorb(reply)
    })()
  }, [attempt, session, onSignedIn])

  const retry = (): void => {
    setErrorCode(null)
    setFailed(false)
    setAttempt((value) => value + 1)
  }

  if (failed) {
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
          {t('socialCallback.retry')}
        </Button>
      </Box>
    )
  }

  // A step the flow parked when it was read back is rendered and answered
  // in place; only the pending read shows the notice below.
  if (driver.step !== null) {
    return (
      <FlowStepFields
        step={driver.step}
        busy={driver.busy}
        onAnswer={(input) => void driver.answer(input)}
      />
    )
  }

  return (
    <Box
      role="status"
      sx={{ display: 'flex', alignItems: 'center', gap: 1.5, width: '100%' }}
    >
      {/* Decorative: the notice on its own announces the pending state from
          the role=status container; naming the spinner too would read the
          notice twice. The text renders only once `pendingCommitted` arms
          it, one commit after this region mounts (see the comment on the
          state above). */}
      <CircularProgress size={18} aria-hidden={true} />
      <Typography>
        {pendingCommitted ? t('socialCallback.pending') : ''}
      </Typography>
    </Box>
  )
}
