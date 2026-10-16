/**
 * StepUpChallenge: the re-verification dialog of the step-up-gated
 * account surfaces (the profile and MFA sections open it when an
 * operation answers 401 authn.insufficient_user_authentication).
 *
 * The dialog is controlled -- {open, session, onSuccess, onCancel} -- and
 * drives one `purpose: 'step-up'` flow through @speed/auth-core's
 * useStepUp hook, which owns the session operation. The hook starts the
 * flow on opening and parks on its first step; the dialog renders that
 * step (FlowStep) and forwards each answer. An account with a usable
 * factor enters at the `factor` step and chooses one -- a six-digit TOTP
 * or a passkey -- where the flow then asks for its proof. The chosen
 * factor is remembered and re-named beside the field of the step that
 * follows it: a challenge reply carries only the field the factor asks
 * for, and the server re-reads the factor from the answer. Whatever the
 * step asks for, the dialog validates nothing client-side: the shape of
 * the answer is the flow's business.
 *
 * Completion settles the elevation on the session: a `step-up`
 * completion (state 'elevated' for a token-delivered session, and the
 * login-shaped 'authenticated' body for a cookie-delivered one) has
 * re-minted the access token, and the hook fires onSuccess so the caller
 * re-runs the gated action. A refused answer resolves through the code
 * whitelist and renders as one banner; the flow stays open and a fresh
 * answer is retryable.
 *
 * A refusal of the start itself is the deployment declining to raise this
 * session at all, which no step can satisfy: the hook's 'unavailable'
 * phase -- a 401 insufficient_user_authentication (the account has
 * factors, but none whose methods may strengthen this session) or a 400
 * (the start named no method the deployment could take, where an account
 * with no factor at all lands, since a step-up is started without one).
 * The dialog then says so and offers a re-login -- it ends the session
 * through the session's own logout, and the host's session gate converges
 * on its sign-in surface -- rather than a verification that cannot come.
 *
 * The dialog never signs in and never navigates on its own: a dead
 * session shows its code text and the host's session gate converges.
 */

import { useId, useRef } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import Typography from '@mui/material/Typography'
import type { AuthSession, AuthnFlowAnswer } from '@speed/auth-core'
import { useStepUp } from '@speed/auth-core'
import { InlineError } from './inline-error.js'
import { FlowStep } from './flow-step.js'
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

export function StepUpChallenge({
  open,
  session,
  onSuccess,
  onCancel,
}: StepUpChallengeProps) {
  const { t } = useAccountUiTranslation()
  const titleId = useId()
  const { phase, step, seq, errorCode, answer } = useStepUp(
    session,
    open,
    onSuccess,
  )
  // The second factor the operator chose. The verification step that
  // follows it (the code challenge) is answered under the `factor` step
  // and must re-name the same factor alongside the field it asks for --
  // a challenge reply carries only that field, and the server re-reads the
  // factor from the answer.
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
      // A `factor`-step challenge (the chosen factor's code, or its
      // passkey): attach the factor's identifier the server re-reads.
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
  // refused action, as a cancel does -- so the host's session gate
  // converges on its sign-in surface, where the operator signs in again
  // for a stronger verification. The session clears its local state even
  // if the revoke is refused, so a failure here needs no retry.
  function handleRelogin(): void {
    onCancel()
    void session.logout().catch(() => undefined)
  }

  return (
    <Dialog
      open={open}
      onClose={onCancel}
      aria-labelledby={titleId}
      maxWidth="xs"
      fullWidth
    >
      <DialogTitle id={titleId}>{t('mfa.stepUp.title')}</DialogTitle>
      <DialogContent>
        {unavailable ? (
          <Typography variant="body2" color="text.secondary">
            {t('mfa.stepUp.cannotElevate')}
          </Typography>
        ) : (
          <>
            <Typography variant="body2" color="text.secondary">
              {t('mfa.stepUp.description')}
            </Typography>
            <InlineError code={errorCode} />
            {starting ? (
              <Typography variant="body2" sx={{ mt: 2 }}>
                {t('mfa.stepUp.starting')}
              </Typography>
            ) : (
              <Box sx={{ mt: 1 }}>
                {step !== null ? (
                  <FlowStep
                    // A fresh step resets the fields: keying the renderer on
                    // the step identity clears its per-answer state.
                    key={`${seq ?? 0}-${step.kind}`}
                    step={step}
                    busy={false}
                    answerStep="factor"
                    submitLabel={t('mfa.stepUp.confirmLabel')}
                    onSubmit={handleAnswer}
                  />
                ) : null}
              </Box>
            )}
          </>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onCancel}>{t('mfa.stepUp.cancelLabel')}</Button>
        {unavailable ? (
          <Button variant="contained" onClick={handleRelogin}>
            {t('mfa.stepUp.relogin')}
          </Button>
        ) : null}
      </DialogActions>
    </Dialog>
  )
}
