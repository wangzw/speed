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
 * flow on opening and parks on its first step; the driver renders that
 * step (FlowStep) and forwards each answer. Completion settles the
 * elevation on the session -- the cookie rotated, or an elevated token --
 * and the hook fires `onSuccess`.
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
 * sign-in surface -- because a new sign-in lets those factors count.
 *
 * The failure surface. Every code an answer can be refused with resolves
 * through the console's whitelist and renders as one banner, so a wrong
 * code shows its own text instead of the generic fallback. A start that
 * fails for any reason other than the unraisable-session refusal above --
 * or the 400 a still-established session is answered for a malformed start
 * -- and an answer the server rejects with `authn.flow_invalid` (the flow
 * expired, was consumed, or has nothing left to answer) render the code's
 * banner with a restart action and a cancel, and neither the description
 * nor a step form: there is no step to answer. The restart is local to the
 * dialog -- it remounts the flow driver, which drives a fresh useStepUp
 * run; auth-core is unchanged. A 400 answered while the session still
 * stands is the start request itself being unusable, so it renders its
 * code as an ordinary error and offers no restart.
 *
 * A failed passkey ceremony is the dialog's second failure source: its
 * text is a bundle key, not an API code. The driver holds it so that the
 * ceremony banner and the server-side banner never stand together -- the
 * newer failure replaces the other, and starting an answer clears the
 * ceremony banner. The dialog carries its own region; the failure banner
 * renders inside the modal.
 */

import { useCallback, useEffect, useId, useRef, useState } from 'react'
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
  // A restart remounts the flow driver: a fresh mount re-drives useStepUp,
  // which has no restart command of its own. The factor read stays put.
  const [run, setRun] = useState(0)
  const restart = useCallback(() => setRun((current) => current + 1), [])

  if (!open) {
    return null
  }

  return (
    <Dialog open onClose={onCancel} aria-labelledby={titleId} maxWidth="xs" fullWidth>
      <DialogTitle id={titleId}>{t('stepUp.title')}</DialogTitle>
      {noFactor ? (
        <>
          <DialogContent>
            <Typography variant="body2" color="text.secondary">
              {t('stepUp.noFactor')}
            </Typography>
            {accountHref === undefined ? null : (
              <Typography variant="body2" sx={{ mt: 1 }}>
                <ConsoleLink href={accountHref}>{t('stepUp.goToAccount')}</ConsoleLink>
              </Typography>
            )}
          </DialogContent>
          <DialogActions>
            <Button onClick={onCancel}>{t('common.cancel')}</Button>
          </DialogActions>
        </>
      ) : checking ? (
        <>
          <DialogContent>
            <Typography variant="body2" sx={{ mt: 1 }}>
              {t('stepUp.starting')}
            </Typography>
          </DialogContent>
          <DialogActions>
            <Button onClick={onCancel}>{t('common.cancel')}</Button>
          </DialogActions>
        </>
      ) : (
        <StepUpFlow
          key={run}
          session={session}
          onSuccess={onSuccess}
          onCancel={onCancel}
          onRestart={restart}
        />
      )}
    </Dialog>
  )
}

interface StepUpFlowProps {
  readonly session: AuthSession
  readonly onSuccess: () => void
  readonly onCancel: () => void
  readonly onRestart: () => void
}

/** The flow driver: one useStepUp run, its step form and its two failure
 * sources (a server code and a client-side passkey ceremony). */
function StepUpFlow({
  session,
  onSuccess,
  onCancel,
  onRestart,
}: StepUpFlowProps) {
  const { t } = useConsoleUiTranslation()
  // A hook reply can outlive this driver: closing the dialog unmounts it,
  // and the shared hook only drops a stale reply when its active flag turns
  // false, which an unmount never does. The gate would then replay a write
  // the operator cancelled, so an elevation only reaches the caller while
  // the driver is still mounted.
  const mounted = useRef(true)
  const elevated = useCallback(() => {
    if (mounted.current) {
      onSuccess()
    }
  }, [onSuccess])
  const { phase, step, seq, errorCode, answer } = useStepUp(session, true, elevated)
  // The second factor the operator chose. The verification step that
  // follows it (the code challenge) is answered under the `factor` step
  // and must re-name the same factor alongside the code -- a challenge
  // reply carries only the field the factor asks for, and the server
  // re-reads the factor from the answer.
  const chosenFactor = useRef<string | null>(null)
  // The passkey ceremony's failure, held here rather than in the step
  // component so one banner stands at a time: while it is set it hides the
  // server-side banner, and starting an answer clears it.
  const [ceremonyError, setCeremonyError] = useState<string | null>(null)

  // Closing the dialog or restarting unmounts this driver. Releasing the
  // flow belongs to the shared hook, which abandons a flow it opened and
  // has not seen settle -- a refused start or a settled elevation leaves
  // nothing to release. This driver only drops a reply that outlives it.
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [session])

  function handleAnswer(value: AuthnFlowAnswer): void {
    setCeremonyError(null)
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

  const starting = phase === 'starting'
  // This deployment cannot raise the session at all (factors exist, none
  // that may strengthen it): no step will come, so the dialog says so and
  // offers a re-login rather than a verification that cannot succeed.
  const unavailable = phase === 'unavailable'
  const failed = phase === 'failed'
  // A start refusal leaves no step behind; an answer refusal keeps the
  // step it was answered from.
  const startRefused = failed && step === null && !ordinaryStartRefusal(session, errorCode)
  // The server no longer holds the flow (expired, consumed, nothing left
  // to answer): no step form can be retried, only a fresh start.
  const flowInvalid = failed && step !== null && errorCode === 'authn.flow_invalid'
  const terminalFailure = startRefused || flowInvalid
  // One banner at a time: a later ceremony failure replaces the server
  // banner; an answer attempt has already cleared the ceremony banner, so
  // a server failure is the only source left.
  const serverFailure = errorCode !== null && ceremonyError === null

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
    <>
      <DialogContent>
        {terminalFailure ? (
          // No description and no step: there is nothing to answer, so the
          // code's banner and the restart action are the whole surface.
          <InlineError code={errorCode} />
        ) : unavailable ? (
          <Typography variant="body2" color="text.secondary">
            {t('stepUp.cannotElevate')}
          </Typography>
        ) : (
          <>
            <Typography variant="body2" color="text.secondary">
              {t('stepUp.description')}
            </Typography>
            {serverFailure ? <InlineError code={errorCode} /> : null}
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
                    ceremonyError={ceremonyError}
                    onCeremonyError={setCeremonyError}
                  />
                ) : null}
              </Box>
            )}
          </>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onCancel}>{t('common.cancel')}</Button>
        {terminalFailure ? (
          <Button variant="contained" onClick={onRestart}>
            {t('stepUp.restart')}
          </Button>
        ) : unavailable ? (
          <Button variant="contained" onClick={handleRelogin}>
            {t('stepUp.relogin')}
          </Button>
        ) : null}
      </DialogActions>
    </>
  )
}

/**
 * Whether a failed start is the 400 a still-established session is answered
 * with for an unusable start request. Such a refusal is the request itself,
 * not a transient condition, so the dialog renders its code as an ordinary
 * error and offers no restart.
 */
function ordinaryStartRefusal(
  session: AuthSession,
  errorCode: string | null,
): boolean {
  return errorCode === 'authn.bad_request' && session.getSnapshot().state === 'authenticated'
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
