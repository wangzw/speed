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
 * An account with no active factor is read from the caller's own factor
 * list (GET /api/v1/auth/factors), and what the refusal asked for is decided
 * from the caller's own session (GET /api/v1/auth/session). The session read is
 * forced when the dialog opens: the cached view may predate a sign-in
 * completed while the dialog was closed, so it is not allowed to answer.
 * The verdict is taken once per opening and stays fixed while the dialog
 * stays open -- a parked dialog never changes its mind because a later
 * render puts the stored authentication time outside the window -- and
 * the dialog shows its starting placeholder, starting no flow, until
 * that read lands. Both requirements the self-service writes can carry
 * are told apart by whether the session already proves a fresh first
 * factor:
 *
 *  - When it does (assurance of at least one factor, the last interactive
 *    authentication within the five minutes the requirement fixes), a
 *    write asking for a fresh first factor would have been allowed, so
 *    the refusal asks for a multi-factor authentication instead. No
 *    step-up can reach that without a factor: the dialog names the
 *    missing two-step verification and starts no flow.
 *  - When it does not, the refusal asks for a fresh first factor -- the
 *    case an account opened by SMS sign-up meets on adding a username or
 *    setting a password past the five-minute window. The dialog starts a
 *    step-up named with the method that established the session
 *    (`password` for the `pwd` amr, `sms` for `sms`): the server re-proves
 *    that factor and refreshes the authentication time, and the caller
 *    re-runs the refused write. A session established through a channel
 *    this dialog cannot re-prove (social or enterprise sign-in) gets a
 *    re-login exit instead.
 *
 * A failed session read is not read as a known method: the dialog offers
 * the re-login exit, which is the way out when the method cannot be
 * named. The no-factor/multi-factor branch names the missing two-step
 * verification; when the host supplies `accountHref` (the account
 * settings route that registers one) the dialog renders it as a plain
 * link beside that text. The package itself navigates nothing: the href
 * is the host's, and a caller that has no such route omits the prop and
 * gets the text alone.
 *
 * A refusal of the start itself, with factors registered, is the
 * deployment declining to raise this session at all, which no step can
 * satisfy: the hook's 'unavailable' phase -- a 401
 * insufficient_user_authentication (the account has factors, but none
 * whose methods may strengthen this session). The dialog then says so and
 * offers a re-login -- it ends the session through the session's own
 * logout, and the host's session gate converges on its sign-in surface --
 * rather than a verification that cannot come, because a new sign-in lets
 * those factors count. Any other start failure, a 400 included, renders as
 * its code.
 *
 * The dialog never signs in and never navigates on its own: a dead
 * session shows its code text and the host's session gate converges.
 */

import { useEffect, useId, useRef, useState } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
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
import { humanGateNeedsProof, useStepUp } from '@speed/auth-core'
import { HumanVerification } from '@speed/ui-kit'
import { InlineError } from './inline-error.js'
import { FlowStep } from './flow-step.js'
import { useFactorsQuery, useSessionViewQuery } from './queries.js'
import { useAccountUiTranslation } from './translation.js'

/** How recent a first factor must be for the self-service writes: the
 * server's fixed FreshFirstFactor window, not a setting. */
const FIRST_FACTOR_WINDOW_MS = 5 * 60 * 1000

/** What a self-service write's refusal asked for, read from the session
 * view taken when the dialog opened: the multi-factor requirement (the
 * session already proves a fresh first factor), a fresh first factor
 * (named with the method that established the session), or a method this
 * dialog cannot re-prove / a failed read (a new sign-in is the only way
 * out). */
type RefusalVerdict =
  | { readonly kind: 'multi-factor' }
  | { readonly kind: 'fresh-first-factor'; readonly method: string }
  | { readonly kind: 'unknown-method' }

export interface StepUpChallengeProps {
  /** Whether the dialog is showing. Opening resets its per-attempt state. */
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
  /**
   * The host's account-settings route, rendered as a link beside the
   * "register two-step verification first" explanation. Omit to render
   * the explanation alone.
   */
  readonly accountHref?: string
  /**
   * The surface that opened the challenge (the host's current path). A
   * redirect method's round trip lands back here, so the refusal's problem
   * code reaches the page that started the flow. Omitted, the deployment's
   * default return target applies.
   */
  readonly returnPath?: string
}

export function StepUpChallenge({
  open,
  reason = 'mfa',
  session,
  onSuccess,
  onCancel,
  accountHref,
  returnPath,
}: StepUpChallengeProps) {
  const { t } = useAccountUiTranslation()
  const titleId = useId()
  // The SSO refusal cannot be satisfied by any local factor, so the dialog
  // starts no flow and reads no factor: it offers the sign-in exit alone.
  const sso = reason === 'sso'
  // The caller's own registered factors, read while the dialog is open. No
  // factor read, or a failed one, falls back to the flow's own refusal.
  const factors = useFactorsQuery(open && !sso)
  const checking = open && !sso && factors.isPending
  const noFactor =
    open && !sso && factors.isSuccess && activeFactorCount(factors.data) === 0
  // Only an account with no factor needs its session read: with a factor
  // the flow enters at the factor step and names no method. The query
  // stays disabled -- the cached view may predate a sign-in completed
  // while the dialog was closed, so the verdict below is taken from the
  // read this opening forces through `refetch`, and an enabled query would
  // race that read: with a cached view present, `refetch`'s default
  // `cancelRefetch` would replace the query's own in-flight read with a
  // second request for the same opening.
  const { refetch: readSessionView } = useSessionViewQuery(false)
  // The refusal's requirement, decided once per opening and frozen while
  // the dialog stays open: a parked dialog does not start a flow because a
  // later render puts the stored authentication time outside the window.
  const [verdict, setVerdict] = useState<RefusalVerdict | null>(null)
  // A new opening decides afresh; nothing is carried over from the last
  // one. Adjusting the state during render, before the reopen commits,
  // keeps the previous verdict from being read even once.
  const [previousOpen, setPreviousOpen] = useState(open)
  if (open !== previousOpen) {
    setPreviousOpen(open)
    setVerdict(null)
  }
  // Decide from a read issued now: the opening forces one, and the dialog
  // waits on its placeholder until it lands. A read that fails names no
  // method.
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
  // The starting placeholder while the decision's read is in flight.
  const readingSession = awaitingVerdict
  // The session already proves a fresh first factor, so the refusal cannot
  // be about freshness: it asks for multi-factor.
  const freshFirst = open && noFactor && verdict?.kind === 'multi-factor'
  const reproofMethod =
    open && noFactor && verdict?.kind === 'fresh-first-factor'
      ? verdict.method
      : undefined
  const reproveFirst = reproofMethod !== undefined
  // The method is unknown (social or enterprise sign-in, or a failed
  // session read): the only way out is a new sign-in.
  const reloginOnly =
    open && noFactor && verdict?.kind === 'unknown-method'
  const startFlow =
    open && !checking && !awaitingVerdict && (!noFactor || reproveFirst)
  // A restart remounts the flow driver: a fresh mount re-drives useStepUp,
  // which has no restart command of its own. The factor and session reads
  // stay put, so the verdict decided for this opening is not read again.
  const [run, setRun] = useState(0)

  if (!open) {
    return null
  }

  // The SSO exit: end the session so the host's session gate converges on its
  // sign-in surface, where the operator signs in through the tenant's identity
  // provider. No local factor can clear this refusal.
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
      {sso ? (
        <>
          <DialogTitle id={titleId}>{t('mfa.stepUp.ssoTitle')}</DialogTitle>
          <DialogContent>
            <Typography variant="body2" color="text.secondary">
              {t('mfa.stepUp.ssoRequired')}
            </Typography>
          </DialogContent>
          <DialogActions>
            <Button onClick={onCancel}>{t('mfa.stepUp.cancelLabel')}</Button>
            <Button variant="contained" onClick={handleRelogin}>
              {t('mfa.stepUp.relogin')}
            </Button>
          </DialogActions>
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
          freshFirst={freshFirst}
          reloginOnly={reloginOnly}
          checking={checking}
          readingSession={readingSession}
          reproveFirst={reproveFirst}
          reproofMethod={reproofMethod}
        />
      )}
    </Dialog>
  )
}

/** The flow a challenge drives, split from the dialog so a restart can
 * remount it: a fresh mount re-drives useStepUp, and the challenge's factor
 * and session reads are not repeated. */
function StepUpFlow({
  session,
  active,
  titleId,
  onSuccess,
  onCancel,
  onRestart,
  accountHref,
  returnPath,
  freshFirst,
  reloginOnly,
  checking,
  readingSession,
  reproveFirst,
  reproofMethod,
}: {
  readonly session: AuthSession
  readonly active: boolean
  readonly titleId: string
  readonly onSuccess: () => void
  readonly onCancel: () => void
  readonly onRestart: () => void
  readonly accountHref?: string
  readonly returnPath?: string
  readonly freshFirst: boolean
  readonly reloginOnly: boolean
  readonly checking: boolean
  readonly readingSession: boolean
  readonly reproveFirst: boolean
  readonly reproofMethod?: string
}): ReactElement {
  const { t } = useAccountUiTranslation()
  const { phase, step, seq, errorCode, human, answer } = useStepUp(
    session,
    active,
    onSuccess,
    reproveFirst ? reproofMethod : undefined,
    returnPath,
  )
  // The second factor the operator chose. The verification step that
  // follows it (the code challenge) is answered under the `factor` step
  // and must re-name the same factor alongside the field it asks for --
  // a challenge reply carries only that field, and the server re-reads the
  // factor from the answer. A first-factor re-proof starts with no chosen
  // factor, whose steps travel under `answer` instead.
  const chosenFactor = useRef<string | null>(null)

  function handleAnswer(value: AuthnFlowAnswer): void {
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

  const starting = phase === 'starting' || checking || readingSession
  // This deployment cannot raise the session at all (factors exist, none
  // that may strengthen it): no step will come, so the dialog says so and
  // offers a re-login rather than a verification that cannot succeed.
  const unavailable = phase === 'unavailable'
  // The server no longer holds the flow the answer named -- it expired, or
  // the deployment's flow store went with the process that held it. No step
  // can be retried, so the code's banner and the restart action are the
  // whole surface, as in the console's challenge.
  const flowInvalid =
    phase === 'failed' && step !== null && errorCode === 'authn.flow_invalid'
  const answerStep =
    chosenFactor.current !== null || !reproveFirst ? 'factor' : 'answer'

  // The unavailable phase's and the unknown-method state's one action:
  // end the session -- forgetting the refused action, as a cancel does --
  // so the host's session gate converges on its sign-in surface, where the
  // operator signs in again for a stronger verification. The session
  // clears its local state even if the revoke is refused, so a failure
  // here needs no retry.
  function handleRelogin(): void {
    onCancel()
    void session.logout().catch(() => undefined)
  }

  return (
    <>
      <DialogTitle id={titleId}>{t('mfa.stepUp.title')}</DialogTitle>
      <DialogContent>
        {flowInvalid ? (
          <InlineError code={errorCode} />
        ) : freshFirst ? (
          <Box>
            <Typography variant="body2" color="text.secondary">
              {t('mfa.stepUp.noFactor')}
            </Typography>
            {accountHref !== undefined && (
              <Link href={accountHref} variant="body2" sx={{ mt: 1 }}>
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
            <InlineError code={errorCode} />
            {starting ? (
              <Typography variant="body2" sx={{ mt: 2 }}>
                {t('mfa.stepUp.starting')}
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
                      busy={false}
                      answerStep={answerStep}
                      submitLabel={t('mfa.stepUp.confirmLabel')}
                      onSubmit={handleAnswer}
                    />
                  </>
                ) : null}
              </Box>
            )}
          </>
        )}
      </DialogContent>
      <DialogActions>
        <Button onClick={onCancel}>{t('mfa.stepUp.cancelLabel')}</Button>
        {flowInvalid ? (
          <Button variant="contained" onClick={onRestart}>
            {t('mfa.stepUp.restart')}
          </Button>
        ) : unavailable || reloginOnly ? (
          <Button variant="contained" onClick={handleRelogin}>
            {t('mfa.stepUp.relogin')}
          </Button>
        ) : null}
      </DialogActions>
    </>
  )
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
  page:
    | { readonly factors: readonly { readonly status: string }[] }
    | undefined,
): number {
  if (page === undefined) {
    return 0
  }
  return page.factors.filter((factor) => factor.status === 'active').length
}

/** The requirement the refusal asked for, read once from the session view
 * an opening forced. */
function decideRefusal(view: AuthnSessionView): RefusalVerdict {
  if (freshFirstFactor(view)) {
    return { kind: 'multi-factor' }
  }
  const method = firstFactorMethod(view)
  return method === undefined
    ? { kind: 'unknown-method' }
    : { kind: 'fresh-first-factor', method }
}

/** Whether the session already proves a first factor recent enough for the
 * FreshFirstFactor requirement (assurance of at least one factor, the
 * authentication no older than the fixed window). When it does, a write
 * that asks for that requirement would have been allowed, so a 401 from one
 * of the gated writes asks for multi-factor instead (the challenge's
 * acr_values 2, where the fresh-first refusal is acr_values 1). The 401's
 * challenge parameters are not surfaced by the request layer, so the
 * session's own state answers the same question. Evaluated once per
 * opening, when the session read lands; the verdict it feeds is frozen
 * while the dialog stays open. A timestamp ahead of this clock is never
 * read as fresh: an uncertain read re-proves, and only a provably fresh
 * session keeps the multi-factor hint. */
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

/** The login method that established the session's first factor, from the
 * amr set the session view publishes: `password` for `pwd`, `sms` for
 * `sms`. A social or enterprise sign-in names no method this dialog can
 * re-prove. */
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
