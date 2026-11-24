/**
 * StepUpChallenge: the re-authentication dialog of the step-up-gated writes; it
 * drives one auth-core flow and resolves every refusal into one banner.
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
import type { AuthnFlowAnswer } from '@speed/auth-core'
import { useStepUp } from '@speed/auth-core'
import { ActionButton, ActionRow, HumanVerification } from '@speed/ui-kit'
import { useAdminAccountHref } from './account-href.js'
import { useAdminReturnPath } from './return-path.js'
import { FlowStep } from './flow-step.js'
import { InlineError } from './inline-error.js'
import { AdminLink } from './navigation.js'
import { useAdminUiTranslation } from './translation.js'

const DIALOG_BODY_SX = {
  display: 'flex',
  flexDirection: 'column',
  gap: 2,
} as const

const DIALOG_ACTIONS_SX = {
  px: 3,
  py: 2,
  flex: '0 0 auto',
} as const

export interface StepUpChallengeProps {
  /** Whether the dialog is showing. Opening starts a fresh flow. */
  readonly open: boolean
  /**
   * What the refusal asked for: a local factor challenge (the default) or a
   * fresh sign-in through the tenant's identity provider ("sso").
   */
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
  const { t } = useAdminUiTranslation()
  const titleId = useId()
  // The SSO refusal takes no local factor: no flow, no factor read.
  const sso = reason === 'sso'
  // The package holds no host path, so the no-factor state is text-only when
  // the host supplies no account settings page.
  const accountHref = useAdminAccountHref()
  // The caller's own factors, read while the dialog is open; no factor, or a
  // failed read, leaves the decision to the flow's own refusal.
  const factors = useAuthnMfaFactors({
    query: { queryKey: getAuthnMfaFactorsQueryKey(), enabled: open && !sso },
  })
  const checking = open && !sso && factors.isPending
  const noFactor =
    open && !sso && factors.isSuccess && activeFactorCount(factors.data) === 0
  // A restart remounts the flow driver: useStepUp has no restart command.
  const [run, setRun] = useState(0)
  const restart = useCallback(() => setRun((current) => current + 1), [])

  if (!open) {
    return null
  }

  // End the session so the host's session gate converges on its sign-in.
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
          <DialogContent sx={DIALOG_BODY_SX}>
            <Typography variant="body2" color="text.secondary">
              {t('stepUp.ssoRequired')}
            </Typography>
          </DialogContent>
          <ActionRow sx={DIALOG_ACTIONS_SX}>
            <ActionButton onClick={onCancel}>{t('common.cancel')}</ActionButton>
            <ActionButton emphasis="primary" onClick={relogin}>
              {t('stepUp.relogin')}
            </ActionButton>
          </ActionRow>
        </>
      ) : noFactor ? (
        <>
          <DialogContent sx={DIALOG_BODY_SX}>
            <Typography variant="body2" color="text.secondary">
              {t('stepUp.noFactor')}
            </Typography>
            {accountHref === undefined ? null : (
              <Typography variant="body2">
                <AdminLink href={accountHref}>{t('stepUp.goToAccount')}</AdminLink>
              </Typography>
            )}
          </DialogContent>
          <ActionRow sx={DIALOG_ACTIONS_SX}>
            <ActionButton onClick={onCancel}>{t('common.cancel')}</ActionButton>
          </ActionRow>
        </>
      ) : checking ? (
        <>
          <DialogContent sx={DIALOG_BODY_SX}>
            <Typography variant="body2">
              {t('stepUp.starting')}
            </Typography>
          </DialogContent>
          <ActionRow sx={DIALOG_ACTIONS_SX}>
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

function StepUpFlow({
  session,
  onSuccess,
  onCancel,
  onRestart,
}: StepUpFlowProps) {
  const { t } = useAdminUiTranslation()
  // The failure banner's text id, described by controls the wait holds.
  const waitTextId = useId()
  // A stale hook reply must not replay a write the operator cancelled: an
  // elevation reaches the caller only while this driver is mounted.
  const mounted = useRef(true)
  const elevated = useCallback(() => {
    if (mounted.current) {
      onSuccess()
    }
  }, [onSuccess])
  const returnPath = useAdminReturnPath()
  const {
    phase,
    step,
    seq,
    submitting,
    pending,
    errorCode,
    retryAfterSeconds,
    human,
    humanScope,
    answer,
  } = useStepUp(session, true, elevated, undefined, returnPath)
  // The chosen second factor. The following code challenge answers under
  // `factor` and must re-name it, because a challenge reply carries only the
  // asked field.
  const chosenFactor = useRef<string | null>(null)
  // The passkey failure, held here so one banner stands at a time.
  const [ceremonyError, setCeremonyError] = useState<string | null>(null)

  // Close or restart unmounts this driver; releasing the flow is the shared
  // hook's job. This driver only drops a reply that outlives it.
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
      // The second factor's code: attach the chosen factor the server re-reads.
      answer({ ...value, input: { ...input, factor: chosenFactor.current } })
      return
    }
    answer(value)
  }

  // A challenge before any second factor is the first factor (`answer`); a
  // challenge after one, and every webauthn step, travels under `factor`.
  const answerStep =
    step !== null && step.kind === 'challenge' && chosenFactor.current === null
      ? 'answer'
      : 'factor'

  const starting = phase === 'starting'
  // The hook is answering the ready `complete` step on its own; a refusal
  // leaves this false so the step's own confirmation renders for a retry.
  const autoCompleting =
    step !== null && step.kind === 'complete' && phase === 'step'
  // This deployment cannot raise the session at all, so offer a re-login.
  const unavailable = phase === 'unavailable'
  const failed = phase === 'failed'
  // A start refusal leaves no step behind; an answer refusal keeps its step.
  const startRefused = failed && step === null && !ordinaryStartRefusal(session, errorCode)
  // The server no longer holds the flow: only a fresh start can retry.
  const flowInvalid = failed && step !== null && errorCode === 'authn.flow_invalid'
  const terminalFailure = startRefused || flowInvalid
  // One banner at a time: a ceremony failure hides the server banner.
  const serverFailure = errorCode !== null && ceremonyError === null
  // A throttled answer holds the primary action until its wait elapses.
  const waiting = retryAfterSeconds !== null

  // End the session -- forgetting the refused write -- so the host's gate
  // converges on its sign-in; the local state clears even if the revoke fails.
  function handleRelogin(): void {
    onCancel()
    void session.logout().catch(() => undefined)
  }

  return (
    <>
      <DialogContent sx={DIALOG_BODY_SX}>
        {terminalFailure ? (
          // No description and no step: the code's banner and restart action
          // are the whole surface.
          <Box id={waitTextId}>
            <InlineError
              code={errorCode}
              retryAfterSeconds={retryAfterSeconds ?? undefined}
            />
          </Box>
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
              <Box id={waitTextId}>
                <InlineError
                  code={errorCode}
                  retryAfterSeconds={retryAfterSeconds ?? undefined}
                />
              </Box>
            ) : null}
            {starting || autoCompleting ? (
              <Typography variant="body2">
                {t('stepUp.starting')}
              </Typography>
            ) : (
              <Box>
                {step !== null ? (
                  <>
                    {pending ? (
                      <HumanVerification
                        scope={humanScope}
                        gate={human.gate ?? undefined}
                        proof={human.proof}
                        status={human.status}
                        captcha={human.captcha}
                        onToken={human.adoptToken}
                        onRetry={human.retry}
                        onRefreshCaptcha={human.refreshCaptcha}
                        onRequestAudio={human.requestAudio}
                        audioRetryAfterSeconds={human.audioRetryAfterSeconds}
                      />
                    ) : null}
                    <FlowStep
                      // A fresh step resets the fields: key on the step identity.
                      key={`${seq ?? 0}-${step.kind}`}
                      step={step}
                      sequence={seq ?? 0}
                      answerStep={answerStep}
                      submitLabel={t('stepUp.submit')}
                      onSubmit={handleAnswer}
                      busy={submitting || pending}
                      unavailable={waiting}
                      unavailableDescribedBy={waiting ? waitTextId : undefined}
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
      <ActionRow sx={DIALOG_ACTIONS_SX}>
        <ActionButton onClick={onCancel}>{t('common.cancel')}</ActionButton>
        {terminalFailure ? (
          <ActionButton
            emphasis="primary"
            unavailable={waiting}
            unavailableDescribedBy={waiting ? waitTextId : undefined}
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
 * Whether a failed start is the 400 a still-established session gets for an
 * unusable start request; such a refusal offers no restart.
 */
function ordinaryStartRefusal(
  session: AuthSession,
  errorCode: string | null,
): boolean {
  return errorCode === 'authn.bad_request' && session.getSnapshot().state === 'authenticated'
}

/** The factors a read names as active; an unsettled or failed read holds none. */
function activeFactorCount(
  page: { readonly factors: readonly { readonly status: string }[] } | undefined,
): number {
  if (page === undefined) {
    return 0
  }
  return page.factors.filter((factor) => factor.status === 'active').length
}
