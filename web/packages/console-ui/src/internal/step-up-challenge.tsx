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
 * open and a fresh answer is retryable. A refusal of the start itself (a
 * 401 while the session is still established -- the hook's 'unavailable'
 * phase) means the deployment has no factor that can raise the session:
 * the dialog says so and points the operator at re-establishing the
 * session, instead of a retry that cannot succeed. The dialog never signs
 * in and never navigates: a dead session shows its code text and the
 * console's session gate converges.
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
  // The deployment cannot raise the session at all (no factor that may
  // strengthen it): no step will come, so the dialog tells the operator to
  // re-establish the session rather than offer a retry that cannot succeed.
  const unavailable = phase === 'unavailable'

  return (
    <Dialog open={open} onClose={onCancel} aria-labelledby={titleId} maxWidth="xs" fullWidth>
      <DialogTitle id={titleId}>{t('stepUp.title')}</DialogTitle>
      <DialogContent>
        <Typography variant="body2" color="text.secondary">
          {t('stepUp.description')}
        </Typography>
        <InlineError code={errorCode} />
        {starting ? (
          <Typography variant="body2" sx={{ mt: 2 }}>
            {t('stepUp.starting')}
          </Typography>
        ) : unavailable ? (
          <Typography variant="body2" sx={{ mt: 2 }}>
            {t('stepUp.cannotElevate')}
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
      </DialogContent>
      <DialogActions>
        <Button onClick={onCancel}>{t('common.cancel')}</Button>
      </DialogActions>
    </Dialog>
  )
}
