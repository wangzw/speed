/**
 * ContactVerificationSection: the account's email-verification surface, the
 * face of the profile's `verification` state.
 */

import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type FormEvent,
} from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  useAuthnLoginAddIdentifier,
  useAuthnLoginVerifyIdentifier,
} from '@speed/api-sdk'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { humanGateNeedsProof, stepUpReasonOf, useHumanVerification, useStepUpGate } from '@speed/auth-core'
import {
  ActionButton,
  ActionRow,
  errorCodeOf,
  FORM_MAX_WIDTH,
  HumanVerification,
  SectionCard,
  useActivationGuard,
  useWriteFeedback,
  visuallyHiddenSx,
  type ActionButtonUnavailableExplanation,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { useProfileQuery } from './internal/queries.js'
import { retryAfterMsOf } from './internal/retry-after.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

const RESEND_NOTE_ID = 'contact-verification-resend-note'

/** Only the control that owns the write reports busy; the rest are
 * unavailable. */
type ContactWriteAction = 'add' | 'change' | 'resend' | 'verify'

/** A send request; `automatic` marks the landing's attempt, which no control
 * started. */
interface ContactSendRequest {
  readonly action: ContactWriteAction | null
  readonly automatic?: boolean
}

/** Whether focus was lost when the control's container left; a connected
 * control outside it means the operator moved focus. */
function focusFellWith(removed: HTMLElement | null): boolean {
  const active = document.activeElement
  if (active === null || active === document.body) {
    return true
  }
  if (!(active instanceof HTMLElement) || !active.isConnected) {
    return true
  }
  return removed !== null && removed.contains(active)
}

/**
 * The host supplies the session, the opening state and the deployment's
 * gate; the section reads and writes the account's contact identifiers.
 */
export interface ContactVerificationSectionProps {
  /** The session the human-verification proof is obtained for. */
  readonly session: AuthSession
  /** Starts the change-address entry open. A mount that starts open never
   * auto-sends, even after the form is closed. */
  readonly initialChangeOpen?: boolean
  /** Sends one code on mount for a pending account with a target address. A
   * mount that starts with the change form open never sends. */
  readonly autoSend?: boolean
  /** The deployment's human-verification gate, when the host already read the
   * discovery document; omitted, the section reads it itself. */
  readonly humanGate?: AuthnHumanGate
}

/**
 * The email-verification surface: verifies the account's address through
 * its own identifier endpoints, each write settling one result region.
 */
export function ContactVerificationSection({
  session,
  initialChangeOpen = false,
  autoSend = false,
  humanGate,
}: ContactVerificationSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const addIdentifier = useAuthnLoginAddIdentifier()
  const verifyIdentifier = useAuthnLoginVerifyIdentifier()
  // The address a send waits on because a proof is not in hand; the challenge
  // shows only once a send is asked for.
  const [pending, setPending] = useState<{
    readonly address: string
    readonly automatic: boolean
  } | null>(null)
  const [activeWrite, setActiveWrite] = useState<ContactWriteAction | null>(null)
  const human = useHumanVerification(session, {
    scope: 'other',
    enabled: pending !== null,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
  })
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, 'other')
  // A required proof that is not in hand keeps a request from leaving.
  const proofBlocked = needsProof && human.proof === null

  // Null until a send lands; the target otherwise comes from the profile.
  const [sentTo, setSentTo] = useState<string | null>(null)
  // The notice's region mounts from the first render and fills one commit
  // later, so the announcement is a content change.
  const [sentCommitted, setSentCommitted] = useState(false)
  const [code, setCode] = useState('')
  const [codeInvalid, setCodeInvalid] = useState(false)
  // One slot for the code step's sends and verification; the attempt that
  // settles last is the one shown.
  const feedback = useWriteFeedback()
  // The verify step's own wording for the wrong-code and taken-address
  // answers; cleared at the start of every attempt.
  const [verifyFailureText, setVerifyFailureText] = useState<string | null>(
    null,
  )
  const [emailEntry, setEmailEntry] = useState('')
  const [entryOpen, setEntryOpen] = useState(initialChangeOpen)
  const [resendUntil, setResendUntil] = useState(0)
  // Only a landed send's hold gets the section's note; a throttled failure's
  // banner counts the same wait down.
  const [resendFromSuccess, setResendFromSuccess] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const [autoStepUp, setAutoStepUp] = useState(false)
  // One automatic attempt per mount, never when the host landed with the
  // change form open (fixed at mount, even after the form closes).
  const autoSent = useRef(false)
  const autoSendSuppressed = useRef(initialChangeOpen)
  const codeRef = useRef<HTMLInputElement>(null)
  // The focus landing when a landed verification removes the code step, and
  // the step that write removes.
  const verifiedLanding = useRef<HTMLDivElement>(null)
  const verifyLandingPending = useRef(false)
  const codeStep = useRef<HTMLElement>(null)
  const removedOnLanding = useRef<HTMLElement | null>(null)
  const gate = useStepUpGate<string>((nextCode) => {
    void verify(nextCode)
  })

  // In flight through the refetches that follow.
  const writeInFlight =
    addIdentifier.isPending ||
    verifyIdentifier.isPending ||
    feedback.pending
  // A send waiting on its proof is the same attempt but not a write in
  // flight: the code already in hand may still be verified.
  const sendWaiting = pending !== null
  const attemptInFlight = writeInFlight || sendWaiting
  useEffect(() => {
    if (!attemptInFlight) {
      setActiveWrite(null)
    }
  }, [attemptInFlight])
  const verification = data?.verification
  const state = verification?.state ?? ''
  const targetEmail = verification?.email ?? ''

  // Stop the timer at its own expiry rather than ticking for the whole mount.
  useEffect(() => {
    if (resendUntil <= Date.now()) {
      return undefined
    }
    const timer = setInterval(() => {
      const current = Date.now()
      setNow(current)
      if (current >= resendUntil) {
        clearInterval(timer)
      }
    }, 1000)
    return () => {
      clearInterval(timer)
    }
  }, [resendUntil])

  // A disabled input cannot take focus, so the hand-off is discharged by the
  // first commit in which the scope is no longer busy.
  const focusCodeWhenSettled = useRef(false)
  useEffect(() => {
    setSentCommitted(sentTo !== null)
    if (sentTo !== null) {
      focusCodeWhenSettled.current = true
    }
  }, [sentTo])

  useEffect(() => {
    if (!focusCodeWhenSettled.current || writeInFlight) {
      return
    }
    focusCodeWhenSettled.current = false
    codeRef.current?.focus()
  }, [writeInFlight, sentTo])

  // Focus moves only when the removal took it; a place the operator chose is
  // left alone.
  useLayoutEffect(() => {
    if (state !== 'verified' || !verifyLandingPending.current) {
      return
    }
    verifyLandingPending.current = false
    const removed = removedOnLanding.current
    removedOnLanding.current = null
    if (!focusFellWith(removed)) {
      return
    }
    verifiedLanding.current?.focus()
  }, [state])

  const remainingSeconds = Math.max(
    0,
    Math.ceil((resendUntil - now) / 1000),
  )
  const emailEntryEmpty = emailEntry.trim() === ''
  const codeEmpty = code.trim() === ''
  // An unmet precondition explains itself; a control held by another write in
  // the scope carries no reason.
  const emailExplanation: ActionButtonUnavailableExplanation = emailEntryEmpty
    ? { unavailableReason: t('verification.emailEmpty') }
    : {}
  const codeExplanation: ActionButtonUnavailableExplanation = codeEmpty
    ? { unavailableReason: t('verification.codeEmpty') }
    : {}
  // A landed send's hold points at the note; a throttled hold carries the
  // wait itself, since its banner explains it.
  const resendExplanation: ActionButtonUnavailableExplanation =
    remainingSeconds > 0
      ? resendFromSuccess
        ? { unavailableDescribedBy: RESEND_NOTE_ID }
        : {
            unavailableReason: t('verification.resendIn', {
              seconds: remainingSeconds,
            }),
          }
      : {}
  // Each form matches its own submit control, so an Enter press during another
  // control's write reaches no request.
  const addSubmit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      if (emailEntry.trim() !== '') {
        askToSend(emailEntry, { action: 'add' })
      }
    },
    {
      busy: attemptInFlight && activeWrite === 'add',
      unavailable: (writeInFlight && activeWrite !== 'add') || emailEntryEmpty,
    },
  )
  const changeSubmit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      if (emailEntry.trim() !== '') {
        askToSend(emailEntry, { action: 'change' })
      }
    },
    {
      busy: attemptInFlight && activeWrite === 'change',
      unavailable:
        (writeInFlight && activeWrite !== 'change') || emailEntryEmpty,
    },
  )
  const verifySubmit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      if (code.trim() !== '') {
        void verify(code)
      }
    },
    {
      busy: writeInFlight && activeWrite === 'verify',
      unavailable: (writeInFlight && activeWrite !== 'verify') || codeEmpty,
    },
  )

  /** Proving the address neither rotates this session's credential nor
   * revokes other sessions. */
  async function invalidateAfterWrite(): Promise<void> {
    await queryClient.invalidateQueries({ queryKey: getAuthnProfileGetQueryKey() })
  }

  function send(nextEmail: string, automatic = false): Promise<void> {
    if (proofBlocked) {
      return Promise.resolve()
    }
    const address = nextEmail.trim()
    // A send to a different address is a change of context for the shared slot.
    const previousTarget = sentTo ?? targetEmail
    setVerifyFailureText(null)
    setCodeInvalid(false)
    setAutoStepUp(false)
    return feedback.run(async () => {
      const presented = needsProof ? human.proof : null
      let result
      try {
        result = await addIdentifier.mutateAsync({
          data:
            presented === null
              ? { kind: 'email', value: address }
              : { kind: 'email', value: address, human_proof: presented },
        })
      } catch (error) {
        const failureCode = errorCodeOf(error)
        // A send is not a gated write, so a step-up refusal renders its code
        // text; the automatic attempt states it instead of opening a dialog.
        if (automatic && stepUpReasonOf(failureCode) !== null) {
          setAutoStepUp(true)
          return null
        }
        // The single-use proof was spent on the way: obtain a fresh one.
        if (presented !== null) {
          human.consume()
        }
        const retryAfter = retryAfterMsOf(error)
        if (retryAfter !== undefined) {
          setResendUntil(Date.now() + retryAfter)
          setResendFromSuccess(false)
          setNow(Date.now())
        }
        throw error
      }
      if (presented !== null) {
        human.consume()
      }
      const interval =
        typeof result.resend_after === 'number' ? result.resend_after : 0
      const held = interval > 0
      setResendUntil(held ? Date.now() + interval * 1000 : 0)
      // A successful send leaves no banner, so its note is the only
      // explanation of the wait.
      setResendFromSuccess(held)
      setNow(Date.now())
      if (address !== previousTarget) {
        feedback.reset()
      }
      setSentTo(address)
      setCode('')
      setEmailEntry('')
      setEntryOpen(false)
      await invalidateAfterWrite()
      // The sent notice is the step's own state, so the slot stays empty.
      return null
    })
  }

  const sendRef = useRef(send)
  sendRef.current = send

  /** Sends now when the gate wants no proof, else holds the send for the
   * effect to release. The automatic landing send takes this path too. */
  function askToSend(
    address: string,
    { action, automatic = false }: ContactSendRequest,
  ): void {
    if (pending !== null) {
      return
    }
    setActiveWrite(action)
    setVerifyFailureText(null)
    setCodeInvalid(false)
    setAutoStepUp(false)
    feedback.reset()
    if (
      !human.pending &&
      gateDocument !== null &&
      (!needsProof || human.proof !== null)
    ) {
      void send(address, automatic)
      return
    }
    setPending({ address, automatic })
  }

  const askToSendRef = useRef(askToSend)
  askToSendRef.current = askToSend

  // A held send leaves as soon as the gate is known and the proof is in hand.
  useEffect(() => {
    if (pending === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    const { address, automatic } = pending
    setPending(null)
    void sendRef.current(address, automatic)
  }, [pending, human.pending, gateDocument, needsProof, human.proof])

  function verify(nextCode: string): Promise<void> {
    // Verifying the code in hand calls off a held send, so a late token
    // cannot send a second code that invalidates the one being proved.
    setPending(null)
    setActiveWrite('verify')
    setCodeInvalid(false)
    // Only the verify that lands moves focus off the step it replaces.
    verifyLandingPending.current = false
    setAutoStepUp(false)
    return feedback.run(async () => {
      setVerifyFailureText(null)
      try {
        // Opens no window: proving rotates nothing. A step-up refusal arms the
        // gate with the same code.
        await verifyIdentifier.mutateAsync({ data: { code: nextCode } })
        verifyLandingPending.current = true
        removedOnLanding.current = codeStep.current
        setCode('')
        setSentTo(null)
        setResendUntil(0)
        setResendFromSuccess(false)
        await invalidateAfterWrite()
      } catch (error) {
        const failureCode = errorCodeOf(error)
        if (failureCode === 'authn.login_failed') {
          setCodeInvalid(true)
          setCode('')
          setVerifyFailureText('verification.codeInvalid')
          return null
        }
        if (
          failureCode === 'authn.conflict' ||
          failureCode === 'authn.email_taken'
        ) {
          setVerifyFailureText('verification.addressTaken')
          return null
        }
        const reason = stepUpReasonOf(failureCode)
        if (reason !== null) {
          gate.arm(nextCode, reason)
          return null
        }
        throw error
      }
      return null
    })
  }

  // The landing's single automatic send; it takes the visitor's path because
  // it has no click of its own to obtain a proof with.
  useEffect(() => {
    if (
      !autoSend ||
      autoSent.current ||
      autoSendSuppressed.current ||
      entryOpen
    ) {
      return
    }
    if (state !== 'pending' || targetEmail === '') {
      return
    }
    autoSent.current = true
    askToSendRef.current(targetEmail, { action: null, automatic: true })
  }, [autoSend, entryOpen, state, targetEmail])

  // A settled answer with no body must reach the async section's error
  // variant, never read as "nothing to verify"; a settled answer that carries
  // no verification renders nothing at all.
  if (data !== undefined && (state === '' || state === 'none')) {
    return null
  }

  // Mutually exclusive with the region: the wrong-code and taken-address
  // answers render the section's own wording.
  const verifyFailureBanner =
    verifyFailureText === null ? null : (
      <Alert severity="error" role="alert" sx={{ width: '100%' }}>
        {t(verifyFailureText)}
      </Alert>
    )

  const sentNotice = sentTo ?? targetEmail
  const hasTarget = targetEmail !== ''

  const humanBlock = (
    <HumanVerification
      scope="other"
      gate={human.gate ?? humanGate}
      proof={human.proof}
      status={human.status}
      captcha={human.captcha}
      onToken={human.adoptToken}
      onRetry={human.retry}
      onRefreshCaptcha={human.refreshCaptcha}
      onRequestAudio={human.requestAudio}
      audioRetryAfterSeconds={human.audioRetryAfterSeconds}
      disabled={writeInFlight}
    />
  )

  const verifiedCard = (
    <Box
      ref={verifiedLanding}
      tabIndex={-1}
      role="group"
      aria-label={t('verification.verifiedTitle')}
    >
      <SectionCard title={t('verification.verifiedTitle')}>
        <Alert severity="success" role="status">
          {targetEmail === ''
            ? t('verification.verifiedDescriptionNoEmail')
            : t('verification.verifiedDescription', {
                email: targetEmail,
                interpolation: { escapeValue: false },
              })}
        </Alert>
      </SectionCard>
    </Box>
  )

  const feedbackRegion = (
    <WriteFeedbackRegion
      outcome={feedback.outcome}
      sx={{ alignSelf: 'stretch', width: '100%' }}
    />
  )

  const addForm = (
    <SectionCard
      title={t('verification.addTitle')}
      description={t('verification.addDescription')}
    >
      <Box
        component="form"
        noValidate
        onSubmit={addSubmit}
        sx={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'flex-start',
          gap: 2,
          maxWidth: FORM_MAX_WIDTH,
        }}
      >
        <TextField
          fullWidth
          type="email"
          autoComplete="email"
          label={t('verification.addLabel')}
          value={emailEntry}
          slotProps={{ htmlInput: { readOnly: writeInFlight } }}
          onChange={(event) => {
            // Editing the address abandons a held send.
            setPending(null)
            setEmailEntry(event.target.value)
          }}
        />
        {pending !== null ? humanBlock : null}
        <ActionRow sx={{ width: '100%' }}>
          <ActionButton
            type="submit"
            emphasis="primary"
            busy={attemptInFlight && activeWrite === 'add'}
            unavailable={(writeInFlight && activeWrite !== 'add') || emailEntryEmpty}
            {...emailExplanation}
          >
            {t('verification.addSubmit')}
          </ActionButton>
        </ActionRow>
        {feedbackRegion}
      </Box>
    </SectionCard>
  )

  const verificationForm = (
    <>
      <SectionCard
        ref={codeStep}
        title={t('verification.title')}
        description={t('verification.description')}
      >
        <Box
          component="form"
          noValidate
          onSubmit={verifySubmit}
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'flex-start',
            gap: 2,
            maxWidth: FORM_MAX_WIDTH,
          }}
        >
          {autoStepUp ? (
            <Alert severity="info" sx={{ width: '100%' }}>
              {t('verification.autoStepUp')}
            </Alert>
          ) : null}
          <Typography variant="body2" color="text.secondary">
            {sentNotice === ''
              ? t('verification.description')
              : t('verification.target', {
                  email: sentNotice,
                  interpolation: { escapeValue: false },
                })}
          </Typography>
          {/* Mounted from the first render, filled one commit later, so it
              announces the destination without becoming a second result. */}
          <Box role="status" sx={visuallyHiddenSx}>
            {sentCommitted && sentTo !== null
              ? t('verification.sent', {
                  email: sentTo,
                  interpolation: { escapeValue: false },
                })
              : ''}
          </Box>
          <TextField
            fullWidth
            inputMode="numeric"
            autoComplete="one-time-code"
            label={t('verification.codeLabel')}
            helperText={t('verification.codeHelper')}
            value={code}
            error={codeInvalid}
            slotProps={{
              htmlInput: {
                'aria-invalid': codeInvalid,
                readOnly: writeInFlight,
              },
            }}
            inputRef={codeRef}
            onChange={(event) => {
              setCode(event.target.value)
              if (codeInvalid) {
                setCodeInvalid(false)
              }
            }}
          />
          {pending !== null ? humanBlock : null}
          <ActionRow sx={{ width: '100%' }}>
            <ActionButton
              type="button"
              busy={attemptInFlight && activeWrite === 'resend'}
              unavailable={
                (writeInFlight && activeWrite !== 'resend') ||
                remainingSeconds > 0
              }
              {...resendExplanation}
              onClick={() => askToSend(sentNotice, { action: 'resend' })}
            >
              {sentTo === null
                ? t('verification.send')
                : t('verification.resend')}
            </ActionButton>
            <ActionButton
              type="button"
              unavailable={writeInFlight}
              onClick={() => {
                // Closing abandons a held send, so no code goes to the old address.
                setPending(null)
                setEntryOpen((open) => !open)
                setEmailEntry('')
              }}
            >
              {entryOpen
                ? t('verification.changeCancel')
                : t('verification.change')}
            </ActionButton>
            <ActionButton
              type="submit"
              emphasis="primary"
              busy={writeInFlight && activeWrite === 'verify'}
              unavailable={
                (writeInFlight && activeWrite !== 'verify') || codeEmpty
              }
              {...codeExplanation}
            >
              {t('verification.verify')}
            </ActionButton>
          </ActionRow>
          {feedbackRegion}
          {verifyFailureBanner}
          {/* Only a landed send's hold has no banner, so it states its wait. */}
          {remainingSeconds > 0 && resendFromSuccess ? (
            <Typography
              id={RESEND_NOTE_ID}
              variant="body2"
              color="text.secondary"
            >
              {t('verification.resendIn', { seconds: remainingSeconds })}
            </Typography>
          ) : null}
        </Box>
        {/* Its own form: inside the code form, Enter would submit the code. */}
        {entryOpen ? (
          <Box
            component="form"
            noValidate
            onSubmit={changeSubmit}
            sx={{ mt: 2, maxWidth: FORM_MAX_WIDTH }}
          >
            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'flex-start',
                gap: 2,
                width: '100%',
              }}
            >
              <TextField
                fullWidth
                type="email"
                autoComplete="email"
                label={t('verification.changeLabel')}
                value={emailEntry}
                slotProps={{ htmlInput: { readOnly: writeInFlight } }}
                onChange={(event) => {
                  // Editing the address abandons a held send.
                  setPending(null)
                  setEmailEntry(event.target.value)
                }}
              />
              <ActionRow sx={{ width: '100%' }}>
                <ActionButton
                  type="submit"
                  emphasis="primary"
                  busy={attemptInFlight && activeWrite === 'change'}
                  unavailable={
                    (writeInFlight && activeWrite !== 'change') ||
                    emailEntryEmpty
                  }
                  {...emailExplanation}
                >
                  {t('verification.changeSubmit')}
                </ActionButton>
              </ActionRow>
            </Box>
          </Box>
        ) : null}
      </SectionCard>
    </>
  )

  return (
    <>
      <AccountAsyncSection
        pending={isPending && data === undefined}
        payload={data}
        empty={false}
        error={error}
        refreshing={isFetching}
        loading={
          <SectionCard title={t('verification.title')}>
            <Box
              aria-busy="true"
              sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}
            >
              <Skeleton variant="text" width="40%" />
              <Skeleton variant="text" width="70%" />
            </Box>
          </SectionCard>
        }
        errorState={{
          title: t('verification.error.title'),
          description: t('verification.error.description'),
          retryLabel: t('verification.retry'),
          onRetry: () => void refetch(),
        }}
      >
        {() =>
          state === 'verified' ? (
            verifiedCard
          ) : !hasTarget && sentTo === null ? (
            addForm
          ) : (
            verificationForm
          )
        }
      </AccountAsyncSection>

      <StepUpChallenge
        open={gate.open}
        reason={gate.reason}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
      />
    </>
  )
}
