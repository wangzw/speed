/**
 * StepUpChallenge: the re-verification dialog of the step-up-gated
 * surfaces (MfaSection opens it when an operation answers 401
 * authn.step_up_required).
 *
 * The dialog is controlled -- {open, session, onSuccess, onCancel} -- and
 * drives one `purpose: 'step-up'` flow on the session
 * (session.startAuthenticatedFlow, session.answerFlow). On opening it
 * starts the flow; the reply's next step is rendered (FlowStep) and each
 * answer advances the flow. An account with a usable factor enters at
 * the `factor` step and chooses one -- a six-digit TOTP or a passkey --
 * where the flow then asks for its proof; an account with none would
 * fall back to the first factor, which this dialog does not reach (its
 * callers gate an action the server already refused for want of a fresh
 * factor). Whatever the step asks for, the dialog validates nothing
 * client-side: the shape of the answer is the flow's business.
 *
 * Completion settles the elevation on the session: a `step-up`
 * completion (state 'elevated' for a token-delivered session, and the
 * login-shaped 'authenticated' body for a cookie-delivered one) has
 * re-minted the access token, and onSuccess fires so the caller re-runs
 * the gated action. Failure answers resolve through the code whitelist
 * and render as one banner; the flow stays open and a fresh answer is
 * retryable. The dialog never signs in and never navigates: a dead
 * session shows its code text and the host's session gate converges.
 *
 * While an answer is in flight every control is disabled, so an in-flight
 * answer cannot be double-submitted or abandoned mid-rotation.
 * Re-opening the dialog resets its state and starts a fresh flow.
 */

import { useEffect, useId, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import Typography from '@mui/material/Typography'
import type { AuthSession, AuthnStep } from '@speed/auth-core'
import { errorCodeOf } from '@speed/ui-kit'
import { InlineError } from './inline-error.js'
import { FlowStep } from './flow-step.js'
import type { FlowStepAnswer } from './flow-step.js'
import { useAccountUiTranslation } from './translation.js'

export interface StepUpChallengeProps {
  /** Whether the dialog is showing. Opening resets its per-attempt state. */
  readonly open: boolean
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
  /** Fired once when the flow settles the elevation. The host closes the dialog. */
  readonly onSuccess: () => void
  /** Fired on cancel while nothing is in flight. The host does not retry. */
  readonly onCancel: () => void
}

/** The dialog's local phase: the flow is starting, is parked on a step,
 * or the last answer failed. */
type Phase = 'starting' | 'step' | 'failed'

export function StepUpChallenge({
  open,
  session,
  onSuccess,
  onCancel,
}: StepUpChallengeProps) {
  const { t } = useAccountUiTranslation()
  const titleId = useId()

  const [phase, setPhase] = useState<Phase>('starting')
  const [step, setStep] = useState<AuthnStep | null>(null)
  const [seq, setSeq] = useState<number | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [errorCode, setErrorCode] = useState<string | null>(null)
  // A started flow is not cancelled on cleanup: StrictMode's
  // mount/effect/cleanup/effect cycle would leave one start, so the
  // first must be allowed to finish. Each opening takes a run token and
  // a resolving start only acts when its token is still current.
  const runRef = useRef(0)

  useEffect(() => {
    if (!open) {
      return
    }
    const run = ++runRef.current
    setPhase('starting')
    setStep(null)
    setSeq(null)
    setSubmitting(false)
    setErrorCode(null)
    void (async () => {
      try {
        const state = await session.startAuthenticatedFlow({
          purpose: 'step-up',
        })
        if (run !== runRef.current) {
          return
        }
        if ('next' in state) {
          setStep(state.next)
          setSeq(state.seq)
          setPhase('step')
          return
        }
        if (state.state === 'elevated' || state.state === 'authenticated') {
          onSuccess()
          return
        }
        // 'linked' / 'enrolled': not a step-up outcome; the flow settled
        // without elevating.
        setErrorCode('client.unknown')
        setPhase('failed')
      } catch (error) {
        if (run === runRef.current) {
          setErrorCode(errorCodeOf(error))
          setPhase('failed')
        }
      }
    })()
  }, [open, session, onSuccess])

  if (!open) {
    return null
  }

  async function handleAnswer(answer: FlowStepAnswer): Promise<void> {
    if (submitting || seq === null) {
      return
    }
    setSubmitting(true)
    setErrorCode(null)
    const run = runRef.current
    try {
      const state = await session.answerFlow({
        seq,
        step: answer.step,
        input: answer.input,
      })
      if (run !== runRef.current) {
        return
      }
      if ('next' in state) {
        setStep(state.next)
        setSeq(state.seq)
        setPhase('step')
        return
      }
      if (state.state === 'elevated' || state.state === 'authenticated') {
        onSuccess()
        return
      }
      // 'linked' / 'enrolled': not a step-up outcome.
      setErrorCode('client.unknown')
      setPhase('failed')
    } catch (error) {
      if (run === runRef.current) {
        setErrorCode(errorCodeOf(error))
        setPhase('failed')
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog
      open={open}
      // Any close request (Escape, backdrop) is a cancel while nothing
      // is in flight; an in-flight answer cannot be abandoned.
      onClose={() => {
        if (!submitting) {
          onCancel()
        }
      }}
      aria-labelledby={titleId}
      maxWidth="xs"
      fullWidth
    >
      <DialogTitle id={titleId}>{t('mfa.stepUp.title')}</DialogTitle>
      <DialogContent>
        <Typography variant="body2" color="text.secondary">
          {t('mfa.stepUp.description')}
        </Typography>
        <InlineError code={errorCode} />
        {phase === 'starting' && (
          <Typography variant="body2" sx={{ mt: 2 }}>
            {t('mfa.stepUp.starting')}
          </Typography>
        )}
        {phase !== 'starting' && (
          <Box sx={{ mt: 1 }}>
            {step !== null ? (
              <FlowStep
                // A fresh step resets the fields: keying the renderer on
                // the step identity clears its per-answer state.
                key={`${seq ?? 0}-${step.kind}`}
                step={step}
                busy={submitting}
                challengeStep="factor"
                submitLabel={t('mfa.stepUp.confirmLabel')}
                onSubmit={(answer) => void handleAnswer(answer)}
              />
            ) : null}
          </Box>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onCancel} disabled={submitting}>
          {t('mfa.stepUp.cancelLabel')}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
