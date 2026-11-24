/**
 * StepUpChallenge: the re-verification dialog of the step-up-gated account
 * surfaces, driving one `purpose: 'step-up'` flow.
 */

import {
  forwardRef,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Dialog from '@mui/material/Dialog'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import Link from '@mui/material/Link'
import Typography from '@mui/material/Typography'
import type { AuthnSessionView } from '@speed/api-sdk'
import type {
  AuthSession,
  AuthnFlowAnswer,
  AuthnStep,
  StepUpReason,
} from '@speed/auth-core'
import { useStepUp } from '@speed/auth-core'
import { ActionButton, ActionRow, HumanVerification } from '@speed/ui-kit'
import { InlineError } from './inline-error.js'
import { FlowStep } from './flow-step.js'
import { useFactorsQuery, useSessionViewQuery } from './queries.js'
import { useAccountUiTranslation } from './translation.js'

// Mirrors the server's fixed FreshFirstFactor window (authn.FreshFirstFactor).
const FIRST_FACTOR_WINDOW_MS = 5 * 60 * 1000

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

type RefusalVerdict =
  | { readonly kind: 'multi-factor' }
  | { readonly kind: 'fresh-first-factor'; readonly method: string }
  | { readonly kind: 'unknown-method' }

/**
 * The host supplies the session, the open state and the refusal that asked for
 * it.
 */
export interface StepUpChallengeProps {
  /** Whether the dialog is showing. Opening resets its per-attempt state. */
  readonly open: boolean
  /** What the refusal asked for: the local factor challenge (the default) or
   * a fresh sign-in through the tenant's identity provider ("sso"). */
  readonly reason?: StepUpReason | null
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
  /** Fired once when the flow settles the elevation. */
  readonly onSuccess: () => void
  /** Fired on cancel while nothing is in flight. */
  readonly onCancel: () => void
  /** The host's account-settings route, rendered as a link beside the
   * "register two-step verification first" explanation. */
  readonly accountHref?: string
  /** The surface that opened the challenge (the host's current path); a
   * redirect method's round trip lands back here. */
  readonly returnPath?: string
  /** Fired once when the operator chooses the re-login exit, before the
   * session ends. The caller writes its resume record here. */
  readonly onRelogin?: () => void
  /** The factor the caller is about to delete. The flow must not offer it. */
  readonly excludeFactorId?: string
  /** A sentence stated above the controls, already translated by the caller. */
  readonly notice?: string
}

export const StepUpChallengeFrame = forwardRef<
  HTMLDivElement,
  StepUpChallengeProps
>(function StepUpChallengeFrame(
  {
    open,
    reason = 'mfa',
    session,
    onSuccess,
    onCancel,
    accountHref,
    returnPath,
    onRelogin,
    excludeFactorId,
    notice,
  },
  ref,
) {
  const { t } = useAccountUiTranslation()
  const titleId = useId()
  const sso = reason === 'sso'
  const factors = useFactorsQuery(open && !sso)
  const checking = open && !sso && factors.isPending
  const noFactor =
    open && !sso && factors.isSuccess && activeFactorCount(factors.data) === 0
  // Disabled on purpose: the verdict comes from the read this opening forces
  // through `refetch`; an enabled cached query would let `refetch`'s
  // cancelRefetch start a second request for the same opening.
  const { refetch: readSessionView } = useSessionViewQuery(false)
  const [verdict, setVerdict] = useState<RefusalVerdict | null>(null)
  const [previousOpen, setPreviousOpen] = useState(open)
  if (open !== previousOpen) {
    setPreviousOpen(open)
    setVerdict(null)
  }
  // A read that fails names no method.
  useEffect(() => {
    if (!open || !noFactor || verdict !== null) {
      return
    }
    let cancelled = false
    void readSessionView().then((result) => {
      if (cancelled) {
        return
      }
      const view = result.data
      setVerdict(
        result.isError || view === undefined
          ? { kind: 'unknown-method' }
          : decideRefusal(view),
      )
    })
    return () => {
      cancelled = true
    }
  }, [open, noFactor, verdict, readSessionView])
  const awaitingVerdict = open && noFactor && verdict === null
  const readingSession = awaitingVerdict
  const freshFirst = open && noFactor && verdict?.kind === 'multi-factor'
  const reproofMethod =
    open && noFactor && verdict?.kind === 'fresh-first-factor'
      ? verdict.method
      : undefined
  const reproveFirst = reproofMethod !== undefined
  const reloginOnly =
    open && noFactor && verdict?.kind === 'unknown-method'
  const startFlow =
    open && !checking && !awaitingVerdict && (!noFactor || reproveFirst)
  // A restart remounts the flow driver: useStepUp has no restart of its own.
  const [run, setRun] = useState(0)

  if (!open) {
    return null
  }

  // The caller records the operation first, so the new sign-in resumes it.
  function handleRelogin(): void {
    onRelogin?.()
    onCancel()
    void session.logout().catch(() => undefined)
  }

  return (
    <Dialog
      ref={ref}
      open={open}
      onClose={onCancel}
      aria-labelledby={titleId}
      maxWidth="xs"
      fullWidth
    >
      {sso ? (
        <>
          <DialogTitle id={titleId}>{t('mfa.stepUp.ssoTitle')}</DialogTitle>
          <DialogContent sx={DIALOG_BODY_SX}>
            <Typography variant="body2" color="text.secondary">
              {t('mfa.stepUp.ssoRequired')}
            </Typography>
          </DialogContent>
          <ActionRow sx={DIALOG_ACTIONS_SX}>
            <ActionButton onClick={onCancel}>
              {t('mfa.stepUp.cancelLabel')}
            </ActionButton>
            <ActionButton emphasis="primary" onClick={handleRelogin}>
              {t('mfa.stepUp.relogin')}
            </ActionButton>
          </ActionRow>
        </>
      ) : (
        <StepUpFlow
          key={run}
          session={session}
          active={startFlow}
          titleId={titleId}
          onSuccess={onSuccess}
          onCancel={onCancel}
          onRestart={() => setRun((count) => count + 1)}
          accountHref={accountHref}
          returnPath={returnPath}
          onRelogin={onRelogin}
          freshFirst={freshFirst}
          reloginOnly={reloginOnly}
          checking={checking}
          readingSession={readingSession}
          reproveFirst={reproveFirst}
          reproofMethod={reproofMethod}
          excludeFactorId={excludeFactorId}
          notice={notice}
        />
      )}
    </Dialog>
  )
})

function StepUpFlow({
  session,
  active,
  titleId,
  onSuccess,
  onCancel,
  onRestart,
  accountHref,
  returnPath,
  onRelogin,
  freshFirst,
  reloginOnly,
  checking,
  readingSession,
  reproveFirst,
  reproofMethod,
  excludeFactorId,
  notice,
}: {
  readonly session: AuthSession
  readonly active: boolean
  readonly titleId: string
  readonly onSuccess: () => void
  readonly onCancel: () => void
  readonly onRestart: () => void
  readonly accountHref?: string
  readonly returnPath?: string
  readonly onRelogin?: () => void
  readonly freshFirst: boolean
  readonly reloginOnly: boolean
  readonly checking: boolean
  readonly readingSession: boolean
  readonly reproveFirst: boolean
  readonly reproofMethod?: string
  readonly excludeFactorId?: string
  readonly notice?: string
}): ReactElement {
  const { t } = useAccountUiTranslation()
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
  } =
    useStepUp(
      session,
      active,
      onSuccess,
      reproveFirst ? reproofMethod : undefined,
      returnPath,
      excludeFactorId,
    )
  // A challenge reply carries only the field the factor asks for, so the chosen
  // factor is re-named beside the following step's field.
  const chosenFactor = useRef<string | null>(null)
  // Focus follows to the title when a settled answer replaces the control that
  // had it; an operator who moved focus elsewhere keeps it.
  const titleRef = useRef<HTMLHeadingElement>(null)
  const previousSeq = useRef(seq)
  useLayoutEffect(() => {
    if (previousSeq.current === seq) {
      return
    }
    const replacing = previousSeq.current !== null
    previousSeq.current = seq
    if (!replacing) {
      return
    }
    const active = document.activeElement
    const fell =
      active === null ||
      active === document.body ||
      !(active instanceof HTMLElement) ||
      !active.isConnected
    if (fell) {
      titleRef.current?.focus()
    }
  }, [seq])
  const throttleNoteId = useId()

  function handleAnswer(value: AuthnFlowAnswer): void {
    const input = value.input ?? {}
    if (input.factor !== undefined) {
      chosenFactor.current = input.factor
      answer(value)
      return
    }
    if (value.step === 'factor' && chosenFactor.current !== null) {
      // Attach the chosen factor's identifier, which the server re-reads.
      answer({ ...value, input: { ...input, factor: chosenFactor.current } })
      return
    }
    answer(value)
  }

  const starting = phase === 'starting' || checking || readingSession
  const stepBusy = pending || submitting
  const waiting = retryAfterSeconds !== null
  const autoCompleting =
    step !== null && step.kind === 'complete' && phase === 'step'
  const unavailable = phase === 'unavailable'
  const flowInvalid =
    phase === 'failed' && step !== null && errorCode === 'authn.flow_invalid'
  // A challenge before any second factor is chosen travels under `answer`;
  // one after it, and every webauthn step, travels under `factor`.
  const answerStep =
    step !== null && step.kind === 'challenge' && chosenFactor.current === null
      ? 'answer'
      : 'factor'
  const stepShown =
    step !== null &&
    !flowInvalid &&
    !freshFirst &&
    !reloginOnly &&
    !unavailable &&
    !starting
  const cancelLabel = t('mfa.stepUp.cancelLabel')
  const cancelButton = (
    <ActionButton onClick={onCancel}>{cancelLabel}</ActionButton>
  )
  const stepCancelButton = (
    <ActionButton onClick={onCancel}>{cancelLabel}</ActionButton>
  )

  function handleRelogin(): void {
    onRelogin?.()
    onCancel()
    void session.logout().catch(() => undefined)
  }

  return (
    <>
      <DialogTitle id={titleId} ref={titleRef} tabIndex={-1}>
        {t('mfa.stepUp.title')}
      </DialogTitle>
      <DialogContent sx={DIALOG_BODY_SX}>
        {notice === undefined ? null : (
          <Typography variant="body2" color="text.secondary">
            {notice}
          </Typography>
        )}
        {flowInvalid ? (
          <Box>
            <InlineError
              code={errorCode}
              retryAfterSeconds={retryAfterSeconds ?? undefined}
              countdownId={throttleNoteId}
            />
          </Box>
        ) : freshFirst ? (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
            <Typography variant="body2" color="text.secondary">
              {t('mfa.stepUp.noFactor')}
            </Typography>
            {accountHref !== undefined && (
              <Link href={accountHref} variant="body2">
                {t('mfa.stepUp.accountSettingsLink')}
              </Link>
            )}
          </Box>
        ) : reloginOnly ? (
          <Typography variant="body2" color="text.secondary">
            {t('mfa.stepUp.firstFactorRelogin')}
          </Typography>
        ) : unavailable ? (
          <Typography variant="body2" color="text.secondary">
            {t('mfa.stepUp.cannotElevate')}
          </Typography>
        ) : (
          <>
            <Typography variant="body2" color="text.secondary">
              {t('mfa.stepUp.description')}
            </Typography>
            {errorCode === null ? null : (
              <Box>
                <InlineError
                  code={errorCode}
                  retryAfterSeconds={retryAfterSeconds ?? undefined}
                  countdownId={throttleNoteId}
                />
              </Box>
            )}
            {starting || autoCompleting ? (
              <Typography variant="body2">
                {t('mfa.stepUp.starting')}
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
                      key={`${seq ?? 0}-${step.kind}`}
                      step={step}
                      sequence={seq ?? 0}
                      busy={stepBusy}
                      submitDisabled={waiting}
                      submitUnavailableDescribedBy={
                        waiting ? throttleNoteId : undefined
                      }
                      answerStep={answerStep}
                      submitLabel={t('mfa.stepUp.confirmLabel')}
                      onSubmit={handleAnswer}
                      secondaryAction={stepCancelButton}
                      lockedFields={lockedFieldsOf(step, session)}
                    />
                  </>
                ) : null}
              </Box>
            )}
          </>
        )}
      </DialogContent>
      {stepShown ? null : (
        <ActionRow sx={DIALOG_ACTIONS_SX}>
          {cancelButton}
          {flowInvalid ? (
            <ActionButton emphasis="primary" onClick={onRestart}>
              {t('mfa.stepUp.restart')}
            </ActionButton>
          ) : unavailable || reloginOnly ? (
            <ActionButton emphasis="primary" onClick={handleRelogin}>
              {t('mfa.stepUp.relogin')}
            </ActionButton>
          ) : null}
        </ActionRow>
      )}
    </>
  )
}

// Locks the account's own login name and its proved phone when it has exactly
// one. Never lock the phone while impersonating: the step-up proves the
// initiator's own factor, so a locked snapshot number is refused.
function lockedFieldsOf(
  step: AuthnStep,
  session: AuthSession,
): Readonly<Record<string, string>> | undefined {
  if (step.kind !== 'challenge') {
    return undefined
  }
  const identifiers = session.getSnapshot().principal?.identifiers ?? []
  const locked: Record<string, string> = {}
  if (step.fields.some((field) => field.name === 'identifier')) {
    const username = identifiers.find(
      (identifier) => identifier.kind === 'username',
    )?.value
    if (username !== undefined && username !== '') {
      locked.identifier = username
    }
  }
  if (
    step.fields.some((field) => field.name === 'phone') &&
    !session.getSnapshot().impersonating
  ) {
    const phones = identifiers.filter(
      (identifier) =>
        identifier.kind === 'phone' &&
        identifier.proven &&
        identifier.value !== '',
    )
    const only = phones.length === 1 ? phones[0] : undefined
    if (only !== undefined) {
      locked.phone = only.value
    }
  }
  return Object.keys(locked).length === 0 ? undefined : locked
}

function activeFactorCount(
  page:
    | { readonly factors: readonly { readonly status: string }[] }
    | undefined,
): number {
  if (page === undefined) {
    return 0
  }
  return page.factors.filter((factor) => factor.status === 'active').length
}

function decideRefusal(view: AuthnSessionView): RefusalVerdict {
  if (freshFirstFactor(view)) {
    return { kind: 'multi-factor' }
  }
  const method = firstFactorMethod(view)
  return method === undefined
    ? { kind: 'unknown-method' }
    : { kind: 'fresh-first-factor', method }
}

// Whether the session proves a first factor recent enough for the
// FreshFirstFactor requirement; when it does, a 401 from a gated write asks for
// multi-factor instead. A timestamp ahead of this clock is not read as fresh.
function freshFirstFactor(view: AuthnSessionView): boolean {
  if (view.authenticated !== true) {
    return false
  }
  if (view.assurance !== 'single' && view.assurance !== 'multi') {
    return false
  }
  if (view.auth_time === undefined) {
    return false
  }
  const authenticatedAt = Date.parse(view.auth_time)
  if (Number.isNaN(authenticatedAt)) {
    return false
  }
  const now = Date.now()
  return (
    authenticatedAt <= now && now - authenticatedAt <= FIRST_FACTOR_WINDOW_MS
  )
}

/** The challenge dialog without a ref. */
export function StepUpChallenge(props: StepUpChallengeProps) {
  return <StepUpChallengeFrame {...props} />
}

function firstFactorMethod(view: AuthnSessionView): string | undefined {
  const methods = view.methods ?? []
  if (methods.includes('pwd')) {
    return 'password'
  }
  if (methods.includes('sms')) {
    return 'sms'
  }
  return undefined
}
