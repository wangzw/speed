/**
 * StepUpChallenge: the re-authentication dialog of the step-up-gated
 * console writes.
 *
 * A console write whose route asks for a stronger authentication than the
 * session proves is refused 401 while the session stays established
 * (@speed/auth-core's session.isStepUpShortfall). The surface that meets
 * that answer opens this dialog with the refused write remembered; a won
 * challenge fires `onSuccess` so the caller re-runs exactly that write,
 * and a cancel fires `onCancel` so it re-runs nothing.
 *
 * The dialog is controlled -- {open, session, onSuccess, onCancel} -- and
 * drives one `purpose: 'step-up'` flow through @speed/auth-core's
 * useStepUp hook, which owns the session operation. The hook starts the
 * flow on opening and parks on its first step; this dialog renders that
 * step (FlowStep) and forwards each answer. Completion settles the
 * elevation on the session -- the cookie rotated, or an elevated token --
 * and the hook fires `onSuccess`. A failure of an answer resolves through
 * the console's code whitelist and renders as one banner; the flow stays
 * open and a fresh answer is retryable. A refusal of the start itself (the
 * hook's 'unavailable' phase -- a 401 insufficient_user_authentication, or
 * a 400 for a deployment with no factor to raise the session) means this
 * deployment cannot complete the step-up at all: the dialog says so and
 * offers a re-login -- it ends the session through the session's own
 * logout, and the host's session gate converges on its sign-in surface --
 * instead of a verification that cannot come. The dialog never signs in
 * and never navigates on its own: a dead session shows its code text and
 * the console's session gate converges.
 */

import { useId, useRef } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import Typography from '@mui/material/Typography'
import type { AuthSession } from '@speed/auth-core'
import type { AuthnFlowAnswer } from '@speed/auth-core'
import { useStepUp } from '@speed/auth-core'
import { FlowStep } from './flow-step.js'
import { InlineError } from './inline-error.js'
import { useConsoleUiTranslation } from './translation.js'

export interface StepUpChallengeProps {
  /** Whether the dialog is showing. Opening starts a fresh flow. */
  readonly open: boolean
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
  /** Fired once when the flow settles the elevation. The host closes the dialog. */
  readonly onSuccess: () => void
  /** Fired on cancel while nothing is in flight. The host does not retry. */
  readonly onCancel: () => void
}

export function StepUpChallenge({
  open,
  session,
  onSuccess,
  onCancel,
}: StepUpChallengeProps) {
  const { t } = useConsoleUiTranslation()
  const titleId = useId()
  const { phase, step, seq, errorCode, answer } = useStepUp(
    session,
    open,
    onSuccess,
  )
  // The second factor the operator chose. The verification step that
  // follows it (the code challenge) is answered under the `factor` step
  // and must re-name the same factor alongside the code -- a challenge
  // reply carries only the field the factor asks for, and the server
  // re-reads the factor from the answer.
  const chosenFactor = useRef<string | null>(null)

  function handleAnswer(value: AuthnFlowAnswer): void {
    if (!open) {
      return
    }
    const input = value.input ?? {}
    if (input.factor !== undefined) {
      // The factor-choice step: remember the chosen factor and forward.
      chosenFactor.current = input.factor
      answer(value)
      return
    }
    if (value.step === 'factor' && chosenFactor.current !== null) {
      // A `factor`-step challenge (the second factor's code): attach the
      // chosen factor's identifier the server re-reads.
      answer({ ...value, input: { ...input, factor: chosenFactor.current } })
      return
    }
    answer(value)
  }

  if (!open) {
    return null
  }

  const starting = phase === 'starting'
  // This deployment cannot raise the session at all (no factor that may
  // strengthen it): no step will come, so the dialog says so and offers a
  // re-login rather than a verification that cannot succeed.
  const unavailable = phase === 'unavailable'

  // The unavailable phase's one action: end the session -- forgetting the
  // refused write, as a cancel does -- so the host's session gate converges
  // on its sign-in surface, where the operator signs in again for a stronger
  // verification. The session clears its local state even if the revoke is
  // refused, so a failure here needs no retry.
  function handleRelogin(): void {
    onCancel()
    void session.logout().catch(() => undefined)
  }

  return (
    <Dialog open={open} onClose={onCancel} aria-labelledby={titleId} maxWidth="xs" fullWidth>
      <DialogTitle id={titleId}>{t('stepUp.title')}</DialogTitle>
      <DialogContent>
        {unavailable ? (
          <Typography variant="body2" color="text.secondary">
            {t('stepUp.cannotElevate')}
          </Typography>
        ) : (
          <>
            <Typography variant="body2" color="text.secondary">
              {t('stepUp.description')}
            </Typography>
            <InlineError code={errorCode} />
            {starting ? (
              <Typography variant="body2" sx={{ mt: 2 }}>
                {t('stepUp.starting')}
              </Typography>
            ) : (
              <Box sx={{ mt: 1 }}>
                {step !== null ? (
                  <FlowStep
                    // A fresh step resets the fields: keying the renderer on
                    // the step identity clears its per-answer state.
                    key={`${seq ?? 0}-${step.kind}`}
                    step={step}
                    challengeStep="factor"
                    submitLabel={t('stepUp.submit')}
                    onSubmit={handleAnswer}
                  />
                ) : null}
              </Box>
            )}
          </>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onCancel}>{t('common.cancel')}</Button>
        {unavailable ? (
          <Button variant="contained" onClick={handleRelogin}>
            {t('stepUp.relogin')}
          </Button>
        ) : null}
      </DialogActions>
    </Dialog>
  )
}
