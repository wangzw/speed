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
 * A flow that reaches its ready state answers `next.kind: 'complete'` -- a
 * step that collects nothing and only confirms. The shared hook
 * (useStepUp) answers that step on its own, once per sequence number; the
 * driver shows its starting placeholder while it does, so a proof that
 * already settled the flow does not leave the operator a contextless
 * second confirmation to click. A refused automatic answer parks the flow
 * on its failure, where the step's own confirmation button renders for a
 * manual retry.
 *
 * Why a step-up cannot come is read before the flow starts, from the
 * caller's own factor list (GET /api/v1/auth/factors). An account with no active
 * factor can never raise a single-factor session, so the dialog names the
 * missing two-step verification and starts no flow at all -- a sign-in
 * cannot add a factor. The host supplies its account settings destination
 * through ConsoleShell's `accountHref` (see internal/account-href): the
 * dialog renders it as a link when present and text only when absent, so
 * the package never holds a host path. An account with
 * factors whose start is refused 401 insufficient_user_authentication has
 * factors none of which may strengthen this session, or no password to
 * prove again (the hook's 'unavailable' phase): the dialog says the
 * deployment cannot complete the step-up and offers a re-login -- it ends
 * the session through the session's own logout, and the host's session gate
 * converges on its sign-in surface -- because a new sign-in lets those
 * factors count.
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
import Dialog from '@mui/material/Dialog'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import Typography from '@mui/material/Typography'
import {
  getAuthnMfaFactorsQueryKey,
  useAuthnMfaFactors,
} from '@speed/api-sdk'
import type { AuthSession, StepUpReason } from '@speed/auth-core'
import type { AuthnFlowAnswer, AuthnStep } from '@speed/auth-core'
import { humanGateNeedsProof, useStepUp } from '@speed/auth-core'
import { ActionButton, ActionRow, HumanVerification } from '@speed/ui-kit'
import { useConsoleAccountHref } from './account-href.js'
import { useConsoleReturnPath } from './return-path.js'
import { FlowStep } from './flow-step.js'
import { InlineError } from './inline-error.js'
import { ConsoleLink } from './navigation.js'
import { useConsoleUiTranslation } from './translation.js'

export interface StepUpChallengeProps {
  /** Whether the dialog is showing. Opening starts a fresh flow. */
  readonly open: boolean
  /** What the refusal asked for: the local factor challenge (the default) or a
   * fresh sign-in through the tenant's identity provider ("sso"), which no
   * local factor can satisfy. */
  readonly reason?: StepUpReason | null
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
  /** Fired once when the flow settles the elevation. The host closes the dialog. */
  readonly onSuccess: () => void
  /** Fired on cancel while nothing is in flight. The host does not retry. */
  readonly onCancel: () => void
}

export function StepUpChallenge({
  open,
  reason = 'mfa',
  session,
  onSuccess,
  onCancel,
}: StepUpChallengeProps) {
  const { t } = useConsoleUiTranslation()
  const titleId = useId()
  // The SSO refusal cannot be satisfied by any local factor, so the dialog
  // starts no flow and reads no factor: it offers the sign-in exit alone.
  const sso = reason === 'sso'
  // The host's account settings destination, published by the shell. The
  // package holds no host path, so the no-factor state is text-only when
  // the host supplies none.
  const accountHref = useConsoleAccountHref()
  // The caller's own registered factors (GET /api/v1/auth/factors), read while the
  // dialog is open. An account with no active factor cannot raise its
  // session, so the dialog answers the enrollment instead of starting a flow
  // that cannot come. No factor read, or a failed one, falls back to the
  // flow's own refusal.
  const factors = useAuthnMfaFactors({
    query: { queryKey: getAuthnMfaFactorsQueryKey(), enabled: open && !sso },
  })
  const checking = open && !sso && factors.isPending
  const noFactor =
    open && !sso && factors.isSuccess && activeFactorCount(factors.data) === 0
  // A restart remounts the flow driver: a fresh mount re-drives useStepUp,
  // which has no restart command of its own. The factor read stays put.
  const [run, setRun] = useState(0)
  const restart = useCallback(() => setRun((current) => current + 1), [])

  if (!open) {
    return null
  }

  // The SSO exit: end the session so the host's session gate converges on its
  // sign-in surface, where the operator signs in through the tenant's identity
  // provider. No local factor can clear this refusal.
  const relogin = (): void => {
    onCancel()
    void session.logout().catch(() => undefined)
  }

  return (
    <Dialog open onClose={onCancel} aria-labelledby={titleId} maxWidth="xs" fullWidth>
      <DialogTitle id={titleId}>
        {t(sso ? 'stepUp.ssoTitle' : 'stepUp.title')}
      </DialogTitle>
      {sso ? (
        <>
          <DialogContent>
            <Typography variant="body2" color="text.secondary">
              {t('stepUp.ssoRequired')}
            </Typography>
          </DialogContent>
          <ActionRow sx={{ p: 1, flex: '0 0 auto' }}>
            <ActionButton onClick={onCancel}>{t('common.cancel')}</ActionButton>
            <ActionButton emphasis="primary" onClick={relogin}>
              {t('stepUp.relogin')}
            </ActionButton>
          </ActionRow>
        </>
      ) : noFactor ? (
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
          <ActionRow sx={{ p: 1, flex: '0 0 auto' }}>
            <ActionButton onClick={onCancel}>{t('common.cancel')}</ActionButton>
          </ActionRow>
        </>
      ) : checking ? (
        <>
          <DialogContent>
            <Typography variant="body2" sx={{ mt: 1 }}>
              {t('stepUp.starting')}
            </Typography>
          </DialogContent>
          <ActionRow sx={{ p: 1, flex: '0 0 auto' }}>
            <ActionButton onClick={onCancel}>{t('common.cancel')}</ActionButton>
          </ActionRow>
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
  const returnPath = useConsoleReturnPath()
  const { phase, step, seq, errorCode, retryAfterSeconds, human, answer } =
    useStepUp(session, true, elevated, undefined, returnPath)
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

  // A challenge answered before any second factor is chosen is the first
  // factor, which travels under `answer`: the password the server asks for
  // when every factor was registered during the session. A challenge that
  // follows a chosen factor, and every webauthn step, travels under `factor`.
  const answerStep =
    step !== null && step.kind === 'challenge' && chosenFactor.current === null
      ? 'answer'
      : 'factor'

  const starting = phase === 'starting'
  // The flow reached its ready state and the hook is answering its
  // `complete` step on its own: the starting placeholder covers that answer
  // exactly as it covers the start itself. A refusal leaves this false and
  // lets the step's own confirmation button render for a manual retry.
  const autoCompleting =
    step !== null && step.kind === 'complete' && phase === 'step'
  // This deployment cannot raise the session at all (factors exist, none
  // that may strengthen it, or no password to prove again): no step will
  // come, so the dialog says so and offers a re-login rather than a
  // verification that cannot succeed.
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
  // A throttled answer names how long the caller must hold off: the banner
  // renders the code's own wait-free sentence once, the remaining seconds
  // tick outside its live region, and the primary action stays disabled
  // until the wait elapses.
  const waiting = retryAfterSeconds !== null

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
          <InlineError
            code={errorCode}
            retryAfterSeconds={retryAfterSeconds ?? undefined}
          />
        ) : unavailable ? (
          <Typography variant="body2" color="text.secondary">
            {t('stepUp.cannotElevate')}
          </Typography>
        ) : (
          <>
            <Typography variant="body2" color="text.secondary">
              {t('stepUp.description')}
            </Typography>
            {serverFailure ? (
              <InlineError
                code={errorCode}
                retryAfterSeconds={retryAfterSeconds ?? undefined}
              />
            ) : null}
            {starting || autoCompleting ? (
              <Typography variant="body2" sx={{ mt: 2 }}>
                {t('stepUp.starting')}
              </Typography>
            ) : (
              <Box sx={{ mt: 1 }}>
                {step !== null ? (
                  <>
                    {humanGateNeedsProof(human.gate) && stepMaySend(step) ? (
                      <HumanVerification
                        gate={human.gate ?? undefined}
                        proof={human.proof}
                        status={human.status}
                        onToken={human.adoptToken}
                        onRetry={human.retry}
                      />
                    ) : null}
                    <FlowStep
                      // A fresh step resets the fields: keying the renderer on
                      // the step identity clears its per-answer state.
                      key={`${seq ?? 0}-${step.kind}`}
                      step={step}
                      sequence={seq ?? 0}
                      answerStep={answerStep}
                      submitLabel={t('stepUp.submit')}
                      onSubmit={handleAnswer}
                      submitDisabled={waiting}
                      ceremonyError={ceremonyError}
                      onCeremonyError={setCeremonyError}
                    />
                  </>
                ) : null}
              </Box>
            )}
          </>
        )}
      </DialogContent>
      <ActionRow sx={{ p: 1, flex: '0 0 auto' }}>
        <ActionButton onClick={onCancel}>{t('common.cancel')}</ActionButton>
        {terminalFailure ? (
          <ActionButton
            emphasis="primary"
            disabled={waiting}
            onClick={onRestart}
          >
            {t('stepUp.restart')}
          </ActionButton>
        ) : unavailable ? (
          <ActionButton emphasis="primary" onClick={handleRelogin}>
            {t('stepUp.relogin')}
          </ActionButton>
        ) : null}
      </ActionRow>
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

/** Whether a step-up step could send a verification message: an SMS second
 * factor to choose, or a first-factor re-proof that asks for the phone. The
 * proof block renders above such a step. */
function stepMaySend(step: AuthnStep): boolean {
  if (step.kind === 'factor') {
    return step.factors.some((factor) => factor.kind === 'sms')
  }
  if (step.kind === 'challenge') {
    return step.fields.some((field) => field.kind === 'phone')
  }
  return false
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
