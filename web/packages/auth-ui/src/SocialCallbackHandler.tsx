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
 *
 * The optional `absent` slot answers a deployment with no callback landing
 * point: when the handler finds no flow (or a re-entry) and the discovery
 * document offers neither a `redirect` nor an `enterprise` method, nothing
 * can return to this route, so the host's "no such page" renders instead
 * of a flow-not-found banner. The discovery document is read only when
 * `absent` is supplied.
 */

import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import type { AuthSession, AuthnMethodList } from '@speed/auth-core'
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
}

/** Whether the deployment offers a method whose round trip can return to
 * this callback route. */
function offersCallbackReturn(doc: AuthnMethodList): boolean {
  return doc.methods.some(
    (method) => method.kind === 'redirect' || method.kind === 'enterprise',
  )
}

/** The code a read that finds no flow renders (the binding is gone, or
 * the flow is done or expired). */
const NO_FLOW = 'authn.flow_invalid'

export function SocialCallbackHandler({
  session,
  onSignedIn,
  absent,
}: SocialCallbackHandlerProps) {
  const { t } = useAuthUiTranslation()
  const [errorCode, setErrorCode] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  const [absentShown, setAbsentShown] = useState(false)
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
        setErrorCode(errorCodeOf(error))
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
        setErrorCode(NO_FLOW)
        setFailed(true)
        return
      }
      await driverRef.current.absorb(reply)
    })()
  }, [absent, attempt, session, onSignedIn])

  const retry = (): void => {
    setErrorCode(null)
    setFailed(false)
    setAttempt((value) => value + 1)
  }

  if (absentShown && absent !== undefined) {
    return <>{absent}</>
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
