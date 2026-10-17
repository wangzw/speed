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
 * open and a fresh answer is retryable.
 *
 * Why a step-up cannot come is read before the flow starts, from the
 * caller's own factor list (GET /auth/factors). An account with no active
 * factor can never raise a single-factor session, so the dialog names the
 * missing two-step verification and starts no flow at all -- a sign-in
 * cannot add a factor. The host supplies its account settings destination
 * through ConsoleShell's `accountHref` (see internal/account-href): the
 * dialog renders it as a link when present and text only when absent, so
 * the package never holds a host path. An account with
 * factors whose start is refused 401 insufficient_user_authentication has
 * factors none of which may strengthen this session (the hook's
 * 'unavailable' phase): the dialog says the deployment cannot complete the
 * step-up and offers a re-login -- it ends the session through the
 * session's own logout, and the host's session gate converges on its
 * sign-in surface -- because a new sign-in lets those factors count. Any
 * other start failure, a 400 included, is an ordinary error rendered as
 * its code. The dialog never signs in and never navigates on its own: a
 * dead session shows its code text and the console's session gate
 * converges.
 */

import { useId, useRef } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import Typography from '@mui/material/Typography'
import {
  getAuthnMfaFactorsQueryKey,
  useAuthnMfaFactors,
} from '@speed/api-sdk'
import type { AuthSession } from '@speed/auth-core'
import type { AuthnFlowAnswer } from '@speed/auth-core'
import { useStepUp } from '@speed/auth-core'
import { useConsoleAccountHref } from './account-href.js'
import { FlowStep } from './flow-step.js'
import { InlineError } from './inline-error.js'
import { ConsoleLink } from './navigation.js'
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
  // The host's account settings destination, published by the shell. The
  // package holds no host path, so the no-factor state is text-only when
  // the host supplies none.
  const accountHref = useConsoleAccountHref()
  // The caller's own registered factors (GET /auth/factors), read while the
  // dialog is open. An account with no active factor cannot raise its
  // session, so the dialog answers the enrollment instead of starting a flow
  // that cannot come. No factor read, or a failed one, falls back to the
  // flow's own refusal.
  const factors = useAuthnMfaFactors({
    query: { queryKey: getAuthnMfaFactorsQueryKey(), enabled: open },
  })
  const checking = open && factors.isPending
  const noFactor = open && factors.isSuccess && activeFactorCount(factors.data) === 0
  const { phase, step, seq, errorCode, answer } = useStepUp(
    session,
    open && !checking && !noFactor,
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

  const starting = phase === 'starting' || checking
  // This deployment cannot raise the session at all (factors exist, none
  // that may strengthen it): no step will come, so the dialog says so and
  // offers a re-login rather than a verification that cannot succeed.
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
        {noFactor ? (
          <>
            <Typography variant="body2" color="text.secondary">
              {t('stepUp.noFactor')}
            </Typography>
            {accountHref === undefined ? null : (
              <Typography variant="body2" sx={{ mt: 1 }}>
                <ConsoleLink href={accountHref}>{t('stepUp.goToAccount')}</ConsoleLink>
              </Typography>
            )}
          </>
        ) : unavailable ? (
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
                    answerStep="factor"
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

/** The factors a factor read names as active, which are the ones a step-up
 * could use. A read that has not settled yet, or one that failed, holds
 * none and leaves the decision to the flow's own refusal. */
function activeFactorCount(
  page: { readonly factors: readonly { readonly status: string }[] } | undefined,
): number {
  if (page === undefined) {
    return 0
  }
  return page.factors.filter((factor) => factor.status === 'active').length
}
